package nofxos

import (
	"fmt"
	"os"
	"strings"

	"nofx/logger"
)

// ResolveClient returns a nofxos data client, routed through the claw402
// x402 payment gateway. Only the explicit walletKey argument is honored; the
// CLAW402_WALLET_KEY environment fallback was removed with the claw402
// onboarding flow. Missing/invalid keys fail closed; they must not bypass
// payment. Legacy constructors cannot enable a direct-origin fallback.
func ResolveClient(walletKey string) *Client {
	walletKey = strings.TrimSpace(walletKey)
	client := NewClient(DefaultBaseURL, DefaultAuthKey)
	if walletKey == "" {
		client.gatewayErr = fmt.Errorf("CLAW402_WALLET_KEY removed; paid data requires a per-user claw402 model")
		return client
	}

	claw402URL := strings.TrimSpace(os.Getenv("CLAW402_URL"))
	claw402Client, err := NewClaw402DataClient(claw402URL, walletKey, &logger.MCPLogger{})
	if err != nil {
		client.gatewayErr = fmt.Errorf("cannot initialize paid Claw402 data client: %w", err)
		logger.Warnf("⚠️ Claw402 data access disabled: %v", err)
		return client
	}
	client.SetClaw402(claw402Client)
	return client
}
