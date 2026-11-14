package api

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"nofx/wallet"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
)

type walletValidateRequest struct {
	PrivateKey string `json:"private_key"`
	DeriveOnly bool   `json:"derive_only"`
}

type walletValidateResponse struct {
	Valid         bool   `json:"valid"`
	Address       string `json:"address,omitempty"`
	BalanceUSDC   string `json:"balance_usdc,omitempty"`
	Claw402Status string `json:"claw402_status"` // "ok", "unreachable", "error"
	Error         string `json:"error,omitempty"`
}

func (s *Server) handleWalletValidate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req walletValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, walletValidateResponse{
			Valid: false,
			Error: "invalid request body",
		})
		return
	}

	pk := req.PrivateKey

	// Validate format
	if !strings.HasPrefix(pk, "0x") {
		c.JSON(http.StatusOK, walletValidateResponse{
			Valid: false,
			Error: "missing 0x prefix",
		})
		return
	}

	if len(pk) != 66 {
		c.JSON(http.StatusOK, walletValidateResponse{
			Valid: false,
			Error: fmt.Sprintf("should be 66 characters, got %d", len(pk)),
		})
		return
	}

	hexPart := pk[2:]
	if _, err := hex.DecodeString(hexPart); err != nil {
		c.JSON(http.StatusOK, walletValidateResponse{
			Valid: false,
			Error: "contains invalid hex characters",
		})
		return
	}

	// Derive address
	privateKey, err := crypto.HexToECDSA(hexPart)
	if err != nil {
		c.JSON(http.StatusOK, walletValidateResponse{
			Valid: false,
			Error: "invalid private key",
		})
		return
	}

	address := crypto.PubkeyToAddress(privateKey.PublicKey)
	addrHex := address.Hex()
	if req.DeriveOnly {
		c.JSON(http.StatusOK, walletValidateResponse{Valid: true, Address: addrHex})
		return
	}

	// Query USDC balance (async-ish, but sequential for simplicity)
	balanceStr := wallet.QueryUSDCBalanceStr(addrHex)

	// Check claw402 health
	claw402Status := checkClaw402Health()

	c.JSON(http.StatusOK, walletValidateResponse{
		Valid:         true,
		Address:       addrHex,
		BalanceUSDC:   balanceStr,
		Claw402Status: claw402Status,
	})
}

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

func checkClaw402Health() string {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://claw402.ai/health")
	if err != nil {
		return "unreachable"
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "ok"
	}
	return "error"
}

// walletAddressFromPrivateKey derives the EVM address for a 0x-prefixed
// private key. The claw402 wallet onboarding/export routes were removed, but
// model listing and launch preflight still display wallet addresses for
// legacy claw402 models.
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
