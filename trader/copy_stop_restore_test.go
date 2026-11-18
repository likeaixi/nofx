package trader

import (
	"context"
	"errors"
	"testing"

	"nofx/provider/binancecopy"
	"nofx/store"
	"nofx/trader/allincrypto"
)

type stoppedSource struct {
	allincrypto.Source
	polls int
	stop  chan struct{}
}

func (s *stoppedSource) Snapshot(context.Context, string) (binancecopy.Snapshot, error) {
	s.polls++
	close(s.stop)
	return binancecopy.Snapshot{}, errors.New("unexpected source poll")
}

type unusedVenueMethods = allincrypto.Venue
type unusedCopyVenue struct{ unusedVenueMethods }
type unusedCopyMarket struct{ allincrypto.Markets }

func TestRestoredCopyStopDrainsWithoutFetchingNewSignals(t *testing.T) {
	st, err := store.New(t.TempDir() + "/copy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Trader().Create(&store.Trader{ID: "t", UserID: "u", CopyStopping: true, IsRunning: true}); err != nil {
		t.Fatal(err)
	}
	at := &AutoTrader{id: "t", userID: "u", store: st, stopMonitorCh: make(chan struct{})}
	src := &stoppedSource{stop: at.stopMonitorCh}
	r, err := allincrypto.New(st.GormDB(), "t", "account", src, unusedCopyVenue{}, unusedCopyMarket{})
	if err != nil {
		t.Fatal(err)
	}
	if err = at.runCopyCycles(r); err != nil || src.polls != 0 {
		t.Fatal(src.polls, err)
	}
}
