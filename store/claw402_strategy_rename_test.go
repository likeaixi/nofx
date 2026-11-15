package store

import "testing"

// The startup migration renames pre-upgrade "NOFX Claw402 Auto Strategy"
// defaults to the current built-in name, unless the user already has one.
func TestLegacyClaw402StrategyRename(t *testing.T) {
	dir := t.TempDir()

	st, err := New(dir + "/rename.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().db.Create(&Strategy{
		ID: "legacy-1", UserID: "u1", Name: "NOFX Claw402 Auto Strategy",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().db.Create(&Strategy{
		ID: "legacy-2", UserID: "u2", Name: "Strategi Otomatis NOFX Claw402",
	}).Error; err != nil {
		t.Fatal(err)
	}
	// u3 has both the legacy name (ACTIVE, like the real pre-upgrade instance)
	// and a fresh build — the inactive fresh duplicate moves aside so the
	// active legacy strategy adopts the clean name.
	if err := st.Strategy().db.Create(&Strategy{
		ID: "fresh-3", UserID: "u3", Name: "NOFX Auto Strategy",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().db.Create(&Strategy{
		ID: "legacy-3", UserID: "u3", Name: "NOFX Claw402 Auto Strategy", IsActive: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// u4 is the mirror: fresh ACTIVE + legacy inactive.
	if err := st.Strategy().db.Create(&Strategy{
		ID: "fresh-4", UserID: "u4", Name: "NOFX Auto Strategy", IsActive: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Strategy().db.Create(&Strategy{
		ID: "legacy-4", UserID: "u4", Name: "NOFX Claw402 Auto Strategy",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Re-open: initTables runs the rename migration.
	st2, err := New(dir + "/rename.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()

	name := func(id string) string {
		var s Strategy
		if err := st2.Strategy().db.Where("id = ?", id).First(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.Name
	}
	if got := name("legacy-1"); got != "NOFX Auto Strategy" {
		t.Fatalf("legacy-1 renamed to %q", got)
	}
	if got := name("legacy-2"); got != "NOFX Auto Strategy" {
		t.Fatalf("legacy-2 (id locale) renamed to %q", got)
	}
	if got := name("legacy-3"); got != "NOFX Auto Strategy" {
		t.Fatalf("active legacy in collision case must adopt the clean name, got %q", got)
	}
	if got := name("fresh-3"); got != "NOFX Auto Strategy (duplicate)" {
		t.Fatalf("inactive fresh duplicate must move aside, got %q", got)
	}
	if got := name("fresh-4"); got != "NOFX Auto Strategy" {
		t.Fatalf("active fresh must be untouched, got %q", got)
	}
	if got := name("legacy-4"); got != "NOFX Auto Strategy (legacy)" {
		t.Fatalf("inactive legacy must archive, got %q", got)
	}
}
