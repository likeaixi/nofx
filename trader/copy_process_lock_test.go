//go:build linux || darwin

package trader

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"path/filepath"
	"testing"
)

func TestCopyProcessLockExcludesDuplicateWorkerAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nofx.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	release, err := acquireCopyProcessLock(db, "binance:native:same-account")
	if err != nil {
		t.Fatal(err)
	}
	other, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := acquireCopyProcessLock(other, "binance:native:same-account"); err == nil {
		r()
		t.Fatal("duplicate worker acquired account")
	}
	release()
	r, err := acquireCopyProcessLock(other, "binance:native:same-account")
	if err != nil {
		t.Fatal("restart cannot acquire released account", err)
	}
	r()
}

func TestLegacyInitializationLockExcludesFixedRunner(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "nofx.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	release, err := acquireNativeProcessLock(db, "okx:native:account", false)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := acquireCopyProcessLock(db, "okx:native:account"); err == nil {
		r()
		t.Fatal("copy overlaps legacy account-mode initialization")
	}
	release()
	r, err := acquireCopyProcessLock(db, "okx:native:account")
	if err != nil {
		t.Fatal(err)
	}
	defer r()
	if unlock, err := acquireNativeProcessLock(db, "okx:native:account", false); err == nil {
		unlock()
		t.Fatal("legacy initialization overlaps fixed runner")
	}
}
