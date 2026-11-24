package allincrypto

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"nofx/provider/binancecopy"
)

// Reuse only the fixed portfolio's source journal. Never copy account state,
// execution cursors, positions or pending orders from a donor.
func (r *Runner) sharedSourceHistory(ctx context.Context, baseline int64) (binancecopy.Snapshot, error) {
	var history binancecopy.Snapshot
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var donors []checkpoint
		if err := tx.Where("trader_id <> ?", r.id).Order("trader_id").Find(&donors).Error; err != nil {
			return err
		}
		for _, row := range donors {
			var donor state
			if json.Unmarshal([]byte(row.Body), &donor) != nil || donor.SourceFault != "" || donor.LedgerBaseline != baseline || donor.LedgerSequence <= 0 {
				continue
			}
			var entries []sourceLedgerEntry
			if err := tx.Where("trader_id = ?", row.TraderID).Order("sequence ASC").Find(&entries).Error; err != nil {
				return err
			}
			if int64(len(entries)) != donor.LedgerSequence {
				continue
			}
			replay := &state{LedgerBaseline: baseline, Lead: map[string]decimal.Decimal{}, LedgerLast: map[string]binancecopy.Event{}}
			events := make([]binancecopy.Event, 0, len(entries))
			valid := true
			seen := map[string]bool{}
			last := int64(0)
			for i, entry := range entries {
				var e binancecopy.Event
				if json.Unmarshal([]byte(entry.EventJSON), &e) != nil || e.OrderUpdateTime < baseline || entry.Sequence != int64(i+1) || entry.Baseline != baseline || entry.Key != e.Key() || entry.Fingerprint != e.Fingerprint() || entry.OrderUpdateTime != e.OrderUpdateTime || seen[e.Key()] || e.OrderUpdateTime < last {
					valid = false
					break
				}
				seen[e.Key()] = true
				last = e.OrderUpdateTime
				if r.applySourceFill(replay, e) != nil {
					valid = false
					break
				}
				rebuilt := replay.ledgerWrites[len(replay.ledgerWrites)-1]
				if rebuilt.Before != entry.Before || rebuilt.After != entry.After {
					valid = false
					break
				}
				events = append(events, e)
			}
			if !valid || len(events) == 0 || events[len(events)-1].Fingerprint() != donor.LedgerHead || !reflect.DeepEqual(replay.Lead, donor.Lead) || !reflect.DeepEqual(replay.LedgerLast, donor.LedgerLast) {
				continue
			}
			if len(events) > len(history.Events) {
				history = binancecopy.Snapshot{Head: donor.LedgerHead, Events: events}
			}
		}
		return nil
	})
	if err != nil {
		return history, err
	}
	if history.Head == "" {
		return history, errors.New("no complete validated shared source journal")
	}
	// Fetch and validate a contiguous live overlap before accepting the donor.
	latest, err := r.source.Snapshot(ctx, history.Head)
	if err != nil {
		return binancecopy.Snapshot{}, err
	}
	known := map[string]string{}
	for _, e := range history.Events {
		known[e.Key()] = e.Fingerprint()
	}
	anchorFound := false
	last := int64(0)
	seen := map[string]bool{}
	for _, e := range latest.Events {
		if seen[e.Key()] || e.OrderUpdateTime < last {
			return binancecopy.Snapshot{}, errors.New("invalid shared source overlap ordering")
		}
		seen[e.Key()] = true
		last = e.OrderUpdateTime
		if e.Fingerprint() == history.Head {
			anchorFound = true
		}
		if fp, ok := known[e.Key()]; ok {
			if fp != e.Fingerprint() {
				return binancecopy.Snapshot{}, errors.New("shared source fill revised")
			}
			continue
		}
		if e.OrderUpdateTime < baseline {
			continue
		}
		if !anchorFound {
			return binancecopy.Snapshot{}, errors.New("shared source overlap has missing historical fill")
		}
		history.Events = append(history.Events, e)
	}
	if !anchorFound || len(latest.Events) == 0 || latest.Head != latest.Events[len(latest.Events)-1].Fingerprint() {
		return binancecopy.Snapshot{}, errors.New("shared source live anchor unconfirmed")
	}
	history.Head = latest.Head
	return history, nil
}
