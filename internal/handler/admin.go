package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
	"golang.org/x/crypto/bcrypt"

	"viberouter/internal/config"
	"viberouter/internal/middleware"
	"viberouter/internal/model"
	"viberouter/internal/service"
)

// AdminHandler serves the web management API (behind admin session auth).
type AdminHandler struct {
	callLogSvc *service.CallLogService
}

func NewAdminHandler(cls *service.CallLogService) *AdminHandler {
	return &AdminHandler{callLogSvc: cls}
}

// cloneConfig returns a deep copy so mutations don't race with live readers.
func cloneConfig(cfg *config.Config) *config.Config {
	data, _ := yaml.Marshal(cfg)
	out := &config.Config{}
	_ = yaml.Unmarshal(data, out)
	return out
}

// saveAndReload persists config and pushes it to the load balancer.
func (h *AdminHandler) saveAndReload(cfg *config.Config) error {
	if err := config.SaveConfig(cfg); err != nil {
		return err
	}
	if lb := service.GetLoadBalancer(); lb != nil {
		lb.Reload(cfg)
	}
	return nil
}

func fail(c *gin.Context, code int, msg string) {
	c.AbortWithStatusJSON(code, gin.H{"error": gin.H{"message": msg}})
}

// ---------- Auth ----------

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *AdminHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	cfg := config.GetConfig()
	for _, u := range cfg.Admin.Users {
		if u.Username == req.Username {
			if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
				fail(c, http.StatusUnauthorized, "invalid credentials")
				return
			}
			token := middleware.GenerateToken(u.Username)
			maxAge := cfg.Admin.Session.MaxAgeSec
			if maxAge <= 0 {
				maxAge = 86400
			}
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(middleware.SessionCookieName(), token, maxAge, "/", "", false, true)
			c.JSON(http.StatusOK, gin.H{"username": u.Username, "role": u.Role})
			return
		}
	}
	fail(c, http.StatusUnauthorized, "invalid credentials")
}

func (h *AdminHandler) Logout(c *gin.Context) {
	if token, err := c.Cookie(middleware.SessionCookieName()); err == nil {
		middleware.RevokeToken(token)
	}
	c.SetCookie(middleware.SessionCookieName(), "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) Me(c *gin.Context) {
	username, _ := middleware.CurrentAdmin(c)
	c.JSON(http.StatusOK, gin.H{"username": username})
}

// ---------- Config overview ----------

// GetConfig returns the full config for the UI (models/tiers, routing, keys).
func (h *AdminHandler) GetConfig(c *gin.Context) {
	cfg := config.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"tiers":   cfg.Tiers,
		"routing": cfg.Routing,
		"keys":    cfg.APIKeys,
		"load_balance": cfg.LoadBalance,
		"circuit_breaker": cfg.CircuitBreaker,
		"retry":   cfg.Retry,
		"log":     cfg.Log,
	})
}

// ---------- Models ----------

type modelRequest struct {
	Tier             string `json:"tier"`
	OldTier          string `json:"old_tier"`
	Name             string `json:"name"`
	OldName          string `json:"old_name"`
	Provider         string `json:"provider"`
	TechnicalName    string `json:"technical_name"`
	BaseURL          string `json:"base_url"`
	APIKey           string `json:"api_key"`
	Priority         int    `json:"priority"`
	LongContext      bool   `json:"long_context"`
	MaxContextTokens int    `json:"max_context_tokens"`
	Enabled          bool   `json:"enabled"`
}

func (r modelRequest) toModel() model.BackendModel {
	return model.BackendModel{
		Name:             r.Name,
		Provider:         model.Provider(r.Provider),
		TechnicalName:    r.TechnicalName,
		BaseURL:          r.BaseURL,
		APIKey:           r.APIKey,
		Priority:         r.Priority,
		LongContext:      r.LongContext,
		MaxContextTokens: r.MaxContextTokens,
		Enabled:          r.Enabled,
	}
}

// ListModels returns every model flattened with its tier.
func (h *AdminHandler) ListModels(c *gin.Context) {
	out := make([]gin.H, 0)
	for _, m := range service.GetLoadBalancer().GetAllModels() {
		out = append(out, gin.H{
			"tier":              string(m.Tier),
			"name":              m.Name,
			"provider":          string(m.Provider),
			"technical_name":    m.TechnicalName,
			"base_url":          m.BaseURL,
			"api_key":           m.APIKey,
			"priority":          m.Priority,
			"long_context":      m.LongContext,
			"max_context_tokens": m.MaxContextTokens,
			"enabled":           m.Enabled,
		})
	}
	c.JSON(http.StatusOK, gin.H{"models": out})
}

func (h *AdminHandler) CreateModel(c *gin.Context) {
	var req modelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Tier != string(model.TierAdvanced) && req.Tier != string(model.TierBasic) {
		fail(c, http.StatusBadRequest, "tier must be advanced or basic")
		return
	}
	if req.Name == "" {
		fail(c, http.StatusBadRequest, "name is required")
		return
	}
	cfg := cloneConfig(config.GetConfig())
	tier := cfg.Tiers[req.Tier]
	for _, m := range tier.Models {
		if m.Name == req.Name {
			fail(c, http.StatusConflict, "model name already exists in tier")
			return
		}
	}
	tier.Models = append(tier.Models, req.toModel())
	if cfg.Tiers == nil {
		cfg.Tiers = map[string]config.TierConfig{}
	}
	cfg.Tiers[req.Tier] = tier
	if err := h.saveAndReload(cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) UpdateModel(c *gin.Context) {
	var req modelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	oldTier := req.OldTier
	if oldTier == "" {
		oldTier = req.Tier
	}
	oldName := req.OldName
	if oldName == "" {
		oldName = req.Name
	}
	cfg := cloneConfig(config.GetConfig())
	if !removeModel(cfg, oldTier, oldName) {
		fail(c, http.StatusNotFound, "model not found")
		return
	}
	if req.Tier != string(model.TierAdvanced) && req.Tier != string(model.TierBasic) {
		fail(c, http.StatusBadRequest, "tier must be advanced or basic")
		return
	}
	tier := cfg.Tiers[req.Tier]
	tier.Models = append(tier.Models, req.toModel())
	if cfg.Tiers == nil {
		cfg.Tiers = map[string]config.TierConfig{}
	}
	cfg.Tiers[req.Tier] = tier
	if err := h.saveAndReload(cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) DeleteModel(c *gin.Context) {
	tier := c.Query("tier")
	name := c.Query("name")
	cfg := cloneConfig(config.GetConfig())
	if !removeModel(cfg, tier, name) {
		fail(c, http.StatusNotFound, "model not found")
		return
	}
	if err := h.saveAndReload(cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func removeModel(cfg *config.Config, tier, name string) bool {
	t, ok := cfg.Tiers[tier]
	if !ok {
		return false
	}
	for i, m := range t.Models {
		if m.Name == name {
			t.Models = append(t.Models[:i], t.Models[i+1:]...)
			cfg.Tiers[tier] = t
			return true
		}
	}
	return false
}

// ---------- Routing rules ----------

func (h *AdminHandler) GetRouting(c *gin.Context) {
	c.JSON(http.StatusOK, config.GetConfig().Routing)
}

func (h *AdminHandler) UpdateRouting(c *gin.Context) {
	var routing config.RoutingConfig
	if err := c.ShouldBindJSON(&routing); err != nil {
		fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	cfg := cloneConfig(config.GetConfig())
	cfg.Routing = routing
	if err := h.saveAndReload(cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- API keys ----------

type keyRequest struct {
	Key      string `json:"key"`
	OldKey   string `json:"old_key"`
	Username string `json:"username"`
	IsActive bool   `json:"is_active"`
}

func (h *AdminHandler) ListKeys(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"keys": config.GetConfig().APIKeys})
}

func (h *AdminHandler) UpsertKey(c *gin.Context) {
	var req keyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "invalid request")
		return
	}
	if req.Key == "" {
		fail(c, http.StatusBadRequest, "key is required")
		return
	}
	cfg := cloneConfig(config.GetConfig())
	// Remove old key if renaming.
	if req.OldKey != "" {
		for i, k := range cfg.APIKeys {
			if k.Key == req.OldKey {
				cfg.APIKeys = append(cfg.APIKeys[:i], cfg.APIKeys[i+1:]...)
				break
			}
		}
	}
	// Update if exists, else append.
	updated := false
	for i, k := range cfg.APIKeys {
		if k.Key == req.Key {
			cfg.APIKeys[i].Username = req.Username
			cfg.APIKeys[i].IsActive = req.IsActive
			updated = true
			break
		}
	}
	if !updated {
		cfg.APIKeys = append(cfg.APIKeys, config.APIKeyConfig{
			Key: req.Key, Username: req.Username, IsActive: req.IsActive,
		})
	}
	if err := config.SaveConfig(cfg); err != nil {
		fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *AdminHandler) DeleteKey(c *gin.Context) {
	key := c.Query("key")
	cfg := cloneConfig(config.GetConfig())
	for i, k := range cfg.APIKeys {
		if k.Key == key {
			cfg.APIKeys = append(cfg.APIKeys[:i], cfg.APIKeys[i+1:]...)
			if err := config.SaveConfig(cfg); err != nil {
				fail(c, http.StatusInternalServerError, err.Error())
				return
			}
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}
	}
	fail(c, http.StatusNotFound, "key not found")
}

// ---------- Logs ----------

func (h *AdminHandler) Logs(c *gin.Context) {
	filter := service.LogFilter{
		Username: c.Query("username"),
		Model:    c.Query("model"),
		Tier:     c.Query("tier"),
		ApiStyle: c.Query("api_style"),
		Status:   c.Query("status"),
	}
	c.JSON(http.StatusOK, gin.H{"logs": h.callLogSvc.Query(filter)})
}
