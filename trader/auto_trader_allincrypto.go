package trader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"nofx/planning/market"
	"nofx/provider/binancecopy"
	"nofx/store"
	"nofx/trader/allincrypto"
)

// All fixed-strategy traders read the same public source; coalesce near-simultaneous reads.
var copySources = struct {
	sync.Mutex
	clients map[string]*binancecopy.SharedClient
}{clients: map[string]*binancecopy.SharedClient{}}

func sharedCopySource(id string) *binancecopy.SharedClient {
	copySources.Lock()
	defer copySources.Unlock()
	if c := copySources.clients[id]; c != nil {
		return c
	}
	c := binancecopy.NewShared(binancecopy.NewForPortfolio(id))
	copySources.clients[id] = c
	return c
}

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
	cfg := at.config.StrategyConfig
	if cfg == nil {
		return errors.New("copy strategy config missing")
	}
	if err := cfg.ValidateCopyConfig(); err != nil {
		return err
	}
	settings := cfg.CopySettings()
	sourceID, err := store.ParseCopySourceURL(settings.SourceURL)
	if err != nil {
		return err
	}
	runner, err := allincrypto.New(at.store.GormDB(), at.id, account, sharedCopySource(sourceID), venue, market.New())
	if err != nil {
		return err
	}
	runner.SourceID = sourceID
	if strategy, readErr := at.store.Strategy().Get(at.userID, at.config.StrategyID); readErr == nil {
		runner.StrategyName = strategy.Name
	}
	runner.Leverage = settings.Leverage
	if value := os.Getenv("NOFX_AOYING_FLAT_BASELINE"); value != "" && sourceID == store.DefaultCopyPortfolioID {
		baseline, parseErr := time.Parse(time.RFC3339, value)
		if parseErr != nil {
			return fmt.Errorf("invalid NOFX_AOYING_FLAT_BASELINE: %w", parseErr)
		}
		runner.ConfirmedSourceBaseline = baseline
	}
	equityCtx, cancelEquity := context.WithCancel(context.Background())
	equityDone := make(chan struct{})
	defer func() { cancelEquity(); <-equityDone }()
	go func() { defer close(equityDone); at.runCopyEquity(equityCtx) }()
	return at.runCopyCycles(runner)
}

func (at *AutoTrader) runCopyCycles(runner *allincrypto.Runner) error {
	defer at.setCycleSchedule(time.Time{}, false)
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
	nextPoll := time.Now().Add(time.Minute)
	defer poll.Stop()
	reconcile := time.NewTicker(5 * time.Second)
	defer reconcile.Stop()
	update := func(full bool) {
		if full {
			at.setCycleSchedule(time.Time{}, true)
			defer func() { at.setCycleSchedule(nextPoll, false) }()
		}
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
		case tick := <-poll.C:
			nextPoll = tick.Add(time.Minute)
			if !runner.Stopping() {
				update(true)
			}
		case <-reconcile.C:
			update(false)
		}
	}
	at.setCycleSchedule(time.Time{}, false)
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
