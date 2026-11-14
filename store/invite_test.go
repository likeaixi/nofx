package store

import (
	"testing"

	"nofx/auth"
	"nofx/crypto"
)

func newTestUserStore(t *testing.T) *UserStore {
	t.Helper()
	st, err := New(t.TempDir() + "/invite.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.User()
}

func TestInviteCodeLifecycle(t *testing.T) {
	st, err := New(t.TempDir() + "/invite.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.Invite().Create("ABC123XYZ9", "owner"); err != nil {
		t.Fatal(err)
	}
	got, err := st.Invite().GetValid("ABC123XYZ9")
	if err != nil || got.Revoked || got.UsedBy != nil {
		t.Fatal(got, err)
	}
	if err := st.Invite().MarkUsed("ABC123XYZ9", "u1"); err != nil {
		t.Fatal(err)
	}
	// 单次语义：第二次标记必须失败
	if err := st.Invite().MarkUsed("ABC123XYZ9", "u2"); err != ErrInviteUnavailable {
		t.Fatalf("want ErrInviteUnavailable, got %v", err)
	}
	if _, err := st.Invite().GetValid("ABC123XYZ9"); err == nil {
		t.Fatal("used code still valid")
	}
	// 作废未用码
	if err := st.Invite().Create("REVOKABLE01", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.Invite().Revoke("REVOKABLE01"); err != nil {
		t.Fatal(err)
	}
	if err := st.Invite().MarkUsed("REVOKABLE01", "u1"); err != ErrInviteUnavailable {
		t.Fatalf("revoked code marked used: %v", err)
	}
}

func TestUserTOTPFields(t *testing.T) {
	us := newTestUserStore(t)
	u := &User{ID: "u1", Email: "u1@x.test", PasswordHash: "h", Role: RoleUser}
	if err := us.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	secret := "JBSWY3DPEHPK3PXP"
	if err := us.UpdateTOTP("u1", crypto.EncryptedString(secret), true); err != nil {
		t.Fatal(err)
	}
	got, err := us.GetByID("u1")
	if err != nil || !got.TotpEnabled || got.TotpSecret != crypto.EncryptedString(secret) {
		t.Fatalf("totp roundtrip failed: %+v %v", got, err)
	}
	codes, _ := auth.GenerateRecoveryCodes(3)
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = auth.HashRecoveryCode(c)
	}
	if err := us.SetRecoveryCodes("u1", hashes); err != nil {
		t.Fatal(err)
	}
	if !us.ConsumeRecoveryCode("u1", codes[0]) {
		t.Fatal("valid recovery code rejected")
	}
	if us.ConsumeRecoveryCode("u1", codes[0]) {
		t.Fatal("recovery code reused")
	}
}

func TestSetDisabledBumpsAuthVersion(t *testing.T) {
	us := newTestUserStore(t)
	if err := us.CreateUser(&User{ID: "u1", Email: "u1@x.test", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := us.SetDisabled("u1", true); err != nil {
		t.Fatal(err)
	}
	got, _ := us.GetByID("u1")
	if !got.Disabled || got.AuthVersion != 1 {
		t.Fatalf("disabled=%v authVersion=%d", got.Disabled, got.AuthVersion)
	}
	if err := us.SetDisabled("u1", false); err != nil {
		t.Fatal(err)
	}
	got, _ = us.GetByID("u1")
	if got.Disabled || got.AuthVersion != 1 {
		t.Fatal("re-enable must not bump again", got)
	}
}

func TestEnsureSingleOwner(t *testing.T) {
	us := newTestUserStore(t)
	if err := us.CreateUser(&User{ID: "legacy", Email: "legacy@x.test", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := us.CreateUser(&User{ID: "new", Email: "new@x.test", PasswordHash: "h", Role: RoleUser}); err != nil {
		t.Fatal(err)
	}
	if err := us.EnsureSingleOwner(); err != nil {
		t.Fatal(err)
	}
	legacy, _ := us.GetByID("legacy")
	newu, _ := us.GetByID("new")
	if legacy.Role != RoleOwner || newu.Role != RoleUser {
		t.Fatalf("owner migration wrong: %q %q", legacy.Role, newu.Role)
	}
	// 幂等
	if err := us.EnsureSingleOwner(); err != nil {
		t.Fatal(err)
	}
	all, _ := us.GetAll()
	owners := 0
	for _, u := range all {
		if u.Role == RoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d", owners)
	}
}
