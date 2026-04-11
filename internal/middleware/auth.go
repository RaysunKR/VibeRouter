package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"viberouter/internal/config"
)

var (
	tokenStore = sync.Map{}
	tokenTTL   = 24 * time.Hour
)

type tokenInfo struct {
	username string
	expiry   time.Time
}

// ValidateAPIKey validates an API key and returns the associated username
func ValidateAPIKey(apiKey string) (string, bool) {
	cfg := config.GetConfig()
	if cfg == nil {
		return "", false
	}
	for _, k := range cfg.APIKeys {
		if k.Key == apiKey && k.IsActive {
			return k.Username, true
		}
	}
	return "", false
}

// GenerateToken creates a new session token
func GenerateToken(username string) string {
	token := randomString(32)
	tokenStore.Store(token, &tokenInfo{
		username: username,
		expiry:   time.Now().Add(tokenTTL),
	})
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

// AuthMiddleware validates API keys for model routing endpoints
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/v1/") {
			c.Next()
			return
		}

		// Skip auth for model listing endpoints
	if path == "/v1/models" || path == "/v1/models/" || strings.HasPrefix(path, "/v1/models/") {
		c.Next()
		return
	}

		apiKey := extractAPIKey(c)
		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "missing API key",
					"type":    "authentication_error",
				},
			})
			return
		}

		username, ok := ValidateAPIKey(apiKey)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"message": "invalid API key",
					"type":    "authentication_error",
				},
			})
			return
		}

		c.Set("api_key", apiKey)
		c.Set("username", username) // Store username for logging
		c.Next()
	}
}

// RequestIDMiddleware adds a unique request ID
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-Id")
		if requestID == "" {
			requestID = generateRequestID()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-Id", requestID)
		c.Next()
	}
}

// CORSMiddleware handles CORS
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key, anthropic-version, openai-version")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id, anthropic-rid")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func extractAPIKey(c *gin.Context) string {
	xApiKey := c.GetHeader("x-api-key")
	if xApiKey != "" {
		return xApiKey
	}
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func generateRequestID() string {
	return "req_" + randomString(16)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[i%len(letters)]
	}
	return string(b)
}
