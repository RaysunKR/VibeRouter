package upstream

import "github.com/viberouter/viberouter/internal/config"

// ModelPreset describes the capabilities models.dev publishes for a model.
// Nil capability pointers mean that models.dev did not provide the metadata.
type ModelPreset struct {
	ID               string
	Multimodal       *bool
	MaxContextTokens *int
}

// ProviderPreset is a supplier template shown in the admin UI when adding a
// provider. Balance types map to the probes in balance.go. Models contains
// internal model metadata keyed by upstream model ID and is not exposed as a
// model catalog through the presets API.
type ProviderPreset struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Protocol string                 `json:"protocol"`
	BaseURL  string                 `json:"base_url"`
	Balance  *config.BalanceConfig  `json:"balance,omitempty"`
	Models   map[string]ModelPreset `json:"-"`
}

// Presets returns the built-in provider templates.
func Presets() []ProviderPreset {
	A := config.ProtocolAnthropic
	C := config.ProtocolChatCompletions
	R := config.ProtocolResponses
	balance := func(kind string) *config.BalanceConfig {
		return &config.BalanceConfig{Type: kind}
	}
	return []ProviderPreset{
		{ID: "anthropic", Name: "Anthropic Official (Messages)", Protocol: A, BaseURL: "https://api.anthropic.com"},
		{ID: "openai", Name: "OpenAI Official (Chat Completions)", Protocol: C, BaseURL: "https://api.openai.com"},
		{ID: "openai-responses", Name: "OpenAI Official (Responses)", Protocol: R, BaseURL: "https://api.openai.com"},

		{ID: "deepseek", Name: "DeepSeek", Protocol: C, BaseURL: "https://api.deepseek.com", Balance: balance("deepseek")},
		{ID: "deepseek-anthropic", Name: "DeepSeek (Anthropic)", Protocol: A, BaseURL: "https://api.deepseek.com/anthropic", Balance: balance("deepseek")},

		{ID: "siliconflow-cn", Name: "SiliconFlow 硅基流动", Protocol: A, BaseURL: "https://api.siliconflow.cn", Balance: balance("siliconflow")},
		{ID: "siliconflow-en", Name: "SiliconFlow Global", Protocol: A, BaseURL: "https://api.siliconflow.com", Balance: balance("siliconflow")},
		{ID: "openrouter", Name: "OpenRouter", Protocol: A, BaseURL: "https://openrouter.ai/api", Balance: balance("openrouter")},
		{ID: "novita", Name: "Novita AI", Protocol: A, BaseURL: "https://api.novita.ai/anthropic", Balance: balance("novita")},
		{ID: "stepfun-cn", Name: "StepFun 阶跃星辰", Protocol: A, BaseURL: "https://api.stepfun.com/step_plan", Balance: balance("stepfun")},
		{ID: "stepfun-en", Name: "StepFun Global", Protocol: A, BaseURL: "https://api.stepfun.ai/step_plan", Balance: balance("stepfun")},

		{ID: "zhipu-cn", Name: "Zhipu GLM 智谱", Protocol: A, BaseURL: "https://open.bigmodel.cn/api/anthropic", Balance: balance("zhipu")},
		{ID: "zhipu-en", Name: "Zhipu GLM (z.ai)", Protocol: A, BaseURL: "https://api.z.ai/api/anthropic", Balance: balance("zhipu")},
		{ID: "minimax-cn", Name: "MiniMax", Protocol: A, BaseURL: "https://api.minimaxi.com/anthropic", Balance: balance("minimax")},
		{ID: "minimax-en", Name: "MiniMax Global", Protocol: A, BaseURL: "https://api.minimax.io/anthropic", Balance: balance("minimax")},

		{ID: "kimi", Name: "Kimi 月之暗面", Protocol: A, BaseURL: "https://api.moonshot.cn/anthropic"},
		{ID: "kimi-coding", Name: "Kimi For Coding", Protocol: A, BaseURL: "https://api.kimi.com/coding", Balance: balance("kimi_coding")},
		{ID: "volcengine-ark", Name: "火山方舟 Coding Plan", Protocol: A, BaseURL: "https://ark.cn-beijing.volces.com/api/coding"},
		{ID: "bailian", Name: "阿里云百炼", Protocol: A, BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic"},
		{ID: "modelscope", Name: "ModelScope 魔搭", Protocol: A, BaseURL: "https://api-inference.modelscope.cn"},
		{ID: "ppio", Name: "PPIO", Protocol: A, BaseURL: "https://api.ppio.com/anthropic"},
		{ID: "longcat", Name: "Longcat 美团", Protocol: A, BaseURL: "https://api.longcat.chat/anthropic"},
		{ID: "qiniu", Name: "七牛云", Protocol: A, BaseURL: "https://api.qnaigc.com"},
		{ID: "dmxapi", Name: "DMXAPI", Protocol: A, BaseURL: "https://www.dmxapi.cn"},
		{ID: "aihubmix", Name: "AiHubMix", Protocol: A, BaseURL: "https://aihubmix.com"},
		{ID: "nvidia", Name: "Nvidia NIM", Protocol: C, BaseURL: "https://integrate.api.nvidia.com"},
	}
}
