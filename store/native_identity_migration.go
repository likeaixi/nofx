package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const BinanceIdentityV2Prefix = "binance:native:v2:"
const legacyBinanceIdentityQuery = "account LIKE 'binance:native:%' AND account NOT LIKE 'binance:native:v2:%'"

type NativeIdentityMigration struct{ TraderID, OldAccount, NewAccount string }

// Identity versions are not interchangeable. Existing approval digests must not
// be rewritten; finish legacy planning leases on the previous release first.
func CheckLegacyBinancePlanningLeases(db *gorm.DB) error {
	if !db.Migrator().HasTable("planning_live_leases") {
		return nil
	}
	var count int64
	if err := db.Table("planning_live_leases").Where("account_key LIKE 'binance:native:%' AND account_key NOT LIKE 'binance:native:v2:%'").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("finish legacy Binance live planning reservations before upgrading account identities")
	}
	return nil
}

// Return all owners requiring verified credentials, including stopped traders
// with checkpoints so no automatic task can bypass an unconverted reservation.
func LegacyBinanceIdentityRefs(db *gorm.DB) ([]NativeIdentityMigration, error) {
	seen := map[string]NativeIdentityMigration{}
	add := func(id, account string) error {
		if old, ok := seen[id]; ok && old.OldAccount != account {
			return errors.New("conflicting legacy identities for trader")
		}
		seen[id] = NativeIdentityMigration{TraderID: id, OldAccount: account}
		return nil
	}
	if db.Migrator().HasTable(&tradingAccountClaim{}) {
		var rows []tradingAccountClaim
		if err := db.Where(legacyBinanceIdentityQuery).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			var owners map[string]bool
			if err := json.Unmarshal([]byte(row.Owners), &owners); err != nil {
				return nil, err
			}
			for id := range owners {
				if err := add(id, row.Account); err != nil {
					return nil, err
				}
			}
		}
	}
	if db.Migrator().HasTable("allincrypto_checkpoints") {
		var rows []struct{ TraderID, Account string }
		// Deleted empty histories do not require credentials. Orphaned reservations
		// remain above and must be resolved explicitly, never silently discarded.
		q := db.Table("allincrypto_checkpoints").Select("allincrypto_checkpoints.trader_id, account").Joins("JOIN traders ON traders.id = allincrypto_checkpoints.trader_id").Where(legacyBinanceIdentityQuery)
		if err := q.Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			if err := add(row.TraderID, row.Account); err != nil {
				return nil, err
			}
		}
	}
	refs := make([]NativeIdentityMigration, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].TraderID < refs[j].TraderID })
	return refs, nil
}

// Call only before workers are started, under old and new process account locks,
// after resolving every trader's owned exchange credentials against Binance v2.
// Claims and checkpoint keys move atomically; signal IDs and checkpoint bodies
// (orders, stops, baseline, cycle and report timestamps) are never rewritten.
func MigrateBinanceIdentities(db *gorm.DB, changes []NativeIdentityMigration) error {
	if err := CheckLegacyBinancePlanningLeases(db); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	if err := db.AutoMigrate(&tradingAccountClaim{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := CheckLegacyBinancePlanningLeases(tx); err != nil {
			return err
		}
		refs, err := LegacyBinanceIdentityRefs(tx)
		if err != nil {
			return err
		}
		bindings := map[string]NativeIdentityMigration{}
		for _, c := range changes {
			if c.TraderID == "" || !strings.HasPrefix(c.NewAccount, BinanceIdentityV2Prefix) || len(c.NewAccount) == len(BinanceIdentityV2Prefix) {
				return errors.New("verified v2 account identity required")
			}
			if _, exists := bindings[c.TraderID]; exists {
				return errors.New("duplicate identity binding")
			}
			bindings[c.TraderID] = c
		}
		if len(refs) != len(bindings) {
			return errors.New("legacy account ownership changed; retry migration")
		}
		for _, ref := range refs {
			if b, ok := bindings[ref.TraderID]; !ok || b.OldAccount != ref.OldAccount {
				return errors.New("missing or changed legacy account binding")
			}
		}
		var oldRows []tradingAccountClaim
		if err := tx.Where(legacyBinanceIdentityQuery).Find(&oldRows).Error; err != nil {
			return err
		}
		for _, old := range oldRows {
			owners := map[string]bool{}
			if err := json.Unmarshal([]byte(old.Owners), &owners); err != nil {
				return err
			}
			for id, fixed := range owners {
				b := bindings[id]
				if tx.Migrator().HasTable("planning_live_leases") {
					var count int64
					if err := tx.Table("planning_live_leases").Where("account_key = ?", b.NewAccount).Count(&count).Error; err != nil {
						return err
					}
					if count > 0 {
						return errors.New("planning reserves the verified destination account")
					}
				}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tradingAccountClaim{Account: b.NewAccount, Owners: "{}"}).Error; err != nil {
					return err
				}
				var target tradingAccountClaim
				if err := tx.First(&target, "account = ?", b.NewAccount).Error; err != nil {
					return err
				}
				targetOwners := map[string]bool{}
				if err := json.Unmarshal([]byte(target.Owners), &targetOwners); err != nil {
					return err
				}
				if targetOwners == nil {
					return errors.New("invalid target account owners")
				}
				fixed = fixed || targetOwners[id]
				for owner, otherFixed := range targetOwners {
					if owner != id && (fixed || otherFixed) {
						return fmt.Errorf("verified account already reserved; cannot migrate trader %s", id)
					}
				}
				targetOwners[id] = fixed
				raw, _ := json.Marshal(targetOwners)
				q := tx.Model(&tradingAccountClaim{}).Where("account = ? AND revision = ?", target.Account, target.Revision).Updates(map[string]any{"owners": string(raw), "revision": target.Revision + 1})
				if q.Error != nil {
					return q.Error
				}
				if q.RowsAffected != 1 {
					return errors.New("concurrent target reservation")
				}
			}
			q := tx.Model(&tradingAccountClaim{}).Where("account = ? AND revision = ?", old.Account, old.Revision).Updates(map[string]any{"owners": "{}", "revision": old.Revision + 1})
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected != 1 {
				return errors.New("concurrent legacy reservation")
			}
		}
		if tx.Migrator().HasTable("allincrypto_checkpoints") {
			for _, b := range changes {
				if err := tx.Table("allincrypto_checkpoints").Where("trader_id = ? AND account = ?", b.TraderID, b.OldAccount).Updates(map[string]any{"account": b.NewAccount, "revision": gorm.Expr("revision + 1")}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
