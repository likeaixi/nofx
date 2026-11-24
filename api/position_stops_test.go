package api

import (
	"errors"
	"nofx/trader/types"
	"testing"
)

type stopReader struct {
	accountReader
	orders []types.OpenOrder
	err    error
	calls  int
}

func (r *stopReader) GetOpenOrders(string) ([]types.OpenOrder, error) {
	r.calls++
	return r.orders, r.err
}
func TestPositionStopsDirectionAndFailure(t *testing.T) {
	positions := []map[string]interface{}{{"symbol": "BTCUSDT", "side": "long"}, {"symbol": "BTCUSDT", "side": "short"}}
	r := &stopReader{orders: []types.OpenOrder{{OrderID: "long", Symbol: "BTCUSDT", Side: "SELL", PositionSide: "LONG", Type: "STOP_MARKET", StopPrice: 90, Status: "NEW"}, {OrderID: "short", Symbol: "BTCUSDT", Side: "BUY", PositionSide: "SHORT", Type: "STOP_MARKET", StopPrice: 110, Status: "NEW"}, {OrderID: "tp", Symbol: "BTCUSDT", Side: "SELL", PositionSide: "LONG", Type: "TAKE_PROFIT_MARKET", StopPrice: 120, Status: "NEW"}}}
	enrichPositionStops(positions, r)
	if r.calls != 1 {
		t.Fatal("duplicate same-symbol read", r.calls)
	}
	for i, id := range []string{"long", "short"} {
		orders := positions[i]["stop_loss_orders"].([]types.OpenOrder)
		if len(orders) != 1 || orders[0].OrderID != id || positions[i]["stop_loss_status"] != "available" {
			t.Fatal(positions)
		}
	}
	r.err = errors.New("signed private URL")
	enrichPositionStops(positions, r)
	if positions[0]["stop_loss_status"] != "unknown" || len(positions[0]["stop_loss_orders"].([]types.OpenOrder)) != 0 {
		t.Fatal(positions)
	}
	r.err = nil
	r.orders = nil
	enrichPositionStops(positions, r)
	if positions[0]["stop_loss_status"] != "none" {
		t.Fatal(positions)
	}
}

func TestPositionStopsOneWayRequiresClosingIntent(t *testing.T) {
	for _, tc := range []struct {
		reduce, close bool
		status        string
	}{{false, false, "unknown"}, {true, false, "available"}, {false, true, "available"}} {
		p := []map[string]interface{}{{"symbol": "BTCUSDT", "side": "short"}}
		r := &stopReader{orders: []types.OpenOrder{{OrderID: "sl", Symbol: "BTCUSDT", Side: "BUY", PositionSide: "BOTH", Type: "STOP_MARKET", StopPrice: 110, Status: "NEW", ReduceOnly: tc.reduce, ClosePosition: tc.close}}}
		enrichPositionStops(p, r)
		if p[0]["stop_loss_status"] != tc.status {
			t.Fatal(p, tc)
		}
	}
}
