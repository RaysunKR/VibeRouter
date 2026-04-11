package model

import (
	"time"
)

type Role string

const (
	RoleSuperAdmin Role = "super_admin"
	RoleAdmin      Role = "admin"
)

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

type Admin struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	Username     string    `json:"username" gorm:"uniqueIndex:username;not null;size:191"`
	PasswordHash string    `json:"-" gorm:"not null"`
	Role         Role      `json:"role" gorm:"not null"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

type BackendModel struct {
	ID            uint          `json:"id" gorm:"primaryKey"`
	Provider      Provider      `json:"provider" gorm:"not null"`
	DisplayName   string        `json:"display_name" gorm:"uniqueIndex:display_name;not null;size:191"`
	TechnicalName string        `json:"technical_name" gorm:"not null;size:191"`
	BaseURL       string        `json:"base_url" gorm:"not null"`
	APIKey        string        `json:"api_key" gorm:"not null;size:500"`
	Weight        int           `json:"weight" gorm:"default:10"`
	IsActive      bool          `json:"is_active" gorm:"default:true"`
	FailureCount  int           `json:"failure_count" gorm:"default:0"`
	CircuitState  CircuitState  `json:"circuit_state" gorm:"default:'closed'"`
	LastFailure   *time.Time    `json:"last_failure"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type APIKey struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	AdminID         uint      `json:"admin_id" gorm:"not null"`
	APIKey          string    `json:"api_key" gorm:"not null;size:191"`
	EncryptedSecret string    `json:"-" gorm:"column:encrypted_secret"`
	IsActive        bool      `json:"is_active" gorm:"default:true"`
	CreatedAt       time.Time `json:"created_at"`

	Admin *Admin `json:"admin,omitempty" gorm:"foreignKey:AdminID"`
}

type CallLog struct {
	ID               uint      `json:"id" gorm:"primaryKey"`
	AdminID          uint      `json:"admin_id" gorm:"not null;index"`
	AdminUsername    string    `json:"admin_username" gorm:"not null;size:191"`
	ClientIP         string    `json:"client_ip" gorm:"not null;size:50"`
	Provider         Provider  `json:"provider" gorm:"not null"`              // backend provider
	ModelName        string    `json:"model_name" gorm:"not null"`           // technical name
	ModelDisplayName string    `json:"model_display_name" gorm:"not null"`  // display name
	ApiStyle         string    `json:"api_style" gorm:"not null;size:50"`   // client API style: openai/anthropic
	RequestPath      string    `json:"request_path" gorm:"not null"`
	RequestMethod    string    `json:"request_method" gorm:"not null"`
	RequestBody      string    `json:"request_body" gorm:"type:text"`
	ResponseBody     string    `json:"response_body" gorm:"type:text"`
	StatusCode       int       `json:"status_code"`
	ErrorMessage     string    `json:"error_message" gorm:"type:text"`
	LatencyMs        int       `json:"latency_ms"`
	TokenUsed        int       `json:"token_used"`
	CreatedAt        time.Time `json:"created_at" gorm:"index"`

	Admin *Admin `json:"admin,omitempty" gorm:"foreignKey:AdminID"`
}

type SystemConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	ConfigKey string    `json:"config_key" gorm:"uniqueIndex:config_key;not null;size:191"`
	Value     string    `json:"value" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName overrides
func (BackendModel) TableName() string   { return "backend_models" }
func (APIKey) TableName() string          { return "api_keys" }
func (CallLog) TableName() string         { return "call_logs" }
func (SystemConfig) TableName() string   { return "system_config" }
