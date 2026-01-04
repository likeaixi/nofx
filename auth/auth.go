package auth

import (
	"crypto/rand"
	"fmt"
	"github.com/gin-gonic/gin"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// JWTSecret JWT密钥，将从配置中动态设置
var JWTSecret []byte

// tokenBlacklist 用于登出后的token黑名单（仅内存，按过期时间清理）
var tokenBlacklist = struct {
	sync.RWMutex
	items map[string]time.Time
}{items: make(map[string]time.Time)}

// maxBlacklistEntries 黑名单最大容量阈值
const maxBlacklistEntries = 100_000

// OTPIssuer OTP发行者名称
const OTPIssuer = "nofxAI"

// SetJWTSecret 设置JWT密钥
func SetJWTSecret(secret string) {
	JWTSecret = []byte(secret)
}

// BlacklistToken 将token加入黑名单直到过期
func BlacklistToken(token string, exp time.Time) {
	tokenBlacklist.Lock()
	defer tokenBlacklist.Unlock()
	tokenBlacklist.items[token] = exp

	// 如果超过容量阈值，则进行一次过期清理；若仍超限，记录警告日志
	if len(tokenBlacklist.items) > maxBlacklistEntries {
		now := time.Now()
		for t, e := range tokenBlacklist.items {
			if now.After(e) {
				delete(tokenBlacklist.items, t)
			}
		}
		if len(tokenBlacklist.items) > maxBlacklistEntries {
			log.Printf("auth: token blacklist size (%d) exceeds limit (%d) after sweep; consider reducing JWT TTL or using a shared persistent store",
				len(tokenBlacklist.items), maxBlacklistEntries)
		}
	}
}

// IsTokenBlacklisted 检查token是否在黑名单中（过期自动清理）
func IsTokenBlacklisted(token string) bool {
	tokenBlacklist.Lock()
	defer tokenBlacklist.Unlock()
	if exp, ok := tokenBlacklist.items[token]; ok {
		if time.Now().After(exp) {
			delete(tokenBlacklist.items, token)
			return false
		}
		return true
	}
	return false
}

// Claims JWT声明
type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	jwt.RegisteredClaims
}

// HashPassword 哈希密码
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword 验证密码
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateOTPSecret 生成OTP密钥
func GenerateOTPSecret() (string, error) {
	secret := make([]byte, 20)
	_, err := rand.Read(secret)
	if err != nil {
		return "", err
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      OTPIssuer,
		AccountName: uuid.New().String(),
	})
	if err != nil {
		return "", err
	}

	return key.Secret(), nil
}

// VerifyOTP 验证OTP码
func VerifyOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

// GenerateJWT 生成JWT token
func GenerateJWT(userID, email string) (string, error) {
	claims := Claims{
		UserID: userID,
		Email:  email,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)), // 24小时过期
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    "nofxAI",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

// ValidateJWT 验证JWT token
func ValidateJWT(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("意外的签名方法: %v", token.Header["alg"])
		}
		return JWTSecret, nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("无效的token")
}

// GetOTPQRCodeURL 获取OTP二维码URL
func GetOTPQRCodeURL(secret, email string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s", OTPIssuer, email, secret, OTPIssuer)
}

func IPWhitelistMiddleware(whitelist []string) gin.HandlerFunc {
	allowedIP := map[string]struct{}{}
	var allowedCIDR []*net.IPNet

	for _, item := range whitelist {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			_, ipnet, err := net.ParseCIDR(item)
			if err == nil && ipnet != nil {
				allowedCIDR = append(allowedCIDR, ipnet)
			}
			continue
		}
		ip := net.ParseIP(item)
		if ip != nil {
			allowedIP[ip.String()] = struct{}{}
		}
	}

	return func(c *gin.Context) {
		ipStr := c.ClientIP() // 依赖 TrustedProxies，见下方 Gin 初始化
		ip := net.ParseIP(ipStr)
		if ip == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid client ip"})
			return
		}

		if _, ok := allowedIP[ip.String()]; ok {
			c.Next()
			return
		}
		for _, ipnet := range allowedCIDR {
			if ipnet.Contains(ip) {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ip not allowed", "client_ip": ipStr})
	}
}
