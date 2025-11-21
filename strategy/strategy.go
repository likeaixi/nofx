package strategy

import (
	"sync"
	"time"

	"nofx/decision"
)

// StrategyState represents the lifecycle stage of a strategy.
type StrategyState string

const (
	// StrategyStateIdle means the strategy has not been started yet.
	StrategyStateIdle StrategyState = "idle"
	// StrategyStateRunning means the strategy is currently evaluating signals.
	StrategyStateRunning StrategyState = "running"
	// StrategyStateStopped means the strategy has finished gracefully.
	StrategyStateStopped StrategyState = "stopped"
	// StrategyStateFailed means the strategy exited because of an error.
	StrategyStateFailed StrategyState = "failed"
)

// StrategyStatus captures the latest runtime information exposed by a strategy.
type StrategyStatus struct {
	Name      string              `json:"name"`
	State     StrategyState       `json:"state"`
	StartedAt time.Time           `json:"started_at"`
	StoppedAt time.Time           `json:"stopped_at"`
	LastError error               `json:"-"`
	Decisions []decision.Decision `json:"decisions,omitempty"`
}

// StrategyConfig is the common configuration shared by all strategies.
type StrategyConfig struct {
	// Symbol is the exchange trading pair (e.g. BTCUSDT).
	Symbol string
	// Interval controls how frequently the main loop evaluates (if applicable).
	Interval time.Duration
	// Warmup is the amount of historical time a strategy expects before trading.
	Warmup time.Duration
	// Params stores strategy specific knobs without forcing a schema.
	Params map[string]any
}

// Strategy defines the required behavior that all strategy implementations must satisfy.
type Strategy interface {
	// Name returns the human readable identifier of the strategy.
	Name() string
	// Configure is called before Decide to pass static configuration.
	Configure(ctx *decision.Context, cfg StrategyConfig) error
	// Decide evaluates market data and returns the latest trading decisions.
	Decide(ctx *decision.Context) ([]decision.Decision, error)

	GetFullDecision(ctx *decision.Context) (*decision.FullDecision, error)
	// Status returns the current runtime information of the strategy, including last decisions.
	Status() StrategyStatus
}

// lifecycleAware is satisfied when a strategy exposes SetError.
type lifecycleAware interface {
	SetError(error)
}

type stateAware interface {
	SetState(StrategyState)
}

type decisionAware interface {
	SetDecisions([]decision.Decision)
}

// BaseStrategy offers reusable lifecycle helpers that concrete strategies can embed.
type BaseStrategy struct {
	name   string
	mu     sync.RWMutex
	status StrategyStatus
}

// NewBaseStrategy creates a BaseStrategy with the provided name.
func NewBaseStrategy(name string) BaseStrategy {
	return BaseStrategy{
		name: name,
		status: StrategyStatus{
			Name:  name,
			State: StrategyStateIdle,
		},
	}
}

// Name returns the strategy identifier.
func (b *BaseStrategy) Name() string {
	return b.name
}

// Status exposes the current lifecycle information.
func (b *BaseStrategy) Status() StrategyStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	snapshot := b.status
	if len(b.status.Decisions) > 0 {
		snapshot.Decisions = make([]decision.Decision, len(b.status.Decisions))
		copy(snapshot.Decisions, b.status.Decisions)
	}
	return snapshot
}

// SetState updates the lifecycle state and timestamps.
func (b *BaseStrategy) SetState(state StrategyState) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.State = state
	if state == StrategyStateRunning {
		b.status.StartedAt = time.Now()
	}
	if state == StrategyStateStopped || state == StrategyStateFailed {
		b.status.StoppedAt = time.Now()
	}
}

// SetError stores the last error that occurred inside the strategy.
func (b *BaseStrategy) SetError(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status.LastError = err
	if err != nil {
		b.status.State = StrategyStateFailed
		b.status.StoppedAt = time.Now()
	}
}

// SetDecisions stores the last generated decisions for reporting purposes.
func (b *BaseStrategy) SetDecisions(decisions []decision.Decision) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(decisions) == 0 {
		b.status.Decisions = nil
		return
	}
	b.status.Decisions = make([]decision.Decision, len(decisions))
	copy(b.status.Decisions, decisions)
}

// Runner manages the lifecycle of a strategy instance.
type Runner struct {
	strategy Strategy
	cfg      StrategyConfig

	mu         sync.Mutex
	configured bool
	lastErr    error
	decisions  []decision.Decision
}

// NewRunner wires the strategy with its configuration.
func NewRunner(strategy Strategy, cfg StrategyConfig) *Runner {
	return &Runner{
		strategy: strategy,
		cfg:      cfg,
	}
}

// Decide configures the strategy on first call, then executes a single decision cycle.
func (r *Runner) Decide(ctx *decision.Context) ([]decision.Decision, error) {
	r.mu.Lock()
	if !r.configured {
		if err := r.strategy.Configure(ctx, r.cfg); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.configured = true
	}
	r.mu.Unlock()

	if tracker, ok := r.strategy.(stateAware); ok {
		tracker.SetState(StrategyStateRunning)
	}

	decisions, err := r.strategy.Decide(ctx)
	if err != nil {
		if lifecycle, ok := r.strategy.(lifecycleAware); ok {
			lifecycle.SetError(err)
		}
		if tracker, ok := r.strategy.(stateAware); ok {
			tracker.SetState(StrategyStateFailed)
		}
		r.mu.Lock()
		r.lastErr = err
		r.mu.Unlock()
		return nil, err
	}

	if tracker, ok := r.strategy.(stateAware); ok {
		tracker.SetState(StrategyStateStopped)
	}
	if recorder, ok := r.strategy.(decisionAware); ok {
		recorder.SetDecisions(decisions)
	}

	r.mu.Lock()
	r.decisions = cloneDecisions(decisions)
	r.lastErr = nil
	r.mu.Unlock()

	return cloneDecisions(decisions), nil
}

// Decide configures the strategy on first call, then executes a single decision cycle.
func (r *Runner) GetFullDecision(ctx *decision.Context) (*decision.FullDecision, error) {
	r.mu.Lock()
	if !r.configured {
		if err := r.strategy.Configure(ctx, r.cfg); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.configured = true
	}
	r.mu.Unlock()

	if tracker, ok := r.strategy.(stateAware); ok {
		tracker.SetState(StrategyStateRunning)
	}

	decisions, err := r.strategy.GetFullDecision(ctx)
	if err != nil {
		if lifecycle, ok := r.strategy.(lifecycleAware); ok {
			lifecycle.SetError(err)
		}
		if tracker, ok := r.strategy.(stateAware); ok {
			tracker.SetState(StrategyStateFailed)
		}
		r.mu.Lock()
		r.lastErr = err
		r.mu.Unlock()
		return nil, err
	}

	if tracker, ok := r.strategy.(stateAware); ok {
		tracker.SetState(StrategyStateStopped)
	}

	return decisions, nil
}

// Err returns the last error produced by Decide, if any.
func (r *Runner) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// Decisions returns the most recent decisions produced by the runner.
func (r *Runner) Decisions() []decision.Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneDecisions(r.decisions)
}

func cloneDecisions(src []decision.Decision) []decision.Decision {
	if len(src) == 0 {
		return nil
	}
	cp := make([]decision.Decision, len(src))
	copy(cp, src)
	return cp
}
