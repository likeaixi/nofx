package api

import (
	"context"
	"errors"
	"nofx/planning/live"
	"nofx/store"
)

func (s *Server) verifiedPlanningBroker(ctx context.Context, b live.Broker, key string) (live.Broker, string, error) {
	legacyKey := key
	if wrapped, ok := b.(live.NOFXBroker); ok {
		if native, ok := wrapped.Trader.(interface {
			NativeAccountIdentity(context.Context) (string, error)
		}); ok {
			var err error
			key, err = native.NativeAccountIdentity(ctx)
			if err != nil {
				return nil, "", err
			}
		}
	}
	claimed, err := store.TradingAccountClaimed(s.store.GormDB(), key)
	if err != nil {
		return nil, "", err
	}
	if claimed {
		return nil, "", errors.New("automatic trading reserves this native account; resolve followed exposure before planning execution")
	}
	// Existing approved plans retain their original approval/account digest.
	if key != legacyKey && s.store.GormDB().Migrator().HasTable(&live.Lease{}) {
		var count int64
		if err := s.store.GormDB().Model(&live.Lease{}).Where("account_key = ?", legacyKey).Count(&count).Error; err != nil {
			return nil, "", err
		}
		if count > 0 {
			key = legacyKey
		}
	}
	return b, key, nil
}
