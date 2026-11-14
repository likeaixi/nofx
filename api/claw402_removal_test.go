package api

import (
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

func TestDefaultStrategyNotVergex(t *testing.T) {
	st := securityStore(t)
	if err := createDefaultStrategiesFor("user1", "en", st); err != nil {
		t.Fatal(err)
	}
	var strat store.Strategy
	if err := st.GormDB().Where("user_id = ?", "user1").First(&strat).Error; err != nil {
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
