package binance

import (
	"context"
	"errors"
	"nofx/store"
	"strconv"
	"strings"

	"nofx/trader/execution"
)

// VerifyCopyPosition reads current native size, direction and (for an add)
// leverage instead of adopting the cache used by the dashboard.
func (b *FuturesTrader) VerifyCopyPosition(ctx context.Context, symbol, side, qty string, checkLeverage bool) error {
	ctx = execution.WithProtectivePriority(ctx)
	rows, err := b.client.NewGetPositionRiskService().Symbol(symbol).Do(ctx)
	if err != nil {
		return binanceReadError("verify followed position", err)
	}
	found := false
	for _, p := range rows {
		if p.Symbol != symbol {
			return errors.New("unexpected Binance position symbol")
		}
		n, err := execution.Number(p.PositionAmt)
		if err != nil {
			return err
		}
		if n.Sign() == 0 {
			continue
		}
		held := strings.ToLower(p.PositionSide)
		if held == "both" {
			held = "long"
			if n.Sign() < 0 {
				held = "short"
			}
		}
		size, err := execution.ExactDecimal(n.Abs(n))
		if err != nil {
			return err
		}
		if found || held != side || !execution.Equal(string(size), qty) || checkLeverage && !execution.Equal(p.Leverage, strconv.Itoa(store.AllinCryptoLeverage)) {
			return errors.New("Binance position ownership or 10x leverage mismatch")
		}
		found = true
	}
	if !found && !execution.Equal(qty, "0") {
		return errors.New("expected Binance position missing")
	}
	return nil
}

// AccountSnapshot's aggregate fields can have different denomination in
// multi-asset mode. Keep each asset's own equity until it is converted.
func (b *FuturesTrader) CopyCollateral(ctx context.Context) (map[string]string, string, error) {
	a, err := b.client.NewGetAccountService().Do(ctx)
	if err != nil {
		return nil, "", binanceReadError("verify Binance collateral", err)
	}
	if !a.CanTrade || a.MultiAssetsMargin {
		return nil, "", errors.New("requires tradable Binance single-asset margin account")
	}
	funds := map[string]string{}
	available := ""
	for _, asset := range a.Assets {
		if _, ok := funds[asset.Asset]; ok {
			return nil, "", errors.New("duplicate Binance balance")
		}
		funds[asset.Asset] = asset.MarginBalance
		if asset.Asset == "USDT" {
			available = asset.AvailableBalance
		}
	}
	return funds, available, nil
}
