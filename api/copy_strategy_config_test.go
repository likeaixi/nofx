package api

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"nofx/manager"
	"nofx/store"
	"strings"
	"testing"
)

func TestCopyStrategyConfigPersistsAndRejectsInvalidChanges(t *testing.T) {
	st := securityStore(t)
	s := NewServer(manager.NewTraderManager(), st, nil, 0)
	call := func(id, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("user_id", "owner")
		c.Request = httptest.NewRequest("POST", "/api/strategies", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		if id == "" {
			s.handleCreateStrategy(c)
		} else {
			c.Params = gin.Params{{Key: "id", Value: id}}
			s.handleUpdateStrategy(c)
		}
		return w
	}
	bad := call("", `{"name":"bad","config":{"strategy_type":"allincrypto","copy_config":{"source_url":"https://evil.test/123","leverage":3}}}`)
	if bad.Code != 400 {
		t.Fatalf("unsafe URL: %d", bad.Code)
	}
	good := call("", `{"name":"copy","config":{"strategy_type":"allincrypto","copy_config":{"source_url":"https://www.binance.com/en/copy-trading/lead-details/123","leverage":3}}}`)
	if good.Code != 200 {
		t.Fatal(good.Body.String())
	}
	var result struct {
		ID string `json:"id"`
	}
	json.Unmarshal(good.Body.Bytes(), &result)
	row, err := st.Strategy().Get("owner", result.ID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := row.ParseConfig()
	if err != nil || cfg.CopySettings().Leverage != 3 {
		t.Fatalf("not persisted %v", err)
	}
	bad = call(result.ID, `{"config":{"copy_config":{"leverage":21}}}`)
	if bad.Code != 400 {
		t.Fatalf("unsafe leverage %d", bad.Code)
	}
	tr := &store.Trader{ID: "bound", UserID: "owner", Name: "bound", StrategyID: result.ID}
	if err = st.Trader().Create(tr); err != nil {
		t.Fatal(err)
	}
	st.GormDB().Model(tr).Update("is_running", true)
	bad = call(result.ID, `{"config":{"copy_config":{"leverage":5}}}`)
	if bad.Code != 400 {
		t.Fatalf("running edit %d %s", bad.Code, bad.Body.String())
	}
	st.GormDB().Model(tr).Update("is_running", false)
	good = call(result.ID, `{"config":{"copy_config":{"leverage":5}}}`)
	if good.Code != 200 {
		t.Fatal(good.Body.String())
	}
	row, _ = st.Strategy().Get("owner", result.ID)
	cfg, _ = row.ParseConfig()
	if cfg.CopySettings().Leverage != 5 || !strings.HasSuffix(cfg.CopySettings().SourceURL, "/123") {
		t.Fatal("partial edit lost source")
	}
	st.GormDB().Exec(`CREATE TABLE IF NOT EXISTS allincrypto_checkpoints (trader_id TEXT PRIMARY KEY, account TEXT, revision INTEGER, body TEXT)`)
	st.GormDB().Exec(`INSERT INTO allincrypto_checkpoints VALUES (?, ?, 0, ?)`, tr.ID, "account", `{"Positions":{},"Waiting":{},"SourceID":"123"}`)
	bad = call(result.ID, `{"config":{"copy_config":{"source_url":"https://www.binance.com/en/copy-trading/lead-details/456"}}}`)
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "new strategy") {
		t.Fatalf("source mutation accepted: %d %s", bad.Code, bad.Body.String())
	}
}
