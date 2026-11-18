package protocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ---------- Anthropic Messages: request parsing ----------

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// image
	Source *struct {
		Type      string `json:"type"` // base64 | url
		MediaType string `json:"media_type,omitempty"`
		Data      string `json:"data,omitempty"`
		URL       string `json:"url,omitempty"`
	} `json:"source,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"` // string or blocks
}

type anthropicRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    json.RawMessage `json:"system,omitempty"` // string or blocks
	Messages  []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"` // string or blocks
	} `json:"messages"`
	Tools []struct {
		Type           string          `json:"type,omitempty"`
		Name           string          `json:"name"`
		Description    string          `json:"description,omitempty"`
		InputSchema    json.RawMessage `json:"input_schema"`
		MaxUses        int             `json:"max_uses,omitempty"`
		AllowedDomains []string        `json:"allowed_domains,omitempty"`
		BlockedDomains []string        `json:"blocked_domains,omitempty"`
	} `json:"tools,omitempty"`
	ToolChoice *struct {
		Type string `json:"type"`
		Name string `json:"name,omitempty"`
	} `json:"tool_choice,omitempty"`
	Temperature   *float64 `json:"temperature,omitempty"`
	TopP          *float64 `json:"top_p,omitempty"`
	StopSequences []string `json:"stop_sequences,omitempty"`
	Stream        bool     `json:"stream,omitempty"`
}

func ParseAnthropicRequest(body []byte) (*CanonicalRequest, error) {
	var ar anthropicRequest
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("invalid anthropic request: %w", err)
	}
	out := &CanonicalRequest{
		Model:       ar.Model,
		MaxTokens:   ar.MaxTokens,
		Temperature: ar.Temperature,
		TopP:        ar.TopP,
		Stop:        ar.StopSequences,
		Stream:      ar.Stream,
	}
	out.System = anthropicSystemText(ar.System)
	for _, m := range ar.Messages {
		var blocks []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(m.Content, &blocks) == nil {
			for _, block := range blocks {
				if block.Type == "server_tool_use" || block.Type == "web_search_tool_result" || block.Type == "web_fetch_tool_result" {
					out.HostedHistory = true
				}
			}
		}
		cm, err := anthropicMessageToCanonical(m.Role, m.Content)
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, cm)
	}
	for _, t := range ar.Tools {
		if kind := HostedWebToolKind(t.Type); kind != "" {
			out.HostedTools = append(out.HostedTools, HostedTool{
				Kind: kind, MaxUses: t.MaxUses, AllowedDomains: t.AllowedDomains, BlockedDomains: t.BlockedDomains,
			})
			continue
		}
		out.Tools = append(out.Tools, Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	if ar.ToolChoice != nil {
		switch ar.ToolChoice.Type {
		case "auto", "none":
			out.ToolChoice = &ToolChoice{Mode: ar.ToolChoice.Type}
		case "any":
			out.ToolChoice = &ToolChoice{Mode: "required"}
		case "tool":
			out.ToolChoice = &ToolChoice{Mode: "tool", Tool: ar.ToolChoice.Name}
		}
	}
	return out, nil
}

func anthropicSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthropicBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" {
				b.WriteString(bl.Text)
			}
		}
		return b.String()
	}
	return ""
}

func anthropicMessageToCanonical(role string, content json.RawMessage) (Message, error) {
	cm := Message{Role: role}
	var s string
	if json.Unmarshal(content, &s) == nil {
		if s != "" {
			cm.Parts = append(cm.Parts, Part{Type: PartText, Text: s})
		}
		return cm, nil
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return cm, fmt.Errorf("unsupported content in %s message", role)
	}
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			cm.Parts = append(cm.Parts, Part{Type: PartText, Text: bl.Text})
		case "image":
			if bl.Source != nil {
				if bl.Source.Type == "base64" {
					cm.Parts = append(cm.Parts, Part{Type: PartImage,
						ImageURL: "data:" + bl.Source.MediaType + ";base64," + bl.Source.Data})
				} else {
					cm.Parts = append(cm.Parts, Part{Type: PartImage, ImageURL: bl.Source.URL})
				}
			}
		case "tool_use":
			cm.Parts = append(cm.Parts, Part{Type: PartToolCall, ToolCallID: bl.ID, ToolName: bl.Name, ToolInput: bl.Input})
		case "tool_result":
			cm.Parts = append(cm.Parts, Part{Type: PartToolResult, ToolResultID: bl.ToolUseID,
				ToolResultContent: anthropicToolResultText(bl.Content)})
		case "server_tool_use":
			cm.Parts = append(cm.Parts, Part{Type: PartText, Text: "Previous hosted tool " + bl.Name + " input: " + string(bl.Input)})
		case "web_search_tool_result":
			if text := anthropicSearchHistory(bl.Content); text != "" {
				cm.Parts = append(cm.Parts, Part{Type: PartText, Text: text})
			}
		case "web_fetch_tool_result":
			if text := anthropicFetchHistory(bl.Content); text != "" {
				cm.Parts = append(cm.Parts, Part{Type: PartText, Text: text})
			}
		}
	}
	return cm, nil
}

func anthropicSearchHistory(raw json.RawMessage) string {
	var results []struct {
		URL              string `json:"url"`
		Title            string `json:"title"`
		PageAge          string `json:"page_age"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if err := json.Unmarshal(raw, &results); err != nil {
		return "Previous web search tool result:\n" + string(raw)
	}
	var out strings.Builder
	for _, result := range results {
		item := map[string]any{"url": result.URL, "title": result.Title, "page_age": result.PageAge}
		if strings.HasPrefix(result.EncryptedContent, "viberouter:") {
			encoded := strings.TrimPrefix(result.EncryptedContent, "viberouter:")
			if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err == nil {
				item["content"] = string(decoded)
			}
		}
		payload, _ := json.Marshal(item)
		out.WriteString("Previous web search result:\n" + string(payload) + "\n")
	}
	return out.String()
}

func anthropicFetchHistory(raw json.RawMessage) string {
	var result struct {
		URL     string `json:"url"`
		Content struct {
			Title  string `json:"title"`
			Source struct {
				Data string `json:"data"`
			} `json:"source"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "Previous web fetch tool result:\n" + string(raw)
	}
	payload, _ := json.Marshal(map[string]string{
		"url": result.URL, "title": result.Content.Title, "content": result.Content.Source.Data,
	})
	return "Previous web fetch result:\n" + string(payload)
}

func anthropicToolResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []anthropicBlock
	if json.Unmarshal(raw, &blocks) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Type == "text" {
				b.WriteString(bl.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}

// ---------- Anthropic Messages: request building ----------

func BuildAnthropicRequest(r *CanonicalRequest, upstreamModel string) ([]byte, error) {
	messages := make([]any, 0, len(r.Messages))
	for _, m := range r.Messages {
		if m.Role == "system" || m.Role == "developer" {
			continue // hoisted into System already
		}
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": canonicalToAnthropicBlocks(m),
		})
	}
	maxTokens := r.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096 // anthropic requires max_tokens
	}
	body := map[string]any{
		"model":      upstreamModel,
		"max_tokens": maxTokens,
		"messages":   messages,
	}
	if r.System != "" {
		body["system"] = r.System
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if len(r.Stop) > 0 {
		body["stop_sequences"] = r.Stop
	}
	if r.Stream {
		body["stream"] = true
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": json.RawMessage(orEmptyObject(t.InputSchema)),
			})
		}
		body["tools"] = tools
	}
	if r.ToolChoice != nil {
		switch r.ToolChoice.Mode {
		case "auto":
			body["tool_choice"] = map[string]any{"type": "auto"}
		case "none":
			body["tool_choice"] = map[string]any{"type": "none"}
		case "required":
			body["tool_choice"] = map[string]any{"type": "any"}
		case "tool":
			body["tool_choice"] = map[string]any{"type": "tool", "name": r.ToolChoice.Tool}
		}
	}
	return json.Marshal(body)
}

func canonicalToAnthropicBlocks(m Message) []any {
	var blocks []any
	for _, p := range m.Parts {
		switch p.Type {
		case PartText:
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case PartImage:
			blocks = append(blocks, map[string]any{"type": "image", "source": anthropicImageSource(p.ImageURL)})
		case PartToolCall:
			input := p.ToolInput
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_use", "id": p.ToolCallID, "name": p.ToolName,
				"input": json.RawMessage(input),
			})
		case PartToolResult:
			blocks = append(blocks, map[string]any{
				"type": "tool_result", "tool_use_id": p.ToolResultID, "content": p.ToolResultContent,
			})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return blocks
}

func anthropicImageSource(url string) map[string]any {
	if strings.HasPrefix(url, "data:") {
		rest := strings.TrimPrefix(url, "data:")
		media, data, ok := strings.Cut(rest, ";base64,")
		if ok {
			return map[string]any{"type": "base64", "media_type": media, "data": data}
		}
	}
	return map[string]any{"type": "url", "url": url}
}

// ---------- Anthropic Messages: responses ----------

type anthropicResponse struct {
	ID         string           `json:"id"`
	Model      string           `json:"model"`
	Content    []anthropicBlock `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func ParseAnthropicResponse(body []byte) (*CanonicalResponse, error) {
	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("invalid anthropic response: %w", err)
	}
	out := &CanonicalResponse{ID: ar.ID, Model: ar.Model, FinishReason: normalizeFinish(ar.StopReason)}
	for _, bl := range ar.Content {
		switch bl.Type {
		case "text":
			out.Parts = append(out.Parts, Part{Type: PartText, Text: bl.Text})
		case "tool_use":
			out.Parts = append(out.Parts, Part{Type: PartToolCall, ToolCallID: bl.ID, ToolName: bl.Name, ToolInput: bl.Input})
		}
	}
	if ar.Usage != nil {
		out.Usage = Usage{InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens}
	}
	return out, nil
}

// WriteAnthropicResponse serializes a canonical response as an Anthropic body.
func WriteAnthropicResponse(r *CanonicalResponse, model string) ([]byte, error) {
	var blocks []any
	for _, p := range r.Parts {
		switch p.Type {
		case PartText:
			blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
		case PartToolCall:
			input := p.ToolInput
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, map[string]any{
				"type": "tool_use", "id": p.ToolCallID, "name": p.ToolName,
				"input": json.RawMessage(input),
			})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	stopReason := "end_turn"
	switch r.FinishReason {
	case "length":
		stopReason = "max_tokens"
	case "tool_calls":
		stopReason = "tool_use"
	}
	id := r.ID
	if id == "" || !strings.HasPrefix(id, "msg_") {
		id = fmt.Sprintf("msg_%d", nowNanos())
	}
	return json.Marshal(map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       blocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  r.Usage.InputTokens,
			"output_tokens": r.Usage.OutputTokens,
		},
	})
}
