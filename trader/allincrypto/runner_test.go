package allincrypto

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"nofx/planning/core"
	"nofx/provider/binancecopy"
	"nofx/store"
	"nofx/trader/execution"
)

type sourceFixture struct {
	initial    decimal.Decimal
	proofFails bool
	events     []binancecopy.Event
	balance    string
}

func (f *sourceFixture) PositionBefore(context.Context, binancecopy.Event) (decimal.Decimal, error) {
	if f.proofFails {
		return decimal.Zero, errors.New("no flat proof")
	}
	return f.initial, nil
}
func (f *sourceFixture) Snapshot(_ context.Context, _ string) (binancecopy.Snapshot, error) {
	return binancecopy.Snapshot{Head: f.events[len(f.events)-1].Fingerprint(), Events: f.events}, nil
}
func (f *sourceFixture) Balance(context.Context) (decimal.Decimal, error) {
	return decimal.NewFromString(f.balance)
}

type venueFixture struct {
	delayedOriginal                           bool
	reads                                     int
	stableBalance, fx                         string
	available                                 string
	leverageErr, stale                        bool
	held                                      decimal.Decimal
	orders                                    []execution.Order
	unknown, stopFails, partial, ignoreCancel bool
	canceled                                  map[string]bool
	stopFilled                                bool
	tripOldOnAdd, partialRescue               bool
	oldStopID                                 string
	price                                     string
	stops                                     int
	side                                      string
	now                                       time.Time
}

func (v *venueFixture) Venue() string                           { return "okx" }
func (v *venueFixture) RouteClientID(id string) (string, error) { return id, nil }
func (v *venueFixture) PrepareExecutionLeverage(_ context.Context, _ string, l int) error {
	if v.leverageErr || l != 10 {
		return errors.New("wrong leverage")
	}
	return nil
}
func (v *venueFixture) CopyCollateral(context.Context) (map[string]string, string, error) {
	available := v.available
	if available == "" {
		available = "1000"
	}
	return map[string]string{"USDT": "1000", "USDC": v.stableBalance}, available, nil
}
func (v *venueFixture) AccountSnapshot(context.Context, string) (execution.Account, error) {
	return execution.Account{Equity: "1000", Available: "1000", FeeBPS: "5", Position: execution.Decimal(v.held.String()), OpenOrders: v.stops}, nil
}
func (v *venueFixture) VerifyCopyPosition(_ context.Context, _, side, qty string, _ bool) error {
	if !v.held.Equal(decimal.RequireFromString(qty)) || !v.held.IsZero() && side != v.side {
		return errors.New("foreign position")
	}
	return nil
}
func (v *venueFixture) SubmitOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	v.orders = append(v.orders, o)
	if o.Role == "sl" {
		if v.stopFails {
			return o, errors.New("stop fault")
		}
		v.stops++
		o.Status = "NEW"
		o.Filled = "0"
		return o, nil
	}
	if v.unknown {
		return o, errors.New("unknown order")
	}
	q := decimal.RequireFromString(string(o.Quantity))
	if o.ReduceOnly {
		if v.partialRescue {
			v.partialRescue = false
			q = q.Div(decimal.NewFromInt(2))
			v.held = v.held.Sub(q)
			o.Status = "CANCELED"
			o.Filled = execution.Decimal(q.String())
			return o, nil
		}
		v.held = v.held.Sub(q)
	} else {
		if v.partial {
			q = q.Div(decimal.NewFromInt(2))
		}
		v.held = v.held.Add(q)
		v.side = o.Side
		if v.tripOldOnAdd {
			v.tripOldOnAdd = false
			v.oldStopID = v.orders[1].ClientID
			v.held = v.held.Sub(decimal.RequireFromString(string(v.orders[1].Quantity)))
			v.stops--
		}
	}
	o.Status = "FILLED"
	o.Filled = o.Quantity
	if v.partial && !o.ReduceOnly {
		o.Status = "PARTIALLY_FILLED"
		o.Filled = execution.Decimal(q.String())
	}
	o.Average = execution.Decimal(v.price)
	return o, nil
}
func (v *venueFixture) LookupOrder(_ context.Context, o execution.Order) (execution.Order, error) {
	if v.delayedOriginal && o.Role == "entry" && !o.ReduceOnly {
		v.reads++
		if v.reads == 1 {
			v.held = decimal.RequireFromString(string(o.Quantity))
			o.Status = "PARTIALLY_FILLED"
			o.Filled = "1"
			return o, nil
		}
		if v.reads == 2 {
			return o, errors.New("final fill unavailable")
		}
		o.Filled = o.Quantity
		o.Status = "FILLED"
		o.Average = "100.2"
		return o, nil
	}
	if v.canceled[o.ClientID] {
		o.Status = "CANCELED"
		o.Filled = "0"
		return o, nil
	}
	if o.Role == "sl" && (v.stopFilled || o.ClientID == v.oldStopID) {
		o.Status = "FILLED"
		o.Filled = o.Quantity
		return o, nil
	}
	if o.Role == "sl" && !v.stopFails {
		o.Status = "NEW"
		o.Filled = "0"
		return o, nil
	}
	return o, errors.New("unconfirmed")
}
func (v *venueFixture) CancelExecutionOrder(_ context.Context, o execution.Order) error {
	if o.Role != "sl" || v.ignoreCancel {
		return nil
	}
	if v.canceled == nil {
		v.canceled = map[string]bool{}
	}
	v.canceled[o.ClientID] = true
	v.stops--
	return nil
}
func (v *venueFixture) SnapshotKind(_ context.Context, venue, symbol, kind string) (core.Snapshot, error) {
	now := v.now
	if v.stale {
		now = now.Add(-time.Hour)
	}
	if kind == "spot" {
		if v.fx == "" {
			return core.Snapshot{}, errors.New("fx unavailable")
		}
		return core.Snapshot{Venue: venue, Symbol: symbol, Kind: kind, Bid: core.Decimal(v.fx), Ask: core.Decimal(v.fx), SourceTime: now, Observed: now, BidSize: "100", AskSize: "100", Step: "0.1", MinQty: "0.1", Tick: "0.1", MinNotional: "1"}, nil
	}
	return core.Snapshot{Venue: venue, Symbol: symbol, Kind: kind, BaseAsset: "BTC", QuoteAsset: "USDT", ContractValue: "0.1", MarketStep: "0.1", MarketMin: "0.1", MarketMax: "100", Bid: "100", Ask: "100", BidSize: "100", AskSize: "100", Step: "0.1", MinQty: "0.1", Tick: "0.1", MinNotional: "1", Observed: now, SourceTime: now}, nil
}
func setup(t *testing.T) (*Runner, *venueFixture, *sourceFixture, func(string, string)) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "state.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&store.DecisionRecordDB{}); err != nil {
		t.Fatal(err)
	}
	v := &venueFixture{stableBalance: "0", now: time.Now(), price: "100.2"}
	s := &sourceFixture{balance: "5000"}
	r, err := New(db, "trader", "account", s, v, v)
	if err != nil {
		t.Fatal(err)
	}
	r.now = func() time.Time { return v.now }
	add := func(side, q string) {
		v.now = v.now.Add(time.Second)
		s.events = append(s.events, binancecopy.Event{Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", PositionSide: "LONG", Side: side, ExecutedQty: decimal.RequireFromString(q), AvgPrice: decimal.NewFromInt(100), OrderTime: v.now.UnixMilli(), OrderUpdateTime: v.now.UnixMilli()})
	}
	add("BUY", "99")
	return r, v, s, add
}
func TestBaselineAddsReductionsAndClose(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(v.orders) != 0 {
		t.Fatal("baseline traded")
	}
	for _, step := range []struct{ side, q, held string }{{"BUY", "10", "2"}, {"BUY", "5", "3"}, {"SELL", "5", "2"}, {"SELL", "10", "0"}} {
		add(step.side, step.q)
		if err := r.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		if v.held.String() != step.held {
			t.Fatal(v.held, step)
		}
	}
	if v.orders[0].Symbol != "BTC-USDT-SWAP" || v.orders[1].Price != "96.2" || v.orders[1].Quantity != "2" {
		t.Fatal(v.orders)
	}
	n := len(v.orders)
	if err := r.Poll(ctx); err != nil || n != len(v.orders) {
		t.Fatal("replay", err)
	}
}
func TestUnconfirmedOrderNeverResubmitsOnRestart(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	add("BUY", "10")
	v.unknown = true
	if err := r.Poll(ctx); err == nil {
		t.Fatal("expected reconciliation")
	}
	r2, err := New(r.db, "trader", "account", s, v, v)
	if err != nil {
		t.Fatal(err)
	}
	if err = r2.Poll(ctx); err == nil || len(v.orders) != 1 {
		t.Fatal(err, v.orders)
	}
}

func TestConfirmedButUnpriceableOrPartialEntryRescuesExposure(t *testing.T) {
	for _, mode := range []string{"missing average", "unresolved partial", "unknown stop"} {
		t.Run(mode, func(t *testing.T) {
			r, v, _, add := setup(t)
			ctx := context.Background()
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			add("BUY", "10")
			switch mode {
			case "missing average":
				v.price = ""
			case "unresolved partial":
				v.partial = true
			case "unknown stop":
				v.stopFails = true
			}
			_ = r.Poll(ctx)
			if !v.held.IsZero() || !v.orders[len(v.orders)-1].ReduceOnly {
				t.Fatalf("unprotected exposure %s, orders %+v", v.held, v.orders)
			}
			n := len(v.orders)
			_ = r.Poll(ctx)
			if len(v.orders) != n {
				t.Fatal("market order replay")
			}
		})
	}
}
func TestUnconfirmedCancellationBlocksAnotherEntry(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.ignoreCancel = true
	add("BUY", "5")
	if err := r.Poll(ctx); err == nil {
		t.Fatal("unconfirmed old stop accepted")
	}
	n := len(v.orders)
	add("BUY", "8")
	if err := r.Poll(ctx); err == nil || len(v.orders) != n {
		t.Fatal("new order before stop reconciliation")
	}
}
func TestStopFillAndForeignPositionNeverOpenReverse(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.stopFilled = true
	v.held = decimal.Zero
	v.stops = 0
	add("SELL", "10")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(v.orders) != 2 {
		t.Fatal("reverse order", v.orders)
	}
	v.held = decimal.NewFromInt(3)
	add("BUY", "8")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(v.orders) != 2 {
		t.Fatal("adopted foreign position")
	}
}

func TestStablecoinEquityUsesFreshFX(t *testing.T) {
	for _, fx := range []string{"0.8", ""} {
		r, v, _, add := setup(t)
		ctx := context.Background()
		_ = r.Poll(ctx)
		v.stableBalance = "1000"
		v.fx = fx
		add("BUY", "10")
		if err := r.Poll(ctx); err != nil {
			t.Fatal(err)
		}
		want := "3.6"
		if fx == "" {
			want = "0"
		}
		if v.held.String() != want {
			t.Fatalf("FX %q: got %s want %s", fx, v.held, want)
		}
	}
}
func TestShortAndMarketEligibility(t *testing.T) {
	for _, mode := range []string{"short", "invalid balance", "stale book", "leverage", "scaled asset", "unknown direction", "old source", "revised order"} {
		t.Run(mode, func(t *testing.T) {
			r, v, s, add := setup(t)
			ctx := context.Background()
			_ = r.Poll(ctx)
			add("SELL", "10")
			e := &s.events[len(s.events)-1]
			e.PositionSide = "SHORT"
			switch mode {
			case "invalid balance":
				s.balance = "0"
			case "stale book":
				v.stale = true
			case "leverage":
				v.leverageErr = true
			case "scaled asset":
				e.BaseAsset = "1000PEPE"
				e.Symbol = "1000PEPEUSDT"
			case "unknown direction":
				e.PositionSide = "BOTH"
			case "old source":
				e.OrderTime -= 600000
				e.OrderUpdateTime -= 600000
			case "revised order":
				s.events[0].AvgPrice = decimal.NewFromInt(11)
			}
			err := r.Poll(ctx)
			if mode == "revised order" && err == nil {
				t.Fatal("revision accepted")
			}
			if mode == "short" {
				if len(v.orders) != 2 || v.orders[0].Side != "short" || v.orders[1].Price != "104.2" {
					t.Fatal(v.orders)
				}
			} else if len(v.orders) != 0 {
				t.Fatal(v.orders)
			}
		})
	}
}

func TestSkippedLeadAddCountsTowardReductionAndCloseRetries(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	s.balance = "0"
	add("BUY", "10")
	_ = r.Poll(ctx)
	add("SELL", "10")
	if err := r.Poll(ctx); err != nil || v.held.String() != "1" {
		t.Fatalf("after half close: %s, %v", v.held, err)
	}
	add("SELL", "10")
	v.stale = true
	if err := r.Poll(ctx); err == nil {
		t.Fatal("failed exit not retained")
	}
	v.stale = false
	s.events = nil // Recovery is independent of lead history.
	if err := r.Reconcile(ctx); err != nil || !v.held.IsZero() {
		t.Fatal(v.held, err)
	}
}

func TestHistoricalExposureAffectsRatioWithoutBeingCopied(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	s.initial = decimal.NewFromInt(90)
	add("BUY", "10")
	_ = r.Poll(ctx)
	add("SELL", "10")
	if err := r.Poll(ctx); err != nil || v.held.String() != "1.8" {
		t.Fatal(v.held, err)
	}
}

func TestOldStopDuringAddRescuesOnlyVerifiedRemainder(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.tripOldOnAdd = true
	add("BUY", "5")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	last := v.orders[len(v.orders)-1]
	if !last.ReduceOnly || last.Quantity != "1" || !v.held.IsZero() {
		t.Fatal(v.held, v.orders)
	}
}
func TestTerminalPartialRescueUsesNewIDForOwnedResidual(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	v.stopFails = true
	v.partialRescue = true
	add("BUY", "10")
	_ = r.Poll(ctx)
	if v.held.String() != "1" {
		t.Fatal(v.held)
	}
	_ = r.Reconcile(ctx)
	n := len(v.orders)
	if n != 4 || !v.held.IsZero() || v.orders[n-1].Quantity != "1" || v.orders[n-1].ClientID == v.orders[n-2].ClientID {
		t.Fatal(v.held, v.orders)
	}
}
func TestLateCloseBlocksAcrossPollsWithoutConsumingNewPosition(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	add("SELL", "10")
	e := &s.events[len(s.events)-1]
	e.OrderUpdateTime -= 1500
	e.OrderTime = e.OrderUpdateTime
	if err := r.Poll(ctx); err == nil || v.held.String() != "2" {
		t.Fatal(v.held, err)
	}
	s.events = s.events[:2]
	if err := r.Poll(ctx); err == nil {
		t.Fatal("ambiguous source auto-resumed")
	}
}
func TestStopWaitsForUnknownOrderAndPreventsNewEntries(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	r.Stopping = func() bool { return true }
	add("BUY", "10")
	_ = r.Poll(ctx)
	if len(v.orders) != 0 {
		t.Fatal("entry after stop")
	}
	r.Stopping = nil
	v.unknown = true
	_ = r.Poll(ctx)
	r.Stopping = func() bool { return true }
	done, err := r.Drain(ctx)
	if done || err == nil || len(v.orders) != 1 {
		t.Fatal(done, err, v.orders)
	}
}

func TestSubLotReductionsAccumulate(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	s.initial = decimal.NewFromInt(90)
	add("BUY", "10")
	_ = r.Poll(ctx)
	for range 5 {
		add("SELL", "1")
		if err := r.Poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if v.held.String() != "1.9" {
		t.Fatal("lost sub-lot reductions", v.held)
	}
}

func TestOriginalFinishesWhilePartialRescueIsReconciling(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	v.partial = true
	v.delayedOriginal = true
	add("BUY", "10")
	if err := r.Poll(ctx); err == nil || v.held.String() != "2" {
		t.Fatal(v.held, err)
	}
	_ = r.Reconcile(ctx)
	if !v.held.IsZero() || len(v.orders) != 2 || !v.orders[1].ReduceOnly || v.orders[1].Quantity != "2" {
		t.Fatal(v.held, v.orders)
	}
	if err := r.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestRetryingCloseRescuesAfterProtectiveCancellation(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	v.stale = true
	add("SELL", "3")
	_ = r.Poll(ctx)
	_ = v.CancelExecutionOrder(ctx, v.orders[1])
	if err := r.Reconcile(ctx); err != nil || !v.held.IsZero() {
		t.Fatal(v.held, err)
	}
}
func TestBlockedETHOrderStillRescuesBTC(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	s, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	s.Pending = &pending{Event: binancecopy.Event{Symbol: "ETHUSDT"}, Stage: "market", Order: execution.Order{Symbol: "ETH-USDT-SWAP", Role: "entry", ClientID: "unconfirmed"}}
	if err = r.save(s, nil, nil); err != nil {
		t.Fatal(err)
	}
	_ = v.CancelExecutionOrder(ctx, v.orders[1])
	if err = r.Reconcile(ctx); err == nil || !v.held.IsZero() {
		t.Fatal("unprotected BTC or lost ETH block", v.held, err)
	}
	s, err = r.load()
	if err != nil || s.Waiting["ETHUSDT"] == nil || len(s.Positions) != 0 {
		t.Fatal(s, err)
	}
}
func TestDelayedPreStartFillOnlyUpdatesSourceAndOverCloseBlocks(t *testing.T) {
	r, v, s, add := setup(t)
	ctx := context.Background()
	_ = r.Poll(ctx)
	add("BUY", "10")
	_ = r.Poll(ctx)
	add("BUY", "10")
	s.events[len(s.events)-1].OrderTime -= 10000
	if err := r.Poll(ctx); err != nil || v.held.String() != "2" {
		t.Fatal(v.held, err)
	}
	add("SELL", "10")
	if err := r.Poll(ctx); err != nil || v.held.String() != "1" {
		t.Fatal(v.held, err)
	}
	add("SELL", "11")
	if err := r.Poll(ctx); err == nil || v.held.String() != "1" {
		t.Fatal(v.held, err)
	}
}

type diagnosticBinanceFixture struct{ *venueFixture }

func (v diagnosticBinanceFixture) Venue() string { return "binance" }

func TestStopFailureDecisionRetainsLookupCause(t *testing.T) {
	r, v, _, add := setup(t)
	r.venue = diagnosticBinanceFixture{v}
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	v.stopFails = true
	add("BUY", "10")
	_ = r.Poll(ctx)
	var rows []store.DecisionRecordDB
	if err := r.db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.ExecutionLog, "replacement stop not confirmed") {
			if !strings.Contains(row.ExecutionLog, "lookup: unconfirmed") || !strings.Contains(row.ExecutionLog, "submit: stop fault") {
				t.Fatalf("lost stop lookup cause: %s", row.ExecutionLog)
			}
			return
		}
	}
	t.Fatal("missing stop failure decision")
}

func TestStopFailureDoesNotPersistUntrustedVenueErrors(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	v.stopFails = true
	add("BUY", "10")
	_ = r.Poll(ctx)
	var rows []store.DecisionRecordDB
	if err := r.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if strings.Contains(row.ExecutionLog, "replacement stop not confirmed") {
			found = true
		}
		if strings.Contains(row.ExecutionLog, "submit: stop fault") || strings.Contains(row.ExecutionLog, "lookup: unconfirmed") {
			t.Fatal("untrusted venue diagnostics persisted")
		}
	}
	if !found {
		t.Fatal("missing failure decision")
	}
}

func TestTenTimesEntryMarginIncludesFee(t *testing.T) {
	for _, tc := range []struct {
		available  string
		wantOrders int
	}{{"15", 0}, {"20", 0}, {"20.1", 2}} {
		t.Run(tc.available, func(t *testing.T) {
			r, v, _, add := setup(t)
			if err := r.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			v.available = tc.available
			add("BUY", "10")
			_ = r.Poll(context.Background())
			if len(v.orders) != tc.wantOrders {
				t.Fatalf("available=%s orders=%d, want %d", tc.available, len(v.orders), tc.wantOrders)
			}
		})
	}
}
