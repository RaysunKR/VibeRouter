// Package server wires the HTTP surface: the adaptive /v1 proxy endpoints,
// the admin JSON API, and the embedded admin SPA.
package server

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/viberouter/viberouter/internal/auth"
	"github.com/viberouter/viberouter/internal/captcha"
	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/mcp"
	"github.com/viberouter/viberouter/internal/router"
	"github.com/viberouter/viberouter/internal/upstream"
	"github.com/viberouter/viberouter/internal/webtools"
)

//go:embed all:web
var webFS embed.FS

type presetCatalog interface {
	List(ctx context.Context, force bool) ([]upstream.ProviderPreset, error)
}

type Server struct {
	cfg      *config.Store
	router   *router.Router
	auth     *auth.Manager
	log      *logger.FileLogger
	captcha  *captcha.Store
	presets  presetCatalog
	webTools *webtools.Service
}

func New(cfg *config.Store, rt *router.Router, am *auth.Manager, fl *logger.FileLogger) *Server {
	return &Server{
		cfg: cfg, router: rt, auth: am, log: fl, captcha: captcha.NewStore(),
		presets: upstream.NewPresetCatalog(), webTools: webtools.New(cfg),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// client-facing adaptive endpoints (API key auth)
	mux.HandleFunc("POST /v1/chat/completions", s.handleProxy)
	mux.HandleFunc("POST /v1/responses", s.handleProxy)
	mux.HandleFunc("POST /v1/messages", s.handleProxy)
	mux.HandleFunc("GET /v1/models", s.handleListModels)
	mux.Handle("/mcp", s.clientKey(mcp.NewHandler(s.cfg, s.webTools)))

	// admin API
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/captcha", s.handleCaptcha)
	mux.Handle("GET /api/config", s.admin(http.HandlerFunc(s.handleGetConfig)))
	mux.Handle("GET /api/presets", s.admin(http.HandlerFunc(s.handlePresets)))
	mux.Handle("POST /api/presets/refresh", s.admin(http.HandlerFunc(s.handlePresetsRefresh)))
	mux.Handle("POST /api/providers", s.admin(http.HandlerFunc(s.handleProviderCreate)))
	mux.Handle("PUT /api/providers/{id}", s.admin(http.HandlerFunc(s.handleProviderUpdate)))
	mux.Handle("POST /api/providers/{id}/sync-preset", s.admin(http.HandlerFunc(s.handleProviderSyncPreset)))
	mux.Handle("DELETE /api/providers/{id}", s.admin(http.HandlerFunc(s.handleProviderDelete)))
	mux.Handle("POST /api/providers/{id}/balance", s.admin(http.HandlerFunc(s.handleBalance)))
	mux.Handle("POST /api/models", s.admin(http.HandlerFunc(s.handleModelCreate)))
	mux.Handle("PUT /api/models/{name}", s.admin(http.HandlerFunc(s.handleModelUpdate)))
	mux.Handle("DELETE /api/models/{name}", s.admin(http.HandlerFunc(s.handleModelDelete)))
	mux.Handle("POST /api/groups", s.admin(http.HandlerFunc(s.handleGroupCreate)))
	mux.Handle("PUT /api/groups/{name}", s.admin(http.HandlerFunc(s.handleGroupUpdate)))
	mux.Handle("DELETE /api/groups/{name}", s.admin(http.HandlerFunc(s.handleGroupDelete)))
	mux.Handle("POST /api/keys", s.admin(http.HandlerFunc(s.handleKeyCreate)))
	mux.Handle("PUT /api/keys/{id}", s.admin(http.HandlerFunc(s.handleKeyUpdate)))
	mux.Handle("DELETE /api/keys/{id}", s.admin(http.HandlerFunc(s.handleKeyDelete)))
	mux.Handle("GET /api/logs", s.admin(http.HandlerFunc(s.handleLogs)))
	mux.Handle("PUT /api/settings", s.admin(http.HandlerFunc(s.handleSettingsUpdate)))
	mux.Handle("GET /api/dispatcher/default-prompt", s.admin(http.HandlerFunc(s.handleDefaultPrompt)))

	// embedded SPA with fallback to index.html
	dist, err := fs.Sub(webFS, "web")
	if err == nil {
		mux.Handle("/", spaHandler(http.FS(dist)))
	}
	return mux
}

// admin wraps a handler with admin-token auth.
func (s *Server) admin(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if _, err := s.auth.Validate(token); err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) clientKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.authorizeKey(extractKey(r)); !ok {
			writeError(w, http.StatusUnauthorized, "invalid api key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorizeKey validates a client API key against config; returns key name.
func (s *Server) authorizeKey(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	snap := s.cfg.Snapshot()
	for _, k := range snap.Keys {
		if k.Enabled && k.Key == key {
			return k.Name, true
		}
	}
	return "", false
}

func (s *Server) handleCaptcha(w http.ResponseWriter, r *http.Request) {
	id, _, img, err := s.captcha.Generate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{
		"captcha_id": id,
		"image":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(img),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		CaptchaID   string `json:"captcha_id"`
		CaptchaCode string `json:"captcha_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Login failure messages follow the client's Accept-Language (browsers send it automatically).
	zh := strings.Contains(strings.ToLower(r.Header.Get("Accept-Language")), "zh")
	captchaMsg, credMsg := "Captcha is invalid or expired", "Invalid username or password"
	if zh {
		captchaMsg, credMsg = "验证码错误或已过期", "用户名或密码错误"
	}
	if !s.cfg.Snapshot().Server.DisableCaptcha && !s.captcha.Verify(req.CaptchaID, req.CaptchaCode) {
		writeError(w, http.StatusBadRequest, captchaMsg)
		return
	}
	if !s.auth.Verify(req.Username, req.Password) {
		writeError(w, http.StatusUnauthorized, credMsg)
		return
	}
	token, err := s.auth.Issue(req.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// spaHandler serves static files, falling back to index.html for client routes.
func spaHandler(root http.FileSystem) http.Handler {
	file := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := root.Open(p); err == nil {
			_ = f.Close()
			file.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		file.ServeHTTP(w, r)
	})
}
