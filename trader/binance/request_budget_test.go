package binance

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/adshao/go-binance/v2/common"
	"io"
	"net/http"
	"net/url"
	"nofx/trader/execution"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type budgetRoundTrip func(*http.Request) (*http.Response, error)

func (f budgetRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestBudgetReserveAndCooldown(t *testing.T) {
	now := time.Unix(1800000000, 0)
	b := &requestBudget{limit: 100, now: func() time.Time { return now }}
	if err := b.reserve(80, false); err != nil {
		t.Fatal(err)
	}
	if b.reserve(1, false) == nil {
		t.Fatal("ordinary request consumed protective reserve")
	}
	if err := b.reserve(20, true); err != nil {
		t.Fatal(err)
	}
	if b.reserve(1, true) == nil {
		t.Fatal("exceeded full budget")
	}
	b.finish(100)
	now = now.Add(time.Minute)
	if err := b.reserve(1, false); err != nil {
		t.Fatal(err)
	}
	b.observe(http.Header{"Retry-After": []string{"10"}}, 429, now)
	if b.reserve(1, true) == nil {
		t.Fatal("urgent request ignored exchange cooldown")
	}
	now = now.Add(11 * time.Second)
	if err := b.reserve(1, true); err != nil {
		t.Fatal(err)
	}
}
func TestBudgetHeadersAndSafeHTTPError(t *testing.T) {
	now := time.Now()
	b := &requestBudget{limit: 100, now: func() time.Time { return now }, refreshed: now}
	tr := &budgetTransport{base: budgetRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 429, Header: http.Header{"X-Mbx-Used-Weight-1m": []string{"99"}, "Retry-After": []string{"7"}}, Body: io.NopCloser(strings.NewReader(`{"code":-1003,"msg":"secret signature"}`))}, nil
	}), budgets: &budgetRegistry{items: map[string]*requestBudget{"example.test": b}}}
	req, _ := http.NewRequest("GET", "https://example.test/fapi/v1/algoOrder?signature=secret", nil)
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	var api common.APIError
	if err = json.NewDecoder(res.Body).Decode(&api); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	err = protectiveLookupError(&api)
	var retry *execution.RetryableError
	if !errors.As(err, &retry) || retry.Delay < 7*time.Second || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe or lost throttle: %v", err)
	}
	if b.reserve(1, true) == nil {
		t.Fatal("shared state lost response cooldown")
	}
}

func TestProtectedLookupDoesNotRetryAuthentication(t *testing.T) {
	err := protectiveLookupError(&url.Error{Op: "Get", URL: "https://secret.test?signature=secret", Err: &binanceHTTPError{status: 401, code: -2015}})
	var retry *execution.RetryableError
	if errors.As(err, &retry) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("auth classified transient or leaked: %v", err)
	}
}
func TestBudgetConcurrentReservations(t *testing.T) {
	now := time.Now()
	b := &requestBudget{limit: 100, now: func() time.Time { return now }}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.reserve(5, false) == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 16 {
		t.Fatalf("concurrent budget exceeded: %d", accepted.Load())
	}
}
func TestBudgetDiscoversExchangeLimit(t *testing.T) {
	calls := 0
	b := &budgetRegistry{}
	tr := &budgetTransport{budgets: b, base: budgetRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `{}`
		if r.URL.Path == "/fapi/v1/exchangeInfo" {
			if r.Header.Get("X-MBX-APIKEY") != "" || r.URL.RawQuery != "" {
				t.Fatal("metadata leaked signed credentials")
			}
			body = `{"rateLimits":[{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":2400}]}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	req, _ := http.NewRequest("GET", "https://example.test/fapi/v1/algoOrder?signature=secret", nil)
	req.Header.Set("X-MBX-APIKEY", "secret")
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if calls != 2 || b.get("example.test").limit != 2400 {
		t.Fatal("missing discovery")
	}
}

func TestBudgetCarriesUnfinishedRequestsAcrossMinute(t *testing.T) {
	now := time.Unix(1800000000, 0)
	started := now
	b := &requestBudget{limit: 100, now: func() time.Time { return now }}
	if err := b.reserve(80, false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if b.reserve(1, false) == nil {
		t.Fatal("lost cross-minute outstanding reservations")
	}
	b.finish(80)
	b.observe(http.Header{"X-Mbx-Used-Weight-1m": []string{"90"}}, 200, started)
	if b.reserve(11, true) == nil {
		t.Fatal("ignored late response weight")
	}
}

func TestBudgetHTTPClientDoesNotExposeSignedURLOnThrottle(t *testing.T) {
	now := time.Now()
	b := &requestBudget{limit: 100, now: func() time.Time { return now }, refreshed: now, until: now.Add(time.Minute)}
	c := &http.Client{Transport: &budgetTransport{budgets: &budgetRegistry{items: map[string]*requestBudget{"example.test": b}}, base: budgetRoundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("sent during cooldown"); return nil, nil })}}
	res, err := c.Get("https://example.test/fapi/v1/order?signature=secret")
	if err != nil {
		t.Fatalf("transport error can expose signed URL: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 429 {
		t.Fatal("lost throttle response")
	}
}

func TestSanitizedTransportPreservesRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{{"cancel", context.Canceled, false}, {"tls", x509.UnknownAuthorityError{}, false}, {"timeout", context.DeadlineExceeded, true}} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			b := &requestBudget{limit: 100, now: func() time.Time { return now }, refreshed: now}
			tr := &budgetTransport{budgets: &budgetRegistry{items: map[string]*requestBudget{"example.test": b}}, base: budgetRoundTrip(func(*http.Request) (*http.Response, error) { return nil, tc.err })}
			req, _ := http.NewRequest("GET", "https://example.test/fapi/v1/algoOrder?signature=secret", nil)
			res, err := tr.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var api common.APIError
			_ = json.NewDecoder(res.Body).Decode(&api)
			got := protectiveLookupError(&api)
			var retry *execution.RetryableError
			if errors.As(got, &retry) != tc.retry {
				t.Fatalf("classification lost: %v", got)
			}
		})
	}
}
func TestSanitizedHTTPKeepsAccountDiagnosis(t *testing.T) {
	err := binanceReadError("read account", &common.APIError{Code: -2015, Message: "NOFX_HTTP status=401 retry_ms=5000"})
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("actionable diagnosis lost: %v", err)
	}
}
