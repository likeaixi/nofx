package store

import (
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Native account identity, not an API key or exchange configuration ID, is the
// boundary. Claims deliberately survive a crash; a trader can reclaim its own.
type tradingAccountClaim struct {
	Account  string `gorm:"primaryKey"`
	Revision int64
	Owners   string
}

func updateTradingClaim(db *gorm.DB, account string, update func(*gorm.DB, map[string]bool) error) error {
	if account == "" {
		return errors.New("verified trading account identity required")
	}
	if err := db.AutoMigrate(&tradingAccountClaim{}); err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tradingAccountClaim{Account: account, Owners: "{}"}).Error; err != nil {
			return err
		}
		var row tradingAccountClaim
		if err := tx.First(&row, "account = ?", account).Error; err != nil {
			return err
		}
		owners := map[string]bool{}
		if err := json.Unmarshal([]byte(row.Owners), &owners); err != nil {
			return err
		}
		q := tx.Model(&tradingAccountClaim{}).Where("account = ? AND revision = ?", account, row.Revision).Update("revision", row.Revision+1)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return errors.New("concurrent account ownership change; retry required")
		}
		if err := update(tx, owners); err != nil {
			return err
		}
		raw, err := json.Marshal(owners)
		if err != nil {
			return err
		}
		if err = tx.Model(&tradingAccountClaim{}).Where("account = ?", account).Update("owners", string(raw)).Error; err != nil {
			return err
		}
		return nil
	})
}
func ClaimTradingAccount(db *gorm.DB, account, traderID string, fixed bool) error {
	return updateTradingClaim(db, account, func(tx *gorm.DB, owners map[string]bool) error {
		if tx.Migrator().HasTable("planning_live_leases") {
			var count int64
			if err := tx.Table("planning_live_leases").Where("account_key = ?", account).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return errors.New("planning has reserved this native account")
			}
			// Legacy leases contain a credential hash, so native alias ownership
			// cannot be proved locally. Preserve their recovery and conservatively
			// refuse new copy ownership until they have finished.
			if fixed && !owners[traderID] {
				if err := tx.Table("planning_live_leases").Where("account_key NOT LIKE ?", "%:native:%").Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return errors.New("legacy planning reservations must finish before starting AllinCrypto")
				}
			}
		}
		if traderID == "" {
			return errors.New("trader identity required")
		}
		for owner, isFixed := range owners {
			if owner != traderID && (fixed || isFixed) {
				return errors.New("account is reserved by another trader; resolve its copy exposure before reusing it")
			}
		}
		owners[traderID] = fixed
		return nil
	})
}
func ReleaseTradingAccount(db *gorm.DB, account, traderID string) error {
	return updateTradingClaim(db, account, func(tx *gorm.DB, owners map[string]bool) error { delete(owners, traderID); return nil })
}
func TradingAccountClaimed(db *gorm.DB, account string) (bool, error) {
	if !db.Migrator().HasTable(&tradingAccountClaim{}) {
		return false, nil
	}
	var row tradingAccountClaim
	err := db.First(&row, "account = ?", account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owners := map[string]bool{}
	if err = json.Unmarshal([]byte(row.Owners), &owners); err != nil {
		return false, err
	}
	return len(owners) > 0, nil
}

func TradingAccountConflicts(db *gorm.DB, account, traderID string, fixed bool) (bool, error) {
	if !db.Migrator().HasTable(&tradingAccountClaim{}) {
		return false, nil
	}
	var row tradingAccountClaim
	err := db.First(&row, "account = ?", account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	owners := map[string]bool{}
	if err = json.Unmarshal([]byte(row.Owners), &owners); err != nil {
		return false, err
	}
	for owner, isFixed := range owners {
		if owner != traderID && (fixed || isFixed) {
			return true, nil
		}
	}
	return false, nil
}

// Called inside the planning lease transaction; both claim paths serialize on
// the same native-account revision row across processes.
func CheckPlanningAccount(db *gorm.DB, account string) error {
	return updateTradingClaim(db, account, func(_ *gorm.DB, owners map[string]bool) error {
		if len(owners) > 0 {
			return errors.New("automatic trading reserves this account")
		}
		return nil
	})
}
