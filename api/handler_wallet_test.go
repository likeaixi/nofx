package api

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// The wallet generate/balance/export/validate routes were removed with the
// claw402 wallet UI; route-level 404 assertions live in
// claw402_removal_test.go. Only the internal derivation helper survives
// (launch preflight still validates legacy claw402 model keys).
func TestWalletAddressDerivationHelper(t *testing.T) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	privKeyHex := "0x" + hex.EncodeToString(crypto.FromECDSA(privateKey))
	derived, err := walletAddressFromPrivateKey(privKeyHex)
	if err != nil || derived != crypto.PubkeyToAddress(privateKey.PublicKey).Hex() {
		t.Fatal("helper derived a different address", derived, err)
	}
	if _, err = walletAddressFromPrivateKey("0x" + strings.Repeat("0", 64)); err == nil {
		t.Fatal("invalid key accepted")
	}
	if _, err = walletAddressFromPrivateKey(strings.Repeat("a", 66)); err == nil {
		t.Fatal("non-0x key accepted")
	}
}
