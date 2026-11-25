package binancecopy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSelectedPortfolioUsedForAllPublicReads(t *testing.T) {
	paths := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths[r.URL.Path] = true
		if !strings.HasSuffix(r.Header.Get("Referer"), "/123") {
			t.Error("wrong referer")
		}
		switch r.URL.Path {
		case "/detail":
			if r.URL.Query().Get("portfolioId") != "123" {
				t.Error("wrong detail ID")
			}
			io.WriteString(w, `{"success":true,"code":"000000","data":{"leadPortfolioId":"123","futuresType":"UM","initInvestAsset":"USDT","status":"ACTIVE","marginBalance":"10"}}`)
		default:
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"portfolioId":"123"`) {
				t.Error("wrong history ID")
			}
			io.WriteString(w, `{"success":true,"code":"000000","data":{"list":[]}}`)
		}
	}))
	defer srv.Close()
	c := NewForPortfolio("123")
	c.BaseURL = srv.URL
	c.HTTP = srv.Client()
	if _, err := c.Balance(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Snapshot(context.Background(), "")
	c.PositionBefore(context.Background(), Event{})
	if len(paths) != 3 {
		t.Fatalf("missing endpoints: %v", paths)
	}
}
