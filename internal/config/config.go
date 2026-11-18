// Package config loads and saves VibeRouter's single source of truth: config.yaml.
// No database is used; all mutations made via the admin API are written back to
// this file and hot-reloaded by the router.
package config

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
)

// Protocol identifiers used across the codebase.
const (
	ProtocolChatCompletions = "chat_completions"
	ProtocolResponses       = "responses"
	ProtocolAnthropic       = "anthropic"
)

// Group modes.
const (
	ModeBalance = "balance" // priority failover group
	ModeSmart   = "smart"   // dispatcher-driven advanced/basic tiering
)

// Tier names.
const (
	TierAdvanced = "advanced"
	TierBasic    = "basic"
)

type ServerConfig struct {
	Addr string `yaml:"addr" json:"addr"`
	// DisableCaptcha turns off the login captcha (zero value false = captcha on).
	// Intended for trusted environments (tests, behind VPN/SSO).
	DisableCaptcha bool `yaml:"disable_captcha" json:"disable_captcha"`
}

type RoutingConfig struct {
	// LongContextThreshold: requests whose estimated input tokens exceed this
	// value are restricted to models with long_context=true.
	LongContextThreshold int `yaml:"long_context_threshold" json:"long_context_threshold"`
	// DispatcherTimeoutSec bounds the smart-mode complexity classification call.
	DispatcherTimeoutSec int `yaml:"dispatcher_timeout_sec" json:"dispatcher_timeout_sec"`
	// Strategy is the same-priority load balance strategy: round_robin | random.
	Strategy string `yaml:"strategy" json:"strategy"`
}

type WebToolProviderConfig struct {
	ID      string `yaml:"id" json:"id"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	APIKey  string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
}

type WebToolsConfig struct {
	Enabled        bool                    `yaml:"enabled" json:"enabled"`
	MCPEnabled     bool                    `yaml:"mcp_enabled" json:"mcp_enabled"`
	SearchProvider string                  `yaml:"search_provider" json:"search_provider"`
	FetchProvider  string                  `yaml:"fetch_provider" json:"fetch_provider"`
	TimeoutSec     int                     `yaml:"timeout_sec" json:"timeout_sec"`
	MaxCalls       int                     `yaml:"max_calls" json:"max_calls"`
	Providers      []WebToolProviderConfig `yaml:"providers" json:"providers"`
}

// BalanceConfig describes how to query a provider's remaining balance/quota.
// Type selects a built-in probe implementation (see internal/upstream/balance.go).
type BalanceConfig struct {
	Type string `yaml:"type" json:"type"`
	// URL overrides the built-in endpoint when non-empty.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
}

type Provider struct {
	ID       string        `yaml:"id" json:"id"`
	Name     string        `yaml:"name" json:"name"`
	Protocol string        `yaml:"protocol" json:"protocol"`
	BaseURL  string        `yaml:"base_url" json:"base_url"`
	APIKey   string        `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	PresetID string        `yaml:"preset_id,omitempty" json:"preset_id,omitempty"`
	Balance  BalanceConfig `yaml:"balance,omitempty" json:"balance,omitempty"`
}

type Model struct {
	Name         string `yaml:"name" json:"name"` // router-facing alias used by clients
	Provider     string `yaml:"provider" json:"provider"`
	UpstreamName string `yaml:"upstream_model" json:"upstream_model"`
	APIKey       string `yaml:"api_key,omitempty" json:"api_key,omitempty"`   // overrides provider key
	BaseURL      string `yaml:"base_url,omitempty" json:"base_url,omitempty"` // overrides provider URL
	Protocol     string `yaml:"protocol,omitempty" json:"protocol,omitempty"` // overrides provider protocol
	Multimodal   bool   `yaml:"multimodal" json:"multimodal"`
	LongContext  bool   `yaml:"long_context" json:"long_context"`
	MaxTokens    int    `yaml:"max_context_tokens" json:"max_context_tokens"`
}

// GroupMember binds a router-facing model name to a priority (lower = tried first)
// inside a group or tier.
type GroupMember struct {
	Model    string `yaml:"model" json:"model"`
	Priority int    `yaml:"priority" json:"priority"`
}

type Group struct {
	Name     string `yaml:"name" json:"name"` // router-facing alias
	Mode     string `yaml:"mode" json:"mode"` // balance | smart
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	// balance mode
	Members []GroupMember `yaml:"members,omitempty" json:"members,omitempty"`
	// smart mode
	DispatcherModel  string        `yaml:"dispatcher_model,omitempty" json:"dispatcher_model,omitempty"`
	DispatcherPrompt string        `yaml:"dispatcher_prompt,omitempty" json:"dispatcher_prompt,omitempty"` // overrides built-in default
	Advanced         []GroupMember `yaml:"advanced,omitempty" json:"advanced,omitempty"`
	Basic            []GroupMember `yaml:"basic,omitempty" json:"basic,omitempty"`
}

type APIKey struct {
	ID      string `yaml:"id" json:"id"`
	Key     string `yaml:"key" json:"key"`
	Name    string `yaml:"name" json:"name"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Created string `yaml:"created_at" json:"created_at"`
}

type LogConfig struct {
	File string `yaml:"file" json:"file"`
	// RetentionDays: how many days of daily log files to keep. <=0 means 7.
	RetentionDays int `yaml:"retention_days" json:"retention_days"`
}

// Config is pure data — the contents of config.yaml. All concurrent access
// goes through Store.
type Config struct {
	Server    ServerConfig   `yaml:"server" json:"server"`
	Routing   RoutingConfig  `yaml:"routing" json:"routing"`
	Providers []Provider     `yaml:"providers" json:"providers"`
	Models    []Model        `yaml:"models" json:"models"`
	Groups    []Group        `yaml:"groups" json:"groups"`
	Keys      []APIKey       `yaml:"keys" json:"keys"`
	Logs      LogConfig      `yaml:"logs" json:"logs"`
	WebTools  WebToolsConfig `yaml:"web_tools" json:"web_tools"`
}

// Store owns the Config, its file path, and the lock guarding mutations.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{Addr: ":8080"},
		Routing: RoutingConfig{
			LongContextThreshold: 200_000,
			DispatcherTimeoutSec: 5,
			Strategy:             "round_robin",
		},
		Logs: LogConfig{File: "logs/router.log", RetentionDays: 7},
		WebTools: WebToolsConfig{
			SearchProvider: "deepseek",
			FetchProvider:  "deepseek",
			TimeoutSec:     30,
			MaxCalls:       8,
			Providers: []WebToolProviderConfig{
				{ID: "bocha", BaseURL: "https://api.bocha.cn"},
				{ID: "kimi", BaseURL: "https://api.moonshot.cn"},
				{ID: "deepseek", BaseURL: "https://api.deepseek.com"},
				{ID: "glm", BaseURL: "https://open.bigmodel.cn"},
			},
		},
	}
}

// Load reads config.yaml from path, creating a default file when absent.
func Load(path string) (*Store, error) {
	s := &Store{path: path, cfg: defaultConfig()}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := s.Save(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &s.cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return s, nil
}

// Save persists the current config atomically (write tmp + rename).
func (s *Store) Save() error {
	s.mu.RLock()
	data, err := yaml.Marshal(&s.cfg)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot returns a deep copy safe to read without locks.
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out Config
	data, _ := yaml.Marshal(&s.cfg)
	_ = yaml.Unmarshal(data, &out)
	return out
}

// Mutate applies fn under write lock and persists the result.
func (s *Store) Mutate(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.cfg); err != nil {
		return err
	}
	data, err := yaml.Marshal(&s.cfg)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// --- lookups (operate on a snapshot or under caller's lock) ---

func (c *Config) FindProvider(id string) *Provider {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}

func (c *Config) FindModel(name string) *Model {
	for i := range c.Models {
		if c.Models[i].Name == name {
			return &c.Models[i]
		}
	}
	return nil
}

func (c *Config) FindGroup(name string) *Group {
	for i := range c.Groups {
		if c.Groups[i].Name == name {
			return &c.Groups[i]
		}
	}
	return nil
}

// ResolveModel fills model fields from its provider defaults and returns the
// effective upstream endpoint configuration.
type ResolvedModel struct {
	Model
	EffectiveProtocol string
	EffectiveBaseURL  string
	EffectiveAPIKey   string
}

func (c *Config) ResolveModel(m *Model) (*ResolvedModel, error) {
	r := &ResolvedModel{Model: *m}
	p := c.FindProvider(m.Provider)
	if p == nil {
		return nil, fmt.Errorf("provider %q not found for model %q", m.Provider, m.Name)
	}
	r.EffectiveProtocol = m.Protocol
	if r.EffectiveProtocol == "" {
		r.EffectiveProtocol = p.Protocol
	}
	r.EffectiveBaseURL = strings.TrimRight(m.BaseURL, "/")
	if r.EffectiveBaseURL == "" {
		r.EffectiveBaseURL = strings.TrimRight(p.BaseURL, "/")
	}
	r.EffectiveAPIKey = m.APIKey
	if r.EffectiveAPIKey == "" {
		r.EffectiveAPIKey = p.APIKey
	}
	if r.EffectiveProtocol == "" || r.EffectiveBaseURL == "" {
		return nil, fmt.Errorf("model %q: missing protocol or base_url", m.Name)
	}
	return r, nil
}
