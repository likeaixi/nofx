package binance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeIdentityMatchesDifferentKeysOfOneAccount(t *testing.T) {
	alias := "same-account"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"accountAlias":%q,"asset":"USDT","balance":"10"}]`, alias)
	}))
	defer server.Close()
	identity := func(key string) (string, error) {
		b := NewExecutionTrader(key, "test-secret", "")
		b.client.BaseURL = server.URL
		b.client.HTTPClient = server.Client()
		return b.NativeAccountIdentity(context.Background())
	}
	a, err := identity("key1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity("key2")
	if err != nil || a != b {
		t.Fatalf("native account differs: %v", err)
	}
	alias = ""
	if _, err = identity("key1"); err == nil {
		t.Fatal("missing native identity accepted")
	}
}
