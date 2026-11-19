package trader

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"nofx/store"
	"nofx/trader/binance"
)

// UpgradeBinanceAccountIdentities runs before any workers/API are started.
// Each legacy owner is resolved using its own user-scoped exchange credentials.
func UpgradeBinanceAccountIdentities(st *store.Store) error {
	return upgradeBinanceAccountIdentities(st, func(ctx context.Context, ex *store.Exchange) (string, error) {
		probe, err := binance.NewAccountTrader(ctx, string(ex.APIKey), string(ex.SecretKey), ex.UserID)
		if err != nil {
			return "", err
		}
		return probe.NativeAccountIdentity(ctx)
	})
}

func upgradeBinanceAccountIdentities(st *store.Store, resolve func(context.Context, *store.Exchange) (string, error)) error {
	db := st.GormDB()
	if err := store.CheckLegacyBinancePlanningLeases(db); err != nil {
		return err
	}
	refs, err := store.LegacyBinanceIdentityRefs(db)
	if err != nil || len(refs) == 0 {
		return err
	}
	releases := []func(){}
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	lock := func(keys map[string]bool) error {
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			release, err := acquireCopyProcessLock(db, key)
			if err != nil {
				return fmt.Errorf("identity migration cannot acquire account ownership: %w", err)
			}
			releases = append(releases, release)
		}
		return nil
	}
	oldKeys := map[string]bool{}
	for _, ref := range refs {
		oldKeys[ref.OldAccount] = true
	}
	if err := lock(oldKeys); err != nil {
		return err
	}
	cache := map[string]string{}
	newKeys := map[string]bool{}
	for i := range refs {
		ref := &refs[i]
		var owner store.Trader
		if err := db.First(&owner, "id = ?", ref.TraderID).Error; err != nil {
			return errors.New("legacy account owner missing; manual review required")
		}
		ex, err := st.Exchange().GetByID(owner.UserID, owner.ExchangeID)
		if err != nil || ex.ExchangeType != "binance" {
			return errors.New("legacy Binance owner exchange cannot be verified")
		}
		if cache[ex.ID] == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			key, err := resolve(ctx, ex)
			cancel()
			if err != nil {
				return fmt.Errorf("verify legacy trader %s account: %w", owner.ID, err)
			}
			cache[ex.ID] = key
		}
		ref.NewAccount = cache[ex.ID]
		newKeys[ref.NewAccount] = true
	}
	if err := lock(newKeys); err != nil {
		return err
	}
	return store.MigrateBinanceIdentities(db, refs)
}
