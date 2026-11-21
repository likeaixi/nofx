package allincrypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"nofx/provider/binancecopy"
	"nofx/store"
	"nofx/trader/execution"
)

type checkpoint struct {
	TraderID string `gorm:"primaryKey"`
	Account  string
	Revision int64
	Body     string
}

func (checkpoint) TableName() string { return "allincrypto_checkpoints" }

type signal struct {
	TraderID    string `gorm:"primaryKey"`
	Key         string `gorm:"primaryKey"`
	Fingerprint string
}

func (signal) TableName() string { return "allincrypto_signals" }

type position struct {
	ReductionDust                decimal.Decimal
	Symbol, Side                 string
	Source, Local, Average, Tick decimal.Decimal
	Stop                         *execution.Order
}
type pending struct {
	DustAfter                              decimal.Decimal
	MarketFill                             *execution.Order
	Keep                                   decimal.Decimal
	StopDebits                             map[string]decimal.Decimal
	SourceAfter, Outstanding               decimal.Decimal
	USDCRate                               decimal.Decimal
	Event                                  binancecopy.Event
	Symbol, Side, Stage                    string
	Order                                  execution.Order
	Original                               *execution.Order // Unsettled partially filled entry being canceled.
	Rescued                                decimal.Decimal  // Cumulative original fill already handled.
	PreviousStop                           *execution.Order
	FaultStop                              *execution.Order
	Balance, Equity, Ratio, Notional, Tick decimal.Decimal
	Reason                                 string
}
type state struct {
	LedgerSequence      int64
	LedgerHead          string
	LedgerLast          map[string]binancecopy.Event
	LedgerBaseline      int64
	ledgerWrites        []sourceLedgerEntry
	LastRuntimeReportAt time.Time
	Waiting             map[string]*pending
	Last                map[string]binancecopy.Event
	SourceFault         string
	Lead                map[string]decimal.Decimal
	Head                string
	Baseline            int64
	Positions           map[string]*position
	Pending             *pending
	Cycle               int
	revision            int64
}

func (r *Runner) load() (*state, error) {
	var row checkpoint
	err := r.db.First(&row, "trader_id = ?", r.id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s := state{Positions: map[string]*position{}}
		b, _ := json.Marshal(s)
		row = checkpoint{TraderID: r.id, Account: r.account, Body: string(b)}
		if err = r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return nil, err
		}
		if err = r.db.First(&row, "trader_id = ?", r.id).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if row.Account != r.account {
		return nil, errors.New("follow state belongs to another exchange account")
	}
	var s state
	if err = json.Unmarshal([]byte(row.Body), &s); err != nil {
		return nil, err
	}
	if s.Positions == nil {
		return nil, errors.New("invalid follow state")
	}
	if s.Waiting == nil {
		s.Waiting = map[string]*pending{}
	}
	if s.LedgerLast == nil {
		s.LedgerLast = map[string]binancecopy.Event{}
	}
	if s.Last == nil {
		s.Last = map[string]binancecopy.Event{}
	}
	if s.Lead == nil {
		s.Lead = map[string]decimal.Decimal{}
	}
	s.revision = row.Revision
	return &s, nil
}
func (r *Runner) save(s *state, events []binancecopy.Event, record *store.DecisionRecord) error {
	if record != nil {
		s.Cycle++
		record.CycleNumber = s.Cycle
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&checkpoint{}).Where("trader_id = ? AND revision = ?", r.id, s.revision).Updates(map[string]any{"revision": s.revision + 1, "body": string(b)})
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errors.New("concurrent follow cycle; refresh required")
		}
		for _, entry := range s.ledgerWrites {
			if err := tx.Create(&entry).Error; err != nil {
				return err
			}
		}
		for _, e := range events {
			if err := tx.Create(&signal{r.id, e.Key(), e.Fingerprint()}).Error; err != nil {
				return err
			}
		}
		if record != nil {
			return store.NewDecisionStore(tx).LogDecision(record)
		}
		return nil
	})
	if err == nil {
		s.revision++
		s.ledgerWrites = nil
	}
	return err
}
func (r *Runner) record(p *pending, reason string, success bool) *store.DecisionRecord {
	detail, _ := json.Marshal(p)
	symbol := p.Symbol
	if symbol == "" {
		symbol = p.Event.Symbol
	}
	action := store.DecisionAction{Action: "allincrypto", Symbol: symbol, Leverage: 20, Success: success, Reasoning: reason}
	outcome := "已记录"
	if !success {
		outcome = "跳过或未完成"
		action.Skipped = true
		action.SkipReason = reason
	}
	if p.MarketFill != nil {
		if qty, err := decimal.NewFromString(string(p.MarketFill.Filled)); err == nil {
			action.Quantity, _ = qty.Float64()
		}
		if price, err := decimal.NewFromString(string(p.MarketFill.Average)); err == nil {
			action.Price, _ = price.Float64()
		}
	}
	if p.Order.Role == "sl" {
		if sl, err := decimal.NewFromString(string(p.Order.Price)); err == nil {
			action.StopLoss, _ = sl.Float64()
		}
	}
	return &store.DecisionRecord{TraderID: r.id, Timestamp: time.Now().UTC(), DecisionJSON: string(detail), ExecutionLog: []string{fmt.Sprintf("源信号：%s %s %s；目标交易所：%s；目标合约：%s", p.Event.Symbol, p.Event.PositionSide, p.Event.Side, r.venue.Venue(), symbol), "结果：" + outcome + "；原因：" + reason}, Success: success, Decisions: []store.DecisionAction{action}}
}

// Outstanding is a read-only guard for administrative changes. Unknown orders
// and unconfirmed protective cancellation retain ownership just like open lots.
func Outstanding(db *gorm.DB, traderID string) (bool, error) {
	return outstanding(db, "trader_id = ?", traderID)
}
func AccountOutstanding(db *gorm.DB, accountKey string) (bool, error) {
	return outstanding(db, "account = ?", accountKey)
}
func outstanding(db *gorm.DB, filter, key string) (bool, error) {
	if db == nil || key == "" {
		return false, errors.New("follow ownership lookup requires a database and identity")
	}
	if !db.Migrator().HasTable(&checkpoint{}) {
		return false, nil
	}
	var rows []checkpoint
	if err := db.Where(filter, key).Find(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		var s state
		if err := json.Unmarshal([]byte(row.Body), &s); err != nil {
			return false, err
		}
		if s.Pending != nil || len(s.Waiting) > 0 || len(s.Positions) > 0 {
			return true, nil
		}
	}
	return false, nil
}

func (r *Runner) sourceFault(s *state, reason string) error {
	s.SourceFault = reason
	if err := r.save(s, nil, r.record(&pending{}, reason, false)); err != nil {
		return err
	}
	return errors.New(reason)
}
