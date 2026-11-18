package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestCopyAccountClaimSurvivesRestartAndBlocksDuplicateCredentials(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:account-claims?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "copy", true); err != nil {
		t.Fatal(err)
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "other", false); err == nil {
		t.Fatal("duplicate account allowed")
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "copy", true); err != nil {
		t.Fatal("restart cannot reclaim", err)
	}
	if err = ReleaseTradingAccount(db, "binance:native-account", "copy"); err != nil {
		t.Fatal(err)
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "regular", false); err != nil {
		t.Fatal(err)
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "regular2", false); err != nil {
		t.Fatal(err)
	}
	if err = ClaimTradingAccount(db, "binance:native-account", "copy", true); err == nil {
		t.Fatal("copy overlapped regular runtime")
	}
}

func TestCopyClaimPreservesLegacyPlanningReservation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:legacy-copy-claim?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE planning_live_leases (account_key TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO planning_live_leases(account_key) VALUES (?)", "old-credential-hash").Error; err != nil {
		t.Fatal(err)
	}
	if err := ClaimTradingAccount(db, "binance:native:different-api-key", "copy", true); err == nil {
		t.Fatal("unmapped legacy reservation bypassed")
	}
	if err := db.Exec("DELETE FROM planning_live_leases").Error; err != nil {
		t.Fatal(err)
	}
	if err := ClaimTradingAccount(db, "binance:native:different-api-key", "copy", true); err != nil {
		t.Fatal(err)
	}
}

func TestCopyOwnerRecoveryIgnoresUnrelatedLegacyLease(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:legacy-copy-recovery?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ClaimTradingAccount(db, "binance:native:existing", "copy", true); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE planning_live_leases (account_key TEXT PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO planning_live_leases(account_key) VALUES (?)", "unrelated-legacy-hash").Error; err != nil {
		t.Fatal(err)
	}
	if err := ClaimTradingAccount(db, "binance:native:existing", "copy", true); err != nil {
		t.Fatal("existing owner recovery blocked", err)
	}
}
