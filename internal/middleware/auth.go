package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"viberouter/internal/config"
)

const sessionCookie = "viberouter_session"

var (
	tokenStore = sync.Map{}
	tokenTTL   = 24 * time.Hour
)

type tokenInfo struct {
	username string
	expiry   time.Time
}

// ValidateAPIKey validates a client API key and returns its username.
func ValidateAPIKey(apiKey string) (string, bool) {
	cfg := config.GetConfig()
	if cfg == nil {
		return "", false
	}
	for _, k := range cfg.APIKeys {
		if k.Key == apiKey {
			return k.Username, k.IsActive
		}
	}
	return "", false
}

// GenerateToken creates a new admin session token.
func GenerateToken(username string) string {
	token := randomHex(32)
	ttl := tokenTTL
	if cfg := config.GetConfig(); cfg != nil && cfg.Admin.Session.MaxAgeSec > 0 {
		ttl = time.Duration(cfg.Admin.Session.MaxAgeSec) * time.Second
	}
	tokenStore.Store(token, &tokenInfo{username: username, expiry: time.Now().Add(ttl)})
	return token
}

func validateToken(token string) (string, bool) {
	info, ok := tokenStore.Load(token)
	if !ok {
		return "", false
	}
	ti := info.(*tokenInfo)
	if time.Now().After(ti.expiry) {
		tokenStore.Delete(token)
		return "", false
	}
	return ti.username, true
}

// RevokeToken invalidates a session token (logout).
func RevokeToken(token string) {
	if token != "" {
		tokenStore.Delete(token)
	}
}

// AuthMiddleware validates client API keys for /v1/* proxy endpoints.
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/v1/") {
			c.Next()
			return
		}
		// Model listing is public.
		if path == "/v1/models" || path == "/v1/models/" || strings.HasPrefix(path, "/v1/models/") {
			c.Next()
			return
		}

		apiKey := extractAPIKey(c)
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "missing API key", "type": "authentication_error"},
			})
			return
		}
		username, ok := ValidateAPIKey(apiKey)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "invalid API key", "type": "authentication_error"},
			})
			return
		}
		c.Set("api_key", apiKey)
		c.Set("username", username)
		c.Next()
	}
}

// AdminAuthMiddleware validates an admin session cookie for /admin/* and /auth/*.
func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, _ := c.Cookie(sessionCookie)
		username, ok := validateToken(token)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
			return
		}
		c.Set("admin_username", username)
		c.Next()
	}
}

// CurrentAdmin returns the authenticated admin username, if any.
func CurrentAdmin(c *gin.Context) (string, bool) {
	v, ok := c.Get("admin_username")
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, s != ""
}

// SessionCookieName exposes the cookie name used for admin sessions.
func SessionCookieName() string { return sessionCookie }

// RequestIDMiddleware adds a unique request ID.
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-Id")
		if requestID == "" {
			requestID = "req_" + randomHex(8)
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-Id", requestID)
		c.Next()
	}
}

// CORSMiddleware handles CORS.
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, openai-version, X-VibeRouter-Tier")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id, anthropic-rid")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func extractAPIKey(c *gin.Context) string {
	if x := c.GetHeader("x-api-key"); x != "" {
		return x
	}
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// randomHex returns n bytes of crypto-random data as a hex string (2n chars).
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Extremely unlikely; fall back to time-based uniqueness.
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405000000000")))[:n*2]
	}
	return hex.EncodeToString(b)
}
