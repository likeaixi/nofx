package trader

import (
	"fmt"
	"math"
	"strings"
)

// validateOpeningMarket queries the execution venue, never the shared market
// data provider (which can return Hyperliquid data for another exchange).
// Fail closed on unavailable data; the caller persists this error in the
// decision action and execution log. Closing positions does not use this gate.
func (at *AutoTrader) validateOpeningMarket(symbol string) error {
	price, err := at.trader.GetMarketPrice(symbol)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "invalid symbol") {
			return fmt.Errorf("opening skipped: %s is not supported on target exchange %s: %w", symbol, at.exchange, err)
		}
		return fmt.Errorf("opening skipped: cannot verify market %s on target exchange %s (symbol unavailable or market query failed): %w", symbol, at.exchange, err)
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("opening skipped: target exchange %s returned an invalid market price for %s", at.exchange, symbol)
	}
	return nil
}
