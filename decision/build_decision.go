package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"nofx/market"
	"strings"
	"time"
)

//
// --------------------
// Types (PAV4 Request/Response)
// --------------------
//

// ✅ 按你的要求：EntryPrice/Qty/P/OHLC 改为 float64，SL/TP 为 *float64，新增 CurrentTPPrice
type DecideRequest struct {
	Symbol   string        `json:"symbol"`
	NowTS    int64         `json:"now_ts"` // ms
	Position PositionInput `json:"position"`
	Market   MarketInput   `json:"market"`
}

type PositionInput struct {
	Side           string   `json:"side"` // "LONG" | "SHORT"
	EntryPrice     float64  `json:"entry_price"`
	Qty            float64  `json:"qty"`
	EntryTS        int64    `json:"entry_ts"` // ms
	CurrentSLPrice *float64 `json:"current_sl_price"`
	CurrentTPPrice *float64 `json:"current_tp_price"`
}

type MarketInput struct {
	P      float64             `json:"P"`
	Bars15 []market.InputKline `json:"bars_15m"`
}

type DecideResponse struct {
	Action        string      `json:"action"` // "HOLD" | "MOVE_SL" | "CLOSE_ALL"
	NewSLPrice    *float64    `json:"new_sl_price"`
	Reason        string      `json:"reason"`
	State         DecideState `json:"state"`
	InvalidReason *string     `json:"invalid_reason"`
}

type DecideState struct {
	PEval         float64 `json:"P_eval"`
	ATR14         float64 `json:"atr14"`
	R             float64 `json:"R"`
	HardSL        float64 `json:"hard_sl"`
	PeakOrTrough  float64 `json:"peak_or_trough"`
	TrailSL       float64 `json:"trail_sl"`
	RRTargetPrice float64 `json:"rr_target_price"`
	PnLR          float64 `json:"pnl_R"`
}

//
// --------------------
// HTTP Client (Only call when has position)
// --------------------
//

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
	Headers    map[string]string // optional auth headers
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: 5 * time.Second,
		},
		Headers: map[string]string{},
	}
}

func (c *Client) Decide(req DecideRequest) (*DecideResponse, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	}
	url := c.BaseURL + "/decide/pav4"

	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal pav4 request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pav4 http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out DecideResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal pav4 response: %w; raw=%s", err, strings.TrimSpace(string(raw)))
	}

	if out.InvalidReason != nil && strings.TrimSpace(*out.InvalidReason) != "" {
		return &out, fmt.Errorf("pav4 invalid_reason: %s", strings.TrimSpace(*out.InvalidReason))
	}

	switch out.Action {
	case "HOLD", "MOVE_SL", "CLOSE_ALL":
	default:
		return &out, fmt.Errorf("unexpected pav4 action: %q", out.Action)
	}

	return &out, nil
}

//
// --------------------
// Core: Build Decision
// --------------------
//

// HasPosition：判断是否有仓位（qty>0 且 side 为 LONG/SHORT）
func HasPosition(pos *PositionInput) bool {
	if pos == nil {
		return false
	}
	if pos.Qty <= 0 {
		return false
	}
	side := strings.ToUpper(strings.TrimSpace(pos.Side))
	return side == "LONG" || side == "SHORT"
}

// BuildDecision：
// - 无仓位：用 sig.RuleDecision 直接返回开仓/等待（不请求 pav4）
// - 有仓位：请求 pav4，并转换为你的 Decision（带止损方向约束）
func BuildDecision(
	symbol string,
	side string,
	pos *PositionInput, // 传 nil 或 qty=0 表示无仓位
	market MarketInput,
	leverage int,
	positionSizeUSD float64,
) (Decision, error) {

	// 统一 symbol 取 sig 优先，否则取 pos/market 由调用方保证一致
	symbol = strings.TrimSpace(symbol)
	if symbol == "" && pos != nil {
		symbol = strings.TrimSpace(symbol) // keep empty if still empty
	}

	// 1) 无仓位：不调用 Decide，按规则返回
	if !HasPosition(pos) {
		return buildOpenDecisionFromSignal(symbol, side, market, leverage, positionSizeUSD), nil
	}

	// 2) 有仓位：忽略 sig，调用 pav4
	decideRes, err := getPav4Decide(symbol, side, market, pos)

	if err != nil {
		// pav4 失败：返回 hold + 错误原因（同时把 err 抛给上层）
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("pav4 decide error: %v", err),
		}, err
	}

	return convertPav4ToDecision(symbol, *pos, decideRes, market.P, leverage), nil
}

func getPav4Decide(symbol string, side string, market MarketInput, pos *PositionInput) (*DecideResponse, error) {
	pav4 := NewClient("http://127.0.0.1:8085")

	// 2) 有仓位：忽略 sig，调用 pav4
	if pav4 == nil {
		return &DecideResponse{
			Action:        "HOLD",
			NewSLPrice:    nil,
			Reason:        "pav4 client is nil",
			State:         DecideState{},
			InvalidReason: nil,
		}, fmt.Errorf("pav4 client is nil")
	}

	req := DecideRequest{
		Symbol:   symbol,
		NowTS:    time.Now().UnixMilli(),
		Position: *pos,
		Market:   market,
	}

	resp, err := pav4.Decide(req)
	fmt.Printf("Decide response %v\n", resp)
	if err != nil {
		// pav4 失败：返回 hold + 错误原因（同时把 err 抛给上层）
		return &DecideResponse{
			Action:        "HOLD",
			NewSLPrice:    nil,
			Reason:        fmt.Sprintf("pav4 decide error: %v", err),
			State:         DecideState{},
			InvalidReason: nil,
		}, err
	}

	return resp, nil
}

func buildOpenDecisionFromSignal(symbol string, side string, market MarketInput, leverage int, positionSizeUSD float64) Decision {
	rd := strings.ToUpper(strings.TrimSpace(side))
	price := market.P

	if leverage <= 0 || price <= 0 {
		return Decision{
			Symbol:    symbol,
			Action:    "wait",
			Reasoning: fmt.Sprintf("invalid leverage/price; leverage=%d price=%.8f rule_decision=%s", leverage, price, rd),
		}
	}

	input := &PositionInput{
		Side:           rd,
		EntryPrice:     market.P,
		Qty:            positionSizeUSD / price,
		EntryTS:        time.Now().UnixMilli(),
		CurrentSLPrice: nil,
		CurrentTPPrice: nil,
	}

	decide := &DecideResponse{
		Action:        "",
		NewSLPrice:    nil,
		Reason:        "",
		State:         DecideState{},
		InvalidReason: nil,
	}

	if side != "" {
		d, err := getPav4Decide(symbol, side, market, input)
		if err != nil {
			decide = d
			return Decision{
				Symbol:    symbol,
				Action:    "wait",
				Reasoning: fmt.Sprintf("pav4 decide error: %v", err),
			}
		}
	}

	// 初始 TP=100%（ROE），SL=5%（ROE）
	// 转成“价格变化比例” = roe / leverage
	tpMove := 1.00 / float64(leverage)
	slMove := 0.05 / float64(leverage)

	switch rd {
	case "LONG":
		tp := price * (1.0 + tpMove)
		sl := price * (1.0 - slMove)

		if decide.State.HardSL != 0 && decide.State.HardSL < price {
			sl = decide.State.HardSL
		}
		return Decision{
			Symbol:          symbol,
			Action:          "open_long",
			Leverage:        leverage,
			PositionSizeUSD: positionSizeUSD,
			StopLoss:        sl,
			TakeProfit:      tp,
			Reasoning:       fmt.Sprintf("no position; rule_decision=LONG; TP=+100%%ROE SL=-20%%ROE => tp_move=%.6f sl_move=%.6f", tpMove, slMove),
		}

	case "SHORT":
		tp := price * (1.0 - tpMove)
		sl := price * (1.0 + slMove)

		if decide.State.HardSL != 0 && decide.State.HardSL > price {
			sl = decide.State.HardSL
		}

		return Decision{
			Symbol:          symbol,
			Action:          "open_short",
			Leverage:        leverage,
			PositionSizeUSD: positionSizeUSD,
			StopLoss:        sl,
			TakeProfit:      tp,
			Reasoning:       fmt.Sprintf("no position; rule_decision=SHORT; TP=+100%%ROE SL=-20%%ROE => tp_move=%.6f sl_move=%.6f", tpMove, slMove),
		}

	case "WAIT", "":
		return Decision{
			Symbol:    symbol,
			Action:    "wait",
			Reasoning: fmt.Sprintf("no position; rule_decision=%s", rd),
		}

	default:
		return Decision{
			Symbol:    symbol,
			Action:    "wait",
			Reasoning: fmt.Sprintf("no position; unknown rule_decision=%s", rd),
		}
	}
}

func convertPav4ToDecision(symbol string, pos PositionInput, resp *DecideResponse, price float64, leverage int) Decision {
	const minMove = 10.0 // 止盈/止损最小移动价差阈值

	side := strings.ToUpper(strings.TrimSpace(pos.Side))

	switch resp.Action {
	case "HOLD":
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: resp.Reason,
		}

	case "CLOSE_ALL":
		if side == "LONG" {
			return Decision{Symbol: symbol, Action: "close_long", Reasoning: resp.Reason}
		}
		if side == "SHORT" {
			return Decision{Symbol: symbol, Action: "close_short", Reasoning: resp.Reason}
		}
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("pav4 CLOSE_ALL but unknown pos.side=%s; %s", side, resp.Reason),
		}

	case "MOVE_SL":
		if resp.NewSLPrice == nil {
			return Decision{Symbol: symbol, Action: "hold", Reasoning: "pav4 MOVE_SL but new_sl_price is null"}
		}
		newSL := *resp.NewSLPrice
		curSL := pos.CurrentSLPrice

		// 价差阈值：新旧SL价差 < 10 不移动（curSL为空则允许设置）
		if curSL != nil {
			delta := math.Abs(newSL - *curSL)
			if delta < minMove {
				return Decision{
					Symbol:    symbol,
					Action:    "hold",
					Reasoning: fmt.Sprintf("reject MOVE_SL: |new_sl-current_sl|=%.8f < %.2f; %s", delta, minMove, resp.Reason),
				}
			}
		}

		if side == "LONG" {
			if curSL != nil && newSL <= *curSL {
				return Decision{
					Symbol:    symbol,
					Action:    "hold",
					Reasoning: fmt.Sprintf("reject MOVE_SL for LONG: new_sl=%.8f <= current_sl=%.8f; %s", newSL, *curSL, resp.Reason),
				}
			}
			return Decision{Symbol: symbol, Action: "update_stop_loss", NewStopLoss: newSL, Reasoning: resp.Reason}
		}

		if side == "SHORT" {
			if curSL != nil && newSL >= *curSL {
				return Decision{
					Symbol:    symbol,
					Action:    "hold",
					Reasoning: fmt.Sprintf("reject MOVE_SL for SHORT: new_sl=%.8f >= current_sl=%.8f; %s", newSL, *curSL, resp.Reason),
				}
			}
			return Decision{Symbol: symbol, Action: "update_stop_loss", NewStopLoss: newSL, Reasoning: resp.Reason}
		}

		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("pav4 MOVE_SL but unknown pos.side=%s; %s", side, resp.Reason),
		}

	case "MOVE_TP":
		// 1) 基础校验：price/leverage
		if leverage <= 0 || price <= 0 {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject MOVE_TP: invalid leverage/price leverage=%d price=%.8f; %s", leverage, price, resp.Reason),
			}
		}

		// 2) TP=100%ROE -> price move = 1.00/leverage
		tpMove := 1.00 / float64(leverage)

		if side == "LONG" {
			newTP := price * (1.0 + tpMove)
			curTP := pos.CurrentTPPrice

			// 价差阈值：新旧TP价差 < 10 不移动（curTP为空则允许设置）
			if curTP != nil {
				delta := math.Abs(newTP - *curTP)
				if delta < minMove {
					return Decision{
						Symbol:    symbol,
						Action:    "hold",
						Reasoning: fmt.Sprintf("reject MOVE_TP: |new_tp-current_tp|=%.8f < %.2f; %s", delta, minMove, resp.Reason),
					}
				}
			}

			if curTP != nil && newTP <= *curTP { // 相同或更低不更新
				return Decision{
					Symbol:    symbol,
					Action:    "hold",
					Reasoning: fmt.Sprintf("reject MOVE_TP for LONG: new_tp=%.8f <= current_tp=%.8f; %s", newTP, *curTP, resp.Reason),
				}
			}

			return Decision{
				Symbol:        symbol,
				Action:        "update_take_profit",
				NewTakeProfit: newTP,
				Reasoning:     fmt.Sprintf("%s | tp_move=%.6f", resp.Reason, tpMove),
			}
		}

		if side == "SHORT" {
			newTP := price * (1.0 - tpMove)
			curTP := pos.CurrentTPPrice

			// 价差阈值：新旧TP价差 < 10 不移动（curTP为空则允许设置）
			if curTP != nil {
				delta := math.Abs(newTP - *curTP)
				if delta < minMove {
					return Decision{
						Symbol:    symbol,
						Action:    "hold",
						Reasoning: fmt.Sprintf("reject MOVE_TP: |new_tp-current_tp|=%.8f < %.2f; %s", delta, minMove, resp.Reason),
					}
				}
			}

			if curTP != nil && newTP >= *curTP { // 相同或更高不更新
				return Decision{
					Symbol:    symbol,
					Action:    "hold",
					Reasoning: fmt.Sprintf("reject MOVE_TP for SHORT: new_tp=%.8f >= current_tp=%.8f; %s", newTP, *curTP, resp.Reason),
				}
			}

			return Decision{
				Symbol:        symbol,
				Action:        "update_take_profit",
				NewTakeProfit: newTP,
				Reasoning:     fmt.Sprintf("%s | tp_move=%.6f", resp.Reason, tpMove),
			}
		}

		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("pav4 MOVE_TP but unknown pos.side=%s; %s", side, resp.Reason),
		}

	default:
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("unhandled pav4 action=%s; %s", resp.Action, resp.Reason),
		}
	}
}
