package allincrypto

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"nofx/provider/binancecopy"
)

type busyProofSource struct {
	*sourceFixture
	busy bool
}

func (s *busyProofSource) PositionBefore(ctx context.Context, e binancecopy.Event) (decimal.Decimal, error) {
	if s.busy {
		return decimal.Zero, &binancecopy.HistoryBusyError{Path: "/order-history"}
	}
	return s.sourceFixture.PositionBefore(ctx, e)
}

func TestBusyProofRetainsSignalAndDoesNotReplayExpiredOpening(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "expired"}[expired], func(t *testing.T) {
			r, v, src, add := setup(t)
			wrapped := &busyProofSource{sourceFixture: src, busy: true}
			r.source = wrapped
			ctx := context.Background()
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			before, _ := r.load()
			add("BUY", "10")
			e := src.events[len(src.events)-1]
			if err := r.Poll(ctx); err == nil {
				t.Fatal("temporary proof failure reported as successful poll")
			}
			var count int64
			r.db.Model(&signal{}).Where("trader_id = ? AND key = ?", r.id, e.Key()).Count(&count)
			if count != 0 {
				t.Fatal("unverified signal was consumed")
			}
			after, _ := r.load()
			if after.Head != before.Head || len(v.orders) != 0 {
				t.Fatal("advanced cursor or traded without source proof")
			}
			wrapped.busy = false
			if expired {
				v.now = v.now.Add(6 * time.Minute)
			}
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			after, _ = r.load()
			if !after.Lead["BTCUSDT/long"].Equal(decimal.NewFromInt(10)) {
				t.Fatal("source ledger did not recover")
			}
			if expired && len(v.orders) != 0 {
				t.Fatal("expired opening replayed")
			}
			if !expired && len(v.orders) == 0 {
				t.Fatal("fresh opening did not recover")
			}
			n := len(v.orders)
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			if len(v.orders) != n {
				t.Fatal("duplicate order on next poll")
			}
		})
	}
}
