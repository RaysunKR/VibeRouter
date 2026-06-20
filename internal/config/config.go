package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/goccy/go-yaml"
	"golang.org/x/crypto/bcrypt"
	"viberouter/internal/model"
)

// Config is the full application configuration, loaded from / saved to config.yaml.
type Config struct {
	Server         ServerConfig          `yaml:"server"`
	Log            LogConfig             `yaml:"log"`
	CircuitBreaker CircuitBreakerConfig  `yaml:"circuit_breaker"`
	Retry          RetryConfig           `yaml:"retry"`
	LoadBalance    LoadBalanceConfig     `yaml:"load_balance"`
	Routing        RoutingConfig         `yaml:"routing"`
	Tiers          map[string]TierConfig `yaml:"tiers"`
	Admin          AdminConfig           `yaml:"admin"`
	APIKeys        []APIKeyConfig        `yaml:"api_keys"`

	// Legacy flat list. If Tiers is empty on load, these are migrated into
	// the "basic" tier so existing config.yaml files keep working.
	BackendModels []LegacyBackendModel `yaml:"backend_models,omitempty"`
}

// TierConfig holds the models belonging to one tier (advanced/basic).
type TierConfig struct {
	Models []model.BackendModel `yaml:"models"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
	Mode    string `yaml:"mode"`
}

type LogConfig struct {
	RetentionDays int    `yaml:"retention_days"`
	File          string `yaml:"file"`
}

type CircuitBreakerConfig struct {
	Threshold  int `yaml:"threshold"`
	TimeoutSec int `yaml:"timeout_sec"`
}

type RetryConfig struct {
	MaxAttempts int `yaml:"max_attempts"`
	TimeoutMs   int `yaml:"timeout_ms"`
}

type LoadBalanceConfig struct {
	Strategy string `yaml:"strategy"` // round_robin | weighted_round_robin | random (same-priority tie-break)
}

// RoutingConfig controls tier selection and long-context filtering.
type RoutingConfig struct {
	Complexity          ComplexityConfig `yaml:"complexity"`
	LongContextThreshold int             `yaml:"long_context_threshold"`
	Override            OverrideConfig   `yaml:"override"`
}

type ComplexityConfig struct {
	DefaultTier string           `yaml:"default_tier"` // basic | advanced
	Rules       []ComplexityRule `yaml:"rules"`
}

// ComplexityRule is one heuristic: if field op value, the task is "complex".
type ComplexityRule struct {
	Field string `yaml:"field"` // message_turns | est_input_tokens | prompt_length | has_tools | has_code
	Op    string `yaml:"op"`    // gte | gt | eq
	Value int    `yaml:"value"`
}

type OverrideConfig struct {
	ModelAlias map[string]string `yaml:"model_alias"` // e.g. auto-advanced -> advanced
	Header     string            `yaml:"header"`      // e.g. X-VibeRouter-Tier
}

type AdminConfig struct {
	Session SessionConfig     `yaml:"session"`
	Users   []AdminUserConfig `yaml:"users"`
}

type SessionConfig struct {
	Secret    string `yaml:"secret"`
	MaxAgeSec int    `yaml:"max_age_sec"`
}

type AdminUserConfig struct {
	Username     string `json:"username" yaml:"username"`
	PasswordHash string `json:"password_hash" yaml:"password_hash"`
	Role         string `json:"role" yaml:"role"`
}

type APIKeyConfig struct {
	Key      string `json:"key" yaml:"key"`
	Username string `json:"username" yaml:"username"`
	IsActive bool   `json:"is_active" yaml:"is_active"`
}

// LegacyBackendModel is the old flat config entry, migrated into tiers on load.
type LegacyBackendModel struct {
	Provider      string `yaml:"provider"`
	DisplayName   string `yaml:"display_name"`
	TechnicalName string `yaml:"technical_name"`
	BaseURL       string `yaml:"base_url"`
	APIKey        string `yaml:"api_key"`
	IsActive      bool   `yaml:"is_active"`
}

var (
	configMu      sync.RWMutex
	configFilePath = ""
	globalConfig   *Config
)

// GetConfig returns the current config snapshot. The returned pointer is never
// mutated in place; Save() swaps in a new snapshot, so callers may hold it.
func GetConfig() *Config {
	configMu.RLock()
	defer configMu.RUnlock()
	return globalConfig
}

func getExecutableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// Load reads config.yaml (creating defaults if absent), applies defaults,
// migrates any legacy flat backend_models into tiers, and stores it globally.
func Load(configPath string) (*Config, error) {
	if configPath != "" {
		configFilePath = configPath
	} else {
		configFilePath = filepath.Join(getExecutableDir(), "config.yaml")
	}

	cfg := &Config{}
	if data, err := os.ReadFile(configFilePath); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			log.Printf("[CONFIG] Warning: failed to parse %s: %v", configFilePath, err)
		} else {
			log.Printf("[CONFIG] Loaded config from: %s", configFilePath)
		}
	} else {
		log.Printf("[CONFIG] No config file at %s, using defaults", configFilePath)
	}

	applyDefaults(cfg)
	migrateLegacy(cfg)

	configMu.Lock()
	globalConfig = cfg
	configMu.Unlock()
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Address == "" {
		cfg.Server.Address = ":8080"
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "release"
	}
	if cfg.Log.RetentionDays == 0 {
		cfg.Log.RetentionDays = 30
	}
	if cfg.Log.File == "" {
		cfg.Log.File = "./logs/viberouter.jsonl"
	}
	if cfg.CircuitBreaker.Threshold == 0 {
		cfg.CircuitBreaker.Threshold = 5
	}
	if cfg.CircuitBreaker.TimeoutSec == 0 {
		cfg.CircuitBreaker.TimeoutSec = 30
	}
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 2
	}
	if cfg.Retry.TimeoutMs == 0 {
		cfg.Retry.TimeoutMs = 30000
	}
	if cfg.LoadBalance.Strategy == "" {
		cfg.LoadBalance.Strategy = "round_robin"
	}
	if cfg.Routing.LongContextThreshold == 0 {
		cfg.Routing.LongContextThreshold = 32000
	}
	if cfg.Routing.Complexity.DefaultTier == "" {
		cfg.Routing.Complexity.DefaultTier = string(model.TierBasic)
	}
	if len(cfg.Routing.Complexity.Rules) == 0 {
		cfg.Routing.Complexity.Rules = []ComplexityRule{
			{Field: "message_turns", Op: "gte", Value: 8},
			{Field: "est_input_tokens", Op: "gte", Value: 8000},
			{Field: "has_tools", Op: "eq", Value: 1},
			{Field: "has_code", Op: "eq", Value: 1},
		}
	}
	if cfg.Routing.Override.Header == "" {
		cfg.Routing.Override.Header = "X-VibeRouter-Tier"
	}
	if cfg.Routing.Override.ModelAlias == nil {
		cfg.Routing.Override.ModelAlias = map[string]string{
			"auto-advanced": string(model.TierAdvanced),
			"auto-basic":    string(model.TierBasic),
		}
	}
	if cfg.Admin.Session.MaxAgeSec == 0 {
		cfg.Admin.Session.MaxAgeSec = 86400
	}
}

// migrateLegacy folds a legacy flat backend_models list into the basic tier
// when no tiers are configured.
func migrateLegacy(cfg *Config) {
	if len(cfg.Tiers) > 0 || len(cfg.BackendModels) == 0 {
		return
	}
	log.Printf("[CONFIG] Migrating %d legacy backend_models into basic tier", len(cfg.BackendModels))
	basic := cfg.Tiers["basic"]
	for i, bm := range cfg.BackendModels {
		name := bm.DisplayName
		if name == "" {
			name = fmt.Sprintf("legacy-%d", i+1)
		}
		basic.Models = append(basic.Models, model.BackendModel{
			Name:          name,
			Provider:      model.Provider(bm.Provider),
			TechnicalName: bm.TechnicalName,
			BaseURL:       bm.BaseURL,
			APIKey:        bm.APIKey,
			Priority:      1,
			Enabled:       bm.IsActive,
		})
	}
	if cfg.Tiers == nil {
		cfg.Tiers = map[string]TierConfig{}
	}
	cfg.Tiers["basic"] = basic
	cfg.BackendModels = nil
}

// SaveConfig persists the given config to config.yaml and swaps it in as the
// global snapshot. Returns the stored config.
func SaveConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	tmp := configFilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, configFilePath); err != nil {
		return fmt.Errorf("rename config: %w", err)
	}
	configMu.Lock()
	globalConfig = cfg
	configMu.Unlock()
	log.Printf("[CONFIG] Saved config to %s", configFilePath)
	return nil
}

// ConfigPath returns the active config file path.
func ConfigPath() string {
	return configFilePath
}

// EnsureDefaultAdmin seeds a default admin/admin user when none is configured,
// persisting it so first-run login works. Returns true if it seeded one.
func EnsureDefaultAdmin(cfg *Config) bool {
	for _, u := range cfg.Admin.Users {
		if u.Username != "" {
			return false
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return false
	}
	cfg.Admin.Users = append(cfg.Admin.Users, AdminUserConfig{
		Username:     "admin",
		PasswordHash: string(hash),
		Role:         "super_admin",
	})
	if err := SaveConfig(cfg); err != nil {
		log.Printf("[CONFIG] Warning: failed to persist default admin: %v", err)
		return false
	}
	log.Printf("[CONFIG] Seeded default admin user (admin/admin) — please change it via the web UI")
	return true
}
