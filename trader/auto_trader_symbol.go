package trader

import (
	"errors"
	"fmt"
	"math"
	"nofx/store"
	"strings"
)

type unsupportedOpeningMarket struct{ symbol, exchange string }

func (e *unsupportedOpeningMarket) Error() string {
	return fmt.Sprintf("opening skipped: %s is not supported on target exchange %s; no order placed", e.symbol, e.exchange)
}

func applyDecisionExecutionError(record *store.DecisionRecord, action *store.DecisionAction, err error) {
	var unsupported *unsupportedOpeningMarket
	if errors.As(err, &unsupported) {
		action.Skipped = true
		action.SkipReason = unsupported.Error()
		record.ExecutionLog = append(record.ExecutionLog, "⚠ "+action.SkipReason)
		return
	}
	action.Error = err.Error()
	record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s failed: %v", action.Symbol, action.Action, err))
}

// validateOpeningMarket queries the execution venue, never the shared market
// data provider (which can return Hyperliquid data for another exchange).
// Fail closed on unavailable data; the caller persists this error in the
// decision action and execution log. Closing positions does not use this gate.
func (at *AutoTrader) validateOpeningMarket(symbol string) error {
	price, err := at.trader.GetMarketPrice(symbol)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "invalid symbol") {
			return &unsupportedOpeningMarket{symbol: symbol, exchange: at.exchange}
		}
		return fmt.Errorf("opening skipped: cannot verify market %s on target exchange %s (symbol unavailable or market query failed): %w", symbol, at.exchange, err)
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("opening skipped: target exchange %s returned an invalid market price for %s", at.exchange, symbol)
	}
	return nil
}
