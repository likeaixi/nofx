package binancecopy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSnapshotPaginationAndIdentity(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/detail" {
			fmt.Fprintf(w, `{"success":true,"code":"000000","data":{"leadPortfolioId":"5075281354358777856","futuresType":"UM","initInvestAsset":"USDT","status":"ACTIVE","marginBalance":"24.50","aumAmount":"99999"}}`)
			return
		}
		var page struct{ PageNumber int }
		_ = json.NewDecoder(r.Body).Decode(&page)
		if mode == "business" {
			fmt.Fprint(w, `{"code":"error","success":false,"data":{"list":[]}}`)
			return
		}
		n := 3 - page.PageNumber
		if n < 1 || mode == "gap" {
			fmt.Fprint(w, `{"code":"000000","success":true,"data":{"list":[]}}`)
			return
		}
		fmt.Fprintf(w, `{"success":true,"code":"000000","data":{"list":[{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1.00,"avgPrice":12,"orderTime":%d,"orderUpdateTime":%d}]}}`, n, n)
	}))
	defer server.Close()
	c := Client{BaseURL: server.URL, HTTP: server.Client(), PageSize: 1}
	balance, err := c.Balance(context.Background())
	if err != nil || balance.String() != "24.5" {
		t.Fatal(balance, err)
	}
	baseline, err := c.Snapshot(context.Background(), "")
	if err != nil || len(baseline.Events) != 1 {
		t.Fatal(baseline, err)
	}
	anchor := baseline.Events[0]
	anchor.OrderTime = 1
	anchor.OrderUpdateTime = 1
	batch, err := c.Snapshot(context.Background(), anchor.Fingerprint())
	if err != nil || len(batch.Events) != 2 || batch.Events[0].OrderTime != 1 {
		t.Fatal(batch, err)
	}
	for _, m := range []string{"business", "gap"} {
		mode = m
		if _, err := c.Snapshot(context.Background(), anchor.Fingerprint()); err == nil {
			t.Fatal(m)
		}
	}
}

func TestPositionBeforeRequiresClosedBoundaryAndContinuousFills(t *testing.T) {
	closed := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/position-history" {
			status := "All Closed"
			if !closed {
				status = "Open"
			}
			fmt.Fprintf(w, `{"code":"000000","success":true,"data":{"list":[{"symbol":"BTCUSDT","side":"Long","type":"UM","status":%q,"opened":3,"closed":10}]}}`, status)
			return
		}
		fmt.Fprint(w, `{"code":"000000","success":true,"data":{"list":[`)
		for i, n := range []int{20, 14, 12, 9} {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			side := "BUY"
			if n == 14 {
				side = "SELL"
			}
			qty := 1
			if n == 12 {
				qty = 4
			}
			fmt.Fprintf(w, `{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":%q,"positionSide":"LONG","executedQty":%d,"avgPrice":12,"orderTime":%d,"orderUpdateTime":%d}`, side, qty, n, n)
		}
		fmt.Fprint(w, `]}}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50}
	batch, err := c.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	e := batch.Events[len(batch.Events)-1]
	n, err := c.PositionBefore(context.Background(), e)
	if err != nil || n.String() != "3" {
		t.Fatal(n, err)
	}
	closed = false
	if _, err = c.PositionBefore(context.Background(), e); err == nil {
		t.Fatal("inferred flat without boundary")
	}
}
