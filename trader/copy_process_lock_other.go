//go:build !linux && !darwin

package trader

import (
	"errors"
	"gorm.io/gorm"
)

func acquireCopyProcessLock(db *gorm.DB, account string) (func(), error) {
	return nil, errors.New("AllinCrypto execution requires Linux or macOS local SQLite storage")
}

func acquireNativeProcessLock(db *gorm.DB, account string, exclusive bool) (func(), error) {
	if !exclusive {
		return func() {}, nil
	}
	return acquireCopyProcessLock(db, account)
}
