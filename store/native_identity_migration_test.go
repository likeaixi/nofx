package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func migrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/identity.db"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&tradingAccountClaim{}, &Trader{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE allincrypto_checkpoints (trader_id TEXT PRIMARY KEY, account TEXT, revision INTEGER, body TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	return db
}
func TestNativeIdentityMigrationSplitsCollisionWithoutLosingState(t *testing.T) {
	db := migrationDB(t)
	old := "binance:native:shared-v3"
	db.Create(&Trader{ID: "one"})
	db.Create(&Trader{ID: "two"})
	db.Create(&tradingAccountClaim{Account: old, Owners: `{"one":true,"two":false}`, Revision: 4})
	body := `{"Head":"keep","Pending":{"Order":{"ClientID":"keep-order"}},"Cycle":77}`
	db.Exec("INSERT INTO allincrypto_checkpoints VALUES(?,?,?,?)", "one", old, 3, body)
	refs, err := LegacyBinanceIdentityRefs(db)
	if err != nil || len(refs) != 2 {
		t.Fatal(refs, err)
	}
	changes := []NativeIdentityMigration{{TraderID: "one", OldAccount: old, NewAccount: "binance:native:v2:one"}, {TraderID: "two", OldAccount: old, NewAccount: "binance:native:v2:two"}}
	if err := MigrateBinanceIdentities(db, changes); err != nil {
		t.Fatal(err)
	}
	for _, change := range changes {
		yes, err := TradingAccountClaimed(db, change.NewAccount)
		if err != nil || !yes {
			t.Fatal(yes, err)
		}
	}
	yes, err := TradingAccountClaimed(db, old)
	if err != nil || yes {
		t.Fatal(yes, err)
	}
	var row struct {
		Account, Body string
		Revision      int64
	}
	db.Table("allincrypto_checkpoints").First(&row, "trader_id = ?", "one")
	if row.Account != changes[0].NewAccount || row.Body != body || row.Revision != 4 {
		t.Fatalf("state lost: %+v", row)
	}
	refs, err = LegacyBinanceIdentityRefs(db)
	if err != nil || len(refs) != 0 {
		t.Fatal(refs, err)
	}
}
func TestNativeIdentityMigrationIsAtomicOnConflictOrMissingBinding(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		t.Run(map[bool]string{false: "occupied", true: "incomplete"}[incomplete], func(t *testing.T) {
			db := migrationDB(t)
			old := "binance:native:old"
			db.Create(&tradingAccountClaim{Account: old, Owners: `{"one":true,"two":true}`})
			db.Create(&tradingAccountClaim{Account: "binance:native:v2:busy", Owners: `{"foreign":true}`})
			changes := []NativeIdentityMigration{{TraderID: "one", OldAccount: old, NewAccount: "binance:native:v2:ok"}}
			if !incomplete {
				changes = append(changes, NativeIdentityMigration{TraderID: "two", OldAccount: old, NewAccount: "binance:native:v2:busy"})
			}
			if err := MigrateBinanceIdentities(db, changes); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			var row tradingAccountClaim
			db.First(&row, "account = ?", old)
			if row.Owners != `{"one":true,"two":true}` {
				t.Fatal("partial migration", row)
			}
			yes, _ := TradingAccountClaimed(db, "binance:native:v2:ok")
			if yes {
				t.Fatal("partial target reservation")
			}
		})
	}
}

func TestNativeIdentityMigrationHonorsPlanningLeaseAndStrongerClaim(t *testing.T) {
	for _, planningLease := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve_fixed", true: "planning_reserved"}[planningLease], func(t *testing.T) {
			db := migrationDB(t)
			old := "binance:native:old"
			target := "binance:native:v2:one"
			db.Create(&tradingAccountClaim{Account: old, Owners: `{"one":false}`})
			db.Create(&tradingAccountClaim{Account: target, Owners: `{"one":true}`})
			if planningLease {
				db.Exec("CREATE TABLE planning_live_leases (account_key TEXT PRIMARY KEY)")
				db.Exec("INSERT INTO planning_live_leases VALUES(?)", target)
			}
			err := MigrateBinanceIdentities(db, []NativeIdentityMigration{{TraderID: "one", OldAccount: old, NewAccount: target}})
			if planningLease {
				if err == nil {
					t.Fatal("overlapped planning reservation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			conflict, err := TradingAccountConflicts(db, target, "other", false)
			if err != nil || !conflict {
				t.Fatal("fixed reservation downgraded", conflict, err)
			}
		})
	}
}
