// Package allincrypto follows newly observed public fills of the fixed Binance
// portfolio. The venue execution contract uses base units, including on OKX,
// where the exact adapter converts to native contracts at the order boundary.
package allincrypto

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"nofx/planning/core"
	"nofx/provider/binancecopy"
	"nofx/trader/execution"
)

type Source interface {
	PositionBefore(context.Context, binancecopy.Event) (decimal.Decimal, error)
	Snapshot(context.Context, string) (binancecopy.Snapshot, error)
	Balance(context.Context) (decimal.Decimal, error)
}
type Markets interface {
	SnapshotKind(context.Context, string, string, string) (core.Snapshot, error)
}
type Venue interface {
	Venue() string
	AccountSnapshot(context.Context, string) (execution.Account, error)
	CopyCollateral(context.Context) (map[string]string, string, error)
	VerifyCopyPosition(context.Context, string, string, string, bool) error
	PrepareExecutionLeverage(context.Context, string, int) error
	RouteClientID(string) (string, error)
	SubmitOrder(context.Context, execution.Order) (execution.Order, error)
	LookupOrder(context.Context, execution.Order) (execution.Order, error)
	CancelExecutionOrder(context.Context, execution.Order) error
}
type Runner struct {
	Stopping    func() bool
	db          *gorm.DB
	id, account string
	source      Source
	venue       Venue
	markets     Markets
	now         func() time.Time
}

func New(db *gorm.DB, id, account string, source Source, venue Venue, markets Markets) (*Runner, error) {
	if db == nil || id == "" || account == "" || source == nil || venue == nil || markets == nil {
		return nil, errors.New("missing fixed strategy dependency")
	}
	if err := db.AutoMigrate(&checkpoint{}, &signal{}); err != nil {
		return nil, err
	}
	return &Runner{db: db, id: id, account: account, source: source, venue: venue, markets: markets, now: time.Now}, nil
}

// An explicit underlying-asset list prevents similarly named stocks, aliases,
// and scaled tokens from being interpreted as interchangeable base units.
func targetSymbol(venue string, e binancecopy.Event) (string, error) {
	if e.QuoteAsset != "USDT" || e.Symbol != e.BaseAsset+"USDT" {
		return "", errors.New("unsupported source currency or contract units")
	}
	switch e.BaseAsset {
	case "BTC", "ETH", "SOL", "XRP", "DOGE", "BNB", "ADA", "AVAX", "LINK", "LTC":
	default:
		return "", errors.New("no verified underlying asset mapping")
	}
	switch venue {
	case "binance":
		return e.Symbol, nil
	case "okx":
		return e.BaseAsset + "-USDT-SWAP", nil
	}
	return "", errors.New("unsupported target venue")
}
func (r *Runner) Poll(ctx context.Context) error {
	s, err := r.load()
	if err != nil {
		return err
	}
	if err = r.reconcile(ctx, s); err != nil {
		return err
	}
	if s.SourceFault != "" {
		return errors.New(s.SourceFault)
	}
	batch, err := r.source.Snapshot(ctx, s.Head)
	if err != nil {
		return err
	}
	if s.Head == "" {
		if _, err = r.source.Balance(ctx); err != nil {
			return err
		}
		s.Head = batch.Head
		s.Baseline = r.now().UnixMilli()
		return r.save(s, batch.Events, nil)
	}
	// Reject revisions before acting on any new row in the same batch.
	var fresh []binancecopy.Event
	for _, e := range batch.Events {
		var old signal
		err = r.db.First(&old, "trader_id = ? AND key = ?", r.id, e.Key()).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fresh = append(fresh, e)
			continue
		}
		if err != nil {
			return err
		}
		if old.Fingerprint != e.Fingerprint() {
			return r.sourceFault(s, "lead order has been revised; paused pending review")
		}
	}
	for _, e := range fresh {
		key := e.Symbol + "/" + e.PositionSide
		old, ok := s.Last[key]
		if ok && e.OrderUpdateTime > s.Baseline && (old.OrderUpdateTime > e.OrderUpdateTime || (old.OrderUpdateTime == e.OrderUpdateTime && old.Side != e.Side)) {
			return r.sourceFault(s, "late or ambiguously ordered lead fill; source position needs review")
		}
	}
	// Without sequence IDs, opposite actions at one timestamp cannot be ordered.
	for i := 1; i < len(fresh); i++ {
		for j := i - 1; j >= 0 && fresh[j].OrderUpdateTime == fresh[i].OrderUpdateTime; j-- {
			if fresh[j].Symbol == fresh[i].Symbol && fresh[j].PositionSide == fresh[i].PositionSide && fresh[j].Side != fresh[i].Side {
				return r.sourceFault(s, "ambiguous same-time lead actions")
			}
		}
	}
	for _, e := range fresh {
		if r.Stopping != nil && r.Stopping() {
			return nil
		}
		p := &pending{Event: e, Side: strings.ToLower(e.PositionSide)}
		if e.OrderUpdateTime <= s.Baseline {
			if err = r.save(s, []binancecopy.Event{e}, nil); err != nil {
				return err
			}
			continue
		}
		s.Last[e.Symbol+"/"+e.PositionSide] = e
		validDirection := (p.Side == "long" || p.Side == "short") && (e.Side == "BUY" || e.Side == "SELL")
		pos := s.Positions[e.Symbol]
		_, mappingErr := targetSymbol(r.venue.Venue(), e)
		if validDirection && mappingErr == nil {
			key := e.Symbol + "/" + p.Side
			if _, known := s.Lead[key]; !known {
				before, proofErr := r.source.PositionBefore(ctx, e)
				if proofErr != nil || before.IsNegative() {
					if err = r.save(s, []binancecopy.Event{e}, r.record(p, "unable to establish lead position from a verified flat boundary", false)); err != nil {
						return err
					}
					continue
				}
				s.Lead[key] = before
			}
			if e.Opening() {
				s.Lead[key] = s.Lead[key].Add(e.ExecutedQty)
			} else {
				if _, known := s.Lead[key]; known && e.ExecutedQty.GreaterThan(s.Lead[key]) {
					return r.sourceFault(s, "lead reduction exceeds reconstructed source position")
				}
				s.Lead[key] = s.Lead[key].Sub(e.ExecutedQty)
			}
			p.SourceAfter = s.Lead[key]
		}
		var planErr error
		if e.Opening() && e.OrderTime <= s.Baseline {
			planErr = errors.New("pre-start order filled after baseline; recorded lead exposure without opening")
		} else {
			planErr = r.plan(ctx, s, p)
		}
		if planErr != nil {
			retry := pos != nil && pos.Side == p.Side && validDirection && !e.Opening() && !errors.Is(planErr, errBelowStep)
			if retry {
				p.Stage = "retryClose"
				s.Pending = p
			} else if pos != nil && pos.Side == p.Side && validDirection {
				pos.Source = p.SourceAfter
				if errors.Is(planErr, errBelowStep) {
					pos.ReductionDust = p.DustAfter
				}
			}
			if err = r.save(s, []binancecopy.Event{e}, r.record(p, planErr.Error(), false)); err != nil {
				return err
			}
			if retry {
				return planErr
			}
			continue
		}
		s.Pending = p
		// Identity and intent are durable before touching the exchange. After a
		// crash this stage can only query/cancel this ID; it never submits it again.
		if err = r.save(s, []binancecopy.Event{e}, r.record(p, "market order intent persisted", true)); err != nil {
			return err
		}
		if err = r.submit(ctx, s); err != nil {
			return err
		}
	}
	if s.Head == batch.Head {
		return nil
	}
	s.Head = batch.Head
	return r.save(s, nil, nil)
}

// Reconcile runs independently of delayed public polling and also on restart.
func (r *Runner) Reconcile(ctx context.Context) error {
	s, err := r.load()
	if err != nil {
		return err
	}
	return r.reconcile(ctx, s)
}
func (r *Runner) reconcile(ctx context.Context, s *state) error {
	// Each outstanding market gets a reconciliation attempt. An unknown
	// order in one market must never prevent rescue of another market's lot.
	var failures []error
	reloadAfterFailure := func(failure error) error {
		failures = append(failures, failure)
		latest, err := r.load()
		if err == nil {
			*s = *latest
		}
		return err
	}
	keys := make([]string, 0, len(s.Waiting)+1)
	if s.Pending != nil {
		keys = append(keys, s.Pending.Event.Symbol)
	}
	for key := range s.Waiting {
		keys = append(keys, key)
	}
	for _, key := range keys {
		if s.Pending == nil || s.Pending.Event.Symbol != key {
			next := s.Waiting[key]
			if next == nil {
				continue
			}
			r.selectPending(s, next)
			if err := r.save(s, nil, nil); err != nil {
				return err
			}
		}
		if err := r.advance(ctx, s, nil); err != nil {
			if reloadErr := reloadAfterFailure(err); reloadErr != nil {
				return reloadErr
			}
		}
	}
	for key := range s.Positions {
		pos := s.Positions[key]
		if pos == nil {
			continue
		}
		if s.Waiting[key] != nil || (s.Pending != nil && s.Pending.Event.Symbol == key) {
			continue
		}
		if err := r.monitorStop(ctx, s, key, pos); err != nil {
			if reloadErr := reloadAfterFailure(err); reloadErr != nil {
				return reloadErr
			}
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if s.Pending != nil || len(s.Waiting) > 0 {
		return errors.New("owned orders awaiting reconciliation")
	}
	return nil
}
func (r *Runner) selectPending(s *state, next *pending) {
	if s.Pending != nil && s.Pending.Event.Symbol != next.Event.Symbol {
		s.Waiting[s.Pending.Event.Symbol] = s.Pending
	}
	delete(s.Waiting, next.Event.Symbol)
	s.Pending = next
}
func (r *Runner) monitorStop(ctx context.Context, s *state, key string, pos *position) error {
	if pos.Stop == nil {
		return errors.New("tracked position lacks protective intent")
	}
	o, err := r.lookup(ctx, *pos.Stop)
	if err != nil {
		return err
	}
	if o.Status == "NEW" && execution.Equal(string(o.Filled), "0") {
		return nil
	}
	if o.Status == "FILLED" && execution.Equal(string(o.Filled), pos.Local.String()) {
		delete(s.Positions, key)
		return r.save(s, nil, r.record(&pending{Symbol: pos.Symbol, Order: o}, "exchange stop closed followed position", true))
	}
	switch o.Status {
	case "CANCELED", "REJECTED", "EXPIRED":
		rescue := &pending{Symbol: pos.Symbol, Side: pos.Side, Reason: "protective order no longer active", Stage: "rescue", Event: binancecopy.Event{Symbol: key}, PreviousStop: pos.Stop}
		r.selectPending(s, rescue)
		if err = r.save(s, nil, nil); err != nil {
			return err
		}
		return r.flatten(ctx, s)
	}
	return fmt.Errorf("protective order %s pending reconciliation", o.Status)
}

func amount(s core.Decimal) decimal.Decimal { return decimal.RequireFromString(string(s)) }

// All inputs are positive. QuoRem avoids rounding across a lot/tick boundary.
func lots(value, divisor, step decimal.Decimal) decimal.Decimal {
	q, _ := value.QuoRem(divisor.Mul(step), 0)
	return q.Mul(step)
}

var errBelowStep = errors.New("proportional reduction below target lot minimum")

func (r *Runner) plan(ctx context.Context, s *state, p *pending) error {
	e := p.Event
	if (p.Side != "long" && p.Side != "short") || (e.Side != "BUY" && e.Side != "SELL") {
		return errors.New("unknown source side")
	}
	pos := s.Positions[e.Symbol]
	if pos != nil && pos.Side != p.Side {
		return errors.New("opposite side already tracked on target market")
	}
	symbol, err := targetSymbol(r.venue.Venue(), e)
	if err != nil {
		return err
	}
	p.Symbol = symbol
	if pos != nil && pos.Symbol != symbol {
		return errors.New("persisted target mapping changed")
	}
	if !e.Opening() && pos == nil {
		return errors.New("no followed exposure to reduce")
	}
	if e.Opening() {
		age := r.now().Sub(time.UnixMilli(e.OrderUpdateTime))
		if age > 5*time.Minute || age < -10*time.Second {
			return errors.New("lead fill outside five-minute entry window")
		}
		p.Balance, err = r.source.Balance(ctx)
		if err != nil || !p.Balance.IsPositive() {
			return errors.New("lead USDT margin balance unavailable")
		}
	}
	a, err := r.venue.AccountSnapshot(ctx, symbol)
	if err != nil {
		return err
	}
	var local decimal.Decimal
	expectedOrders := 0
	if pos != nil {
		local = pos.Local
		expectedOrders = 1
	}
	if !execution.Equal(string(a.Position), local.String()) || a.OpenOrders != expectedOrders {
		return errors.New("target position or pending orders conflict with tracked exposure")
	}
	if err = r.venue.VerifyCopyPosition(ctx, symbol, p.Side, local.String(), e.Opening() && pos != nil); err != nil {
		return err
	}
	m, err := r.markets.SnapshotKind(ctx, r.venue.Venue(), symbol, "perpetual")
	if err != nil {
		return err
	}
	if err = m.Validate(r.now()); err != nil {
		return err
	}
	if m.SourceTime.IsZero() || m.Venue != r.venue.Venue() || m.Symbol != symbol || m.Kind != "perpetual" || m.BaseAsset != e.BaseAsset || m.QuoteAsset != "USDT" || (m.Venue == "okx" && !core.Positive(m.ContractValue)) {
		return errors.New("target asset, units, timestamp or quote currency not verified")
	}
	side := p.Side
	if !e.Opening() {
		side = opposite(side)
	}
	price := amount(m.Ask)
	if side == "short" {
		price = amount(m.Bid)
	}
	if !core.Positive(m.MarketMax) || !core.Positive(m.MarketMin) || !core.Positive(m.MarketStep) {
		return errors.New("target market-order limits unavailable")
	}
	step := decimal.Max(amount(m.Step), amount(m.MarketStep))
	if !execution.Aligned(step.String(), string(m.Step)) || !execution.Aligned(step.String(), string(m.MarketStep)) {
		return errors.New("unsupported market/stop lot increments")
	}
	p.Tick = amount(m.Tick)
	var qty decimal.Decimal
	if e.Opening() {
		if price.Sub(e.AvgPrice).Abs().GreaterThan(e.AvgPrice.Mul(decimal.New(1, -2))) {
			return errors.New("target price deviates over 1% from lead fill")
		}
		var free string
		p.Equity, free, err = r.equity(ctx, p)
		if err != nil || !p.Equity.IsPositive() {
			return errors.New("target equity unavailable")
		}
		p.Ratio = p.Equity.Div(p.Balance)
		nom := p.Equity.Mul(e.ExecutedQty).Mul(e.AvgPrice)
		p.Notional = nom.Div(p.Balance)
		qty = lots(nom, p.Balance.Mul(price), step)
		available, err := decimal.NewFromString(free)
		if err != nil {
			return err
		}
		fees, err := decimal.NewFromString(string(a.FeeBPS))
		if err != nil || fees.IsNegative() {
			return errors.New("target fee unavailable")
		}
		needed := qty.Mul(price).Mul(decimal.New(5, -2).Add(fees.Shift(-4)))
		if needed.GreaterThan(available) {
			return errors.New("insufficient free margin for 20x entry and fee")
		}
	} else if p.Outstanding.IsPositive() {
		qty = p.Outstanding
	} else {
		desired := local
		if e.ExecutedQty.LessThan(pos.Source) {
			desired = local.Sub(pos.ReductionDust).Mul(e.ExecutedQty).DivRound(pos.Source, 24).Add(pos.ReductionDust)
		}
		qty = lots(desired, decimal.NewFromInt(1), step)
		if qty.LessThan(decimal.Max(amount(m.MarketMin), amount(m.MinQty))) && p.SourceAfter.IsPositive() {
			p.DustAfter = desired
			return errBelowStep
		}
		p.DustAfter = desired.Sub(qty)
	}
	if e.Opening() && qty.Add(local).GreaterThan(amount(m.MarketMax)) {
		return errors.New("aggregate position exceeds protective market order limit")
	}
	if qty.GreaterThan(amount(m.MarketMax)) || qty.LessThan(amount(m.MarketMin)) || qty.LessThan(amount(m.MinQty)) || (e.Opening() && qty.Mul(price).LessThan(amount(m.MinNotional))) {
		return errors.New("quantity outside target market size limits")
	}
	if e.Opening() && r.Stopping != nil && r.Stopping() {
		return errors.New("trader stopping")
	}
	if e.Opening() && r.now().Sub(time.UnixMilli(e.OrderUpdateTime)) > 5*time.Minute {
		return errors.New("lead entry expired during preflight")
	}
	if e.Opening() && pos == nil {
		if err = r.venue.PrepareExecutionLeverage(ctx, symbol, 20); err != nil {
			return err
		}
	}
	if err = m.Validate(r.now()); err != nil {
		return err
	}
	p.Stage = "market"
	p.Order, err = r.order(symbol, side, "entry", qty, price, !e.Opening())
	return err
}
func opposite(side string) string {
	if side == "long" {
		return "short"
	}
	return "long"
}
func (r *Runner) order(symbol, side, role string, qty, price decimal.Decimal, reduce bool) (execution.Order, error) {
	id, err := execution.NewClientID()
	if err != nil {
		return execution.Order{}, err
	}
	route, err := r.venue.RouteClientID(id)
	if err != nil {
		return execution.Order{}, err
	}
	o := execution.Order{ClientID: id, VenueClientID: route, Role: role, Side: side, Symbol: symbol, Price: execution.Decimal(price.String()), Quantity: execution.Decimal(qty.String()), ReduceOnly: reduce}
	if role == "entry" {
		o.Type = "market"
	}
	return o, nil
}

// Value all supported collateral in the lead account's quote currency. USDT
// available margin alone funds the order. For a USDC liability use the ask.
func (r *Runner) equity(ctx context.Context, p *pending) (decimal.Decimal, string, error) {
	balances, free, err := r.venue.CopyCollateral(ctx)
	if err != nil {
		return decimal.Zero, "", err
	}
	var total decimal.Decimal
	for asset, raw := range balances {
		if _, err := execution.Number(raw); err != nil {
			return decimal.Zero, "", err
		}
		n, err := decimal.NewFromString(raw)
		if err != nil {
			return decimal.Zero, "", err
		}
		if n.IsZero() {
			continue
		}
		switch asset {
		case "USDT":
			total = total.Add(n)
		case "USDC":
			fx, err := r.markets.SnapshotKind(ctx, "okx", "USDC-USDT", "spot")
			if err != nil {
				return decimal.Zero, "", err
			}
			if fx.Venue != "okx" || fx.Symbol != "USDC-USDT" || fx.Kind != "spot" || fx.SourceTime.IsZero() || fx.Validate(r.now()) != nil {
				return decimal.Zero, "", errors.New("fresh USDC/USDT conversion unavailable")
			}
			p.USDCRate = amount(fx.Bid)
			if n.IsNegative() {
				p.USDCRate = amount(fx.Ask)
			}
			total = total.Add(n.Mul(p.USDCRate))
		default:
			return decimal.Zero, "", errors.New("target collateral currency lacks verified conversion")
		}
	}
	return total, free, nil
}

// Drain processes only previously persisted intents. A stopped trader may
// leave an owned position at the venue only after verifying its standing stop.
func (r *Runner) Drain(ctx context.Context) (bool, error) {
	s, err := r.load()
	if err != nil {
		return false, err
	}
	if err = r.reconcile(ctx, s); err != nil {
		return false, err
	}
	return s.Pending == nil && len(s.Waiting) == 0, nil
}
