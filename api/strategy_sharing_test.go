package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"nofx/manager"
	"nofx/store"
)

func TestPublicStrategyCopyIsPrivateIndependentAndRedacted(t *testing.T) {
	st := securityStore(t)
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.Indicators.NofxOSAPIKey = "private-provider-key"
	cfg.Indicators.ExternalDataSources = []store.ExternalDataSource{{URL: "https://private.test/?token=private-provider-key", Headers: map[string]string{"Authorization": "private-provider-key"}}}
	cfg.PublishConfig = &store.PublishStrategyConfig{IsPublic: true, ConfigVisible: true}
	source := &store.Strategy{ID: "public-source", UserID: "owner", Name: "Public", IsPublic: true, ConfigVisible: true}
	if err := source.SetConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().Create(source); err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().Duplicate("reader", source.ID, "copy", "My copy"); err != nil {
		t.Fatal(err)
	}
	copied, err := st.Strategy().Get("reader", "copy")
	if err != nil {
		t.Fatal(err)
	}
	if copied.UserID != "reader" || copied.IsPublic || copied.IsActive || copied.IsDefault || copied.ConfigVisible || copied.IsBuiltin {
		t.Fatal("copy inherited source ownership/publication")
	}
	if strings.Contains(copied.Config, "private-provider-key") {
		t.Fatal("copy leaked provider credential")
	}
	cc, _ := copied.ParseConfig()
	if cc.PublishConfig == nil || cc.PublishConfig.IsPublic || cc.PublishConfig.ConfigVisible {
		t.Fatal("nested publication flags inherited")
	}
	original, _ := st.Strategy().Get("owner", source.ID)
	if !strings.Contains(original.Config, "private-provider-key") {
		t.Fatal("redaction mutated source")
	}
	for _, mode := range []string{"hidden", "private"} {
		st.GormDB().Model(source).Updates(map[string]any{"is_public": mode != "private", "config_visible": mode != "hidden"})
		if err := st.Strategy().Duplicate("reader", source.ID, "denied-"+mode, "Denied"); err == nil {
			t.Fatal("copied unavailable source", mode)
		}
	}
}

func TestPublicStrategyListingRedactsCredentials(t *testing.T) {
	st := securityStore(t)
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.Indicators.NofxOSAPIKey = "private-provider-key"
	cfg.Indicators.ExternalDataSources = []store.ExternalDataSource{{URL: "https://private.test/?token=private-provider-key", Headers: map[string]string{"Authorization": "private-provider-key"}}}
	source := &store.Strategy{ID: "public-source", UserID: "owner", Name: "Public", IsPublic: true, ConfigVisible: true}
	source.SetConfig(&cfg)
	st.Strategy().Create(source)
	s := NewServer(manager.NewTraderManager(), st, nil, 0)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/strategies/public", nil)
	s.handlePublicStrategies(c)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-provider-key") {
		t.Fatal("public credential leak", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationIsOwnerOnlyAndDoesNotChangeRunningTradingConfig(t *testing.T) {
	st := securityStore(t)
	cfg := store.GetDefaultStrategyConfig("en")
	cfg.Indicators.NofxOSAPIKey = "keep-private"
	row := &store.Strategy{ID: "owned", UserID: "owner", Name: "Owned"}
	row.SetConfig(&cfg)
	st.Strategy().Create(row)
	st.Trader().Create(&store.Trader{ID: "running", UserID: "owner", Name: "Running", StrategyID: row.ID, IsRunning: true})
	s := NewServer(manager.NewTraderManager(), st, nil, 0)
	call := func(user, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", user)
		c.Params = gin.Params{{Key: "id", Value: row.ID}}
		c.Request = httptest.NewRequest("PUT", "/api/strategies/owned/publication", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		s.handleStrategyPublication(c)
		return w
	}
	if w := call("other", `{"is_public":true,"config_visible":true}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call("owner", `{"is_public":true,"config_visible":true}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	updated, _ := st.Strategy().Get("owner", row.ID)
	got, _ := updated.ParseConfig()
	if !updated.IsPublic || !updated.ConfigVisible || got.Indicators.NofxOSAPIKey != "keep-private" || got.RiskControl != cfg.RiskControl {
		t.Fatal("publication changed trading config")
	}
	if w := call("owner", `{"is_public":false,"config_visible":false}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if err := st.Strategy().Duplicate("other", row.ID, "denied", "Denied"); err == nil {
		t.Fatal("unpublished strategy still copyable")
	}
}
