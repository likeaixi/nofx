package trader

import (
	"nofx/store"
	"testing"
)

func TestStartReportsRejectedRuntime(t *testing.T) {
	at := &AutoTrader{isRunning: true}
	if err := at.Start(); err == nil {
		t.Fatal("already-running start incorrectly acknowledged")
	}
	if !at.isRunning {
		t.Fatal("rejected duplicate start changed active runtime")
	}
}

func TestRuntimeExitPreservesOnlyServiceShutdownIntent(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "user_stop", true: "service_restart"}[shutdown], func(t *testing.T) {
			st, err := store.New(t.TempDir() + "/state.db")
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if err := st.Trader().Create(&store.Trader{ID: "t", UserID: "u", IsRunning: true}); err != nil {
				t.Fatal(err)
			}
			at := &AutoTrader{id: "t", userID: "u", store: st, isRunning: true, stopMonitorCh: make(chan struct{})}
			if shutdown {
				at.Shutdown()
			} else {
				at.Stop()
			}
			at.persistRuntimeExit()
			var row store.Trader
			if err := st.GormDB().First(&row, "id = ?", "t").Error; err != nil {
				t.Fatal(err)
			}
			if row.IsRunning != shutdown {
				t.Fatalf("running=%v, shutdown=%v", row.IsRunning, shutdown)
			}
		})
	}
}
