package allincrypto

import (
	"context"
	"strings"
	"testing"
	"time"

	"nofx/store"
)

func TestSourceSwitchArchivesHistoryAndNeverReplays(t *testing.T) {
	r, v, _, add := setup(t)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.save(old, nil, r.record(&pending{}, "old-period-marker", false)); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Create(&sourceLedgerEntry{TraderID: r.id, Key: "old-ledger", Sequence: 1}).Error; err != nil {
		t.Fatal(err)
	}
	var originalSignals int64
	r.db.Model(&signal{}).Where("trader_id = ?", r.id).Count(&originalSignals)
	r.SourceID = "123"
	add("BUY", "5") // Already present when the new period starts.
	if err := r.Poll(ctx); err != nil {
		t.Fatalf("flat source switch failed: %v", err)
	}
	current, err := r.load()
	if err != nil {
		t.Fatal(err)
	}
	if current.SourceID != "123" || len(v.orders) != 0 || current.Head == "" {
		t.Fatal("new period replayed history or failed to bootstrap")
	}
	var archives []struct{ ID, TraderID, SourceID, Body string }
	if err := r.db.Table("allincrypto_periods").Find(&archives).Error; err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 || archives[0].TraderID != r.id || archives[0].SourceID != store.DefaultCopyPortfolioID {
		t.Fatalf("missing archive: %+v", archives)
	}
	var archivedSignals int64
	r.db.Model(&signal{}).Where("trader_id = ?", archives[0].ID).Count(&archivedSignals)
	if archivedSignals != originalSignals {
		t.Fatal("lost archived signals")
	}
	var archivedLedger int64
	r.db.Model(&sourceLedgerEntry{}).Where("trader_id = ?", archives[0].ID).Count(&archivedLedger)
	if archivedLedger != 1 {
		t.Fatal("lost source ledger archive")
	}
	if err := r.RecordRuntimeStatus(nil, false); err != nil {
		t.Fatal(err)
	}
	var report store.DecisionRecordDB
	r.db.Order("id desc").First(&report)
	if strings.Contains(report.ExecutionLog, "old-period-marker") {
		t.Fatal("new period showed previous source signal")
	}
	var oldReports int64
	r.db.Model(&store.DecisionRecordDB{}).Where("trader_id = ? AND execution_log LIKE ?", r.id, "%old-period-marker%").Count(&oldReports)
	if oldReports != 1 {
		t.Fatal("old decision history lost")
	}
	if err := r.save(old, nil, nil); err == nil {
		t.Fatal("stale previous-period writer accepted")
	}
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var count int64
	r.db.Table("allincrypto_periods").Count(&count)
	if count != 1 {
		t.Fatal("same source restart created a new period")
	}
	r.SourceID = store.DefaultCopyPortfolioID
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	r.db.Table("allincrypto_periods").Count(&count)
	if count != 2 || len(v.orders) != 0 {
		t.Fatal("switching back reused old state or traded history")
	}
}

func TestSourceSwitchRejectsUnfinishedOrInvalidState(t *testing.T) {
	for _, mode := range []string{"position", "pending", "waiting", "fault", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			r, v, _, _ := setup(t)
			s, _ := r.load()
			switch mode {
			case "position":
				s.Positions["BTCUSDT"] = &position{}
			case "pending":
				s.Pending = &pending{}
			case "waiting":
				s.Waiting["BTCUSDT"] = &pending{}
			case "fault":
				s.SourceFault = "ambiguous history"
			case "invalid":
				s.Positions = nil
			}
			if err := r.save(s, nil, nil); err != nil {
				t.Fatal(err)
			}
			r.SourceID = "123"
			if err := r.Poll(context.Background()); err == nil {
				t.Fatal("unsafe switch accepted")
			}
			if len(v.orders) != 0 {
				t.Fatal("unsafe switch placed orders")
			}
		})
	}
}

func TestSourceSwitchArchiveFailureRollsBack(t *testing.T) {
	r, _, _, _ := setup(t)
	ctx := context.Background()
	if err := r.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var before checkpoint
	r.db.First(&before, "trader_id = ?", r.id)
	if err := r.db.Exec(`CREATE TRIGGER reject_archive BEFORE UPDATE ON allincrypto_signals BEGIN SELECT RAISE(ABORT, 'archive unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	r.SourceID = "123"
	if err := r.Poll(ctx); err == nil {
		t.Fatal("failed archive accepted")
	}
	var after checkpoint
	r.db.First(&after, "trader_id = ?", r.id)
	if before.Body != after.Body || before.Revision != after.Revision {
		t.Fatal("partial period switch committed")
	}
	var n int64
	r.db.Table("allincrypto_periods").Count(&n)
	if n != 0 {
		t.Fatal("partial archive committed")
	}
}

func TestSourceSwitchChecksStoppingOutsideSingleConnectionTransaction(t *testing.T) {
	r, _, _, _ := setup(t)
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := r.db.DB()
	sqlDB.SetMaxOpenConns(1)
	r.SourceID = "123"
	var stopReadErr error
	r.Stopping = func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var n int64
		stopReadErr = r.db.WithContext(ctx).Model(&checkpoint{}).Count(&n).Error
		return stopReadErr != nil
	}
	if err := r.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stopReadErr != nil {
		t.Fatalf("stop check held the transaction connection: %v", stopReadErr)
	}
}
