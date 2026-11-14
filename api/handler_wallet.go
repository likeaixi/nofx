package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
)

type walletGenerateResponse struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

// handleWalletGenerate mints a fresh EVM keypair for the Hyperliquid NOFX
// agent wallet (the exchange connect flow prepares its agent key here). The
// claw402 onboarding/export/validate routes and the generic balance route were
// removed with the claw402 wallet UI.
func (s *Server) handleWalletGenerate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate wallet"})
		return
	}

	address := crypto.PubkeyToAddress(privateKey.PublicKey)
	privKeyHex := "0x" + hex.EncodeToString(crypto.FromECDSA(privateKey))

	c.JSON(http.StatusOK, walletGenerateResponse{
		Address:    address.Hex(),
		PrivateKey: privKeyHex,
	})
}

// walletAddressFromPrivateKey derives the EVM address for a 0x-prefixed
// private key. The claw402 wallet onboarding/export/validation routes were
// removed; launch preflight still derives addresses for legacy claw402 models.
func walletAddressFromPrivateKey(privateKey string) (string, error) {
	key := strings.TrimSpace(privateKey)
	if !strings.HasPrefix(key, "0x") {
		return "", fmt.Errorf("private key must start with 0x")
	}
	if len(key) != 66 {
		return "", fmt.Errorf("private key must be 66 characters")
	}

	privateKeyObj, err := crypto.HexToECDSA(strings.TrimPrefix(key, "0x"))
	if err != nil {
		return "", err
	}

	return crypto.PubkeyToAddress(privateKeyObj.PublicKey).Hex(), nil
}
