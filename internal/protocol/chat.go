package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

// RewriteModel replaces the "model" field in a raw JSON request body without
// otherwise touching it. Used for same-protocol passthrough.
func RewriteModel(body []byte, model string) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	m, _ := json.Marshal(model)
	raw["model"] = m
	return json.Marshal(raw)
}

// ---------- Chat Completions: request parsing ----------

type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string or []part
	ToolCalls  []chatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

type chatContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type chatToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatRequest struct {
	Model       string           `json:"model"`
	Messages    []chatMessage    `json:"messages"`
	Tools       []json.RawMessage `json:"tools,omitempty"`
	ToolChoice  json.RawMessage  `json:"tool_choice,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens int       `json:"max_completion_tokens,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	TopP        *float64         `json:"top_p,omitempty"`
	Stop        json.RawMessage  `json:"stop,omitempty"`
	Stream      bool             `json:"stream,omitempty"`
}

// ParseChatRequest converts a Chat Completions request body to canonical form.
func ParseChatRequest(body []byte) (*CanonicalRequest, error) {
	var cr chatRequest
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("invalid chat completions request: %w", err)
	}
	out := &CanonicalRequest{
		Model:       cr.Model,
		MaxTokens:   cr.MaxTokens,
		Temperature: cr.Temperature,
		TopP:        cr.TopP,
		Stream:      cr.Stream,
	}
	if out.MaxTokens == 0 {
		out.MaxTokens = cr.MaxCompletionTokens
	}
	if len(cr.Stop) > 0 {
		var arr []string
		if json.Unmarshal(cr.Stop, &arr) == nil {
			out.Stop = arr
		} else {
			var s string
			if json.Unmarshal(cr.Stop, &s) == nil && s != "" {
				out.Stop = []string{s}
			}
		}
	}
	for _, m := range cr.Messages {
		cm, err := chatMessageToCanonical(m)
		if err != nil {
			return nil, err
		}
		if m.Role == "system" || m.Role == "developer" {
			// Hoist system text; keep positional parts if mixed with others.
			if out.System == "" && len(cm.Parts) > 0 && cm.Parts[0].Type == PartText {
				out.System = cm.Text()
				continue
			}
		}
		out.Messages = append(out.Messages, cm)
	}
	for _, t := range cr.Tools {
		var fn struct {
			Function struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
			} `json:"function"`
		}
		if json.Unmarshal(t, &fn) == nil && fn.Function.Name != "" {
			out.Tools = append(out.Tools, Tool{
				Name:        fn.Function.Name,
				Description: fn.Function.Description,
				InputSchema: fn.Function.Parameters,
			})
		}
	}
	if len(cr.ToolChoice) > 0 {
		out.ToolChoice = parseChatToolChoice(cr.ToolChoice)
	}
	return out, nil
}

func chatMessageToCanonical(m chatMessage) (Message, error) {
	cm := Message{Role: m.Role}
	if m.Role == "tool" {
		cm.Role = "user"
		var s string
		_ = json.Unmarshal(m.Content, &s)
		cm.Parts = append(cm.Parts, Part{
			Type:              PartToolResult,
			ToolResultID:      m.ToolCallID,
			ToolResultContent: s,
		})
		return cm, nil
	}
	for _, tc := range m.ToolCalls {
		cm.Parts = append(cm.Parts, Part{
			Type:       PartToolCall,
			ToolCallID: tc.ID,
			ToolName:   tc.Function.Name,
			ToolInput:  json.RawMessage(tc.Function.Arguments),
		})
	}
	if len(m.Content) == 0 || string(m.Content) == "null" {
		return cm, nil
	}
	var s string
	if json.Unmarshal(m.Content, &s) == nil {
		if s != "" {
			cm.Parts = append(cm.Parts, Part{Type: PartText, Text: s})
		}
		return cm, nil
	}
	var parts []chatContentPart
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return cm, fmt.Errorf("unsupported content in %s message", m.Role)
	}
	for _, p := range parts {
		switch p.Type {
		case "text":
			cm.Parts = append(cm.Parts, Part{Type: PartText, Text: p.Text})
		case "image_url":
			if p.ImageURL != nil {
				cm.Parts = append(cm.Parts, Part{Type: PartImage, ImageURL: p.ImageURL.URL})
			}
		}
	}
	return cm, nil
}

func parseChatToolChoice(raw json.RawMessage) *ToolChoice {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return &ToolChoice{Mode: s}
	}
	var obj struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		return &ToolChoice{Mode: "tool", Tool: obj.Function.Name}
	}
	return nil
}

// ---------- Chat Completions: request building ----------

// BuildChatRequest serializes a canonical request as a Chat Completions body,
// substituting upstreamModel as the wire model name.
func BuildChatRequest(r *CanonicalRequest, upstreamModel string) ([]byte, error) {
	messages := make([]any, 0, len(r.Messages)+1)
	if r.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": r.System})
	}
	for _, m := range r.Messages {
		messages = append(messages, canonicalToChatMessages(m)...)
	}
	body := map[string]any{"model": upstreamModel, "messages": messages}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if len(r.Stop) > 0 {
		body["stop"] = r.Stop
	}
	if r.Stream {
		body["stream"] = true
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  json.RawMessage(orEmptyObject(t.InputSchema)),
				},
			})
		}
		body["tools"] = tools
	}
	if r.ToolChoice != nil {
		body["tool_choice"] = buildChatToolChoice(r.ToolChoice)
	}
	return json.Marshal(body)
}

func canonicalToChatMessages(m Message) []any {
	// Split into: tool results (role=tool messages), tool calls + text (assistant), content (others).
	var results []any
	var calls []any
	var contentParts []chatContentPart
	var plainText string
	multiPart := false

	for _, p := range m.Parts {
		switch p.Type {
		case PartToolResult:
			results = append(results, map[string]any{
				"role":         "tool",
				"tool_call_id": p.ToolResultID,
				"content":      p.ToolResultContent,
			})
		case PartToolCall:
			args := string(p.ToolInput)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id":   p.ToolCallID,
				"type": "function",
				"function": map[string]any{
					"name":      p.ToolName,
					"arguments": args,
				},
			})
		case PartText:
			contentParts = append(contentParts, chatContentPart{Type: "text", Text: p.Text})
			plainText += p.Text
		case PartImage:
			multiPart = true
			contentParts = append(contentParts, chatContentPart{
				Type:     "image_url",
				ImageURL: &struct{ URL string `json:"url"` }{URL: p.ImageURL},
			})
		}
	}

	var out []any
	// Tool results must precede the assistant's next message in chat format;
	// emit them first, then any remaining content as a user message.
	out = append(out, results...)

	msg := map[string]any{}
	switch m.Role {
	case "assistant":
		msg["role"] = "assistant"
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
	case "user", "system", "developer":
		if m.Role == "" {
			m.Role = "user"
		}
		msg["role"] = m.Role
	default:
		msg["role"] = m.Role
	}
	if len(calls) > 0 && m.Role != "assistant" {
		// Tool calls only exist on assistant messages; force the role.
		msg["role"] = "assistant"
		msg["tool_calls"] = calls
	}

	if multiPart {
		msg["content"] = contentParts
	} else if plainText != "" || len(msg) <= 1 {
		msg["content"] = plainText
	}
	if _, hasContent := msg["content"]; !hasContent && len(calls) > 0 {
		msg["content"] = nil
	}
	if len(msg) > 1 || len(results) == 0 {
		out = append(out, msg)
	}
	return out
}

func buildChatToolChoice(tc *ToolChoice) any {
	switch tc.Mode {
	case "auto", "none", "required":
		return tc.Mode
	case "tool":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Tool},
		}
	}
	return "auto"
}

// ---------- Chat Completions: responses ----------

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   *string        `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func ParseChatResponse(body []byte) (*CanonicalResponse, error) {
	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("invalid chat completions response: %w", err)
	}
	out := &CanonicalResponse{ID: cr.ID, Model: cr.Model}
	if len(cr.Choices) > 0 {
		ch := cr.Choices[0]
		if ch.Message.Content != nil && *ch.Message.Content != "" {
			out.Parts = append(out.Parts, Part{Type: PartText, Text: *ch.Message.Content})
		}
		for _, tc := range ch.Message.ToolCalls {
			out.Parts = append(out.Parts, Part{
				Type:       PartToolCall,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				ToolInput:  json.RawMessage(tc.Function.Arguments),
			})
		}
		out.FinishReason = normalizeFinish(ch.FinishReason)
	}
	if cr.Usage != nil {
		out.Usage = Usage{InputTokens: cr.Usage.PromptTokens, OutputTokens: cr.Usage.CompletionTokens}
	}
	return out, nil
}

func normalizeFinish(reason string) string {
	switch reason {
	case "stop", "end_turn":
		return "stop"
	case "length", "max_tokens":
		return "length"
	case "tool_calls", "tool_use", "function_call":
		return "tool_calls"
	}
	if reason == "" {
		return "stop"
	}
	return reason
}

// WriteChatResponse serializes a canonical response as a Chat Completions body.
func WriteChatResponse(r *CanonicalResponse, model string) ([]byte, error) {
	var text string
	var calls []any
	for _, p := range r.Parts {
		switch p.Type {
		case PartText:
			text += p.Text
		case PartToolCall:
			args := string(p.ToolInput)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id":   p.ToolCallID,
				"type": "function",
				"function": map[string]any{"name": p.ToolName, "arguments": args},
			})
		}
	}
	msg := map[string]any{"role": "assistant", "content": text}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	id := r.ID
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	finish := r.FinishReason
	if finish == "" {
		finish = "stop"
	}
	return json.Marshal(map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finish,
		}},
		"usage": map[string]any{
			"prompt_tokens":     r.Usage.InputTokens,
			"completion_tokens": r.Usage.OutputTokens,
			"total_tokens":      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	})
}

func orEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return raw
}
