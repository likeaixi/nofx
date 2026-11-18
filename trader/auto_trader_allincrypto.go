package trader

import (
	"context"
	"errors"
	"sync"
	"time"

	"nofx/planning/market"
	"nofx/provider/binancecopy"
	"nofx/trader/allincrypto"
)

// Prevent simultaneous automatic runners from changing the same account
// underneath a copy position's ownership checks (including duplicate configs).
var copyAccounts = struct {
	sync.Mutex
	all   map[string]int
	fixed map[string]bool
}{all: map[string]int{}, fixed: map[string]bool{}}

func registerCopyAccount(key string, fixed bool) (func(), error) {
	if key == "" {
		if fixed {
			return nil, errors.New("copy account identity required")
		}
		return func() {}, nil
	}
	copyAccounts.Lock()
	defer copyAccounts.Unlock()
	if copyAccounts.fixed[key] || (fixed && copyAccounts.all[key] != 0) {
		return nil, errors.New("AllinCrypto needs exclusive use of this automatic trading account")
	}
	copyAccounts.all[key]++
	if fixed {
		copyAccounts.fixed[key] = true
	}
	return func() {
		copyAccounts.Lock()
		defer copyAccounts.Unlock()
		copyAccounts.all[key]--
		if copyAccounts.all[key] == 0 {
			delete(copyAccounts.all, key)
			delete(copyAccounts.fixed, key)
		}
	}, nil
}
func (at *AutoTrader) runAllinCrypto(account string) error {
	defer func() { at.isRunningMutex.Lock(); at.isRunning = false; at.isRunningMutex.Unlock() }()
	venue, ok := at.trader.(allincrypto.Venue)
	if !ok {
		return errors.New("target lacks exact copy execution capability")
	}
	runner, err := allincrypto.New(at.store.GormDB(), at.id, account, binancecopy.New(), venue, market.New())
	if err != nil {
		return err
	}
	return at.runCopyCycles(runner)
}

func (at *AutoTrader) runCopyCycles(runner *allincrypto.Runner) error {
	runner.Stopping = func() bool {
		select {
		case <-at.stopMonitorCh:
			return true
		default:
			requested, err := at.store.Trader().CopyStopRequested(at.userID, at.id)
			return requested || err != nil
		}
	}
	// Finish reconciling any in-flight order on stop. Context deadlines bound
	// external requests; stopping never cancels a venue's standing protective SL.
	poll := time.NewTicker(time.Minute)
	defer poll.Stop()
	reconcile := time.NewTicker(5 * time.Second)
	defer reconcile.Stop()
	update := func(full bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		var err error
		if full {
			err = runner.Poll(ctx)
		} else {
			err = runner.Reconcile(ctx)
		}
		if full {
			if reportErr := runner.RecordRuntimeStatus(err, false); reportErr != nil {
				at.logWarnf("AllinCrypto runtime decision log: %v", reportErr)
			}
		}
		at.runtimeHealthMu.Lock()
		defer at.runtimeHealthMu.Unlock()
		if full || err != nil {
			at.tradingCheckedAt = time.Now().UTC()
			at.tradingBlocked = err != nil
			at.tradingError = ""
			if err != nil {
				at.tradingError = err.Error()
				at.logWarnf("AllinCrypto: %v", err)
			}
		}
	}
	// Read the durable marker before the first source poll. Restoration of
	// an interrupted stop only drains orders already submitted by this trader.
	if !runner.Stopping() {
		update(true)
	}
	for !runner.Stopping() {
		select {
		case <-at.stopMonitorCh:
		case <-poll.C:
			if !runner.Stopping() {
				update(true)
			}
		case <-reconcile.C:
			update(false)
		}
	}
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		done, err := runner.Drain(ctx)
		cancel()
		if reportErr := runner.RecordRuntimeStatus(err, true); reportErr != nil {
			at.logWarnf("AllinCrypto stopping decision log: %v", reportErr)
		}
		if done && err == nil {
			return nil
		}
		at.runtimeHealthMu.Lock()
		at.tradingBlocked = true
		at.tradingError = "Stopping: reconciling previously submitted copy orders"
		at.tradingCheckedAt = time.Now().UTC()
		at.runtimeHealthMu.Unlock()
		<-reconcile.C
	}
}
