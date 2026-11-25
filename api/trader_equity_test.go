package api

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"nofx/manager"
	"nofx/store"
	"testing"
	"time"
)

func TestOwnedEquityDoesNotRequirePublicVisibility(t *testing.T) {
	st := securityStore(t)
	tr := &store.Trader{ID: "private", UserID: "owner", Name: "private", InitialBalance: 100}
	if e := st.Trader().Create(tr); e != nil {
		t.Fatal(e)
	}
	st.GormDB().Model(tr).Update("show_in_competition", false)
	st.Equity().Save(&store.EquitySnapshot{TraderID: tr.ID, Timestamp: time.Now().UTC(), TotalEquity: 105})
	s := NewServer(manager.NewTraderManager(), st, nil, 0)
	for _, user := range []string{"owner", "other", ""} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest("GET", "/api/trader-equity-history?trader_id=private", nil)
		ctx.Set("user_id", user)
		s.handleTraderEquityHistory(ctx)
		want := 404
		if user == "owner" {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", user, w.Code, w.Body.String())
		}
	}
}
