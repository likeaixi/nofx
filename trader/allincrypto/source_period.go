package allincrypto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"nofx/store"
)

// Source signals and source-ledger rows retain their original keys under ID.
// Executed orders and decision history stay attached to the real trader ID.
type sourcePeriod struct {
	ID        string `gorm:"primaryKey"`
	TraderID  string `gorm:"index"`
	SourceID  string
	Account   string
	Revision  int64
	StartedAt int64
	EndedAt   int64
	Body      string
}

func (sourcePeriod) TableName() string { return "allincrypto_periods" }
func newPeriodID() string              { return "copy-period:" + uuid.NewString() }

// Only a full poll may switch sources. Reconcile/stop must never silently
// adopt a new source while draining the previous period's orders.
func (r *Runner) prepareSourcePeriod(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Stopping may read the database; evaluate it before acquiring the SQLite
	// transaction connection (production uses a single database connection).
	if r.Stopping != nil && r.Stopping() {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row checkpoint
		err := tx.First(&row, "trader_id = ?", r.id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Account != r.account {
			return errors.New("follow state belongs to another exchange account")
		}
		var old state
		if err := json.Unmarshal([]byte(row.Body), &old); err != nil {
			return err
		}
		sourceID := old.SourceID
		if sourceID == "" {
			sourceID = store.DefaultCopyPortfolioID
		}
		if sourceID == r.sourceID() {
			return nil
		}
		if old.Positions == nil || old.SourceFault != "" {
			return errors.New("copy source switch requires a valid, reviewed previous source ledger")
		}
		if len(old.Positions) > 0 || old.Pending != nil || len(old.Waiting) > 0 {
			return errors.New("copy source switch blocked: previous period has followed positions or unresolved orders; restore its source and reconcile first")
		}
		archiveID := old.PeriodID
		if archiveID == "" {
			archiveID = newPeriodID()
		}
		now := r.now().UTC()
		archived := sourcePeriod{ID: archiveID, TraderID: r.id, SourceID: sourceID, Account: row.Account, Revision: row.Revision, StartedAt: old.PeriodStartedAt, EndedAt: now.UnixMilli(), Body: row.Body}
		if err := tx.Create(&archived).Error; err != nil {
			return err
		}
		if err := tx.Model(&signal{}).Where("trader_id = ?", r.id).Update("trader_id", archiveID).Error; err != nil {
			return err
		}
		if err := tx.Model(&sourceLedgerEntry{}).Where("trader_id = ?", r.id).Update("trader_id", archiveID).Error; err != nil {
			return err
		}
		var decisionFloor int64
		if err := tx.Model(&store.DecisionRecordDB{}).Where("trader_id = ?", r.id).Select("COALESCE(MAX(id), 0)").Scan(&decisionFloor).Error; err != nil {
			return err
		}
		fresh := state{SourceID: r.sourceID(), PeriodID: newPeriodID(), PeriodStartedAt: now.UnixMilli(), PeriodDecisionFloor: decisionFloor, Positions: map[string]*position{}, Cycle: old.Cycle + 1}
		body, err := json.Marshal(fresh)
		if err != nil {
			return err
		}
		q := tx.Model(&checkpoint{}).Where("trader_id = ? AND revision = ?", r.id, row.Revision).Updates(map[string]any{"body": string(body), "revision": row.Revision + 1})
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errors.New("concurrent source period change; refresh required")
		}
		return store.NewDecisionStore(tx).LogDecision(&store.DecisionRecord{
			TraderID: r.id, Timestamp: now, CycleNumber: fresh.Cycle, Success: true,
			ExecutionLog: []string{fmt.Sprintf("跟单周期切换：%s → %s", sourceID, r.sourceID()), "旧周期历史已归档；新周期仅跟随初始化完成后的新成交，不补开历史订单。", "跟单周期：" + fresh.PeriodID},
		})
	})
}
