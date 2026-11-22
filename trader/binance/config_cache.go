package binance

import (
	"context"
	"github.com/adshao/go-binance/v2/futures"
	"sync"
	"sync/atomic"
	"time"
)

type cachedCommission struct {
	value      futures.CommissionRate
	at         time.Time
	generation uint64
}
type executionConfigCache struct {
	once              sync.Once
	modeGate, feeGate chan struct{}
	generation        atomic.Uint64
	modeGeneration    uint64
	mode              futures.PositionMode
	modeAt            time.Time
	fees              map[string]cachedCommission
}

func (c *executionConfigCache) lock(ctx context.Context, mode bool) (func(), error) {
	c.once.Do(func() { c.modeGate = make(chan struct{}, 1); c.feeGate = make(chan struct{}, 1) })
	gate := c.feeGate
	if mode {
		gate = c.modeGate
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Short-lived configuration only; positions, balances and protection stay fresh.
func (b *FuturesTrader) executionMode(ctx context.Context) (*futures.PositionMode, error) {
	c := &b.configCache
	unlock, err := c.lock(ctx, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	generation := c.generation.Load()
	if c.modeGeneration == generation && time.Since(c.modeAt) < 30*time.Second {
		v := c.mode
		return &v, nil
	}
	v, err := b.client.NewGetPositionModeService().Do(ctx)
	if err != nil {
		c.modeAt = time.Time{}
		return nil, err
	}
	c.mode = *v
	c.modeAt = time.Now()
	c.modeGeneration = generation
	copy := *v
	return &copy, nil
}
func (b *FuturesTrader) executionCommission(ctx context.Context, symbol string) (*futures.CommissionRate, error) {
	c := &b.configCache
	unlock, err := c.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	generation := c.generation.Load()
	if v, ok := c.fees[symbol]; ok && v.generation == generation && time.Since(v.at) < 5*time.Minute {
		copy := v.value
		return &copy, nil
	}
	v, err := b.client.NewCommissionRateService().Symbol(symbol).Do(ctx)
	if err != nil {
		delete(c.fees, symbol)
		return nil, err
	}
	// Only cache validated rates; callers still validate before using them.
	maker, e1 := ratString(v.MakerCommissionRate)
	taker, e2 := ratString(v.TakerCommissionRate)
	if e1 == nil && e2 == nil && maker.Sign() >= 0 && taker.Sign() >= 0 {
		if c.fees == nil {
			c.fees = map[string]cachedCommission{}
		}
		c.fees[symbol] = cachedCommission{*v, time.Now(), generation}
	}
	return v, nil
}
func (b *FuturesTrader) invalidateExecutionConfig() {
	// Nonblocking even while an older read is in flight. That read's generation
	// cannot populate a usable cache after invalidation.
	b.configCache.generation.Add(1)
}
