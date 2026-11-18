package trader

import (
	"context"
	"errors"
	"nofx/planning"
	"nofx/planning/live"
	"nofx/store"
	"time"
)

// Legacy constructors can change position mode. Check native ownership before
// allowing those writes, even for duplicate exchange configs or different keys.
func initializeOwnedAccount(st *store.Store, config AutoTraderConfig, probe Trader, legacy func() Trader) (Trader, error) {
	fixed := config.StrategyConfig != nil && config.StrategyConfig.StrategyType == store.AllinCrypto
	if st == nil {
		if fixed {
			return nil, errors.New("copy state store required")
		}
		return legacy(), nil
	}
	native, ok := probe.(interface {
		NativeAccountIdentity(context.Context) (string, error)
	})
	if !ok {
		return nil, errors.New("native account identity unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key, err := native.NativeAccountIdentity(ctx)
	if err != nil {
		return nil, err
	}
	unlock := planning.LockAccount(key)
	defer unlock()
	if st.GormDB().Migrator().HasTable(&live.Lease{}) {
		var count int64
		if err := st.GormDB().Model(&live.Lease{}).Where("account_key = ?", key).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, errors.New("planning reserves this native account; account initialization refused")
		}
	}
	conflict, err := store.TradingAccountConflicts(st.GormDB(), key, config.ID, fixed)
	if err != nil {
		return nil, err
	}
	if conflict {
		return nil, errors.New("native account reserved by another trader")
	}
	if fixed {
		return probe, nil
	}
	// Serialize legacy account-mode writes against a fixed runner in another
	// process sharing this deployment's local SQLite database.
	if st.GormDB().Dialector.Name() == "sqlite" {
		var databases []struct {
			Name string
			File string
		}
		if err := st.GormDB().Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
			return nil, err
		}
		for _, d := range databases {
			if d.Name == "main" && d.File != "" {
				release, err := acquireNativeProcessLock(st.GormDB(), key, false)
				if err != nil {
					return nil, err
				}
				defer release()
				conflict, err := store.TradingAccountConflicts(st.GormDB(), key, config.ID, false)
				if err != nil {
					return nil, err
				}
				if conflict {
					return nil, errors.New("native account reserved by another trader")
				}
			}
		}
	}
	return legacy(), nil
}
