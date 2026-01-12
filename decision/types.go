package decision

import "nofx/market"

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

// PositionInput：新增 PnlPct
type PositionInput struct {
	Side           string   `json:"side"` // "LONG" | "SHORT"
	EntryPrice     float64  `json:"entry_price"`
	Qty            float64  `json:"qty"`
	EntryTS        int64    `json:"entry_ts"` // ms
	CurrentSLPrice *float64 `json:"current_sl_price"`
	CurrentTPPrice *float64 `json:"current_tp_price"`

	PnlPct float64 `json:"pnl_pct"` // 0.15 = 15% (ROE)
}

type MarketInput struct {
	P      float64             `json:"P"`
	Bars15 []market.InputKline `json:"bars_15m"`
}

// ROE 阶梯：达到阈值后，止损锁定 lockROE
type ROELevel struct {
	RoeAtOrAbove float64 // e.g. 0.05
	LockRoe      float64 // e.g. 0.02
}

var ROELevels = []ROELevel{
	{0.05, 0.02},
	{0.06, 0.025},
	{0.07, 0.05},
	{0.10, 0.08},
	{0.13, 0.11},
	{0.16, 0.14},
	{0.20, 0.18},
	{0.23, 0.21},
	{0.26, 0.24},
	{0.30, 0.28},
	{0.35, 0.33},
	{0.40, 0.38},
	{0.50, 0.48},
	{0.60, 0.58},
	{0.80, 0.78},
}
