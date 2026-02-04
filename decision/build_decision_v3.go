package decision

import (
	"fmt"
	"strings"
	"time"
)

// BuildDecisionV3：
// - 无仓位：按规则开仓/等待（不请求 pav4）
// - 有仓位：不调用 pav4，根据持仓时间决定是否平仓（>15 分钟才平仓）
func BuildDecisionV3(
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
		symbol = strings.TrimSpace(symbol)
	}

	// 1) 无仓位：按规则返回开仓/等待
	if !HasPosition(pos) {
		return buildOpenDecisionFromSignalV3(symbol, side, market, leverage, positionSizeUSD), nil
	}

	// 2) 有仓位：根据持仓时间决定是否平仓
	if pos == nil {
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: "position missing while qty>0",
		}, nil
	}

	if pos.EntryTS <= 0 {
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: "position entry_ts missing",
		}, nil
	}

	const holdMillis = 15 * time.Minute
	heldFor := time.Since(time.UnixMilli(pos.EntryTS))
	if heldFor < holdMillis {
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("held %s < 15m", heldFor.Truncate(time.Second)),
		}, nil
	}

	switch strings.ToUpper(strings.TrimSpace(pos.Side)) {
	case "LONG":
		return Decision{
			Symbol:    symbol,
			Action:    "close_long",
			Reasoning: fmt.Sprintf("held %s >= 15m", heldFor.Truncate(time.Second)),
		}, nil
	case "SHORT":
		return Decision{
			Symbol:    symbol,
			Action:    "close_short",
			Reasoning: fmt.Sprintf("held %s >= 15m", heldFor.Truncate(time.Second)),
		}, nil
	default:
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("unknown position side=%s", pos.Side),
		}, nil
	}
}

func buildOpenDecisionFromSignalV3(symbol string, side string, market MarketInput, leverage int, positionSizeUSD float64) Decision {
	rd := strings.ToUpper(strings.TrimSpace(side))
	price := market.P

	// 基础参数校验
	if leverage <= 0 || price <= 0 || positionSizeUSD <= 0 {
		return Decision{
			Symbol:    symbol,
			Action:    "wait",
			Reasoning: fmt.Sprintf("invalid params; leverage=%d price=%.2f position_size_usd=%.2f rule_decision=%s", leverage, price, positionSizeUSD, rd),
		}
	}

	// 初始 TP=+100% ROE，SL=-20% ROE
	// 转成“价格变化比例” = roe / leverage
	tpMove := 1.00 / float64(leverage)
	slMove := 0.20 / float64(leverage)

	switch rd {
	case "LONG":
		tp := price * (1.0 + tpMove)
		sl := price * (1.0 - slMove)

		return Decision{
			Symbol:          symbol,
			Action:          "open_long",
			Leverage:        leverage,
			PositionSizeUSD: positionSizeUSD,
			StopLoss:        sl,
			TakeProfit:      tp,
			Reasoning:       fmt.Sprintf("no position; rule_decision=LONG; entry=%.2f tp=%.2f sl=%.2f", price, tp, sl),
		}

	case "SHORT":
		tp := price * (1.0 - tpMove)
		sl := price * (1.0 + slMove)

		return Decision{
			Symbol:          symbol,
			Action:          "open_short",
			Leverage:        leverage,
			PositionSizeUSD: positionSizeUSD,
			StopLoss:        sl,
			TakeProfit:      tp,
			Reasoning:       fmt.Sprintf("no position; rule_decision=SHORT; entry=%.2f tp=%.2f sl=%.2f", price, tp, sl),
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
