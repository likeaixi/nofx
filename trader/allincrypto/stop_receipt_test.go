package allincrypto

import (
	"context"
	"errors"
	"nofx/trader/execution"
	"testing"
	"time"
)

type stopReceiptVenue struct {
	*transientStopVenue
	incomplete, failed bool
}

func (v *stopReceiptVenue) SubmitOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	got, err := v.venueFixture.SubmitOrder(ctx, o)
	if err == nil && o.Role == "sl" {
		if !v.incomplete {
			got.OrderID = "123"
		}
		if v.failed {
			return got, errors.New("submission outcome unknown")
		}
	}
	return got, err
}
func TestValidStopReceiptDefersReadAndSurvivesRestart(t *testing.T) {
	r, v, source, add := setup(t)
	w := &stopReceiptVenue{transientStopVenue: &transientStopVenue{venueFixture: v, failures: 1}}
	r.venue = w
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if w.reads != 0 || s.Pending != nil || s.Positions["BTCUSDT"].Stop.OrderID != "123" || v.held.IsZero() {
		t.Fatal("complete receipt did not establish protection without immediate query")
	}
	r2, err := New(r.db, r.id, r.account, source, w, v)
	if err != nil {
		t.Fatal(err)
	}
	r2.now = func() time.Time { return v.now }
	if err = r2.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if w.reads != 0 {
		t.Fatal("restart lost deferred check")
	}
	v.now = v.now.Add(5 * time.Second)
	_ = r2.Reconcile(ctx)
	if w.reads != 1 || v.held.IsZero() {
		t.Fatal("scheduled verification missing or transient query flattened position")
	}
	v.now = v.now.Add(5 * time.Second)
	if err = r2.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	// A terminal failure discovered by periodic monitoring must still rescue.
	s, _ = r2.load()
	v.canceled = map[string]bool{s.Positions["BTCUSDT"].Stop.ClientID: true}
	v.now = v.now.Add(5 * time.Second)
	_ = r2.Reconcile(ctx)
	if !v.held.IsZero() {
		t.Fatal("terminal protection loss did not rescue")
	}
}
func TestUncertainStopSubmissionStillQueries(t *testing.T) {
	for _, mode := range []string{"incomplete", "failed"} {
		t.Run(mode, func(t *testing.T) {
			r, v, _, add := setup(t)
			w := &stopReceiptVenue{transientStopVenue: &transientStopVenue{venueFixture: v, failures: 1}, incomplete: mode == "incomplete", failed: mode == "failed"}
			r.venue = w
			ctx := context.Background()
			_ = r.Poll(ctx)
			add("BUY", "10")
			_ = r.Poll(ctx)
			s, _ := r.load()
			if w.reads != 1 || s.Pending == nil || s.Pending.Stage != "stop" {
				t.Fatal("uncertain receipt was accepted without query")
			}
			v.now = v.now.Add(5 * time.Second)
			if err := r.Reconcile(ctx); err != nil {
				t.Fatal(err)
			}
			if len(v.orders) != 2 || v.held.IsZero() {
				t.Fatal("query confirmation replayed or flattened")
			}
		})
	}
}
