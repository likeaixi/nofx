package binancecopy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type pacingTransport func(*http.Request) (*http.Response, error)

func (f pacingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHistoryRequestsWaitAcrossClientsAndCancel(t *testing.T) {
	var calls []time.Time
	transport := pacingTransport(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, time.Now())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"success":true,"code":"000000","data":{}}`)), Header: make(http.Header)}, nil
	})
	a, b := NewForPortfolio("123"), NewForPortfolio("456")
	a.HTTP = &http.Client{Transport: transport}
	b.HTTP = a.HTTP
	var out map[string]any
	if err := a.request(context.Background(), "POST", "/order-history", nil, &out); err != nil {
		t.Fatal(err)
	}
	finished := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := b.request(ctx, "POST", "/position-history", nil, &out); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting request must cancel, got %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("canceled wait sent an HTTP request: %d", len(calls))
	}
	if err := b.request(context.Background(), "POST", "/position-history", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gap := calls[1].Sub(finished); gap < 990*time.Millisecond {
		t.Fatalf("history requests only waited %v", gap)
	}
}
