//go:build linux || darwin

package trader

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"gorm.io/gorm"
)

// Copy execution is supported only with a local, disk-backed SQLite database.
// The lock lives beside that database and is never unlinked: replacing the inode
// would let a new worker bypass an existing lock. Kernel ownership ends on crash.
func acquireCopyProcessLock(db *gorm.DB, account string) (func(), error) {
	return acquireNativeProcessLock(db, account, true)
}
func acquireNativeProcessLock(db *gorm.DB, account string, exclusive bool) (func(), error) {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return nil, errors.New("AllinCrypto requires local SQLite storage; distributed execution is unsupported")
	}
	var databases []struct {
		Name string
		File string
	}
	if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
		return nil, err
	}
	var path string
	for _, d := range databases {
		if d.Name == "main" {
			path = d.File
		}
	}
	if path == "" || account == "" {
		return nil, errors.New("AllinCrypto requires disk-backed SQLite and a verified account")
	}
	lockPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".nofx-copy-%x.lock", sha256.Sum256([]byte(account))))
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), lockPath)
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	if err := unix.Flock(fd, mode|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("AllinCrypto account is already owned by another process")
	}
	return func() { _ = f.Close() }, nil
}
