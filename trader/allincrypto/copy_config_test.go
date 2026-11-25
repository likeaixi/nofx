package allincrypto

import (
	"context"
	"testing"
	"time"
)

type configurableVenue struct {
	*venueFixture
	prepared, verified int
}

func (v *configurableVenue) PrepareExecutionLeverage(_ context.Context, _ string, n int) error {
	v.prepared = n
	return nil
}
func (v *configurableVenue) VerifyCopyPosition(ctx context.Context, symbol, side, qty string, n int) error {
	if n > 0 {
		v.verified = n
	}
	return v.venueFixture.VerifyCopyPosition(ctx, symbol, side, qty, n)
}
func TestConfiguredLeverageMarginAndAdds(t *testing.T) {
	for _, lev := range []int{2, 5} {
		t.Run(string(rune('0'+lev)), func(t *testing.T) {
			r, v, _, add := setup(t)
			cv := &configurableVenue{venueFixture: v}
			r.venue = cv
			r.Leverage = lev
			v.available = "50"
			ctx := context.Background()
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			add("BUY", "10")
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			if lev == 2 {
				if len(v.orders) != 0 {
					t.Fatal("used 10x instead of selected margin requirement")
				}
				return
			}
			if cv.prepared != 5 || len(v.orders) == 0 {
				t.Fatal("configured leverage not submitted")
			}
			add("BUY", "1")
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			if cv.verified != 5 {
				t.Fatal("add did not verify configured leverage")
			}
		})
	}
}
func TestCheckpointRejectsDifferentCopySource(t *testing.T) {
	r, _, _, _ := setup(t)
	if _, err := r.load(); err != nil {
		t.Fatal(err)
	}
	r.SourceID = "123"
	if _, err := r.load(); err == nil {
		t.Fatal("adopted another source checkpoint")
	}
}
func TestCustomCopyBaselineDoesNotTradeHistory(t *testing.T) {
	r, v, _, _ := setup(t)
	r.SourceID = "123"
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := r.load()
	if err != nil || s.SourceID != "123" || s.Head == "" || len(v.orders) != 0 {
		t.Fatalf("baseline: %+v %v", s, err)
	}
}
func TestDonorHistoryCannotCrossSource(t *testing.T) {
	r, v, src, add := setup(t)
	add("BUY", "1")
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	rr, err := New(r.db, "another", "account2", &ledgerSourceFixture{sourceFixture: src, fail: true}, v, v)
	if err != nil {
		t.Fatal(err)
	}
	rr.SourceID = "123"
	rr.ConfirmedSourceBaseline = r.ConfirmedSourceBaseline
	if _, err = rr.sharedSourceHistory(context.Background(), r.ConfirmedSourceBaseline.UnixMilli()); err == nil {
		t.Fatal("shared another portfolio's journal")
	}
}
