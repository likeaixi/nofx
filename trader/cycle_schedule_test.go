package trader

import (
	"testing"
	"time"
)

func TestStatusUsesSchedulerTimeAndSuppressesStoppedCountdown(t *testing.T) {
	at := &AutoTrader{isRunning: true}
	next := time.Now().Add(time.Minute).UTC()
	at.setCycleSchedule(next, false)
	status := at.GetStatus()
	if status["next_cycle_at"] != next.Format(time.RFC3339Nano) || status["cycle_in_progress"] != false {
		t.Fatal(status)
	}
	at.setCycleSchedule(time.Time{}, true)
	status = at.GetStatus()
	if status["next_cycle_at"] != nil || status["cycle_in_progress"] != true {
		t.Fatal(status)
	}
	at.setCycleSchedule(next, false)
	at.isRunning = false
	status = at.GetStatus()
	if status["next_cycle_at"] != nil || status["cycle_in_progress"] != false {
		t.Fatal("stopped trader exposes countdown", status)
	}
}
