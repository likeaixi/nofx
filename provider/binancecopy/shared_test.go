package binancecopy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedSourceReusesCompleteSnapshotForConcurrentBootstrap(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			fmt.Fprint(w, `{"code":"11012005","success":false,"data":null}`)
			return
		}
		fmt.Fprint(w, `{"code":"000000","success":true,"data":{"list":[{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1,"avgPrice":100,"orderTime":20,"orderUpdateTime":20},{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1,"avgPrice":100,"orderTime":9,"orderUpdateTime":9}]}}`)
	}))
	defer srv.Close()
	c := NewShared(&Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50})
	first, err := c.Snapshot(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := c.HistorySince(context.Background(), 10)
			if err != nil || len(got.Events) != 2 {
				t.Errorf("bootstrap %v %v", got, err)
			}
		}()
	}
	wg.Wait()
	// A consumer cannot mutate another trader's cached event slice.
	first.Events[0].Symbol = "MUTATED"
	got, err := c.Snapshot(context.Background(), first.Head)
	if err != nil || got.Events[0].Symbol != "BTCUSDT" {
		t.Fatal(got, err)
	}
	c.now = func() time.Time { return time.Now().Add(time.Minute) }
	if _, err = c.Snapshot(context.Background(), first.Head); err == nil {
		t.Fatal("stale public data must not mask an upstream error")
	}
}

func TestSharedHistoryDoesNotHideUnorderedPublicPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":"000000","success":true,"data":{"list":[`)
		for i, n := range []int{20, 9, 15} {
			if i > 0 {
				fmt.Fprint(w, ",")
			}
			fmt.Fprintf(w, `{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1,"avgPrice":100,"orderTime":%d,"orderUpdateTime":%d}`, n, n)
		}
		fmt.Fprint(w, `]}}`)
	}))
	defer srv.Close()
	c := NewShared(&Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50})
	_, _ = c.Snapshot(context.Background(), "")
	if _, err := c.HistorySince(context.Background(), 10); err == nil {
		t.Fatal("cached Snapshot bypassed historical ordering validation")
	}
}
