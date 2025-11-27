// spider_strategy.go
//
// 一套 Spider + C1/C3/C5 的开仓决策 & 止盈止损逻辑。
// - 开仓决策：只控制“是否允许开新仓”，具体下单/仓位大小/TP/SL 仍用你原有逻辑。
// - 止盈止损：按蜘蛛丝结构 + 盈利百分比，自动上移/下压止损，且只朝有利方向移动。

package strategy

import (
	"fmt"
	"github.com/shopspring/decimal"
	"math"
	"nofx/decision"
)

///////////////////////
// 基础类型 & 常量定义 //
///////////////////////

// PositionState 当前是否持仓
//type PositionState string

const (
	PositionStateFlat  = "NEUTRAL"
	PositionStateLong  = "LONG"
	PositionStateShort = "SHORT"
)

// SignalSide C1/C3/C5 组合方向

const (
	SignalSideLong  string = "LONG"
	SignalSideShort string = "SHORT"
	SignalSideFlat  string = "NEUTRAL"
)

// SpiderPos 价格相对某一蜘蛛丝带 [L,U] 的位置
type SpiderPos string

const (
	PosBelow  SpiderPos = "BELOW"
	PosInside SpiderPos = "INSIDE"
	PosAbove  SpiderPos = "ABOVE"
)

// SpiderState 蜘蛛丝形态 9 个原子状态（及扩展）
// 实际上 BREAK_INVALID_* 在执行层可以和 ACCEPT_* 视作类似含义。
type SpiderState string

const (
	SpiderStateUnknown          SpiderState = "UNKNOWN"
	SpiderStateNoAcceptUp       SpiderState = "NO_ACCEPT_UP"
	SpiderStateNoAcceptDown     SpiderState = "NO_ACCEPT_DOWN"
	SpiderStateAcceptInBand     SpiderState = "ACCEPT_IN_BAND"
	SpiderStateAcceptAbove      SpiderState = "ACCEPT_ABOVE"
	SpiderStateAcceptBelow      SpiderState = "ACCEPT_BELOW"
	SpiderStateBreakInvalidUp   SpiderState = "BREAK_INVALID_UP"
	SpiderStateBreakInvalidDown SpiderState = "BREAK_INVALID_DOWN"
)

// OpenDecision 是否开新仓（本模块只负责这一层）
type OpenDecision int

const (
	DecisionNoTrade       OpenDecision = iota // 不开仓
	DecisionOpenLong                          // 允许开多
	DecisionOpenShort                         // 允许开空
	DecisionNoNewPosition                     // 已持仓，不开新仓
)

// Bias / 信号有效时间阈值（可根据实际情况改）
const (
	BIAS_TH_LONG    = 0.20 // bias >= +0.20 才允许做多
	BIAS_TH_SHORT   = -0.20
	SIG_MAX_AGE     = 15 // 信号最大有效时间（分钟）
	SIG_OPEN_WINDOW = 7  // 允许开新仓的时间窗（分钟）
)

//////////////////////
// 蜘蛛丝形态判定层 //
//////////////////////

// SpiderEventInput 描述一次“触碰/穿越”事件相对于价格带 [L,U] 的位置变化
type SpiderEventInput struct {
	PosBefore SpiderPos    // 事件前一根 K 线收盘位置
	PosEvent  SpiderPos    // 事件当根 K 线收盘位置
	PosNext   [3]SpiderPos // 事件后连续 3 根 1mK 收盘位置
	Touched   bool         // 事件 K 的高/低是否触碰过 [L,U]
}

// ClassifySpiderPos 根据 close 与 [low, high] 判定 ABOVE/INSIDE/BELOW
func ClassifySpiderPos(close, low, high float64) SpiderPos {
	if close < low {
		return PosBelow
	}
	if close > high {
		return PosAbove
	}
	return PosInside
}

// EvaluateSpiderState 按文档规则把一次事件映射为蜘蛛丝形态状态
func EvaluateSpiderState(ev SpiderEventInput) SpiderState {
	posBefore := ev.PosBefore
	posEvent := ev.PosEvent
	posNext := ev.PosNext
	touched := ev.Touched

	countNext := func(pred func(SpiderPos) bool) int {
		n := 0
		for _, p := range posNext {
			if pred(p) {
				n++
			}
		}
		return n
	}

	allNext := func(pred func(SpiderPos) bool) bool {
		for _, p := range posNext {
			if !pred(p) {
				return false
			}
		}
		return true
	}

	// 1.1 未站稳（NO_ACCEPT）

	// ① 原本在下方 → 向上触碰 → 又回到下方
	if posBefore == PosBelow &&
		touched &&
		countNext(func(p SpiderPos) bool { return p == PosBelow }) >= 2 {
		return SpiderStateNoAcceptUp
	}

	// ② 原本在下方 → 向上穿越到上方 → 又被打回带内/带下
	if posBefore == PosBelow &&
		posEvent == PosAbove &&
		countNext(func(p SpiderPos) bool { return p == PosInside || p == PosBelow }) >= 2 {
		return SpiderStateNoAcceptUp
	}

	// ③ 原本在上方 → 向下触碰 → 又回到上方
	if posBefore == PosAbove &&
		touched &&
		countNext(func(p SpiderPos) bool { return p == PosAbove }) >= 2 {
		return SpiderStateNoAcceptDown
	}

	// ④ 原本在上方 → 向下穿越到下方 → 又被拉回带内/带上
	if posBefore == PosAbove &&
		posEvent == PosBelow &&
		countNext(func(p SpiderPos) bool { return p == PosInside || p == PosAbove }) >= 2 {
		return SpiderStateNoAcceptDown
	}

	// 1.2 站稳（ACCEPT）

	// ⑤ 触碰后 3 根都在价格带内 → 带内站稳
	if touched &&
		allNext(func(p SpiderPos) bool { return p == PosInside }) {
		return SpiderStateAcceptInBand
	}

	// ⑥ 从带内/下方 → 向上穿越 → 3 根都在带上方
	if (posBefore == PosBelow || posBefore == PosInside) &&
		posEvent == PosAbove &&
		allNext(func(p SpiderPos) bool { return p == PosAbove }) {
		return SpiderStateAcceptAbove
	}

	// ⑦ 从带内/上方 → 向下穿越 → 3 根都在带下方
	if (posBefore == PosAbove || posBefore == PosInside) &&
		posEvent == PosBelow &&
		allNext(func(p SpiderPos) bool { return p == PosBelow }) {
		return SpiderStateAcceptBelow
	}

	// 1.3 穿越无效（BREAK_INVALID）
	// 实际执行层可视为“这轮对某侧拦截失效”

	// ⑧ 从上到下 → 干净穿越 → 一直呆在带下方
	if posBefore == PosAbove &&
		posEvent == PosBelow &&
		allNext(func(p SpiderPos) bool { return p == PosBelow }) {
		return SpiderStateBreakInvalidDown
	}

	// ⑨ 从下到上 → 干净穿越 → 一直呆在带上方
	if posBefore == PosBelow &&
		posEvent == PosAbove &&
		allNext(func(p SpiderPos) bool { return p == PosAbove }) {
		return SpiderStateBreakInvalidUp
	}

	return SpiderStateUnknown
}

////////////////////////////
// 形态映射：多/空可交易性 //
////////////////////////////

// ShapeOKLong 对做多来说，支撑形态是否允许做多
func ShapeOKLong(s SpiderState) bool {
	switch s {
	case SpiderStateAcceptInBand,
		SpiderStateAcceptAbove,
		SpiderStateNoAcceptUp,
		SpiderStateNoAcceptDown:
		return true
	default:
		return false
	}
}

// ShapeForbidLong 对做多来说，是否“极端禁止”
// （支撑被真跌破并在下方站稳）
func ShapeForbidLong(s SpiderState) bool {
	switch s {
	case SpiderStateBreakInvalidDown, SpiderStateAcceptBelow:
		return true
	default:
		return false
	}
}

// ShapeOKShort 对做空来说，压力形态是否允许做空
func ShapeOKShort(s SpiderState) bool {
	switch s {
	case SpiderStateAcceptInBand,
		SpiderStateAcceptBelow,
		SpiderStateNoAcceptUp,
		SpiderStateNoAcceptDown:
		return true
	default:
		return false
	}
}

// ShapeForbidShort 对做空来说，是否“极端禁止”
// （压力被干净上破并在上方站稳）
func ShapeForbidShort(s SpiderState) bool {
	switch s {
	case SpiderStateBreakInvalidUp, SpiderStateAcceptAbove:
		return true
	default:
		return false
	}
}

/////////////////////////
// 信号 + Bias 开仓决策 //
/////////////////////////

// OpenDecisionContext 决策树输入
type OpenDecisionContext struct {
	// 顶层：当前持仓状态
	PositionState string

	// Bias 门控
	Bias float64

	// 信号层（C1/C3/C5 组合）
	SignalSide    string
	SignalValid   bool
	SignalAgeMin  int // 当前信号年龄（分钟）
	SigMaxAge     int // 可选，0 使用默认 SIG_MAX_AGE
	SigOpenWindow int // 可选，0 使用默认 SIG_OPEN_WINDOW

	// 蜘蛛丝形态（可为 nil 表示当前无有效蜘蛛丝）
	//SupportState *SpiderState // 用于多头
	//ResistState  *SpiderState // 用于空头
}

// DecideOpenPosition 综合决策是否允许开新仓（只在 NEUTRAL 才会考虑开仓）
func DecideOpenPosition(ctx OpenDecisionContext, symbol string, price decimal.Decimal, leverage decimal.Decimal, spider *decision.SpiderSnapshot) decision.Decision {
	var dec = decision.Decision{}
	dec.Symbol = symbol
	dec.Action = "wait"
	dec.Reasoning = "没有开仓信号"

	// 5.1 只在空仓时走这棵树
	if ctx.PositionState != PositionStateFlat {
		//return DecisionNoNewPosition
		dec.Reasoning = "已有仓位"
		return dec
	}

	maxAge := ctx.SigMaxAge
	if maxAge <= 0 {
		maxAge = SIG_MAX_AGE
	}
	openWindow := ctx.SigOpenWindow
	if openWindow <= 0 {
		openWindow = SIG_OPEN_WINDOW
	}

	// 5.2 信号有效性判断
	if !ctx.SignalValid {
		//return DecisionNoTrade

		dec.Reasoning = "信号无效"
		return dec
	}
	if ctx.SignalSide == SignalSideFlat {
		//return DecisionNoTrade

		dec.Reasoning = "AI信号为" + SignalSideFlat
		return dec
	}
	if ctx.SignalAgeMin > maxAge {
		//return DecisionNoTrade
		dec.Reasoning = "信号已过期"
		return dec
	}
	if ctx.SignalAgeMin > openWindow {
		// 超过开仓时间窗，只用于管理，不开新仓
		//return DecisionNoTrade
		dec.Reasoning = "超过开仓时间窗"
		return dec
	}

	//// 准备形态布尔
	//var (
	//	shapeOKLong, shapeForbidLong   bool
	//	shapeOKShort, shapeForbidShort bool
	//)

	//if ctx.SupportState != nil {
	//	shapeOKLong = ShapeOKLong(*ctx.SupportState)
	//	shapeForbidLong = ShapeForbidLong(*ctx.SupportState)
	//}
	//if ctx.ResistState != nil {
	//	shapeOKShort = ShapeOKShort(*ctx.ResistState)
	//	shapeForbidShort = ShapeForbidShort(*ctx.ResistState)
	//}

	// 5.3 做多分支
	if ctx.SignalSide == SignalSideLong {
		// 极端禁止：支撑被真跌破，这轮不拿它做锚
		//if shapeForbidLong {
		//	return DecisionNoTrade
		//}

		// 1) Bias 门控
		if ctx.Bias < BIAS_TH_LONG {
			//return DecisionNoTrade
			dec.Reasoning = "Bias不满足条件"
			return dec
		}

		//// 2) 形态门控
		//if !shapeOKLong {
		//	return DecisionNoTrade
		//}

		// 3) 通过两层门控，允许开多
		dec.Action = "open_long"
		dec.Reasoning = "LONG"

		sl := spider.SupLow
		tp := spider.ResHigh

		dec.StopLoss = sl
		dec.TakeProfit = tp
	}

	// 5.4 做空分支
	if ctx.SignalSide == SignalSideShort {
		// 极端禁止
		//if shapeForbidShort {
		//	return DecisionNoTrade
		//}

		// 1) Bias 门控
		if ctx.Bias > BIAS_TH_SHORT { // 注意：BIAS_TH_SHORT 是负数
			//return DecisionNoTrade
			dec.Reasoning = "Bias不满足条件"
			return dec
		}

		//// 2) 形态门控
		//if !shapeOKShort {
		//	return DecisionNoTrade
		//}

		//return DecisionOpenShort
		dec.Action = "open_short"
		dec.Reasoning = "SHORT"
		sl := spider.ResHigh
		tp := spider.SupLow

		dec.StopLoss = sl
		dec.TakeProfit = tp
	}

	// 5.5 兜底
	return dec
}

////////////////////////////////////
// 止盈止损：蜘蛛丝结构 + 盈利百分比 //
////////////////////////////////////

// UpdateSLWithSpider 按你给的伪代码接口实现：
// def update_sl_with_spider(
//
//	side, entry, sl_current, price_now,
//	S0, R0, SUP_LOW, SUP_HIGH, RES_LOW, RES_HIGH,
//	unrealized_profit=None, initial_margin=None
//
// ):
//
// Go 版本：unrealizedProfit & initialMargin <=0 时视为不用盈利部分。
func UpdateSLWithSpider(
	symbol string,
	side string,
	entry, slCurrent, priceNow float64,
	S0, R0, supLow, supHigh, resLow, resHigh float64,
	unrealizedProfit, initialMargin float64,
) decision.Decision {
	var dec = decision.Decision{}
	dec.Symbol = symbol
	dec.Action = "hold"
	dec.Reasoning = "没有退出信号"

	var slNew float64
	var shouldClose bool

	bandWidth := R0 - S0
	midBand := (S0 + R0) / 2.0

	// 1) 结构部分
	slStruct := slCurrent

	if side == SignalSideLong {
		// 多单结构档位（只往上抬）
		if priceNow >= supHigh {
			slStruct = math.Max(slStruct, supLow)
		}
		if priceNow >= midBand {
			slStruct = math.Max(slStruct, math.Max(entry, supHigh))
		}
		if priceNow >= resLow {
			slStruct = math.Max(slStruct, midBand)
		}
		if priceNow >= resHigh {
			slStruct = math.Max(slStruct, resLow)
		}
	} else if side == SignalSideShort {
		// 空单结构档位（只往下压）
		if priceNow <= resLow {
			slStruct = math.Min(slStruct, resHigh)
		}
		if priceNow <= midBand {
			slStruct = math.Min(slStruct, math.Min(entry, resLow))
		}
		if priceNow <= supHigh {
			slStruct = math.Min(slStruct, midBand)
		}
		if priceNow <= supLow {
			slStruct = math.Min(slStruct, supHigh)
		}
	}

	// 2) 盈利部分（可选）
	var (
		profitPct float64
		useProfit bool
	)

	if initialMargin > 0 {
		useProfit = true
		profitPct = unrealizedProfit / initialMargin
	}

	slProfit := slCurrent

	if useProfit {
		if side == SignalSideLong {
			// 多单：盈利 15% → SL >= entry
			if profitPct >= 0.15 {
				slProfit = math.Max(slProfit, entry)
			}
			// 盈利 40% → SL >= entry*(1+15%)
			if profitPct >= 0.40 {
				slProfit = math.Max(slProfit, entry*1.15)
			}
		} else if side == SignalSideShort {
			// 空单对称：盈利 15% → SL <= entry
			if profitPct >= 0.15 {
				slProfit = math.Min(slProfit, entry)
			}
			// 盈利 40% → SL <= entry*(1-15%)
			if profitPct >= 0.40 {
				slProfit = math.Min(slProfit, entry*(1.0-0.15))
			}
		}
	}

	// 3) 汇总
	if side == SignalSideLong {
		// 多单：取更“保守”的（更靠上的）
		slNew = math.Max(slCurrent, math.Max(slStruct, slProfit))
		shouldClose = priceNow <= slNew
	} else if side == SignalSideShort {
		// 空单：取更“紧”的（更靠下的）
		slNew = math.Min(slCurrent, math.Min(slStruct, slProfit))
		shouldClose = priceNow >= slNew
	} else {
		// 非 LONG/SHORT，理论上不会出现，直接原样返回
		slNew = slCurrent
		shouldClose = false
	}

	// bandWidth 目前只用于 midBand 计算，若 bandWidth<=0，
	// 结构部分仍然不会把止损移动到对自己不利的位置。
	_ = bandWidth

	if shouldClose {
		if side == SignalSideLong {
			dec.Action = "close_long"
			dec.Reasoning = fmt.Sprintf("当前价格需要止损，当前价格/止损价格: %v/%v", priceNow, slNew)
		}

		if side == SignalSideShort {
			dec.Action = "close_short"
			dec.Reasoning = fmt.Sprintf("当前价格需要止损，当前价格/止损价格: %v/%v", priceNow, slNew)
		}

	} else {
		if slNew != slCurrent {
			dec.Action = "update_stop_loss"
			dec.NewStopLoss = slNew
			dec.Reasoning = fmt.Sprintf("移动止损价格到 %v", slNew)
		}
	}

	return dec
}
