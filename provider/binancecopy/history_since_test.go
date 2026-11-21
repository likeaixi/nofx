package binancecopy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistorySinceRequiresCoverageAndStablePages(t *testing.T) {
	for _, mode := range []string{"ok", "missing", "changed", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ PageNumber int }
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body.PageNumber == 1 {
					reads++
				}
				nums := []int{30, 20}
				if body.PageNumber == 2 {
					nums = []int{12, 9}
					if mode == "missing" {
						fmt.Fprint(w, `{"code":"11012005","success":false,"data":null}`)
						return
					}
					if mode == "duplicate" {
						nums = []int{20, 9}
					}
				}
				if mode == "changed" && reads > 1 {
					nums = []int{31, 30}
				}
				fmt.Fprint(w, `{"code":"000000","success":true,"data":{"list":[`)
				for i, n := range nums {
					if i > 0 {
						fmt.Fprint(w, ",")
					}
					fmt.Fprintf(w, `{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1,"avgPrice":100,"orderTime":%d,"orderUpdateTime":%d}`, n, n)
				}
				fmt.Fprint(w, `]}}`)
			}))
			defer srv.Close()
			c := Client{srv.URL, srv.Client(), 2}
			got, err := c.HistorySince(context.Background(), 10)
			if mode != "ok" {
				if err == nil {
					t.Fatal("accepted unsafe history", got)
				}
				return
			}
			if err != nil || len(got.Events) != 4 || got.Events[0].OrderTime != 9 || got.Events[3].OrderTime != 30 {
				t.Fatal(got, err)
			}
		})
	}
}
