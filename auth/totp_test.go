package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTP(t *testing.T) {
	secret, url, err := GenerateTOTP("user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 { // base32(SHA1 20-byte seed) = 32 chars
		t.Fatalf("secret length = %d, want 32", len(secret))
	}
	if !strings.HasPrefix(url, "otpauth://totp/NOFX:") || !strings.Contains(url, "secret="+secret) {
		t.Fatalf("url = %q", url)
	}
}

func TestValidateTOTP(t *testing.T) {
	secret, _, err := GenerateTOTP("user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateTOTP(code, secret) {
		t.Fatal("valid code rejected")
	}
	if ValidateTOTP("000000", secret) && code != "000000" {
		t.Fatal("invalid code accepted")
	}
}

func TestRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 8 {
		t.Fatalf("got %d codes", len(codes))
	}
	format := regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	for _, c := range codes {
		if !format.MatchString(c) {
			t.Fatalf("bad format: %q", c)
		}
	}
	if HashRecoveryCode(codes[0]) == HashRecoveryCode(codes[1]) {
		t.Fatal("collision")
	}
	if len(HashRecoveryCode(codes[0])) != 64 {
		t.Fatal("not sha256 hex")
	}
}

func TestGenerateInviteCode(t *testing.T) {
	a, err := GenerateInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := GenerateInviteCode()
	if !regexp.MustCompile(`^[A-Z0-9]{10}$`).MatchString(a) {
		t.Fatalf("bad code: %q", a)
	}
	if a == b {
		t.Fatal("codes must not repeat")
	}
}
