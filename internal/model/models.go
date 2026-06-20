package model

// Provider is the upstream API style of a backend model.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
)

// Tier classifies a backend model by capability/cost.
type Tier string

const (
	TierAdvanced Tier = "advanced" // 高级模型，复杂任务
	TierBasic    Tier = "basic"    // 普通模型，普通任务
)

func (t Tier) Valid() bool {
	return t == TierAdvanced || t == TierBasic
}

// CircuitState is the circuit-breaker state for a backend model.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

// BackendModel is one configured upstream model.
// Tier and Key are runtime-only (yaml:"-"); the tier comes from the
// config map key and Key is a stable circuit-breaker id ("<tier>:<name>").
type BackendModel struct {
	Tier             Tier     `json:"tier" yaml:"-"`
	Key              string   `json:"key" yaml:"-"`
	Name             string   `json:"name" yaml:"name"`
	Provider         Provider `json:"provider" yaml:"provider"`
	TechnicalName    string   `json:"technical_name" yaml:"technical_name"`
	BaseURL          string   `json:"base_url" yaml:"base_url"`
	APIKey           string   `json:"api_key" yaml:"api_key"`
	Priority         int      `json:"priority" yaml:"priority"`
	LongContext      bool     `json:"long_context" yaml:"long_context"`
	MaxContextTokens int      `json:"max_context_tokens" yaml:"max_context_tokens"`
	Enabled          bool     `json:"enabled" yaml:"enabled"`
}

// CallLog is a single proxied call record, persisted as one JSON Lines row.
type CallLog struct {
	Username         string   `json:"username"`
	ClientIP         string   `json:"client_ip"`
	Provider         Provider `json:"provider"`
	ModelName        string   `json:"model_name"`
	ModelDisplayName string   `json:"model_display_name"`
	Tier             Tier     `json:"tier,omitempty"`
	IsLongContext    bool     `json:"is_long_context,omitempty"`
	ApiStyle         string   `json:"api_style"`
	RequestPath      string   `json:"request_path"`
	RequestMethod    string   `json:"request_method"`
	StatusCode       int      `json:"status_code"`
	ErrorMessage     string   `json:"error_message,omitempty"`
	LatencyMs        int      `json:"latency_ms"`
}
