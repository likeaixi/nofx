package trader

import (
	"context"
	"errors"
	"testing"

	"nofx/store"
)

func TestStartupIdentityUpgradeResolvesOwnedExchangeAndPreservesCheckpoint(t *testing.T) {
	st, err := store.New(t.TempDir() + "/copy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	db := st.GormDB()
	for _, name := range []string{"one", "two"} {
		if err := db.Create(&store.Exchange{ID: "ex-" + name, UserID: "user-" + name, ExchangeType: "binance"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := st.Trader().Create(&store.Trader{ID: name, UserID: "user-" + name, ExchangeID: "ex-" + name}); err != nil {
			t.Fatal(err)
		}
	}
	old := "binance:native:common-v3"
	if err := store.ClaimTradingAccount(db, old, "one", true); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE allincrypto_checkpoints (trader_id TEXT PRIMARY KEY,account TEXT,revision INTEGER,body TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	db.Exec("INSERT INTO allincrypto_checkpoints VALUES(?,?,?,?)", "one", old, 3, `{"Head":"retained"}`)
	calls := 0
	resolver := func(_ context.Context, ex *store.Exchange) (string, error) {
		calls++
		if ex.ID != "ex-one" || ex.UserID != "user-one" {
			t.Fatal("wrong owner's credentials", ex.ID, ex.UserID)
		}
		return "binance:native:v2:one", nil
	}
	if err := upgradeBinanceAccountIdentities(st, resolver); err != nil {
		t.Fatal(err)
	}
	if err := upgradeBinanceAccountIdentities(st, resolver); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("repeated migrated account lookup", calls)
	}
	if conflict, err := store.TradingAccountConflicts(db, "binance:native:v2:two", "two", true); err != nil || conflict {
		t.Fatal("independent account blocked", conflict, err)
	}
	var row struct{ Account, Body string }
	db.Table("allincrypto_checkpoints").First(&row)
	if row.Account != "binance:native:v2:one" || row.Body != `{"Head":"retained"}` {
		t.Fatal(row)
	}
}

func TestStartupIdentityUpgradeRefusesWhileOldWorkerOwnsAccount(t *testing.T) {
	st, err := store.New(t.TempDir() + "/copy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := "binance:native:old-worker"
	if err := store.ClaimTradingAccount(st.GormDB(), old, "one", true); err != nil {
		t.Fatal(err)
	}
	release, err := acquireCopyProcessLock(st.GormDB(), old)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := upgradeBinanceAccountIdentities(st, func(context.Context, *store.Exchange) (string, error) {
		t.Fatal("must lock before resolving")
		return "", errors.New("unexpected")
	}); err == nil {
		t.Fatal("migration overlaps old worker")
	}
	yes, err := store.TradingAccountClaimed(st.GormDB(), old)
	if err != nil || !yes {
		t.Fatal("claim changed on rejected migration", yes, err)
	}
}
