package allincrypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"nofx/provider/binancecopy"
)

type ledgerSourceFixture struct {
	*sourceFixture
	fail bool
}

func (s *ledgerSourceFixture) HistorySince(ctx context.Context, since int64) (binancecopy.Snapshot, error) {
	if s.fail {
		return binancecopy.Snapshot{}, errors.New("missing source page")
	}
	return s.Snapshot(ctx, "")
}

func TestConfirmedLedgerMigrationRestartAndFutureOrders(t *testing.T) {
	r, v, src, add := setup(t)
	ctx := context.Background()
	// Old implementation already consumed these historical fills but failed proof.
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	src.proofFails = true
	add("BUY", "10")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if s.Lead["BTCUSDT/long"].String() != "109" || len(v.orders) != 0 {
		t.Fatal("migration must only rebuild source", s.Lead, v.orders)
	}
	var count int64
	r.db.Model(&sourceLedgerEntry{}).Count(&count)
	if count != 2 {
		t.Fatal(count)
	}
	// Restart with durable configuration, repeat old records, then a new order.
	rr, err := New(r.db, r.id, r.account, r.source, v, v)
	if err != nil {
		t.Fatal(err)
	}
	rr.now = r.now
	if err = rr.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	add("BUY", "10")
	if err = rr.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if v.held.String() != "2" {
		t.Fatal("new entry did not execute", v.held)
	}
	s, _ = rr.load()
	if s.Lead["BTCUSDT/long"].String() != "119" {
		t.Fatal(s.Lead)
	}
	add("SELL", "59.5")
	if err = rr.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if v.held.String() != "1" {
		t.Fatal("half source reduction must halve followed lot", v.held)
	}
	if err = rr.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	rr.db.Model(&sourceLedgerEntry{}).Count(&count)
	if count != 4 {
		t.Fatal("duplicate ledger rows", count)
	}
}

func TestConfirmedLedgerTracksSkippedOrdersAndRejectsExcessClose(t *testing.T) {
	r, v, src, add := setup(t)
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	v.leverageErr = true // Local preflight rejects entry; source still gains quantity.
	add("BUY", "1")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if s.Lead["BTCUSDT/long"].String() != "100" || len(v.orders) != 0 {
		t.Fatal(s.Lead, v.orders)
	}
	add("SELL", "101")
	if err := r.Poll(ctx); err == nil {
		t.Fatal("accepted negative source exposure")
	}
	s, _ = r.load()
	if !s.Lead["BTCUSDT/long"].Equal(decimal.NewFromInt(100)) {
		t.Fatal("corrupted source", s.Lead)
	}
}

func TestConfirmedLedgerFailedBootstrapDoesNotConsume(t *testing.T) {
	r, v, src, _ := setup(t)
	feed := &ledgerSourceFixture{sourceFixture: src, fail: true}
	r.source = feed
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(context.Background()); err == nil {
		t.Fatal("accepted gap")
	}
	s, _ := r.load()
	if s.Head != "" || s.LedgerBaseline != 0 || len(v.orders) != 0 {
		t.Fatal("partial bootstrap committed")
	}
	feed.fail = false
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConfirmedLedgerActualNovember20Fills(t *testing.T) {
	r, v, src, _ := setup(t)
	raw, err := os.ReadFile("testdata/aoying-20261121.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data struct{ List []binancecopy.Event }
	}
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	sort.SliceStable(body.Data.List, func(i, j int) bool { return body.Data.List[i].OrderUpdateTime < body.Data.List[j].OrderUpdateTime })
	src.events = body.Data.List
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline, _ = time.Parse(time.RFC3339, "2026-11-20T00:00:00+08:00")
	v.now, _ = time.Parse(time.RFC3339, "2026-11-21T21:00:00+08:00")
	if err = r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if s.Lead["ZECUSDT/short"].String() != "727.875" || !s.Lead["MUUSDT/long"].IsZero() || len(v.orders) != 0 {
		t.Fatal(s.Lead, v.orders)
	}
	var entries []sourceLedgerEntry
	if err = r.db.Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 26 {
		t.Fatal("expected all 26 source fills", len(entries))
	}
	for _, e := range entries {
		if e.EventJSON == "" || e.Fingerprint == "" {
			t.Fatal("missing audit", e)
		}
	}
}

func TestConfirmedLedgerMigrationRejectsRevisionsAndExposure(t *testing.T) {
	for _, mode := range []string{"revised", "position", "pending", "unknown", "late"} {
		t.Run(mode, func(t *testing.T) {
			r, v, src, add := setup(t)
			ctx := context.Background()
			if err := r.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			s, _ := r.load()
			switch mode {
			case "position":
				s.Positions["BTCUSDT"] = &position{Symbol: "BTC-USDT-SWAP", Side: "long", Local: decimal.NewFromInt(1)}
			case "pending":
				s.Pending = &pending{Stage: "unknown"}
			case "revised":
				src.events[0].ExecutedQty = decimal.NewFromInt(100)
			case "unknown":
				add("BUY", "1")
				src.events[1].PositionSide = "BOTH"
			case "late":
				add("BUY", "1")
				src.events[1].OrderUpdateTime = src.events[0].OrderUpdateTime - 1
			}
			// Direct bootstrap isolates migration guards from normal order reconciliation.
			r.source = &ledgerSourceFixture{sourceFixture: src}
			r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 2)
			if err := r.bootstrapSourceLedger(ctx, s); err == nil {
				t.Fatal("accepted", mode)
			}
			persisted, _ := r.load()
			if persisted.LedgerBaseline != 0 || len(v.orders) != 0 {
				t.Fatal("partial migration")
			}
			var count int64
			r.db.Model(&sourceLedgerEntry{}).Count(&count)
			if count != 0 {
				t.Fatal("partial journal", count)
			}
		})
	}
}

func TestConfirmedLedgerCollectsWhileTargetOrderUnknown(t *testing.T) {
	r, v, src, add := setup(t)
	ctx := context.Background()
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	v.unknown = true
	add("BUY", "10")
	_ = r.Poll(ctx)
	n := len(v.orders)
	add("BUY", "5")
	_ = r.Poll(ctx)
	s, _ := r.load()
	if s.Lead["BTCUSDT/long"].String() != "114" {
		t.Fatal("target failure blocked source ledger", s.Lead)
	}
	if len(v.orders) != n {
		t.Fatal("submitted additional order during uncertain outcome")
	}
}

func TestConfirmedMigrationChecksBeforeReconciliation(t *testing.T) {
	r, v, src, _ := setup(t)
	ctx := context.Background()
	s, _ := r.load()
	s.Positions["BTCUSDT"] = &position{Symbol: "BTC-USDT-SWAP", Side: "long", Source: decimal.NewFromInt(1), Local: decimal.NewFromInt(1)}
	if err := r.save(s, nil, nil); err != nil {
		t.Fatal(err)
	}
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(ctx); err == nil {
		t.Fatal("migration accepted outstanding exposure")
	}
	after, _ := r.load()
	if len(after.Positions) != 1 || after.LedgerBaseline != 0 || len(v.orders) != 0 {
		t.Fatal("migration touched existing exposure", after.Positions, v.orders)
	}
}

func TestSourceLedgerPreservesAccountingOrderForSameTimestamp(t *testing.T) {
	r, v, src, add := setup(t)
	ctx := context.Background()
	r.source = &ledgerSourceFixture{sourceFixture: src}
	r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	add("BUY", "10")
	add("BUY", "20")
	src.events[1].OrderUpdateTime = src.events[2].OrderUpdateTime
	// Provider tie order differs from lexicographic order of identity keys.
	src.events[1], src.events[2] = src.events[2], src.events[1]
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := r.load()
	if s.Positions["BTCUSDT"].Source.String() != "129" {
		t.Fatal("execution reordered journal", s.Positions["BTCUSDT"].Source)
	}
	add("SELL", "64.5")
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if v.held.String() != "3" {
		t.Fatal("half reduction used wrong denominator", v.held)
	}
}

func TestBootstrapReusesVerifiedHistoryWithoutTrading(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			r, v, src, add := setup(t)
			r.source = &ledgerSourceFixture{sourceFixture: src}
			r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
			if err := r.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			donorState, _ := r.load()
			donorState.Positions["OTHERUSDT"] = &position{Symbol: "OTHERUSDT", Local: decimal.NewFromInt(7)}
			if err := r.save(donorState, nil, nil); err != nil {
				t.Fatal(err)
			}
			if corrupt {
				r.db.Model(&sourceLedgerEntry{}).Where("trader_id = ?", r.id).Update("after", "1")
			}
			add("BUY", "10")
			rr, err := New(r.db, "new-trader", "new-account", &ledgerSourceFixture{sourceFixture: src, fail: true}, v, v)
			if err != nil {
				t.Fatal(err)
			}
			rr.now = r.now
			rr.ConfirmedSourceBaseline = r.ConfirmedSourceBaseline
			err = rr.Poll(context.Background())
			if corrupt {
				if err == nil {
					t.Fatal("accepted corrupt donor")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s, _ := rr.load()
			if s.Lead["BTCUSDT/long"].String() != "109" || len(s.Positions) != 0 || s.Pending != nil || len(v.orders) != 0 {
				t.Fatal("unsafe bootstrap", s, v.orders)
			}
			if err = rr.Poll(context.Background()); err != nil || len(v.orders) != 0 {
				t.Fatal("historical replay", err)
			}
			add("BUY", "10")
			if err = rr.Poll(context.Background()); err != nil || v.held.String() != "2" {
				t.Fatal("new signal", err, v.held)
			}
		})
	}
}

func TestSharedBootstrapRejectsMissingJournalAndRevisedOverlap(t *testing.T) {
	for _, mode := range []string{"missing", "revision", "baseline", "before-baseline"} {
		t.Run(mode, func(t *testing.T) {
			r, v, src, add := setup(t)
			add("BUY", "1")
			r.source = &ledgerSourceFixture{sourceFixture: src}
			r.ConfirmedSourceBaseline = time.UnixMilli(src.events[0].OrderTime - 1)
			if err := r.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				r.db.Where("trader_id = ? AND sequence = ?", r.id, 1).Delete(&sourceLedgerEntry{})
			case "before-baseline":
				e := src.events[0]
				e.OrderTime = r.ConfirmedSourceBaseline.UnixMilli() - 2
				e.OrderUpdateTime = e.OrderTime
				raw, _ := json.Marshal(e)
				r.db.Model(&sourceLedgerEntry{}).Where("trader_id = ? AND sequence = ?", r.id, 1).Updates(map[string]any{"key": e.Key(), "fingerprint": e.Fingerprint(), "event_json": string(raw), "order_update_time": e.OrderUpdateTime})
			case "revision":
				src.events[0].AvgPrice = decimal.NewFromInt(101)
			}
			rr, err := New(r.db, "new-trader", "other-account", &ledgerSourceFixture{sourceFixture: src, fail: true}, v, v)
			if err != nil {
				t.Fatal(err)
			}
			rr.now = r.now
			rr.ConfirmedSourceBaseline = r.ConfirmedSourceBaseline
			if mode == "baseline" {
				rr.ConfirmedSourceBaseline = rr.ConfirmedSourceBaseline.Add(-time.Second)
			}
			if err = rr.Poll(context.Background()); err == nil {
				t.Fatal("accepted invalid history")
			}
			s, _ := rr.load()
			if s.LedgerBaseline != 0 || len(s.Positions) != 0 || len(v.orders) != 0 {
				t.Fatal("failed bootstrap changed state")
			}
			var count int64
			rr.db.Model(&sourceLedgerEntry{}).Where("trader_id = ?", rr.id).Count(&count)
			if count != 0 {
				t.Fatal("partial journal committed")
			}
		})
	}
}

func TestSharedPositionComparisonUsesNumericEquality(t *testing.T) {
	if !equalSourcePositions(map[string]decimal.Decimal{"BTC": decimal.RequireFromString("0.00")}, map[string]decimal.Decimal{"BTC": decimal.Zero}) {
		t.Fatal("decimal scales must not invalidate durable history")
	}
}
