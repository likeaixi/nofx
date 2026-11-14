package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"nofx/wallet"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
)

type walletGenerateResponse struct {
	Address    string `json:"address"`
	PrivateKey string `json:"private_key"`
}

func (s *Server) handleWalletGenerate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	// Generate new EVM wallet
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

// handleWalletBalance checks funding using only the public address. Private
// keys must not accompany periodic balance requests.
func (s *Server) handleWalletBalance(c *gin.Context) {
	writeWalletBalance(c, wallet.QueryUSDCBalanceCached)
}

func writeWalletBalance(c *gin.Context, query func(string) (float64, error)) {
	c.Header("Cache-Control", "no-store")
	address := strings.TrimSpace(c.Query("address"))
	if !common.IsHexAddress(address) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid wallet address"})
		return
	}
	balance, err := query(address)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Balance is temporarily unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"address": common.HexToAddress(address).Hex(), "balance_usdc": fmt.Sprintf("%.6f", balance)})
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
