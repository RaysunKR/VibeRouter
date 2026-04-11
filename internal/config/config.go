package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Server         ServerConfig
	Log            LogConfig
	CircuitBreaker CircuitBreakerConfig
	Retry          RetryConfig
	BackendModels  []BackendModelConfig
	APIKeys        []APIKeyConfig
}

type BackendModelConfig struct {
	Provider      string `json:"provider"`
	DisplayName   string `json:"display_name"`
	TechnicalName string `json:"technical_name"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	IsActive      bool   `json:"is_active"`
}

type APIKeyConfig struct {
	Key      string `json:"key"`
	Username string `json:"username"` // username for log attribution
	IsActive bool   `json:"is_active"`
}

type ServerConfig struct {
	Address string
	Mode   string
}

type LogConfig struct {
	RetentionDays int
}

type CircuitBreakerConfig struct {
	Threshold  int
	TimeoutSec int
}

type RetryConfig struct {
	MaxAttempts int
	TimeoutMs   int
}

var (
	configFilePath = ""
	globalConfig   *Config
)

func GetConfig() *Config {
	return globalConfig
}

func getExecutableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func Load(configPath string) (*Config, error) {
	v := viper.New()

	if configPath != "" {
		configFilePath = configPath
		v.SetConfigFile(configPath)
	} else {
		configFilePath = filepath.Join(getExecutableDir(), "config.yaml")
		v.SetConfigFile(configFilePath)
	}

	// Defaults
	v.SetDefault("server.address", ":8080")
	v.SetDefault("server.mode", "release")
	v.SetDefault("log.retention_days", 30)
	v.SetDefault("circuit_breaker.threshold", 5)
	v.SetDefault("circuit_breaker.timeout_sec", 30)
	v.SetDefault("retry.max_attempts", 2)
	v.SetDefault("retry.timeout_ms", 30000)

	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		log.Printf("[CONFIG] Warning: config file not found or error reading: %v", err)
	} else {
		log.Printf("[CONFIG] Loaded config from: %s", configFilePath)
	}

	cfg := &Config{}
	cfg.Server.Address = v.GetString("server.address")
	cfg.Server.Mode = v.GetString("server.mode")
	cfg.Log.RetentionDays = v.GetInt("log.retention_days")
	cfg.CircuitBreaker.Threshold = v.GetInt("circuit_breaker.threshold")
	cfg.CircuitBreaker.TimeoutSec = v.GetInt("circuit_breaker.timeout_sec")
	cfg.Retry.MaxAttempts = v.GetInt("retry.max_attempts")
	cfg.Retry.TimeoutMs = v.GetInt("retry.timeout_ms")

	// Load backend models
	if v.IsSet("backend_models") {
		bmSlice := v.Get("backend_models")
		if bmSlice != nil {
			bmBytes, _ := json.Marshal(bmSlice)
			json.Unmarshal(bmBytes, &cfg.BackendModels)
		}
	}

	// Load API keys
	if v.IsSet("api_keys") {
		keySlice := v.Get("api_keys")
		if keySlice != nil {
			keyBytes, _ := json.Marshal(keySlice)
			json.Unmarshal(keyBytes, &cfg.APIKeys)
		}
	}

	globalConfig = cfg
	return cfg, nil
}
