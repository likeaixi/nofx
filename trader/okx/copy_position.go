package okx

import (
	"context"
	"errors"

	"nofx/trader/execution"
)

func (t *OKXTrader) VerifyCopyPosition(ctx context.Context, symbol, side, qty string, checkLeverage bool) error {
	inst, err := t.executionInstrument(ctx, symbol)
	if err != nil {
		return err
	}
	rows, err := t.executionPositions(ctx, symbol)
	if err != nil {
		return err
	}
	found := false
	for _, p := range rows {
		if p.InstId != symbol {
			return errors.New("unexpected OKX position market")
		}
		n, err := execution.Number(p.Pos)
		if err != nil {
			return err
		}
		if n.Sign() == 0 {
			continue
		}
		held := p.PosSide
		if held == "net" {
			held = "long"
			if n.Sign() < 0 {
				held = "short"
			}
		}
		unit, err := execution.ExactDecimal(n.Abs(n))
		if err != nil {
			return err
		}
		base, err := execution.Multiply(string(unit), inst.CtVal)
		if err != nil {
			return err
		}
		if found || held != side || !execution.Equal(string(base), qty) || checkLeverage && (p.MgnMode != "cross" || !execution.Equal(p.Lever, "20")) {
			return errors.New("OKX position ownership, margin mode or 20x leverage mismatch")
		}
		found = true
	}
	if !found && !execution.Equal(qty, "0") {
		return errors.New("expected OKX position missing")
	}
	return nil
}

func (t *OKXTrader) CopyCollateral(ctx context.Context) (map[string]string, string, error) {
	var balances []struct {
		Details []struct{ Ccy, Eq, AvailEq, AvailBal string }
	}
	if err := t.readExecution(ctx, okxAccountPath, &balances); err != nil {
		return nil, "", err
	}
	if len(balances) != 1 {
		return nil, "", errors.New("unconfirmed OKX collateral")
	}
	funds := map[string]string{}
	available := ""
	for _, d := range balances[0].Details {
		if _, ok := funds[d.Ccy]; ok {
			return nil, "", errors.New("duplicate OKX balance")
		}
		funds[d.Ccy] = d.Eq
		if d.Ccy == "USDT" {
			available = d.AvailEq
			if available == "" {
				available = d.AvailBal
			}
		}
	}
	return funds, available, nil
}
