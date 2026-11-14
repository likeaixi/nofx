package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"nofx/crypto"
	"nofx/store"
)

// Claw402 AI wallet onboarding was removed: the setup-wallet endpoint must
// stay mounted (so clients get a clear message, not a 404) but never create,
// expose, adopt or modify any wallet row.
func TestPlanningSetupWalletReturnsRemovalError(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "setup.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = db.AutoMigrate(&store.AIModel{}); err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(crypto.EnvRSAPrivateKey, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})))
	t.Setenv(crypto.EnvDataEncryptionKey, "setup-removal-test-storage-key")
	service, err := crypto.NewCryptoService()
	if err != nil {
		t.Fatal(err)
	}
	crypto.SetGlobalCryptoService(service)
	defer crypto.SetGlobalCryptoService(nil)

	legacy := &store.AIModel{ID: "legacy", UserID: "alice", Provider: "claw402", APIKey: crypto.EncryptedString("legacy-fixture-key")}
	if err = db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFromGorm(db)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st}

	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("user_id", "alice") })
	router.GET("/setup-wallet", server.handlePlanningSetupWallet)
	router.POST("/setup-wallet", server.handlePlanningSetupWallet)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/setup-wallet", nil))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "AI wallet onboarding removed; configure a custom model in Config") {
			t.Fatalf("%s setup-wallet = %d %s", method, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "legacy-fixture-key") || strings.Contains(w.Body.String(), "private_key") {
			t.Fatal("removal response leaked wallet material", w.Body.String())
		}
	}

	// Unauthenticated callers still get 401, not the removal message.
	w := httptest.NewRecorder()
	anon := &Server{store: st}
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/setup-wallet", nil)
	anon.handlePlanningSetupWallet(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code, w.Body.String())
	}

	// No wallet row is created, adopted or re-written by the endpoint.
	var count int64
	db.Model(&store.AIModel{}).Count(&count)
	if count != 1 {
		t.Fatal("setup-wallet modified ai_models rows", count)
	}
	var stored string
	db.Raw("SELECT api_key FROM ai_models WHERE id = ?", legacy.ID).Scan(&stored)
	if !strings.HasPrefix(stored, "ENC:") {
		t.Fatal("stored wallet key was re-encoded in plaintext")
	}
}
