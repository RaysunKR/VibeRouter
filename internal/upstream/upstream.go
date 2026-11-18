// Package upstream talks to provider endpoints: forwarding chat traffic and
// querying provider balances/quotas.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

var dialer = &net.Dialer{Timeout: 10 * time.Second}

// Client is a plain HTTP client (no overall timeout; stream lifetime is
// bounded by ctx) with a fast dial so dead hosts fail over quickly.
var Client = &http.Client{
	Transport: &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          200,
	},
}

// DefaultDispatcherPrompt is the built-in complexity classification prompt.
const DefaultDispatcherPrompt = `你是请求复杂度分类器。根据用户请求，判断应路由到强模型(advanced)还是普通模型(basic)。

判为 advanced 的典型特征：
- 多步推理、数学/逻辑推导、复杂调试
- 代码架构设计、大规模重构、跨文件分析
- 长文写作、深度分析、需要专业知识的开放问题

判为 basic 的典型特征：
- 简单问答、事实查询、翻译、摘要、格式转换
- 单函数/小段代码的编写或解释
- 闲聊、指令明确的机械任务

只输出一个 JSON 对象，不要输出任何其他内容：
{"tier": "advanced"} 或 {"tier": "basic"}`

// Dispatcher returns a router.Classifier bound to this config store.
func Dispatcher(cfg *config.Store) func(ctx context.Context, group *config.Group, req *protocol.CanonicalRequest) (string, error) {
	return func(ctx context.Context, group *config.Group, req *protocol.CanonicalRequest) (string, error) {
		snap := cfg.Snapshot()
		if group.DispatcherModel == "" {
			return "", fmt.Errorf("group %q has no dispatcher_model", group.Name)
		}
		m := snap.FindModel(group.DispatcherModel)
		if m == nil {
			return "", fmt.Errorf("dispatcher model %q not found", group.DispatcherModel)
		}
		resolved, err := snap.ResolveModel(m)
		if err != nil {
			return "", err
		}
		prompt := group.DispatcherPrompt
		if prompt == "" {
			prompt = DefaultDispatcherPrompt
		}
		// Only the text of the last user message, truncated; no history, no images.
		text := truncateRunes(lastUserText(req), 4000)
		if strings.TrimSpace(text) == "" {
			return config.TierBasic, nil
		}
		return classify(ctx, resolved, prompt, text, snap.Routing.DispatcherTimeoutSec)
	}
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	for i := range s {
		if limit == 0 {
			return s[:i]
		}
		limit--
	}
	return s
}

func lastUserText(req *protocol.CanonicalRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" || req.Messages[i].Role == "" {
			if t := req.Messages[i].Text(); t != "" {
				return t
			}
		}
	}
	return ""
}

func classify(ctx context.Context, dm *config.ResolvedModel, prompt, text string, timeoutSec int) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	zero := 0.0
	creq := &protocol.CanonicalRequest{
		Model:       dm.UpstreamName,
		System:      prompt,
		Messages:    []protocol.Message{{Role: "user", Parts: []protocol.Part{{Type: protocol.PartText, Text: text}}}},
		Temperature: &zero,
		// 1024: reasoning models (deepseek-v4 etc.) burn tokens on chain-of-thought
		// before the answer; a tiny budget would yield empty content and force a
		// degrade-to-basic on every call.
		MaxTokens: 1024,
	}
	body, err := BuildRequest(dm.EffectiveProtocol, creq, dm.UpstreamName)
	if err != nil {
		return "", err
	}
	resp, err := Do(ctx, dm, body, false)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("dispatcher upstream status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	cresp, err := ParseResponse(dm.EffectiveProtocol, raw)
	if err != nil {
		return "", err
	}
	out := ""
	for _, p := range cresp.Parts {
		if p.Type == protocol.PartText {
			out += p.Text
		}
	}
	tier, err := parseDispatcherTier(out)
	if err != nil {
		return "", fmt.Errorf("unparseable dispatcher output %q: %w", truncate(out, 120), err)
	}
	return tier, nil
}

func parseDispatcherTier(out string) (string, error) {
	raw := strings.TrimSpace(out)
	if strings.HasPrefix(raw, "```") {
		if len(raw) < 6 || !strings.HasSuffix(raw, "```") {
			return "", fmt.Errorf("incomplete dispatcher code block")
		}
		raw = strings.TrimSpace(raw[3 : len(raw)-3])
		if strings.HasPrefix(raw, "json") {
			raw = strings.TrimSpace(raw[len("json"):])
		}
	}

	var result struct {
		Tier string `json:"tier"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return "", err
	}
	tier := strings.ToLower(strings.TrimSpace(result.Tier))
	if tier != config.TierAdvanced && tier != config.TierBasic {
		return "", fmt.Errorf("invalid dispatcher tier %q", result.Tier)
	}
	return tier, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// BuildRequest serializes a canonical request for the given upstream protocol.
func BuildRequest(proto string, r *protocol.CanonicalRequest, upstreamModel string) ([]byte, error) {
	switch proto {
	case config.ProtocolChatCompletions:
		return protocol.BuildChatRequest(r, upstreamModel)
	case config.ProtocolResponses:
		return protocol.BuildResponsesRequest(r, upstreamModel)
	case config.ProtocolAnthropic:
		return protocol.BuildAnthropicRequest(r, upstreamModel)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

// ParseResponse parses a non-streaming upstream response body.
func ParseResponse(proto string, body []byte) (*protocol.CanonicalResponse, error) {
	switch proto {
	case config.ProtocolChatCompletions:
		return protocol.ParseChatResponse(body)
	case config.ProtocolResponses:
		return protocol.ParseResponsesResponse(body)
	case config.ProtocolAnthropic:
		return protocol.ParseAnthropicResponse(body)
	}
	return nil, fmt.Errorf("unknown protocol %q", proto)
}

// Do sends a request to the resolved upstream endpoint with provider auth.
func Do(ctx context.Context, dm *config.ResolvedModel, body []byte, stream bool) (*http.Response, error) {
	url := dm.EffectiveBaseURL + protocol.EndpointFor(dm.EffectiveProtocol)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+dm.EffectiveAPIKey)
	if dm.EffectiveProtocol == config.ProtocolAnthropic {
		req.Header.Set("x-api-key", dm.EffectiveAPIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return Client.Do(req)
}

// ShouldFailOver reports whether an upstream HTTP status warrants trying the
// next candidate: unreachable/ratelimit/server errors and insufficient balance.
func ShouldFailOver(status int) bool {
	if status >= 500 || status == http.StatusTooManyRequests || status == http.StatusRequestTimeout {
		return true
	}
	// 402 payment required: balance exhausted (DeepSeek etc.)
	// 403: quota/permission failures on relay providers
	return status == http.StatusPaymentRequired || status == http.StatusForbidden
}
