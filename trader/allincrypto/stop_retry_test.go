package allincrypto

import (
	"context"
	"nofx/trader/execution"
	"testing"
	"time"
)

type transientStopVenue struct {
	*venueFixture
	failures, reads int
	onRead          func()
}

func (v *transientStopVenue) Venue() string { return "binance" }
func (v *transientStopVenue) LookupOrder(ctx context.Context, o execution.Order) (execution.Order, error) {
	if o.Role == "sl" {
		v.reads++
		if v.onRead != nil {
			v.onRead()
		}
		if v.failures > 0 {
			v.failures--
			return o, &execution.RetryableError{Message: "temporary lookup", Delay: 5 * time.Second}
		}
	}
	return v.venueFixture.LookupOrder(ctx, o)
}
func TestStopTransientConfirmationRecoversWithoutResubmit(t *testing.T) {
	r, v, _, add := setup(t)
	w := &transientStopVenue{venueFixture: v, failures: 1}
	r.venue = w
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	if v.held.IsZero() || len(v.orders) != 2 {
		t.Fatalf("flattened transient stop: %+v", v.orders)
	}
	_ = r.Reconcile(ctx)
	if w.reads != 1 {
		t.Fatal("retried before backoff")
	}
	v.now = v.now.Add(5 * time.Second)
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if s.Pending != nil || len(v.orders) != 2 || s.Positions["BTCUSDT"].Stop == nil {
		t.Fatal("failed recovery or resubmitted")
	}
}
func TestStopRetryDeadlineSurvivesRestart(t *testing.T) {
	r, v, s, add := setup(t)
	w := &transientStopVenue{venueFixture: v, failures: 100}
	r.venue = w
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.now = v.now.Add(16 * time.Second)
	r2, err := New(r.db, r.id, r.account, s, w, v)
	if err != nil {
		t.Fatal(err)
	}
	r2.now = func() time.Time { return v.now }
	_ = r2.Reconcile(ctx)
	if !v.held.IsZero() {
		t.Fatal("restart extended unprotected exposure")
	}
}

func TestStopConfirmationRescuesAfterThirdTransientFailure(t *testing.T) {
	r, v, _, add := setup(t)
	w := &transientStopVenue{venueFixture: v, failures: 3}
	r.venue = w
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.now = v.now.Add(5 * time.Second)
	_ = r.Reconcile(ctx)
	if v.held.IsZero() {
		t.Fatal("rescued before third confirmation")
	}
	v.now = v.now.Add(5 * time.Second)
	_ = r.Reconcile(ctx)
	if !v.held.IsZero() || len(v.orders) != 3 {
		t.Fatal("missing bounded rescue or replayed stop")
	}
}

func TestStopRetryReservationPersistsBackoffBeforeRead(t *testing.T) {
	r, v, _, add := setup(t)
	w := &transientStopVenue{venueFixture: v, failures: 1}
	r.venue = w
	w.onRead = func() {
		s, err := r.load()
		if err != nil {
			t.Fatal(err)
		}
		if s.Pending != nil && s.Pending.Stage == "stop" && (!s.Pending.StopConfirmNext.After(v.now) || s.Pending.StopConfirmAttempts < 1) {
			t.Error("retry reservation lacks durable backoff")
		}
	}
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
}
