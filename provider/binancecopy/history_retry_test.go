package binancecopy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHistoryBusyRetriesSamePage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			PageNumber  int
			PortfolioID string
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.PageNumber != 2 || request.PortfolioID != PortfolioID {
			t.Errorf("retry changed request: %+v %v", request, err)
		}
		if calls == 1 {
			fmt.Fprint(w, `{"success":false,"code":"11012005","data":null}`)
			return
		}
		fmt.Fprint(w, `{"success":true,"code":"000000","data":{"list":[{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","side":"BUY","positionSide":"LONG","executedQty":1,"avgPrice":100,"orderTime":20,"orderUpdateTime":20}]}}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50}
	if _, err := c.page(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("requests=%d", calls)
	}
}

func TestHistoryBusyWaitIsCancellable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"code":"11012005","data":null}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.page(ctx, 2)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestHistoryBusyRetriesAreBounded(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"success":false,"code":"11012005","data":null}`)
	}))
	defer srv.Close()
	c := Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50}
	_, err := c.page(context.Background(), 2)
	if _, ok := err.(*HistoryBusyError); !ok || calls != 4 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestHistoryInvalidDataIsNotRetried(t *testing.T) {
	for _, body := range []string{`{"success":false,"code":"other","data":null}`, `{"success":true,"code":"000000","data":null}`, `broken`} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, body) }))
		c := Client{BaseURL: srv.URL, HTTP: srv.Client(), PageSize: 50}
		_, err := c.page(context.Background(), 2)
		srv.Close()
		if err == nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}
