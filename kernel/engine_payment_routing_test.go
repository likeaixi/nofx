package kernel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"nofx/store"
)

func TestStrategyDataCannotFallBackToOriginWithoutFeeWallet(t *testing.T) {
	var originRequests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originRequests.Add(1)
		w.WriteHeader(400)
	}))
	defer origin.Close()
	for _, key := range []string{"", " ", "invalid-key"} {
		cfg := store.GetDefaultStrategyConfig("en")
		cfg.Indicators.NofxOSAPIKey = "legacy-key-must-not-enable-fallback"
		engine := NewStrategyEngine(&cfg, key)
		// Make any accidental origin fallback observable without external access.
		engine.nofxosClient.SetConfig(origin.URL, "legacy-key")
		_, err := engine.nofxosClient.GetAI500List()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "claw402") {
			t.Fatalf("missing paid-data configuration error: %v", err)
		}
	}
	if originRequests.Load() != 0 {
		t.Fatalf("strategy bypassed payment: %d origin requests", originRequests.Load())
	}
}

// A missing wallet argument must fail closed with the claw402 removal error —
// the CLAW402_WALLET_KEY environment fallback no longer activates a gateway.
func TestStrategyEngineMissingWalletFailsClosedWithRemovalError(t *testing.T) {
	key, _ := crypto.GenerateKey()
	hexKey := fmt.Sprintf("%x", crypto.FromECDSA(key))
	t.Setenv("CLAW402_WALLET_KEY", hexKey) // must be ignored now
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg) // no wallet argument
	if engine == nil {
		t.Fatal("engine is nil")
	}
	_, err := engine.nofxosClient.GetAI500List()
	if err == nil || !strings.Contains(err.Error(), "claw402 wallet removed; configure a custom model API key") {
		t.Fatalf("missing wallet did not fail closed with the removal error: %v", err)
	}
}

func TestStrategyDataUsesConfiguredGateway(t *testing.T) {
	key, _ := crypto.GenerateKey()
	var requests atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/v1/nofx/ai500/list" || r.URL.Query().Has("auth") {
			t.Errorf("bad gateway route: %s", r.URL)
		}
		w.WriteHeader(400) // No signature, payment or external request.
	}))
	defer gateway.Close()
	t.Setenv("CLAW402_URL", gateway.URL)
	t.Setenv("CLAW402_WALLET_KEY", "")
	hexKey := fmt.Sprintf("%x", crypto.FromECDSA(key))
	cfg := store.GetDefaultStrategyConfig("en")
	engine := NewStrategyEngine(&cfg, hexKey)
	if _, err := engine.nofxosClient.GetAI500List(); err == nil {
		t.Fatal("expected mock rejection")
	}
	if requests.Load() != 1 {
		t.Fatalf("gateway requests = %d", requests.Load())
	}
}
