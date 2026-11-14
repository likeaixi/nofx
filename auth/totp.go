package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// GenerateTOTP creates a new TOTP secret and otpauth:// URL for the account.
func GenerateTOTP(accountName string) (string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "NOFX",
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", err
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTP checks a 6-digit code with the standard ±1 step window.
func ValidateTOTP(code, secret string) bool {
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes returns count single-use plaintext codes (XXXX-XXXX).
func GenerateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, 0, count)
	seen := make(map[string]struct{}, count)
	for len(codes) < count {
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf) // 13 chars A-Z2-7
		code := s[:4] + "-" + s[4:8]
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

// HashRecoveryCode returns the SHA-256 hex digest used for storage.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}

// GenerateInviteCode returns a random 10-character invite code.
func GenerateInviteCode() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.New("invite code entropy unavailable")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)[:10], nil
}
