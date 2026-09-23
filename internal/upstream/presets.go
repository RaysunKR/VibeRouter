package upstream

import "github.com/viberouter/viberouter/internal/config"

// ProviderPreset is a built-in supplier template shown in the admin UI when
// adding a provider. Sourced from cc-switch's preset list; balance types map to
// the probes in balance.go ("" = no known balance API).
type ProviderPreset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"base_url"`
	Balance  string `json:"balance"`
}

// Presets returns the built-in provider templates.
func Presets() []ProviderPreset {
	A := config.ProtocolAnthropic
	C := config.ProtocolChatCompletions
	R := config.ProtocolResponses
	return []ProviderPreset{
		{ID: "anthropic", Name: "Anthropic Official (Messages)", Protocol: A, BaseURL: "https://api.anthropic.com"},
		{ID: "openai", Name: "OpenAI Official (Chat Completions)", Protocol: C, BaseURL: "https://api.openai.com"},
		{ID: "openai-responses", Name: "OpenAI Official (Responses)", Protocol: R, BaseURL: "https://api.openai.com"},

		{ID: "deepseek", Name: "DeepSeek", Protocol: C, BaseURL: "https://api.deepseek.com", Balance: "deepseek"},
		{ID: "deepseek-anthropic", Name: "DeepSeek (Anthropic)", Protocol: A, BaseURL: "https://api.deepseek.com/anthropic", Balance: "deepseek"},

		{ID: "siliconflow-cn", Name: "SiliconFlow 硅基流动", Protocol: A, BaseURL: "https://api.siliconflow.cn", Balance: "siliconflow"},
		{ID: "siliconflow-en", Name: "SiliconFlow Global", Protocol: A, BaseURL: "https://api.siliconflow.com", Balance: "siliconflow"},
		{ID: "openrouter", Name: "OpenRouter", Protocol: A, BaseURL: "https://openrouter.ai/api", Balance: "openrouter"},
		{ID: "novita", Name: "Novita AI", Protocol: A, BaseURL: "https://api.novita.ai/anthropic", Balance: "novita"},
		{ID: "stepfun-cn", Name: "StepFun 阶跃星辰", Protocol: A, BaseURL: "https://api.stepfun.com/step_plan", Balance: "stepfun"},
		{ID: "stepfun-en", Name: "StepFun Global", Protocol: A, BaseURL: "https://api.stepfun.ai/step_plan", Balance: "stepfun"},

		{ID: "zhipu-cn", Name: "Zhipu GLM 智谱", Protocol: A, BaseURL: "https://open.bigmodel.cn/api/anthropic", Balance: "zhipu"},
		{ID: "zhipu-en", Name: "Zhipu GLM (z.ai)", Protocol: A, BaseURL: "https://api.z.ai/api/anthropic", Balance: "zhipu"},
		{ID: "minimax-cn", Name: "MiniMax", Protocol: A, BaseURL: "https://api.minimaxi.com/anthropic", Balance: "minimax"},
		{ID: "minimax-en", Name: "MiniMax Global", Protocol: A, BaseURL: "https://api.minimax.io/anthropic", Balance: "minimax"},

		{ID: "kimi", Name: "Kimi 月之暗面", Protocol: A, BaseURL: "https://api.moonshot.cn/anthropic"},
		{ID: "kimi-coding", Name: "Kimi For Coding", Protocol: A, BaseURL: "https://api.kimi.com/coding", Balance: "kimi_coding"},
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
