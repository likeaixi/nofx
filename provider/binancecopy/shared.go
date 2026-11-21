package binancecopy

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// SharedClient coalesces the fixed public portfolio's simultaneous readers.
// Only successful snapshots are reused, for at most two seconds. Account state
// and execution remain outside this client and are never shared between traders.
type SharedClient struct {
	mu         sync.Mutex
	client     *Client
	now        func() time.Time
	snapshot   Snapshot
	snapshotAt time.Time
	balance    decimal.Decimal
	balanceAt  time.Time
}

func NewShared(client *Client) *SharedClient { return &SharedClient{client: client, now: time.Now} }
func cloneSnapshot(s Snapshot) Snapshot      { s.Events = append([]Event(nil), s.Events...); return s }
func (s *SharedClient) fresh(at time.Time) bool {
	age := s.now().Sub(at)
	return !at.IsZero() && age >= 0 && age < 2*time.Second
}
func (s *SharedClient) Snapshot(ctx context.Context, anchor string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if s.fresh(s.snapshotAt) {
		if anchor == "" {
			return cloneSnapshot(s.snapshot), nil
		}
		for _, e := range s.snapshot.Events {
			if e.Fingerprint() == anchor {
				return cloneSnapshot(s.snapshot), nil
			}
		}
	}
	got, err := s.client.Snapshot(ctx, anchor)
	if err == nil {
		s.snapshot = cloneSnapshot(got)
		s.snapshotAt = s.now()
	}
	return got, err
}
func (s *SharedClient) HistorySince(ctx context.Context, since int64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	// Snapshot already reads complete contiguous pages from newest to its anchor.
	// If that response crosses the baseline it also covers the bootstrap interval.
	if s.fresh(s.snapshotAt) && len(s.snapshot.Events) > 0 && s.snapshot.Events[0].OrderUpdateTime < since {
		return cloneSnapshot(s.snapshot), nil
	}
	got, err := s.client.HistorySince(ctx, since)
	if err == nil {
		s.snapshot = cloneSnapshot(got)
		s.snapshotAt = s.now()
	}
	return got, err
}
func (s *SharedClient) Balance(ctx context.Context) (decimal.Decimal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	if s.fresh(s.balanceAt) {
		return s.balance, nil
	}
	got, err := s.client.Balance(ctx)
	if err == nil {
		s.balance = got
		s.balanceAt = s.now()
	}
	return got, err
}
func (s *SharedClient) PositionBefore(ctx context.Context, e Event) (decimal.Decimal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return decimal.Zero, err
	}
	return s.client.PositionBefore(ctx, e)
}
