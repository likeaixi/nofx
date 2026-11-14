package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
)

func TestWalletBalanceDoesNotTreatFailureAsZero(t *testing.T) {
	for _, tc := range []struct {
		name, address string
		fail          bool
		status        int
	}{
		{"invalid address", "bad", false, 400},
		{"unavailable", "0x0000000000000000000000000000000000000001", true, 503},
		{"empty wallet", "0x0000000000000000000000000000000000000001", false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/wallet/balance?address="+tc.address, nil)
			calls := 0
			writeWalletBalance(c, func(address string) (float64, error) {
				calls++
				if tc.fail {
					return 0, errors.New("unreachable")
				}
				return 0, nil
			})
			if w.Code != tc.status {
				t.Fatalf("status %d", w.Code)
			}
			if tc.status == 400 && calls != 0 {
				t.Fatal("invalid address reached RPC")
			}
			if tc.fail && strings.Contains(w.Body.String(), "balance_usdc") {
				t.Fatal("invented balance on failure")
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), `"balance_usdc":"0.000000"`) {
				t.Fatal("real zero not returned")
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("balance must not be browser cached")
			}
		})
	}
}

func TestWalletGenerationAndImportDerivation(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	s.handleWalletGenerate(c)
	var generated walletGenerateResponse
	if json.Unmarshal(w.Body.Bytes(), &generated) != nil || w.Code != 200 {
		t.Fatal("wallet generation failed")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private key response must not be cached")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(generated.PrivateKey, "0x"))
	if err != nil || crypto.PubkeyToAddress(key.PublicKey).Hex() != generated.Address {
		t.Fatal("address does not match generated key")
	}
	for _, valid := range []bool{true, false} {
		privateKey := generated.PrivateKey
		if !valid {
			privateKey = "0x" + strings.Repeat("0", 64)
		}
		w = httptest.NewRecorder()
		c, _ = gin.CreateTestContext(w)
		body, _ := json.Marshal(walletValidateRequest{PrivateKey: privateKey, DeriveOnly: true})
		c.Request = httptest.NewRequest(http.MethodPost, "/wallet/validate", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		s.handleWalletValidate(c)
		var result walletValidateResponse
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Valid != valid {
			t.Fatal("incorrect validation result")
		}
		if valid && result.Address != generated.Address {
			t.Fatal("incorrect derived address")
		}
		if strings.Contains(w.Body.String(), privateKey) {
			t.Fatal("import response contains a private key")
		}
		if result.BalanceUSDC != "" {
			t.Fatal("derivation must not query funding or require external services")
		}
	}
}

// The claw402 wallet-export route was removed with wallet onboarding; route
// assertions live in claw402_removal_test.go. The surviving wallet endpoints
// (validate/generate/balance) are covered above.
