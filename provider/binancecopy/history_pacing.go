package binancecopy

import (
	"context"
	"time"
)

// All production portfolios share one queue: these public history endpoints
// share the server's egress IP. Leave one second after each completed request,
// including failures, before sending the next page or verification request.
var publicHistoryPacer = &historyPacer{busy: make(chan struct{}, 1)}

type historyPacer struct {
	busy chan struct{}
	next time.Time // protected by busy
}

func (p *historyPacer) acquire(ctx context.Context) (func(), error) {
	select {
	case p.busy <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-p.busy }
	if wait := time.Until(p.next); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return func() {
		p.next = time.Now().Add(time.Second)
		release()
	}, nil
}
