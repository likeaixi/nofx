package trader

import (
	"context"
	"errors"
	"nofx/store"
	"testing"
)

type equityFixture struct {
	Trader
	fail bool
}

func (f *equityFixture) GetBalance() (map[string]interface{}, error) {
	if f.fail {
		return nil, errors.New("unavailable")
	}
	return map[string]interface{}{"totalWalletBalance": 100.0, "totalUnrealizedProfit": 5.0, "availableBalance": 80.0}, nil
}
func (f *equityFixture) GetPositions() ([]map[string]interface{}, error) {
	return []map[string]interface{}{{"markPrice": 50.0, "positionAmt": 2.0, "unRealizedProfit": 5.0, "leverage": 10.0}}, nil
}
func TestCopyEquityRecordsAccountAndSkipsReadFailure(t *testing.T) {
	st, err := store.New(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	f := &equityFixture{}
	at := &AutoTrader{id: "copy", store: st, trader: f, initialBalance: 100}
	if err = at.recordCopyEquity(); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Equity().GetLatest("copy", 10)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if rows[0].TotalEquity != 105 || rows[0].Balance != 100 || rows[0].AvailableBalance != 80 || rows[0].UnrealizedPnL != 5 || rows[0].PositionCount != 1 {
		t.Fatal(rows[0])
	}
	f.fail = true
	if at.recordCopyEquity() == nil {
		t.Fatal("read failure hidden")
	}
	rows, _ = st.Equity().GetLatest("copy", 10)
	if len(rows) != 1 {
		t.Fatal("failed read wrote a snapshot")
	}
}

func TestCanceledCopyEquityDoesNotReadOrWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// No trader/store: a canceled sampler must return without accessing either.
	(&AutoTrader{}).runCopyEquity(ctx)
}
