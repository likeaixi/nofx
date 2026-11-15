package api

import (
	"testing"
)

func TestNormalizePositionFieldsAddsContractAliases(t *testing.T) {
	positions := []map[string]interface{}{
		{
			"symbol":           "BTCUSDT",
			"side":             "long",
			"positionAmt":      0.5,
			"entryPrice":       100.0,
			"markPrice":        110.0,
			"unRealizedProfit": 5.0,
			"leverage":         10.0,
		},
	}
	normalizePositionFields(positions)

	p := positions[0]
	if p["entry_price"] != 100.0 {
		t.Fatal("entry_price alias missing", p)
	}
	if p["mark_price"] != 110.0 {
		t.Fatal("mark_price alias missing", p)
	}
	if p["unrealized_pnl"] != 5.0 {
		t.Fatal("unrealized_pnl alias missing", p)
	}
	if p["quantity"] != 0.5 {
		t.Fatal("quantity alias missing", p)
	}
	// ROE% = pnl / (entry*|qty|/lev) * 100 = 5 / (100*0.5/10) * 100 = 100
	if p["unrealized_pnl_pct"] != 100.0 {
		t.Fatal("unrealized_pnl_pct wrong", p["unrealized_pnl_pct"])
	}
	// camelCase keys must survive for the engine-side contract.
	if p["entryPrice"] != 100.0 || p["positionAmt"] != 0.5 {
		t.Fatal("camelCase keys must be preserved", p)
	}
}

func TestNormalizePositionFieldsShortAndGuards(t *testing.T) {
	positions := []map[string]interface{}{
		{
			"symbol":           "ETHUSDT",
			"side":             "short",
			"positionAmt":      -2.0,
			"entryPrice":       0.0, // zero entry: pct must not divide by zero
			"markPrice":        0.0,
			"unRealizedProfit": -1.0,
			"leverage":         0.0,
		},
	}
	normalizePositionFields(positions)

	p := positions[0]
	if p["quantity"] != 2.0 {
		t.Fatal("quantity must be absolute", p["quantity"])
	}
	if v, ok := p["unrealized_pnl_pct"].(float64); !ok || v != 0 {
		t.Fatal("pct must guard zero margin", p["unrealized_pnl_pct"])
	}
}

func TestNormalizePositionFieldsKeepsExistingSnakeCase(t *testing.T) {
	positions := []map[string]interface{}{
		{"symbol": "X", "entry_price": 42.0},
	}
	normalizePositionFields(positions)
	if positions[0]["entry_price"] != 42.0 {
		t.Fatal("pre-existing snake_case values must not be overwritten")
	}
}
