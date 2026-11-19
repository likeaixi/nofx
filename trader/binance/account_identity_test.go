package binance

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeIdentityUsesV2AndMatchesAccountAcrossKeys(t *testing.T) {
	var v3Calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v3/balance" {
			v3Calls.Add(1)
			fmt.Fprint(w, `[{"accountAlias":"common-colliding-alias"}]`)
			return
		}
		if r.URL.Path != "/fapi/v2/balance" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
			return
		}
		q := r.URL.Query()
		signature := q.Get("signature")
		q.Del("signature")
		mac := hmac.New(sha256.New, []byte("test-secret"))
		mac.Write([]byte(q.Encode()))
		if signature != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid signature")
		}
		ts, err := strconv.ParseInt(q.Get("timestamp"), 10, 64)
		if err != nil || time.Now().UnixMilli()-ts < 59000 || time.Now().UnixMilli()-ts > 61000 {
			t.Error("TimeOffset not applied")
		}
		alias := "account-one"
		if r.Header.Get("X-MBX-APIKEY") == "other-account-key" {
			alias = "account-two"
		}
		fmt.Fprintf(w, `[{"accountAlias":%q,"balance":{"unknown":"shape"},"newField":true},{"accountAlias":%q}]`, alias, alias)
	}))
	defer server.Close()
	identity := func(key string) string {
		b := NewExecutionTrader(key, "test-secret", "")
		b.client.BaseURL = server.URL
		b.client.HTTPClient = server.Client()
		b.client.TimeOffset = 60000
		id, err := b.NativeAccountIdentity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(id, "binance:native:v2:") {
			t.Fatalf("unversioned identity: %s", id)
		}
		return id
	}
	a := identity("key1")
	if a != identity("key2") {
		t.Fatal("same account differs across keys")
	}
	if a == identity("other-account-key") {
		t.Fatal("different accounts collide")
	}
	if v3Calls.Load() != 0 {
		t.Fatal("identity depends on v3")
	}
}

func TestNativeIdentityRejectsInvalidAliases(t *testing.T) {
	for _, body := range []string{`[]`, `null`, `[null]`, `[{}]`, `[{"accountAlias":""}]`, `[{"accountAlias":" "}]`, `[{"accountAlias":"a"},{"accountAlias":"b"}]`, `[{"accountAlias":123}]`, `not json`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			b := NewExecutionTrader("key", "secret", "")
			b.client.BaseURL = server.URL
			if id, err := b.NativeAccountIdentity(context.Background()); err == nil || id != "" {
				t.Fatal("invalid alias accepted")
			}
		})
	}
}

func TestNativeIdentityBlocksRedirectsAndSanitizesErrors(t *testing.T) {
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer target.Close()
	for _, status := range []int{302, 401, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				fmt.Fprintf(w, `{"code":-2015,"msg":%q}`, r.URL.String()+" private-key private-secret")
			}))
			defer server.Close()
			b := NewExecutionTrader("private-key", "private-secret", "")
			b.client.BaseURL = server.URL
			_, err := b.NativeAccountIdentity(context.Background())
			if err == nil {
				t.Fatal("HTTP error accepted")
			}
			for _, sensitive := range []string{"private-key", "private-secret", "signature=", server.URL} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("unsafe error: %s", err)
				}
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestNativeIdentityRejectsUnsupportedSigningAndTransportErrors(t *testing.T) {
	b := NewExecutionTrader("private-key", "private-secret", "")
	b.client.KeyType = "unsupported-key-type"
	if _, err := b.NativeAccountIdentity(context.Background()); err == nil {
		t.Fatal("unsupported signing accepted")
	}
	b.client.KeyType = ""
	b.client.BaseURL = "://private-key"
	if _, err := b.NativeAccountIdentity(context.Background()); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatalf("unsafe malformed URL result: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.client.BaseURL = "https://example.invalid"
	if _, err := b.NativeAccountIdentity(ctx); err == nil || strings.Contains(err.Error(), "signature=") {
		t.Fatalf("unsafe transport result: %v", err)
	}
}
