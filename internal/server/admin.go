package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/upstream"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	relayRaw(w, status, "application/json", data)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return false
	}
	return true
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Snapshot())
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	presets, err := s.presets.List(r.Context(), false)
	if err != nil {
		log.Printf("models.dev preset sync failed, using fallback: %v", err)
	}
	writeJSON(w, http.StatusOK, presets)
}

func (s *Server) handlePresetsRefresh(w http.ResponseWriter, r *http.Request) {
	presets, err := s.presets.List(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusBadGateway, "models.dev sync failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, presets)
}

func (s *Server) handleDefaultPrompt(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"prompt": upstream.DefaultDispatcherPrompt})
}

// --- providers ---

func providerCredentialsConflict(c *config.Config, p config.Provider, excludeID string) bool {
	baseURL := strings.TrimRight(p.BaseURL, "/")
	for _, existing := range c.Providers {
		if existing.ID == excludeID {
			continue
		}
		if existing.Protocol == p.Protocol &&
			strings.TrimRight(existing.BaseURL, "/") == baseURL &&
			existing.APIKey == p.APIKey {
			return true
		}
	}
	return false
}

func (s *Server) handleProviderCreate(w http.ResponseWriter, r *http.Request) {
	var p config.Provider
	if !decodeJSON(w, r, &p) {
		return
	}
	if p.ID == "" || p.BaseURL == "" {
		writeError(w, http.StatusBadRequest, "id and base_url are required")
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		if c.FindProvider(p.ID) != nil {
			return errConflict("provider id already exists")
		}
		if providerCredentialsConflict(c, p, "") {
			return errConflict("provider credentials already exist")
		}
		c.Providers = append(c.Providers, p)
		return nil
	})
	respondMutate(w, err)
}

func (s *Server) handleProviderUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		config.Provider
		PresetID *string `json:"preset_id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p := req.Provider
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				p.ID = id
				p.PresetID = c.Providers[i].PresetID
				if req.PresetID != nil {
					p.PresetID = *req.PresetID
				}
				if providerCredentialsConflict(c, p, id) {
					return errConflict("provider credentials already exist")
				}
				c.Providers[i] = p
				return nil
			}
		}
		return errNotFound("provider not found")
	})
	respondMutate(w, err)
}

func (s *Server) handleProviderSyncPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snapshot := s.cfg.Snapshot()
	provider := snapshot.FindProvider(id)
	if provider == nil {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	presetID := provider.PresetID
	if presetID == "" {
		writeError(w, http.StatusBadRequest, "provider is not linked to a preset")
		return
	}
	presets, err := s.presets.List(r.Context(), true)
	if err != nil {
		writeError(w, http.StatusBadGateway, "models.dev sync failed: "+err.Error())
		return
	}
	var preset *upstream.ProviderPreset
	for i := range presets {
		if presets[i].ID == presetID {
			preset = &presets[i]
			break
		}
	}
	if preset == nil {
		writeError(w, http.StatusNotFound, "linked preset not found")
		return
	}

	var updatedProvider config.Provider
	var skipped []string
	updatedModels := 0
	err = s.cfg.Mutate(func(c *config.Config) error {
		current := c.FindProvider(id)
		if current == nil {
			return errNotFound("provider not found")
		}
		if current.PresetID != presetID {
			return errConflict("provider preset binding changed during sync")
		}
		updated := *current
		updated.Protocol = preset.Protocol
		updated.BaseURL = preset.BaseURL
		if providerCredentialsConflict(c, updated, id) {
			return errConflict("provider credentials already exist")
		}

		updatedProvider = updated
		*current = updated
		for i := range c.Models {
			model := &c.Models[i]
			if model.Provider != id {
				continue
			}
			metadata, ok := preset.Models[model.UpstreamName]
			if !ok || metadata.Multimodal == nil && metadata.MaxContextTokens == nil {
				skipped = append(skipped, model.Name)
				continue
			}
			if metadata.Multimodal != nil {
				model.Multimodal = *metadata.Multimodal
			}
			if metadata.MaxContextTokens != nil {
				model.MaxTokens = *metadata.MaxContextTokens
				model.LongContext = model.MaxTokens > c.Routing.LongContextThreshold
			}
			updatedModels++
		}
		return nil
	})
	if err != nil {
		respondMutate(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":       updatedProvider,
		"models_updated": updatedModels,
		"models_skipped": skipped,
	})
}

func (s *Server) handleProviderDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.cfg.Mutate(func(c *config.Config) error {
		for _, m := range c.Models {
			if m.Provider == id {
				return errConflict("provider still referenced by model " + m.Name)
			}
		}
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
				return nil
			}
		}
		return errNotFound("provider not found")
	})
	respondMutate(w, err)
}

func (s *Server) handleBalance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap := s.cfg.Snapshot()
	p := snap.FindProvider(id)
	if p == nil {
		writeError(w, http.StatusNotFound, "provider not found")
		return
	}
	writeJSON(w, http.StatusOK,
		upstream.QueryBalance(upstream.WithClientLang(r.Context(), r.Header.Get("Accept-Language")), p))
}

// --- models ---

func (s *Server) handleModelCreate(w http.ResponseWriter, r *http.Request) {
	var m config.Model
	if !decodeJSON(w, r, &m) {
		return
	}
	if m.Name == "" || m.Provider == "" || m.UpstreamName == "" {
		writeError(w, http.StatusBadRequest, "name, provider and upstream_model are required")
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		if c.FindModel(m.Name) != nil || c.FindGroup(m.Name) != nil {
			return errConflict("name already in use")
		}
		if c.FindProvider(m.Provider) == nil {
			return errNotFound("provider not found")
		}
		c.Models = append(c.Models, m)
		return nil
	})
	respondMutate(w, err)
}

func (s *Server) handleModelUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var m config.Model
	if !decodeJSON(w, r, &m) {
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Models {
			if c.Models[i].Name == name {
				m.Name = name
				c.Models[i] = m
				return nil
			}
		}
		return errNotFound("model not found")
	})
	respondMutate(w, err)
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.cfg.Mutate(func(c *config.Config) error {
		for _, g := range c.Groups {
			for _, mem := range g.Members {
				if mem.Model == name {
					return errConflict("model still referenced by group " + g.Name)
				}
			}
			for _, mem := range g.Advanced {
				if mem.Model == name {
					return errConflict("model still referenced by group " + g.Name)
				}
			}
			for _, mem := range g.Basic {
				if mem.Model == name {
					return errConflict("model still referenced by group " + g.Name)
				}
			}
			if g.DispatcherModel == name {
				return errConflict("model is dispatcher of group " + g.Name)
			}
		}
		for i := range c.Models {
			if c.Models[i].Name == name {
				c.Models = append(c.Models[:i], c.Models[i+1:]...)
				return nil
			}
		}
		return errNotFound("model not found")
	})
	respondMutate(w, err)
}

// --- groups ---

func (s *Server) handleGroupCreate(w http.ResponseWriter, r *http.Request) {
	var g config.Group
	if !decodeJSON(w, r, &g) {
		return
	}
	if g.Name == "" || (g.Mode != config.ModeBalance && g.Mode != config.ModeSmart) {
		writeError(w, http.StatusBadRequest, "name and valid mode (balance|smart) are required")
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		if c.FindGroup(g.Name) != nil || c.FindModel(g.Name) != nil {
			return errConflict("name already in use")
		}
		c.Groups = append(c.Groups, g)
		return nil
	})
	respondMutate(w, err)
}

func (s *Server) handleGroupUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var g config.Group
	if !decodeJSON(w, r, &g) {
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Groups {
			if c.Groups[i].Name == name {
				g.Name = name
				c.Groups[i] = g
				return nil
			}
		}
		return errNotFound("group not found")
	})
	respondMutate(w, err)
}

func (s *Server) handleGroupDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Groups {
			if c.Groups[i].Name == name {
				c.Groups = append(c.Groups[:i], c.Groups[i+1:]...)
				return nil
			}
		}
		return errNotFound("group not found")
	})
	respondMutate(w, err)
}

// --- keys ---

func (s *Server) handleKeyCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // name optional
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	k := config.APIKey{
		ID:      hex.EncodeToString(buf[:4]),
		Key:     "vr-" + hex.EncodeToString(buf),
		Name:    req.Name,
		Enabled: true,
		Created: time.Now().Format(time.RFC3339),
	}
	if k.Name == "" {
		k.Name = "key-" + k.ID
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		c.Keys = append(c.Keys, k)
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) handleKeyUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Keys {
			if c.Keys[i].ID == id {
				c.Keys[i].Name = req.Name
				c.Keys[i].Enabled = req.Enabled
				return nil
			}
		}
		return errNotFound("key not found")
	})
	respondMutate(w, err)
}

func (s *Server) handleKeyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := s.cfg.Mutate(func(c *config.Config) error {
		for i := range c.Keys {
			if c.Keys[i].ID == id {
				c.Keys = append(c.Keys[:i], c.Keys[i+1:]...)
				return nil
			}
		}
		return errNotFound("key not found")
	})
	respondMutate(w, err)
}

// --- logs ---

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := logger.QueryFilter{
		Model: r.URL.Query().Get("model"),
		Key:   r.URL.Query().Get("key"),
		Since: r.URL.Query().Get("since"),
	}
	q.Limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	q.Status, _ = strconv.Atoi(r.URL.Query().Get("status"))
	entries, err := s.log.Query(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleSettingsUpdate applies global routing, logging, and web tool settings.
func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Routing  *config.RoutingConfig  `json:"routing"`
		Logs     *config.LogConfig      `json:"logs"`
		WebTools *config.WebToolsConfig `json:"web_tools"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	var retention *int
	err := s.cfg.Mutate(func(c *config.Config) error {
		if req.Routing != nil {
			c.Routing = *req.Routing
		}
		if req.WebTools != nil {
			c.WebTools = *req.WebTools
		}
		if req.Logs != nil {
			if req.Logs.File == "" {
				req.Logs.File = c.Logs.File
			}
			c.Logs = *req.Logs
			d := req.Logs.RetentionDays
			retention = &d
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Logs != nil {
		if err := s.log.SetBasePath(req.Logs.File); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if retention != nil {
		s.log.SetRetentionDays(*retention)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- mutation plumbing ---

type httpError struct {
	msg    string
	status int
}

func (e *httpError) Error() string { return e.msg }

func errConflict(msg string) error { return &httpError{msg, http.StatusConflict} }
func errNotFound(msg string) error { return &httpError{msg, http.StatusNotFound} }

func respondMutate(w http.ResponseWriter, err error) {
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if he, ok := err.(*httpError); ok {
		writeError(w, he.status, he.msg)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// handleListModels serves GET /v1/models for SDK discoverability.
func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authorizeKey(extractKey(r)); !ok {
		writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	snap := s.cfg.Snapshot()
	var data []any
	for _, m := range snap.Models {
		data = append(data, map[string]any{"id": m.Name, "object": "model", "created": 0, "owned_by": m.Provider})
	}
	for _, g := range snap.Groups {
		data = append(data, map[string]any{"id": g.Name, "object": "model", "created": 0, "owned_by": "viberouter-" + g.Mode})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}
