package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExecutionModeCacheExpiresAndIsPerClient(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; fmt.Fprint(w, `{"dualSidePosition":true}`) }))
	defer s.Close()
	a := NewExecutionTrader("a", "a", "")
	a.client.BaseURL = s.URL
	a.client.HTTPClient = s.Client()
	b := NewExecutionTrader("b", "b", "")
	b.client.BaseURL = s.URL
	b.client.HTTPClient = s.Client()
	for i := 0; i < 3; i++ {
		if _, err := a.executionMode(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated mode reads %d", calls)
	}
	if _, err := b.executionMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	a.configCache.modeAt = time.Now().Add(-time.Minute)
	if _, err := a.executionMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expiry/account isolation: %d", calls)
	}
}

func TestCommissionCacheIsSymbolScopedAndInvalidated(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"symbol":%q,"makerCommissionRate":"0.0002","takerCommissionRate":"0.0004"}`, r.URL.Query().Get("symbol"))
	}))
	defer s.Close()
	b := NewExecutionTrader("a", "a", "")
	b.client.BaseURL = s.URL
	b.client.HTTPClient = s.Client()
	for _, symbol := range []string{"BTCUSDT", "BTCUSDT", "ETHUSDT"} {
		if _, err := b.executionCommission(context.Background(), symbol); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("fee cache crosses symbols or misses cache")
	}
	b.invalidateExecutionConfig()
	if _, err := b.executionCommission(context.Background(), "BTCUSDT"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("invalidation not effective")
	}
}

func TestConfigCacheWaitRespectsProtectionDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v1/commissionRate" {
			close(entered)
			<-release
			fmt.Fprint(w, `{"symbol":"BTCUSDT","makerCommissionRate":"0.0002","takerCommissionRate":"0.0004"}`)
			return
		}
		fmt.Fprint(w, `{"dualSidePosition":true}`)
	}))
	defer s.Close()
	b := NewExecutionTrader("a", "a", "")
	b.client.BaseURL = s.URL
	b.client.HTTPClient = s.Client()
	feeDone := make(chan struct{})
	go func() { defer close(feeDone); _, _ = b.executionCommission(context.Background(), "BTCUSDT") }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	modeDone := make(chan error, 1)
	go func() { _, err := b.executionMode(ctx); modeDone <- err }()
	select {
	case <-modeDone:
	case <-time.After(200 * time.Millisecond):
		t.Error("protective read blocked on unrelated cache request")
	}
	close(release)
	<-feeDone
}
