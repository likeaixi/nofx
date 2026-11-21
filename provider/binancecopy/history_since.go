package binancecopy

import (
	"context"
	"errors"
	"sort"
)

// HistorySince requires a row older than the confirmed zero-position boundary.
// A short/empty page alone is not evidence of complete history.
func (c *Client) HistorySince(ctx context.Context, since int64) (Snapshot, error) {
	if since <= 0 {
		return Snapshot{}, errors.New("confirmed source baseline required")
	}
	var out Snapshot
	var first []Event
	seen := map[string]bool{}
	last := int64(1<<63 - 1)
	for n := 1; n <= 24; n++ {
		rows, err := c.page(ctx, n)
		if err != nil {
			return Snapshot{}, err
		}
		if n == 1 {
			first = rows
			out.Head = rows[0].Fingerprint()
		}
		crossed := false
		for _, e := range rows {
			if seen[e.Key()] || e.OrderUpdateTime > last {
				return Snapshot{}, errors.New("ambiguous or unordered baseline history")
			}
			seen[e.Key()] = true
			last = e.OrderUpdateTime
			out.Events = append(out.Events, e)
			if e.OrderUpdateTime < since {
				crossed = true
			}
		}
		if crossed {
			// One response cannot shift between pages. Recheck only multi-page reads.
			if n > 1 {
				again, err := c.page(ctx, 1)
				if err != nil {
					return Snapshot{}, err
				}
				if len(again) != len(first) {
					return Snapshot{}, errors.New("source history changed during baseline reconstruction")
				}
				for i := range first {
					if first[i].Fingerprint() != again[i].Fingerprint() {
						return Snapshot{}, errors.New("source history changed during baseline reconstruction")
					}
				}
			}
			sort.SliceStable(out.Events, func(i, j int) bool { return out.Events[i].OrderUpdateTime < out.Events[j].OrderUpdateTime })
			return out, nil
		}
		if len(rows) < c.PageSize {
			break
		}
	}
	return Snapshot{}, errors.New("source history does not cover confirmed baseline")
}
