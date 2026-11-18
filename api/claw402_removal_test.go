package api

import (
	"encoding/json"
	"strings"
	"testing"

	"nofx/auth"
	"nofx/store"
)

// authGenerateToken registers a user in st and returns a fresh session token
// for the removed-claw402 route assertions.
func authGenerateToken(t *testing.T, st *store.Store) (string, error) {
	t.Helper()
	u := storeUser("claw402-removal@x.test", store.RoleUser)
	u.TotpEnabled = true
	if err := st.User().CreateUser(u); err != nil {
		return "", err
	}
	return auth.GenerateJWT(u.ID, u.Email, 0)
}

func TestClaw402UserFacingRoutesRemoved(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	token, _ := authGenerateToken(t, st)
	for _, path := range []string{
		"/api/onboarding/beginner",
		"/api/onboarding/beginner/current",
		"/api/wallet/export",
		"/api/wallet/validate",
		"/api/wallet/balance",
		"/api/vergex/direction-change/leaderboard",
		"/api/vergex/cost-liquidation-heatmap",
	} {
		for _, method := range []string{"GET", "POST"} {
			if w := securityRequest(s.router, method, path, `{}`, token); w.Code != 404 {
				t.Fatalf("%s %s = %d, want 404", method, path, w.Code)
			}
		}
	}
}

// The claw402 model provider was removed: PUT /api/models must not mint a
// claw402 AI-model row (the re-arm path back to a paying x402 client).
func TestClaw402ModelReArmRejected(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	token, _ := authGenerateToken(t, st)
	for _, modelID := range []string{"claw402", "myname_claw402"} {
		body := `{"models":{"` + modelID + `":{"enabled":true,"api_key":"0x0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}}`
		w := securityRequest(s.router, "PUT", "/api/models", body, token)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "claw402 provider removed") {
			t.Fatalf("PUT /api/models %s = %d %s", modelID, w.Code, w.Body.String())
		}
	}
	var count int64
	if err := st.GormDB().Model(&store.AIModel{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("claw402 model row created: count=%d err=%v", count, err)
	}
}

// Neither the user's model list nor the public supported-models catalog may
// advertise claw402 anymore.
func TestModelCatalogHasNoClaw402(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	token, _ := authGenerateToken(t, st)
	for _, tc := range []struct{ method, path, token string }{
		{"GET", "/api/models", token},
		{"GET", "/api/supported-models", ""},
	} {
		w := securityRequest(s.router, tc.method, tc.path, ``, tc.token)
		if w.Code != 200 {
			t.Fatalf("%s %s = %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if strings.Contains(strings.ToLower(w.Body.String()), "claw402") {
			t.Fatalf("%s %s still advertises claw402: %s", tc.method, tc.path, w.Body.String())
		}
	}
}

// The supported-models catalog must expose the custom (OpenAI-compatible)
// provider entry so ModelSelectionStep renders a tile and a fresh user can
// reach the config form. The user's own model list starts empty.
func TestSupportedModelsCatalogHasCustomEntry(t *testing.T) {
	securityJWTSecret(t)
	st := securityStore(t)
	s := NewServer(nil, st, nil, 0)
	token, _ := authGenerateToken(t, st)

	w := securityRequest(s.router, "GET", "/api/supported-models", ``, token)
	if w.Code != 200 {
		t.Fatalf("GET /api/supported-models = %d %s", w.Code, w.Body.String())
	}
	var catalog []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil {
		t.Fatalf("supported-models is not a JSON list: %v (%s)", err, w.Body.String())
	}
	if len(catalog) != 1 {
		t.Fatalf("supported-models len = %d, want 1: %s", len(catalog), w.Body.String())
	}
	entry := catalog[0]
	if entry["id"] != "custom" || entry["provider"] != "custom" {
		t.Fatalf("supported-models entry is not the custom provider: %s", w.Body.String())
	}
	if name, _ := entry["name"].(string); name == "" {
		t.Fatalf("supported-models entry has no display name: %s", w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "claw402") {
		t.Fatalf("supported-models still advertises claw402: %s", w.Body.String())
	}

	// A logged-in user with nothing configured sees an empty model list.
	w = securityRequest(s.router, "GET", "/api/models", ``, token)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("GET /api/models = %d %s, want 200 []", w.Code, w.Body.String())
	}
}

func TestDefaultStrategyNotVergex(t *testing.T) {
	st := securityStore(t)
	if err := createDefaultStrategiesFor("user1", "en", st); err != nil {
		t.Fatal(err)
	}
	var strat store.Strategy
	if err := st.GormDB().Where("user_id = ? AND is_active = ?", "user1", true).First(&strat).Error; err != nil {
		t.Fatal(err)
	}
	cfg, err := strat.ParseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CoinSource.SourceType == "vergex_signal" || cfg.CoinSource.VergexLimit > 0 {
		t.Fatalf("default strategy still depends on vergex: %+v", cfg.CoinSource)
	}
	if !cfg.CoinSource.UseOITop {
		t.Fatal("default coin source should fall back to OI top (free)")
	}
}
