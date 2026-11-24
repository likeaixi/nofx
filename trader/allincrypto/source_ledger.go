package allincrypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"nofx/provider/binancecopy"
)

// Source journal and its checkpoint commit atomically; execution consumption
// progresses independently through the existing signal table.
// This is source exposure, never proof of an actual follower fill.
type sourceLedgerEntry struct {
	Sequence        int64  `gorm:"index"`
	OrderUpdateTime int64  `gorm:"index"`
	TraderID        string `gorm:"primaryKey"`
	Key             string `gorm:"primaryKey"`
	Fingerprint     string
	EventJSON       string
	Before          string
	After           string
	Baseline        int64
}

func (sourceLedgerEntry) TableName() string { return "allincrypto_source_ledger" }

type baselineHistory interface {
	HistorySince(context.Context, int64) (binancecopy.Snapshot, error)
}

func (r *Runner) bootstrapSourceLedger(ctx context.Context, s *state) error {
	if s.Pending != nil || len(s.Waiting) > 0 || len(s.Positions) > 0 {
		return errors.New("source baseline migration requires no outstanding followed exposure")
	}
	if s.SourceFault != "" {
		return errors.New("source fault must be reviewed before baseline migration")
	}
	source, ok := r.source.(baselineHistory)
	if !ok {
		return errors.New("source does not support confirmed baseline history")
	}
	baseline := r.ConfirmedSourceBaseline.UnixMilli()
	if baseline <= 0 || baseline > r.now().UnixMilli() {
		return errors.New("confirmed source baseline must be in the past")
	}
	batch, err := r.sharedSourceHistory(ctx, baseline)
	if err != nil {
		batch, err = source.HistorySince(ctx, baseline)
	}
	if err != nil {
		return fmt.Errorf("confirmed source ledger bootstrap: %w", err)
	}
	if batch.Head == "" || len(batch.Events) == 0 {
		return errors.New("empty source baseline history")
	}
	s.Lead = map[string]decimal.Decimal{}
	s.Last = map[string]binancecopy.Event{}
	s.LedgerLast = map[string]binancecopy.Event{}
	s.LedgerBaseline = baseline
	s.LedgerSequence = 0
	var unseen []binancecopy.Event
	for _, e := range batch.Events {
		var old signal
		err = r.db.First(&old, "trader_id = ? AND key = ?", r.id, e.Key()).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			unseen = append(unseen, e)
		} else if err != nil {
			return err
		} else if old.Fingerprint != e.Fingerprint() {
			return errors.New("source fill revised during baseline migration")
		}
		if e.OrderUpdateTime < baseline {
			continue
		}
		if err = r.applySourceFill(s, e); err != nil {
			return err
		}
	}
	s.Head = batch.Head
	s.LedgerHead = batch.Head
	// Migration only rebuilds bookkeeping. Never catch up historical executions.
	s.Baseline = r.now().UnixMilli()
	return r.save(s, unseen, r.record(&pending{}, "source ledger rebuilt from user-confirmed flat baseline; historical fills recorded without trading", true))
}

func (r *Runner) applySourceFill(s *state, e binancecopy.Event) error {
	if e.OrderUpdateTime < s.LedgerBaseline {
		return nil
	}
	if (e.PositionSide != "LONG" && e.PositionSide != "SHORT") || (e.Side != "BUY" && e.Side != "SELL") || !e.ExecutedQty.IsPositive() {
		return errors.New("invalid source ledger direction or quantity")
	}
	if e.OrderTime < s.LedgerBaseline {
		return errors.New("source order crosses confirmed flat baseline; review required")
	}
	k := e.Symbol + "/" + strings.ToLower(e.PositionSide)
	lastKey := e.Symbol + "/" + e.PositionSide
	if old, ok := s.LedgerLast[lastKey]; ok && (old.OrderUpdateTime > e.OrderUpdateTime || old.OrderUpdateTime == e.OrderUpdateTime && old.Side != e.Side) {
		return errors.New("late or ambiguously ordered source ledger fill")
	}
	before := s.Lead[k]
	after := before.Add(e.ExecutedQty)
	if !e.Opening() {
		after = before.Sub(e.ExecutedQty)
	}
	if after.IsNegative() {
		return errors.New("lead reduction exceeds source ledger position")
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.LedgerSequence++
	s.ledgerWrites = append(s.ledgerWrites, sourceLedgerEntry{Sequence: s.LedgerSequence, OrderUpdateTime: e.OrderUpdateTime, TraderID: r.id, Key: e.Key(), Fingerprint: e.Fingerprint(), EventJSON: string(raw), Before: before.String(), After: after.String(), Baseline: s.LedgerBaseline})
	s.Lead[k] = after
	s.LedgerLast[lastKey] = e
	return nil
}

// Source collection progresses independently of uncertain target orders. Only
// execution consumes signals; unconsumed journal rows form the durable backlog.
func (r *Runner) syncSourceLedger(ctx context.Context, s *state) error {
	batch, err := r.source.Snapshot(ctx, s.LedgerHead)
	if err != nil {
		return err
	}
	var fresh []binancecopy.Event
	for _, e := range batch.Events {
		if e.OrderUpdateTime < s.LedgerBaseline {
			continue
		}
		var old sourceLedgerEntry
		err = r.db.First(&old, "trader_id = ? AND key = ?", r.id, e.Key()).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fresh = append(fresh, e)
		} else if err != nil {
			return err
		} else if old.Fingerprint != e.Fingerprint() {
			return r.sourceFault(s, "source ledger fill revised; review required")
		}
	}
	for _, e := range fresh {
		if err = r.applySourceFill(s, e); err != nil {
			return r.sourceFault(s, err.Error())
		}
	}
	if len(fresh) == 0 && s.LedgerHead == batch.Head {
		return nil
	}
	s.LedgerHead = batch.Head
	return r.save(s, nil, nil)
}

func (r *Runner) sourceLedgerBacklog(s *state) (binancecopy.Snapshot, error) {
	var rows []sourceLedgerEntry
	err := r.db.Where("trader_id = ?", r.id).Where("NOT EXISTS (SELECT 1 FROM allincrypto_signals consumed WHERE consumed.trader_id = allincrypto_source_ledger.trader_id AND consumed.key = allincrypto_source_ledger.key)").Order("sequence ASC").Find(&rows).Error
	if err != nil {
		return binancecopy.Snapshot{}, err
	}
	out := binancecopy.Snapshot{Head: s.LedgerHead}
	for _, row := range rows {
		var e binancecopy.Event
		if err = json.Unmarshal([]byte(row.EventJSON), &e); err != nil {
			return binancecopy.Snapshot{}, err
		}
		out.Events = append(out.Events, e)
	}
	return out, nil
}
