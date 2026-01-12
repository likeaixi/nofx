package decision

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	minMoveAbs = 10.0 // 新旧止损价差阈值：小于该值不移动
)

// HasPosition：判断是否有仓位（qty>0 且 side 为 LONG/SHORT）
func HasPositionV2(pos *PositionInput) bool {
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
// - 无仓位：用 side(规则信号) 直接返回开仓/等待
// - 有仓位：不再请求 PAV4，按 pnl_pct + ROELevels 计算移动止损
func BuildDecisionV2(
	symbol string,
	side string, // 无仓位时的规则方向: "LONG" | "SHORT" | "WAIT"
	pos *PositionInput, // nil 或 qty=0 表示无仓位
	market MarketInput,
	leverage int,
	positionSizeUSD float64,
) (Decision, error) {

	symbol = strings.TrimSpace(symbol)

	// 1) 无仓位：按规则开仓（初始 SL/TP 仍按原逻辑）
	if !HasPositionV2(pos) {
		return buildOpenDecisionFromSignalV2(symbol, side, market, leverage, positionSizeUSD), nil
	}

	// 2) 有仓位：按 pnl_pct 计算移动止损
	return buildTrailingStopDecision(symbol, *pos, market, leverage), nil
}

func buildOpenDecisionFromSignalV2(symbol string, side string, market MarketInput, leverage int, positionSizeUSD float64) Decision {
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

	// 初始 TP=+100% ROE，SL=-5% ROE
	// 转成“价格变化比例” = roe / leverage
	tpMove := 1.00 / float64(leverage)
	slMove := 0.03 / float64(leverage)

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

func buildTrailingStopDecision(symbol string, pos PositionInput, market MarketInput, leverage int) Decision {
	side := strings.ToUpper(strings.TrimSpace(pos.Side))

	if leverage <= 0 || pos.EntryPrice <= 0 || market.P <= 0 {
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("invalid leverage/price; leverage=%d entry=%.8f price=%.8f", leverage, pos.EntryPrice, market.P),
		}
	}

	pos.PnlPct = pos.PnlPct / 100

	lockedROE, ok := lockedRoeByPnlPct(pos.PnlPct)
	if !ok || lockedROE <= 0 {
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("pnl_pct=%.6f below trailing threshold; hold", pos.PnlPct),
		}
	}

	// ROE -> price move
	move := lockedROE / float64(leverage)

	var targetSL float64
	switch side {
	case "LONG":
		targetSL = pos.EntryPrice * (1.0 + move)
		// 防止止损 >= 当前价（会立刻触发）
		if targetSL >= market.P {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject move_sl: target_sl=%.8f >= price=%.8f (would trigger). pnl_pct=%.6f locked_roe=%.6f", targetSL, market.P, pos.PnlPct, lockedROE),
			}
		}
	case "SHORT":
		targetSL = pos.EntryPrice * (1.0 - move)
		// 防止止损 <= 当前价（会立刻触发）
		if targetSL <= market.P {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject move_sl: target_sl=%.8f <= price=%.8f (would trigger). pnl_pct=%.6f locked_roe=%.6f", targetSL, market.P, pos.PnlPct, lockedROE),
			}
		}
	default:
		return Decision{
			Symbol:    symbol,
			Action:    "hold",
			Reasoning: fmt.Sprintf("unknown pos.side=%s", side),
		}
	}

	// 阈值：新旧 SL 变化太小则不动
	if pos.CurrentSLPrice != nil {
		delta := math.Abs(targetSL - *pos.CurrentSLPrice)
		if delta < minMoveAbs {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject move_sl: |target-current|=%.8f < %.2f; pnl_pct=%.6f locked_roe=%.6f", delta, minMoveAbs, pos.PnlPct, lockedROE),
			}
		}
	}

	// 方向约束：只能更“锁盈”
	if side == "LONG" {
		if pos.CurrentSLPrice != nil && targetSL <= *pos.CurrentSLPrice {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject move_sl for LONG: target_sl=%.8f <= current_sl=%.8f; pnl_pct=%.6f locked_roe=%.6f", targetSL, *pos.CurrentSLPrice, pos.PnlPct, lockedROE),
			}
		}
	}
	if side == "SHORT" {
		if pos.CurrentSLPrice != nil && targetSL >= *pos.CurrentSLPrice {
			return Decision{
				Symbol:    symbol,
				Action:    "hold",
				Reasoning: fmt.Sprintf("reject move_sl for SHORT: target_sl=%.8f >= current_sl=%.8f; pnl_pct=%.6f locked_roe=%.6f", targetSL, *pos.CurrentSLPrice, pos.PnlPct, lockedROE),
			}
		}
	}

	return Decision{
		Symbol:      symbol,
		Action:      "update_stop_loss",
		NewStopLoss: targetSL,
		Reasoning: fmt.Sprintf(
			"trail_sl by pnl_pct; pnl_pct=%.6f locked_roe=%.6f move=%.8f entry=%.8f price=%.8f target_sl=%.8f at=%d",
			pos.PnlPct, lockedROE, move, pos.EntryPrice, market.P, targetSL, time.Now().UnixMilli(),
		),
	}
}

// 根据 pnl_pct(ROE) 选择应锁定的 ROE（取“满足阈值的最后一档”）
func lockedRoeByPnlPct(pnlPct float64) (lock float64, ok bool) {
	lock = 0
	ok = false
	for _, lv := range ROELevels {
		if pnlPct >= lv.RoeAtOrAbove {
			lock = lv.LockRoe
			ok = true
			continue
		}
		break
	}
	return lock, ok
}
