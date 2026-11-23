package allincrypto

import (
	"context"
	"errors"
	"fmt"
	"nofx/store"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"nofx/trader/execution"
)

func (r *Runner) submit(ctx context.Context, s *state) error {
	if s.Pending.Stage == "market" && !s.Pending.Order.ReduceOnly && r.Stopping != nil && r.Stopping() {
		p := s.Pending
		s.Pending = nil
		return r.save(s, nil, r.record(p, "trader stopped before order submission", false))
	}
	submitCtx := ctx
	if s.Pending.Stage == "stop" {
		var cancel context.CancelFunc
		submitCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	o, err := r.venue.SubmitOrder(submitCtx, s.Pending.Order)
	if err != nil {
		if s.Pending.Stage == "stop" && r.venue.Venue() == "binance" {
			s.Pending.StopSubmitError = err.Error()
		}
		return r.advance(ctx, s, nil)
	}
	return r.advance(ctx, s, &o)
}
func (r *Runner) advance(ctx context.Context, s *state, receipt *execution.Order) error {
	p := s.Pending
	switch p.Stage {
	case "retryClose":
		pos := s.Positions[p.Event.Symbol]
		if pos != nil && pos.Stop != nil {
			stop, err := r.lookup(ctx, *pos.Stop)
			if err != nil {
				return err
			}
			switch stop.Status {
			case "CANCELED", "REJECTED", "EXPIRED":
				p.Stage = "rescue"
				p.Reason = "stop lost while source reduction was awaiting reconciliation"
				p.PreviousStop = pos.Stop
				if err = r.save(s, nil, nil); err != nil {
					return err
				}
				return r.flatten(ctx, s)
			}
			if stop.Status == "FILLED" && r.venue.VerifyCopyPosition(ctx, pos.Symbol, pos.Side, "0", false) == nil {
				delete(s.Positions, p.Event.Symbol)
				s.Pending = nil
				return r.save(s, nil, r.record(p, "exchange stop already closed the position", true))
			}
		}
		if err := r.plan(ctx, s, p); err != nil {
			return err
		}
		if err := r.save(s, nil, r.record(p, "retry verified reduction", true)); err != nil {
			return err
		}
		return r.submit(ctx, s)
	case "remainder":
		return r.remainder(ctx, s)
	case "cleanup":
		return r.cleanup(ctx, s)
	case "rescue":
		return r.flatten(ctx, s)
	}
	var o execution.Order
	var err error
	if p.Stage == "stop" && r.venue.Venue() == "binance" && completeStopReceipt(p.Order, receipt) {
		// The adapter verified the native creation response. Persist it without
		// immediately querying an eventually visible order index.
		o = *receipt
		s.Positions[p.Event.Symbol].StopCheckAfter = r.now().Add(5 * time.Second)
	} else if p.Stage == "stop" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := r.now()
		if p.StopConfirmStarted.IsZero() {
			p.StopConfirmStarted = now
		}
		remaining := p.StopConfirmStarted.Add(15 * time.Second).Sub(now)
		if remaining <= 0 || p.StopConfirmAttempts >= 3 {
			err = errors.New("protective stop confirmation deadline or attempt limit reached")
		} else {
			if now.Before(p.StopConfirmNext) {
				return errors.New("protective stop confirmation backoff")
			}
			p.StopConfirmAttempts++
			p.StopConfirmNext = now.Add(5 * time.Second)
			if err = r.save(s, nil, nil); err != nil {
				return err
			}
			timeout := 3 * time.Second
			if remaining < timeout {
				timeout = remaining
			}
			readCtx, cancel := context.WithTimeout(ctx, timeout)
			o, err = r.lookup(readCtx, p.Order)
			cancel()
		}
	} else if receipt != nil {
		o = *receipt
	} else {
		o, err = r.lookup(ctx, p.Order)
	}
	if p.Stage == "stop" {
		if err == nil && o.Status == "NEW" && execution.Equal(string(o.Filled), "0") {
			s.Positions[p.Event.Symbol].Stop = &o
			p.Stage = "cleanup"
			if err = r.save(s, nil, nil); err != nil {
				return err
			}
			return r.cleanup(ctx, s)
		}
		var retry *execution.RetryableError
		if errors.As(err, &retry) && p.StopConfirmAttempts < 3 {
			delay := retry.Delay
			if delay < 5*time.Second {
				delay = 5 * time.Second
			}
			now := r.now()
			deadline := p.StopConfirmStarted.Add(15 * time.Second)
			if now.Before(deadline) {
				p.StopConfirmNext = now.Add(delay)
				// Reconciliation at the deadline rescues even if server cooldown is longer.
				if p.StopConfirmNext.After(deadline) {
					p.StopConfirmNext = deadline
				}
				reason := fmt.Sprintf("protective stop confirmation pending; attempt=%d/3; %s", p.StopConfirmAttempts, retry.Error())
				if saveErr := r.save(s, nil, r.record(p, reason, false)); saveErr != nil {
					return saveErr
				}
				return errors.New(reason)
			}
		}
		// A stop could have been accepted despite a timeout. Keep its exact ID
		// until it is confirmed inactive. Never reuse this market in that window.
		failedStop := p.Order
		p.FaultStop = &failedStop
		p.Reason = "replacement stop not confirmed; closing owned exposure"
		if r.venue.Venue() == "binance" && p.StopSubmitError != "" {
			p.Reason += "; submit: " + p.StopSubmitError
		}
		if err != nil {
			if r.venue.Venue() == "binance" {
				p.Reason += "; lookup: " + err.Error()
			}
		} else {
			p.Reason += fmt.Sprintf("; observed status=%q filled=%q", o.Status, o.Filled)
		}
		p.Stage = "rescue"
		if err = r.save(s, nil, r.record(p, p.Reason, false)); err != nil {
			return err
		}
		return r.flatten(ctx, s)
	}
	if err != nil {
		return fmt.Errorf("market order outcome unknown; reconcile by client ID: %w", err)
	}
	if o.Status == "NEW" && receipt != nil {
		o, err = r.lookup(ctx, o)
		if err != nil {
			return err
		}
	}
	if o.Status == "PARTIALLY_FILLED" {
		partial := o
		_ = r.venue.CancelExecutionOrder(ctx, o)
		final, lookupErr := r.lookup(ctx, o)
		if lookupErr == nil {
			o = final
		}
		if lookupErr != nil || o.Status == "PARTIALLY_FILLED" || o.Status == "NEW" {
			if p.Stage == "market" && !p.Order.ReduceOnly {
				if lookupErr == nil && execution.Positive(o.Filled) {
					partial = o
				}
				return r.rescuePartial(ctx, s, partial)
			}
			return errors.New("partial reduction pending exchange reconciliation")
		}
	}
	switch o.Status {
	case "FILLED", "CANCELED", "EXPIRED", "REJECTED":
	default:
		return fmt.Errorf("order %s awaiting final fill", o.Status)
	}
	filled, err := decimal.NewFromString(string(o.Filled))
	if err != nil || filled.IsNegative() || filled.GreaterThan(decimal.RequireFromString(string(p.Order.Quantity))) || (o.Status == "FILLED" && !execution.Equal(string(o.Filled), string(o.Quantity))) {
		return errors.New("invalid terminal fill quantity")
	}
	pos := s.Positions[p.Event.Symbol]
	if p.Stage == "flatten" {
		if pos == nil || filled.GreaterThan(pos.Local.Sub(p.Keep)) {
			return errors.New("emergency fill exceeds owned exposure")
		}
		pos.Local = pos.Local.Sub(filled)
		if pos.Local.GreaterThan(p.Keep) {
			p.Stage = "rescue"
			if err = r.save(s, nil, r.record(p, "terminal emergency order left residual; reconciling owned remainder", false)); err != nil {
				return err
			}
			return errors.New("emergency reduction has a verified remainder")
		}
		if pos.Local.IsZero() {
			delete(s.Positions, p.Event.Symbol)
		} else {
			pos.Stop = p.PreviousStop
			pos.Source = p.SourceAfter
		}

		p.Stage = "cleanup"
		if err = r.save(s, nil, nil); err != nil {
			return err
		}
		return r.cleanup(ctx, s)
	}
	if p.Stage != "market" {
		return errors.New("invalid persisted execution stage")
	}
	if !filled.IsPositive() {
		if p.Order.ReduceOnly {
			p.Stage = "retryClose"
			p.Outstanding = decimal.RequireFromString(string(o.Quantity))
			if err = r.save(s, nil, r.record(p, "terminal reduction did not execute", false)); err != nil {
				return err
			}
			return errors.New("reduction requires retry")
		}
		if pos != nil {
			pos.Source = p.SourceAfter
		}
		s.Pending = nil
		return r.save(s, nil, r.record(p, "order ended without a fill", false))
	}
	p.MarketFill = &o
	price, priceErr := decimal.NewFromString(string(o.Average))
	if pos == nil {
		pos = &position{Symbol: p.Symbol, Side: p.Side, Tick: p.Tick}
		s.Positions[p.Event.Symbol] = pos
	}
	p.PreviousStop = pos.Stop
	if p.Order.ReduceOnly {
		if filled.GreaterThan(pos.Local) {
			return errors.New("reduction exceeds owned quantity")
		}
		pos.Local = pos.Local.Sub(filled)
		requested := decimal.RequireFromString(string(o.Quantity))
		if filled.LessThan(requested) {
			p.Outstanding = requested.Sub(filled)
			p.Stage = "retryClose"
			if err = r.save(s, nil, r.record(p, "terminal partial reduction; reconciling remainder", false)); err != nil {
				return err
			}
			return errors.New("partial reduction requires retry")
		}
		pos.Source = p.SourceAfter
		pos.ReductionDust = p.DustAfter
	} else {
		if priceErr != nil || !price.IsPositive() {
			pos.Local = pos.Local.Add(filled)
			p.Reason = "verified target fill has no valid average; reduce-only exit"
			p.Stage = "rescue"
			if err = r.save(s, nil, r.record(p, p.Reason, false)); err != nil {
				return err
			}
			return r.flatten(ctx, s)
		}
		cost := pos.Local.Mul(pos.Average).Add(filled.Mul(price))
		pos.Local = pos.Local.Add(filled)
		pos.Average = cost.DivRound(pos.Local, 24)
		pos.Source = p.SourceAfter
	}
	if pos.Local.IsZero() {
		delete(s.Positions, p.Event.Symbol)
		p.Stage = "cleanup"
		if err = r.save(s, nil, nil); err != nil {
			return err
		}
		return r.cleanup(ctx, s)
	}
	pos.Tick = p.Tick
	price = pos.Average.Mul(decimal.New(100-store.AllinCryptoStopPercent, -2))
	if pos.Side == "short" {
		price = pos.Average.Mul(decimal.New(100+store.AllinCryptoStopPercent, -2))
	}
	units, rem := price.QuoRem(pos.Tick, 0)
	if pos.Side == "long" && !rem.IsZero() {
		units = units.Add(decimal.NewFromInt(1))
	}
	stop := units.Mul(pos.Tick)
	if !stop.IsPositive() || (pos.Side == "long" && !stop.LessThan(pos.Average)) || (pos.Side == "short" && !stop.GreaterThan(pos.Average)) {
		p.Reason = "no legal protective tick"
		p.Stage = "rescue"
		if err = r.save(s, nil, nil); err != nil {
			return err
		}
		return r.flatten(ctx, s)
	}
	p.Order, err = r.order(pos.Symbol, pos.Side, "sl", pos.Local, stop, false)
	if err != nil {
		return err
	}
	p.Stage = "stop"
	p.StopConfirmStarted = r.now()
	p.StopConfirmNext = time.Time{}
	p.StopConfirmAttempts = 0
	p.StopSubmitError = ""
	if err = r.save(s, nil, r.record(p, "target fill reconciled; exchange stop intent persisted", true)); err != nil {
		return err
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 3*time.Second)
	err = r.venue.VerifyCopyPosition(verifyCtx, pos.Symbol, pos.Side, pos.Local.String(), false)
	verifyCancel()
	if err != nil {
		p.Stage = "rescue"
		p.Reason = "position changed before replacement stop submission"
		if err = r.save(s, nil, nil); err != nil {
			return err
		}
		return r.flatten(ctx, s)
	}
	return r.submit(ctx, s)
}
func (r *Runner) flatten(ctx context.Context, s *state) error {
	p := s.Pending
	pos := s.Positions[p.Event.Symbol]
	if pos == nil {
		return errors.New("missing position for emergency reduction")
	}
	// A market order can continue filling after cancellation was requested.
	// Use verified cumulative fills to grow the owned rescue amount before
	// comparing it to the venue position. An unknown lookup never adds size.
	if p.Original != nil {
		original, err := r.lookup(ctx, *p.Original)
		if err == nil {
			cumulative, err := decimal.NewFromString(string(original.Filled))
			if err != nil || cumulative.LessThan(p.Rescued) || cumulative.GreaterThan(decimal.RequireFromString(string(original.Quantity))) {
				return errors.New("invalid original cumulative fill during rescue")
			}
			pos.Local = pos.Local.Add(cumulative.Sub(p.Rescued))
			p.Rescued = cumulative
			p.Original = &original
		}
	}
	// Confirm fills on both overlapping stops. Track cumulative debits so a
	// restart or repeated lookup cannot subtract the same exit twice.
	p.Keep = decimal.Zero
	if p.StopDebits == nil {
		p.StopDebits = map[string]decimal.Decimal{}
	}
	remaining := pos.Local
	for _, stop := range []*execution.Order{p.PreviousStop, p.FaultStop} {
		if stop == nil {
			continue
		}
		seen, err := r.lookup(ctx, *stop)
		if err != nil {
			continue
		}
		if filled, err := decimal.NewFromString(string(seen.Filled)); err == nil && filled.GreaterThan(p.StopDebits[stop.ClientID]) {
			remaining = remaining.Sub(filled.Sub(p.StopDebits[stop.ClientID]))
			p.StopDebits[stop.ClientID] = filled
		}
		if stop == p.PreviousStop && seen.Status == "NEW" && p.Event.Opening() && execution.Equal(string(seen.Filled), "0") && p.Original == nil {
			price, err := decimal.NewFromString(string(seen.Price))
			if err != nil {
				return err
			}
			// Preserve the old lot only when its confirmed stop still protects
			// the aggregate fill basis at least as tightly as the fixed stop distance.
			if pos.Side == "long" && !price.LessThan(pos.Average.Mul(decimal.New(100-store.AllinCryptoStopPercent, -2))) || pos.Side == "short" && !price.GreaterThan(pos.Average.Mul(decimal.New(100+store.AllinCryptoStopPercent, -2))) {
				p.Keep = decimal.RequireFromString(string(seen.Quantity))
			}
		}
	}
	if remaining.IsNegative() {
		return errors.New("protective fills exceed owned exposure")
	}
	if err := r.venue.VerifyCopyPosition(ctx, pos.Symbol, pos.Side, remaining.String(), false); err != nil {
		return errors.New("position ownership changed during stop reconciliation")
	}
	pos.Local = remaining
	if p.Keep.GreaterThan(remaining) {
		p.Keep = decimal.Zero
	}
	if remaining.Equal(p.Keep) {
		if remaining.IsZero() {
			delete(s.Positions, p.Event.Symbol)
		} else {
			pos.Stop = p.PreviousStop
			pos.Source = p.SourceAfter
		}
		p.Stage = "cleanup"
		if err := r.save(s, nil, nil); err != nil {
			return err
		}
		return r.cleanup(ctx, s)
	}

	var err error
	// Market exits do not transmit a limit price. If a fill lacks its
	// average, use the original quote only as the required review metadata.
	reference := pos.Average
	if !reference.IsPositive() {
		reference, err = decimal.NewFromString(string(p.Order.Price))
		if err != nil {
			return err
		}
	}
	p.Order, err = r.order(pos.Symbol, opposite(pos.Side), "entry", pos.Local.Sub(p.Keep), reference, true)
	if err != nil {
		return err
	}
	p.Stage = "flatten"
	if err = r.save(s, nil, r.record(p, "reduce-only emergency exit intent persisted", false)); err != nil {
		return err
	}
	return r.submit(ctx, s)
}
func (r *Runner) cleanup(ctx context.Context, s *state) error {
	p := s.Pending
	for _, old := range []*execution.Order{p.PreviousStop, p.FaultStop} {
		if old == nil || (old == p.PreviousStop && p.Keep.IsPositive()) {
			continue
		}
		o, err := r.lookup(ctx, *old)
		if err != nil {
			return errors.New("prior stop outcome unknown; follow blocked")
		}
		switch o.Status {
		case "CANCELED", "REJECTED", "EXPIRED", "FILLED":
		case "NEW":
			if err = r.venue.CancelExecutionOrder(ctx, o); err != nil {
				return err
			}
			final, err := r.lookup(ctx, o)
			if err != nil || (final.Status != "CANCELED" && final.Status != "EXPIRED" && final.Status != "FILLED") {
				return errors.New("prior stop cancellation not final")
			}
			o = final
		default:
			return fmt.Errorf("prior stop still executing: %s", o.Status)
		}
		pos := s.Positions[p.Event.Symbol]
		if pos != nil && execution.Positive(o.Filled) && (p.StopDebits == nil || !execution.Equal(string(o.Filled), p.StopDebits[o.ClientID].String())) {
			// The old stop executed during replacement. The remaining size no
			// longer matches the replacement protective quantity.
			if p.FaultStop == nil {
				p.FaultStop = pos.Stop
			}
			p.Stage = "rescue"
			p.Reason = "previous stop executed during replacement"
			if err := r.save(s, nil, nil); err != nil {
				return err
			}
			return r.flatten(ctx, s)
		}
	}
	if p.Original != nil {
		p.PreviousStop = nil
		p.FaultStop = nil
		p.Stage = "remainder"
		if err := r.save(s, nil, r.record(p, "partial fill flattened; waiting for final original order state", false)); err != nil {
			return err
		}
		return errors.New("partial fill flattened; original order pending")
	}
	s.Pending = nil
	reason := "followed source fill"
	if p.Reason != "" {
		reason = p.Reason
	}
	return r.save(s, nil, r.record(p, reason, p.Reason == ""))
}

// A positive cumulative fill is owned even if cancellation cannot be confirmed.
// Flatten the verified quantity, retain the original ID and reconcile any later
// increase separately. Never use a timeout as authorization to submit again.
func (r *Runner) rescuePartial(ctx context.Context, s *state, o execution.Order) error {
	p := s.Pending
	cum, err := decimal.NewFromString(string(o.Filled))
	if err != nil || !cum.GreaterThan(p.Rescued) || cum.GreaterThan(decimal.RequireFromString(string(o.Quantity))) {
		return errors.New("no verified new partial fill")
	}
	pos := s.Positions[p.Event.Symbol]
	if pos == nil {
		pos = &position{Symbol: p.Symbol, Side: p.Side}
		s.Positions[p.Event.Symbol] = pos
	}
	p.PreviousStop = pos.Stop
	pos.Local = pos.Local.Add(cum.Sub(p.Rescued))
	p.Original = &o
	p.Rescued = cum
	p.Order = o
	p.Stage = "rescue"
	p.Reason = "partial entry not terminal after cancellation"
	if err = r.save(s, nil, r.record(p, p.Reason, false)); err != nil {
		return err
	}
	return r.flatten(ctx, s)
}
func (r *Runner) remainder(ctx context.Context, s *state) error {
	p := s.Pending
	o, err := r.lookup(ctx, *p.Original)
	if err != nil {
		return err
	}
	qty, err := decimal.NewFromString(string(o.Filled))
	if err != nil || qty.LessThan(p.Rescued) {
		return errors.New("unconfirmed cumulative fill")
	}
	if qty.GreaterThan(p.Rescued) {
		return r.rescuePartial(ctx, s, o)
	}
	switch o.Status {
	case "FILLED", "CANCELED", "REJECTED", "EXPIRED":
		s.Pending = nil
		return r.save(s, nil, r.record(p, "original entry settled after emergency close", false))
	}
	_ = r.venue.CancelExecutionOrder(ctx, o)
	return errors.New("original partially filled order not yet terminal")
}

func (r *Runner) lookup(ctx context.Context, o execution.Order) (execution.Order, error) {
	receipt, err := r.venue.LookupOrder(ctx, o)
	if errors.Is(err, execution.ErrFillAverageUnavailable) && o.Role == "entry" && execution.Positive(receipt.Filled) {
		return receipt, nil
	}
	return receipt, err
}

// Only a complete, matching creation receipt can bypass immediate lookup.
// Native status, side, position mode and trigger semantics are checked by the
// Binance adapter before it returns this normalized receipt without an error.
func completeStopReceipt(intent execution.Order, receipt *execution.Order) bool {
	if receipt == nil {
		return false
	}
	id, err := strconv.ParseInt(receipt.OrderID, 10, 64)
	return err == nil && id > 0 && receipt.Role == "sl" && receipt.Role == intent.Role &&
		receipt.ClientID == intent.ClientID && receipt.VenueClientID == intent.VenueClientID &&
		receipt.Symbol == intent.Symbol && receipt.Side == intent.Side && receipt.Type == intent.Type &&
		receipt.ReduceOnly == intent.ReduceOnly && receipt.Status == "NEW" &&
		execution.Equal(string(receipt.Filled), "0") &&
		execution.Equal(string(receipt.Quantity), string(intent.Quantity)) &&
		execution.Equal(string(receipt.Price), string(intent.Price))
}
