package trader

import "testing"

func TestStartReportsRejectedRuntime(t *testing.T) {
	at := &AutoTrader{isRunning: true}
	if err := at.Start(); err == nil {
		t.Fatal("already-running start incorrectly acknowledged")
	}
	if !at.isRunning {
		t.Fatal("rejected duplicate start changed active runtime")
	}
}
