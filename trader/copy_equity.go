package trader

import (
	"context"
	"fmt"
	"math"
	"nofx/store"
	"time"
)

// Separate from source polling and order reconciliation: telemetry failures
// must neither block protective checks nor create a trading-blocked state.
func (at *AutoTrader) runCopyEquity(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := at.recordCopyEquity(); err != nil {
			at.logWarnf("AllinCrypto equity history: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (at *AutoTrader) recordCopyEquity() error {
	info, err := at.GetAccountInfo()
	if err != nil {
		return err
	}
	values := map[string]float64{}
	for _, key := range []string{"total_equity", "wallet_balance", "unrealized_profit", "available_balance", "margin_used_pct"} {
		v, ok := info[key].(float64)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("invalid account equity field %s", key)
		}
		values[key] = v
	}
	count, ok := info["position_count"].(int)
	if !ok || count < 0 {
		return fmt.Errorf("invalid account position count")
	}
	return at.store.Equity().Save(&store.EquitySnapshot{TraderID: at.id, Timestamp: time.Now().UTC(), TotalEquity: values["total_equity"], Balance: values["wallet_balance"], AvailableBalance: values["available_balance"], UnrealizedPnL: values["unrealized_profit"], PositionCount: count, MarginUsedPct: values["margin_used_pct"]})
}
