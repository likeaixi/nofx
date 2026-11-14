package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"nofx/auth"
	"nofx/crypto"
	"nofx/logger"
	"nofx/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// handleLogout durably revokes the current session before acknowledging logout.
func (s *Server) handleLogout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
		return
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid Authorization format"})
		return
	}
	tokenString := parts[1]
	claims, err := auth.ValidateJWT(tokenString)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if err := s.store.User().RevokeSession(claims.UserID, claims.ID, claims.ExpiresAt.Time); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Unable to revoke session; please retry logout"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}

// handleRegister starts two-step registration. Step 1 verifies the invite (if
// the instance is already initialized), creates a pending user with a fresh
// TOTP secret, and returns the otpauth URL. The account cannot log in until
// step 2 (handleRegisterConfirm) verifies a code from the authenticator.
func (s *Server) handleRegister(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		Password   string `json:"password" binding:"required,min=8"`
		Lang       string `json:"lang"`
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	lang := req.Lang
	if lang != "zh" && lang != "id" {
		lang = "en"
	}

	userCount, err := s.store.User().Count()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check user count"})
		return
	}
	isFirst := userCount == 0

	// Invite gate: the untouched first registration claims instance ownership;
	// every later registration needs a valid, unused invite.
	if !isFirst {
		if strings.TrimSpace(req.InviteCode) == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invite code required"})
			return
		}
		if _, err := s.store.Invite().GetValid(strings.TrimSpace(req.InviteCode)); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid invite code"})
			return
		}
	}

	if _, err := s.store.User().GetByEmail(req.Email); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Email already registered"})
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Password processing failed"})
		return
	}

	secret, url, err := auth.GenerateTOTP(req.Email)
	if err != nil {
		SafeInternalError(c, "Failed to generate authenticator secret", err)
		return
	}

	userID := uuid.New().String()
	role := store.RoleUser
	if isFirst {
		role = store.RoleOwner
	}
	user := &store.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         role,
		TotpSecret:   crypto.EncryptedString(secret),
	}
	if isFirst {
		// registrationLock serializes the ownership race across processes.
		if err := s.store.User().CreateFirst(user); errors.Is(err, store.ErrAlreadyInitialized) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invite code required"})
			return
		} else if err != nil {
			SafeInternalError(c, "Failed to create user", err)
			return
		}
	} else if err := s.store.User().CreateUser(user); err != nil {
		SafeInternalError(c, "Failed to create user", err)
		return
	}
	_ = lang // default strategies are created at confirm time

	c.JSON(http.StatusOK, gin.H{
		"totp_secret":   secret,
		"otpauth_url":   url,
		"is_first_user": isFirst,
	})
}

// handleRegisterConfirm activates a pending registration: password + TOTP code
// verified, invite atomically claimed (single use), recovery codes issued.
func (s *Server) handleRegisterConfirm(c *gin.Context) {
	var req struct {
		Email      string `json:"email" binding:"required,email"`
		Password   string `json:"password" binding:"required"`
		TotpCode   string `json:"totp_code" binding:"required"`
		InviteCode string `json:"invite_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		auth.CheckPassword(req.Password, dummyPasswordHash)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}
	if user.TotpEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "Account already activated; use login"})
		return
	}
	if !auth.ValidateTOTP(req.TotpCode, user.TotpSecret.String()) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid verification code"})
		return
	}
	if req.InviteCode != "" {
		if err := s.store.Invite().MarkUsed(strings.TrimSpace(req.InviteCode), user.ID); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid invite code"})
			return
		}
	}
	if err := s.store.User().UpdateTOTP(user.ID, user.TotpSecret, true); err != nil {
		SafeInternalError(c, "Failed to activate account", err)
		return
	}
	codes, err := auth.GenerateRecoveryCodes(8)
	if err != nil {
		SafeInternalError(c, "Failed to generate recovery codes", err)
		return
	}
	hashes := make([]string, len(codes))
	for i, code := range codes {
		hashes[i] = auth.HashRecoveryCode(code)
	}
	if err := s.store.User().SetRecoveryCodes(user.ID, hashes); err != nil {
		SafeInternalError(c, "Failed to store recovery codes", err)
		return
	}
	if err := s.initUserDefaultConfigs(user.ID, "en"); err != nil {
		logger.Infof("Failed to initialize user default configs: %v", err)
	}
	token, err := auth.GenerateJWT(user.ID, user.Email, user.AuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token": token, "user_id": user.ID, "email": user.Email,
		"role": user.Role, "recovery_codes": codes,
	})
}

// dummyPasswordHash is a valid bcrypt hash of a throwaway value. It is compared
// against when the submitted email does not exist so that login takes roughly
// the same time whether or not the account exists — closing the timing side
// channel that would otherwise let an attacker enumerate valid emails (a fast
// "no such user" vs. a slow bcrypt compare). It is not a secret.
const dummyPasswordHash = "$2a$10$0iF0bCoQLJ6Ph1bF.MXwHOW.IMTxQjeEW.w38dctRQAB2kwB6ga1q"

// handleLogin Handle user login request
func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Email    string `json:"email" binding:"required,email"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "Invalid request parameters")
		return
	}

	// Get user information
	user, err := s.store.User().GetByEmail(req.Email)
	if err != nil {
		// Perform a dummy comparison so the response time does not reveal
		// whether the email exists (anti user-enumeration), then fail uniformly.
		auth.CheckPassword(req.Password, dummyPasswordHash)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}

	// Verify password
	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Email or password incorrect"})
		return
	}

	// Issue token directly after password verification.
	token, err := auth.GenerateJWT(user.ID, user.Email, user.AuthVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": user.ID,
		"email":   user.Email,
		"message": "Login successful",
	})
}

// handleChangePassword changes the password for the currently authenticated user.
func (s *Server) handleChangePassword(c *gin.Context) {
	userID := c.GetString("user_id")
	var req struct {
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		SafeBadRequest(c, "new_password is required (min 8 chars)")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		SafeInternalError(c, "Password processing failed", err)
		return
	}
	if err := s.store.User().UpdatePassword(userID, hash); err != nil {
		SafeInternalError(c, "Failed to update password", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Password updated"})
}

// NOTE: Password and account recovery used to live here as the public,
// unauthenticated handlers handleResetPassword / handleResetAccount. They were
// removed because an unauthenticated recovery endpoint is a remotely
// exploitable auth-bypass on any public-facing deployment: the confirm phrase
// was embedded in the frontend (and echoed back by the API), so it was friction
// rather than authentication. Recovery now lives in the local CLI
// (`nofx reset-password` / `nofx reset-account`, see cli.go), which requires
// shell access to the host — something a remote attacker does not have.

// initUserDefaultConfigs Initialize default configs for new user
func (s *Server) initUserDefaultConfigs(userID string, lang string) error {
	if err := s.createDefaultStrategies(userID, lang); err != nil {
		logger.Warnf("Failed to create default strategies for user %s: %v", userID, err)
		// Non-fatal: user can create strategies manually
	}
	logger.Infof("✓ User %s registration completed with default strategies", userID)
	return nil
}

func (s *Server) createDefaultStrategies(userID string, lang string) error {
	type strategyI18n struct {
		name, description string
	}
	type strategyLocale struct {
		defaultStrategy strategyI18n
	}
	locales := map[string]strategyLocale{
		"zh": {
			defaultStrategy: strategyI18n{"NOFX Claw402 Auto Strategy", "The only built-in strategy: read the Claw402.ai direction board each cycle, load direction history and cost/liquidation heatmaps, then manage positions by the live board."},
		},
		"en": {
			defaultStrategy: strategyI18n{"NOFX Claw402 Auto Strategy", "The only built-in strategy: read the Claw402.ai direction board each cycle, load direction history and cost/liquidation heatmaps, then manage positions by the live board."},
		},
		"id": {
			defaultStrategy: strategyI18n{"Strategi Otomatis NOFX Claw402", "Satu strategi bawaan: membaca papan arah Claw402.ai setiap siklus, memuat riwayat arah dan heatmap biaya/likuidasi, lalu mengelola posisi mengikuti papan langsung."},
		},
	}
	locale, ok := locales[lang]
	if !ok {
		locale = locales["en"]
	}

	type strategyDef struct {
		name        string
		description string
		isActive    bool
		applyConfig func(*store.StrategyConfig)
	}

	setClaw402Strategy := func(c *store.StrategyConfig) {
		c.CoinSource.SourceType = "vergex_signal"
		c.CoinSource.StaticCoins = nil
		c.CoinSource.UseAI500 = false
		c.CoinSource.UseOITop = false
		c.CoinSource.UseOILow = false
		c.CoinSource.UseHyperAll = false
		c.CoinSource.UseHyperMain = false
		c.CoinSource.HyperRankCategory = "all"
		c.CoinSource.VergexLimit = 10
		c.CoinSource.VergexMarketType = "all"
		c.CoinSource.VergexChain = "hyperliquid"
		c.RiskControl.MaxPositions = store.AutopilotDefaultMaxPositions
		c.RiskControl.BTCETHMaxLeverage = 10
		c.RiskControl.AltcoinMaxLeverage = 10
		// Position-value ratios are hard per-position caps. Actual Autopilot
		// allocation is calculated separately from leverage and slot count.
		c.RiskControl.BTCETHMaxPositionValueRatio = store.AutopilotMaxPositionValueRatio
		c.RiskControl.AltcoinMaxPositionValueRatio = store.AutopilotMaxPositionValueRatio
		c.RiskControl.MaxMarginUsage = 1.0
		c.RiskControl.MinConfidence = 78
		c.RiskControl.MinRiskRewardRatio = 3.0
		c.Indicators.Klines.PrimaryTimeframe = "15m"
		c.Indicators.Klines.PrimaryCount = 30
		c.Indicators.Klines.LongerTimeframe = ""
		c.Indicators.Klines.LongerCount = 0
		c.Indicators.Klines.EnableMultiTimeframe = false
		c.Indicators.Klines.SelectedTimeframes = []string{"15m"}
		c.Indicators.EnableRawKlines = true
	}

	definitions := []strategyDef{
		{
			name:        locale.defaultStrategy.name,
			description: locale.defaultStrategy.description,
			isActive:    true,
			applyConfig: func(c *store.StrategyConfig) {
				setClaw402Strategy(c)
			},
		},
	}

	// GetDefaultStrategyConfig only supports zh/en; map id -> en
	configLang := lang
	if lang == "id" {
		configLang = "en"
	}

	// Pre-build all strategy objects before opening the transaction
	var strategies []*store.Strategy
	for _, def := range definitions {
		config := store.GetDefaultStrategyConfig(configLang)
		def.applyConfig(&config)
		config.ClampLimits()

		strategy := &store.Strategy{
			ID:          uuid.New().String(),
			UserID:      userID,
			Name:        def.name,
			Description: def.description,
			IsActive:    def.isActive,
			IsDefault:   false,
		}
		if err := strategy.SetConfig(&config); err != nil {
			return fmt.Errorf("failed to set config for strategy %q: %w", def.name, err)
		}
		strategies = append(strategies, strategy)
	}

	legacyDefaultNames := []string{
		"Balanced Strategy", "Steady Strategy", "Aggressive Strategy",
		"US Stock Trend Strategy", "US Stock Steady Strategy", "US Stock Breakout Strategy",
		"Balanced Strategy", "Conservative Strategy", "Aggressive Strategy",
		"US Stock Trend Strategy", "US Stock Steady Strategy", "US Stock Breakout Strategy",
		"Strategi Seimbang", "Strategi Konservatif", "Strategi Agresif",
		"Strategi Tren Saham AS", "Strategi Stabil Saham AS", "Strategi Breakout Saham AS",
	}

	return s.store.Transaction(func(tx *gorm.DB) error {
		// Remove obsolete built-in risk-profile presets for this user. If a trader still
		// references one of them, keep it to avoid breaking an existing running setup.
		deleteResult := tx.Where("user_id = ? AND name IN ? AND id NOT IN (SELECT strategy_id FROM traders WHERE user_id = ? AND strategy_id IS NOT NULL)", userID, legacyDefaultNames, userID).
			Delete(&store.Strategy{})
		if deleteResult.Error != nil {
			return fmt.Errorf("failed to remove legacy default strategies: %w", deleteResult.Error)
		}
		if deleteResult.RowsAffected > 0 {
			logger.Infof("  ✓ Removed %d legacy default strategy preset(s)", deleteResult.RowsAffected)
		}

		var activeCount int64
		if err := tx.Model(&store.Strategy{}).Where("user_id = ? AND is_active = ?", userID, true).Count(&activeCount).Error; err != nil {
			return fmt.Errorf("failed to count active strategies: %w", err)
		}

		for _, strategy := range strategies {
			var existing store.Strategy
			query := tx.Where("user_id = ? AND name = ?", userID, strategy.Name).First(&existing)
			if query.Error == nil {
				config, err := existing.ParseConfig()
				if err != nil {
					return fmt.Errorf("failed to parse existing strategy %q: %w", strategy.Name, err)
				}
				if store.MigrateLegacyAutopilotRiskDefaults(config) {
					config.ClampLimits()
					if err := existing.SetConfig(config); err != nil {
						return fmt.Errorf("failed to serialize migrated strategy %q: %w", strategy.Name, err)
					}
					if err := tx.Model(&store.Strategy{}).
						Where("id = ? AND user_id = ?", existing.ID, userID).
						Updates(map[string]interface{}{"config": existing.Config, "updated_at": time.Now().UTC()}).Error; err != nil {
						return fmt.Errorf("failed to migrate strategy %q: %w", strategy.Name, err)
					}
					logger.Infof("  ✓ Migrated default strategy to eight-position Autopilot: %s", strategy.Name)
				}
				continue
			}
			if query.Error != gorm.ErrRecordNotFound {
				return fmt.Errorf("failed to check strategy %q: %w", strategy.Name, query.Error)
			}
			if activeCount > 0 {
				strategy.IsActive = false
			}
			if err := tx.Create(strategy).Error; err != nil {
				return fmt.Errorf("failed to create strategy %q: %w", strategy.Name, err)
			}
			if strategy.IsActive {
				activeCount++
			}
			logger.Infof("  ✓ Created default strategy: %s (active=%v)", strategy.Name, strategy.IsActive)
		}
		return nil
	})
}
