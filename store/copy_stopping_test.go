package store

import "testing"

func TestCopyStopRequestSurvivesReopen(t *testing.T) {
	path := t.TempDir() + "/state.db"
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Trader().Create(&Trader{ID: "t", UserID: "u", Name: "copy", ExchangeID: "ex"}); err != nil {
		t.Fatal(err)
	}
	if err = st.Trader().SetCopyStopping("u", "t", true); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	st, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stopped, err := st.Trader().CopyStopRequested("u", "t")
	if err != nil || !stopped {
		t.Fatal(stopped, err)
	}
	if err = st.Trader().SetCopyStopping("another", "t", false); err == nil {
		t.Fatal("stop request cleared without ownership")
	}
}
