// Package auth implements admin login backed by .env credentials (no database)
// and self-contained HMAC-SHA256 session tokens.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Manager struct {
	Username     string
	PasswordHash string
	secret       []byte
}

type tokenPayload struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

// LoadEnv reads admin credentials from a .env file. On first run (missing keys)
// it seeds ADMIN_USERNAME=admin, a bcrypt hash of "admin", and a random session
// secret, returning seeded=true so the caller can warn the user.
func LoadEnv(path string) (*Manager, bool, error) {
	vals := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if ok {
				vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}

	seeded := false
	var b strings.Builder
	if vals["ADMIN_USERNAME"] == "" {
		vals["ADMIN_USERNAME"] = "admin"
		_, _ = fmt.Fprintf(&b, "ADMIN_USERNAME=admin\n")
		seeded = true
	}
	if vals["ADMIN_PASSWORD_HASH"] == "" {
		hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		if err != nil {
			return nil, false, err
		}
		vals["ADMIN_PASSWORD_HASH"] = string(hash)
		_, _ = fmt.Fprintf(&b, "ADMIN_PASSWORD_HASH=%s\n", hash)
		seeded = true
	}
	if vals["ADMIN_SESSION_SECRET"] == "" {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, false, err
		}
		vals["ADMIN_SESSION_SECRET"] = hex.EncodeToString(buf)
		_, _ = fmt.Fprintf(&b, "ADMIN_SESSION_SECRET=%s\n", hex.EncodeToString(buf))
		seeded = true
	}
	if seeded {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, false, err
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(b.String()); err != nil {
			return nil, false, err
		}
	}

	secret, err := hex.DecodeString(vals["ADMIN_SESSION_SECRET"])
	if err != nil {
		return nil, false, fmt.Errorf("invalid ADMIN_SESSION_SECRET: %w", err)
	}
	return &Manager{
		Username:     vals["ADMIN_USERNAME"],
		PasswordHash: vals["ADMIN_PASSWORD_HASH"],
		secret:       secret,
	}, seeded, nil
}

func (m *Manager) Verify(username, password string) bool {
	if username != m.Username {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(m.PasswordHash), []byte(password)) == nil
}

// Issue creates a signed token valid for 24h.
func (m *Manager) Issue(username string) (string, error) {
	p := tokenPayload{Sub: username, Exp: time.Now().Add(24 * time.Hour).Unix()}
	body, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	bodyEnc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(bodyEnc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return bodyEnc + "." + sig, nil
}

// Validate parses and verifies a token, returning the username.
func (m *Manager) Validate(token string) (string, error) {
	bodyEnc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(bodyEnc))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(expected, got) {
		return "", fmt.Errorf("invalid signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyEnc)
	if err != nil {
		return "", err
	}
	var p tokenPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return "", err
	}
	if time.Now().Unix() > p.Exp {
		return "", fmt.Errorf("token expired")
	}
	return p.Sub, nil
}
