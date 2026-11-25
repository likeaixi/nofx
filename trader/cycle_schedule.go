package trader

import "time"

// Read by the dashboard, written by the scheduler rather than decision logging.
func (at *AutoTrader) setCycleSchedule(next time.Time, inProgress bool) {
	at.runtimeHealthMu.Lock()
	defer at.runtimeHealthMu.Unlock()
	at.nextCycleAt = next
	at.cycleInProgress = inProgress
}
