package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// The AI wallet onboarding flow (auto-created Claw402 fee wallets) was removed
// for the multi-user upgrade. The planning setup-wallet endpoint stays mounted
// so older clients get an explicit removal error instead of a 404, and users
// are pointed at configuring their own model in Config.
func (s *Server) handlePlanningSetupWallet(c *gin.Context) {
	user := c.GetString("user_id")
	if user == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}
	c.Header("Cache-Control", "no-store")
	planningError(c, errors.New("AI wallet onboarding removed; configure a custom model in Config"))
}
