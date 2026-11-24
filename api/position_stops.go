package api

import (
	"math"
	"nofx/trader/types"
	"strings"
)

// Orders are queried once per native symbol through the same authorized account
// reader as positions. available means matching orders, not full-size protection.
func enrichPositionStops(positions []map[string]interface{}, reader accountReader) {
	type result struct {
		orders []types.OpenOrder
		err    error
	}
	cache := map[string]result{}
	for _, p := range positions {
		p["stop_loss_orders"] = []types.OpenOrder{}
		p["stop_loss_status"] = "unknown"
		symbol, _ := p["symbol"].(string)
		side, _ := p["side"].(string)
		side = strings.ToUpper(side)
		if symbol == "" || (side != "LONG" && side != "SHORT") {
			continue
		}
		got, ok := cache[symbol]
		if !ok {
			got.orders, got.err = reader.GetOpenOrders(symbol)
			cache[symbol] = got
		}
		if got.err != nil {
			continue
		}
		matches := []types.OpenOrder{}
		ambiguous := false
		closeSide := "SELL"
		if side == "SHORT" {
			closeSide = "BUY"
		}
		for _, o := range got.orders {
			if o.Symbol != symbol || strings.ToUpper(o.Side) != closeSide {
				continue
			}
			kind := strings.ToUpper(o.Type)
			if kind != "STOP_MARKET" && kind != "STOP" && kind != "STOP_LOSS" && kind != "STOP_LOSS_LIMIT" {
				continue
			}
			ps := strings.ToUpper(o.PositionSide)
			if ps != "" && ps != "BOTH" && ps != "NET" && ps != side {
				continue
			}
			if (ps == "" || ps == "BOTH" || ps == "NET") && !o.ReduceOnly && !o.ClosePosition {
				ambiguous = true
				continue
			}
			status := strings.ToUpper(o.Status)
			if status != "NEW" && status != "PARTIALLY_FILLED" && status != "LIVE" {
				continue
			}
			if o.OrderID == "" || o.StopPrice <= 0 || math.IsNaN(o.StopPrice) || math.IsInf(o.StopPrice, 0) {
				ambiguous = true
				continue
			}
			matches = append(matches, o)
		}
		p["stop_loss_orders"] = matches
		p["stop_loss_status"] = "none"
		if len(matches) > 0 {
			p["stop_loss_status"] = "available"
		} else if ambiguous {
			p["stop_loss_status"] = "unknown"
		}
	}
}
