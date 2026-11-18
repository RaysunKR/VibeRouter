package protocol

import (
	"encoding/json"
	"fmt"
)

// ---------- OpenAI Responses API: request parsing ----------

type responsesContentPart struct {
	Type     string `json:"type"` // input_text | output_text | input_image
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesItem struct {
	Type    string                 `json:"type"` // message | function_call | function_call_output
	Role    string                 `json:"role,omitempty"`
	Content []responsesContentPart `json:"content,omitempty"`
	// function_call
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	// function_call_output
	Output json.RawMessage `json:"output,omitempty"`
	// hosted web history
	Action  json.RawMessage `json:"action,omitempty"`
	Sources []struct {
		Type  string `json:"type,omitempty"`
		URL   string `json:"url,omitempty"`
		Title string `json:"title,omitempty"`
	} `json:"sources,omitempty"`
}

type responsesRequest struct {
	Model           string            `json:"model"`
	Input           json.RawMessage   `json:"input"` // string or []item
	Instructions    string            `json:"instructions,omitempty"`
	Tools           []json.RawMessage `json:"tools,omitempty"`
	ToolChoice      json.RawMessage   `json:"tool_choice,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	TopP            *float64          `json:"top_p,omitempty"`
	Stream          bool              `json:"stream,omitempty"`
}

func ParseResponsesRequest(body []byte) (*CanonicalRequest, error) {
	var rr responsesRequest
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("invalid responses request: %w", err)
	}
	out := &CanonicalRequest{
		Model:       rr.Model,
		System:      rr.Instructions,
		MaxTokens:   rr.MaxOutputTokens,
		Temperature: rr.Temperature,
		TopP:        rr.TopP,
		Stream:      rr.Stream,
	}
	if len(rr.Input) > 0 {
		var s string
		if json.Unmarshal(rr.Input, &s) == nil {
			if s != "" {
				out.Messages = append(out.Messages, Message{Role: "user",
					Parts: []Part{{Type: PartText, Text: s}}})
			}
		} else {
			var items []responsesItem
			if err := json.Unmarshal(rr.Input, &items); err != nil {
				return nil, fmt.Errorf("invalid responses input: %w", err)
			}
			for _, it := range items {
				if it.Type == "web_search_call" {
					out.HostedHistory = true
				}
				out.Messages = append(out.Messages, responsesItemToCanonical(it))
			}
		}
	}
	for _, t := range rr.Tools {
		var tool struct {
			Type           string          `json:"type"`
			Name           string          `json:"name"`
			Description    string          `json:"description"`
			Parameters     json.RawMessage `json:"parameters"`
			MaxUses        int             `json:"max_uses"`
			AllowedDomains []string        `json:"allowed_domains"`
			BlockedDomains []string        `json:"blocked_domains"`
			Filters        *struct {
				AllowedDomains []string `json:"allowed_domains"`
				BlockedDomains []string `json:"blocked_domains"`
			} `json:"filters"`
		}
		if json.Unmarshal(t, &tool) != nil {
			continue
		}
		if kind := HostedWebToolKind(tool.Type); kind != "" {
			allowed, blocked := tool.AllowedDomains, tool.BlockedDomains
			if tool.Filters != nil {
				allowed, blocked = tool.Filters.AllowedDomains, tool.Filters.BlockedDomains
			}
			out.HostedTools = append(out.HostedTools, HostedTool{
				Kind: kind, MaxUses: tool.MaxUses, AllowedDomains: allowed, BlockedDomains: blocked,
			})
			continue
		}
		if tool.Type == "function" && tool.Name != "" {
			out.Tools = append(out.Tools, Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters})
		}
	}
	if len(rr.ToolChoice) > 0 {
		out.ToolChoice = parseResponsesToolChoice(rr.ToolChoice)
	}
	return out, nil
}

func responsesItemToCanonical(it responsesItem) Message {
	switch it.Type {
	case "web_search_call":
		action := it.Action
		if len(action) == 0 {
			action = json.RawMessage(`{}`)
		}
		payload, _ := json.Marshal(map[string]any{"action": action, "sources": it.Sources})
		return Message{Role: "assistant", Parts: []Part{{Type: PartText, Text: "Previous hosted web search:\n" + string(payload)}}}
	case "function_call":
		return Message{Role: "assistant", Parts: []Part{{
			Type:       PartToolCall,
			ToolCallID: it.CallID,
			ToolName:   it.Name,
			ToolInput:  json.RawMessage(it.Arguments),
		}}}
	case "function_call_output":
		var out string
		if json.Unmarshal(it.Output, &out) != nil {
			out = string(it.Output)
		}
		return Message{Role: "user", Parts: []Part{{
			Type:              PartToolResult,
			ToolResultID:      it.CallID,
			ToolResultContent: out,
		}}}
	default: // message (or item without explicit type but with role)
		m := Message{Role: it.Role}
		if m.Role == "" {
			m.Role = "user"
		}
		for _, c := range it.Content {
			switch c.Type {
			case "input_text", "output_text", "text":
				m.Parts = append(m.Parts, Part{Type: PartText, Text: c.Text})
			case "input_image":
				m.Parts = append(m.Parts, Part{Type: PartImage, ImageURL: c.ImageURL})
			}
		}
		return m
	}
}

func parseResponsesToolChoice(raw json.RawMessage) *ToolChoice {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "required" || s == "auto" || s == "none" {
			return &ToolChoice{Mode: s}
		}
		return nil
	}
	var obj struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
		return &ToolChoice{Mode: "tool", Tool: obj.Name}
	}
	return nil
}

// ---------- Responses API: request building ----------

func BuildResponsesRequest(r *CanonicalRequest, upstreamModel string) ([]byte, error) {
	var input []any
	for _, m := range r.Messages {
		role := m.Role
		if role == "system" || role == "developer" {
			continue
		}
		if role != "assistant" {
			role = "user"
		}
		// Tool calls and results are standalone items, not message content.
		var content []any
		for _, p := range m.Parts {
			switch p.Type {
			case PartText:
				t := "input_text"
				if role == "assistant" {
					t = "output_text"
				}
				content = append(content, map[string]any{"type": t, "text": p.Text})
			case PartImage:
				content = append(content, map[string]any{"type": "input_image", "image_url": p.ImageURL})
			case PartToolCall:
				args := string(p.ToolInput)
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]any{
					"type": "function_call", "call_id": p.ToolCallID,
					"name": p.ToolName, "arguments": args,
				})
			case PartToolResult:
				input = append(input, map[string]any{
					"type": "function_call_output", "call_id": p.ToolResultID,
					"output": p.ToolResultContent,
				})
			}
		}
		if len(content) > 0 {
			input = append(input, map[string]any{"type": "message", "role": role, "content": content})
		}
	}
	body := map[string]any{"model": upstreamModel, "input": input}
	if r.System != "" {
		body["instructions"] = r.System
	}
	if r.MaxTokens > 0 {
		body["max_output_tokens"] = r.MaxTokens
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if r.TopP != nil {
		body["top_p"] = *r.TopP
	}
	if r.Stream {
		body["stream"] = true
	}
	if len(r.Tools) > 0 {
		tools := make([]any, 0, len(r.Tools))
		for _, t := range r.Tools {
			tools = append(tools, map[string]any{
				"type": "function", "name": t.Name,
				"description": t.Description,
				"parameters":  json.RawMessage(orEmptyObject(t.InputSchema)),
			})
		}
		body["tools"] = tools
	}
	if r.ToolChoice != nil {
		switch r.ToolChoice.Mode {
		case "auto", "none", "required":
			body["tool_choice"] = r.ToolChoice.Mode
		case "tool":
			body["tool_choice"] = map[string]any{"type": "function", "name": r.ToolChoice.Tool}
		}
	}
	return json.Marshal(body)
}

// ---------- Responses API: responses ----------

type responsesObject struct {
	ID     string `json:"id"`
	Model  string `json:"model"`
	Status string `json:"status"`
	Output []struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"output"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
}

func ParseResponsesResponse(body []byte) (*CanonicalResponse, error) {
	var ro responsesObject
	if err := json.Unmarshal(body, &ro); err != nil {
		return nil, fmt.Errorf("invalid responses object: %w", err)
	}
	out := &CanonicalResponse{ID: ro.ID, Model: ro.Model, FinishReason: "stop"}
	for _, item := range ro.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					out.Parts = append(out.Parts, Part{Type: PartText, Text: c.Text})
				}
			}
		case "function_call":
			out.Parts = append(out.Parts, Part{
				Type:       PartToolCall,
				ToolCallID: item.CallID,
				ToolName:   item.Name,
				ToolInput:  json.RawMessage(item.Arguments),
			})
			out.FinishReason = "tool_calls"
		}
	}
	if ro.IncompleteDetails != nil && ro.IncompleteDetails.Reason == "max_output_tokens" {
		out.FinishReason = "length"
	}
	if ro.Usage != nil {
		out.Usage = Usage{InputTokens: ro.Usage.InputTokens, OutputTokens: ro.Usage.OutputTokens}
	}
	return out, nil
}

// WriteResponsesResponse serializes a canonical response as a Responses object.
func WriteResponsesResponse(r *CanonicalResponse, model string) ([]byte, error) {
	var output []any
	var text string
	for _, p := range r.Parts {
		switch p.Type {
		case PartText:
			text += p.Text
		case PartToolCall:
			args := string(p.ToolInput)
			if args == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"type": "function_call", "id": p.ToolCallID, "call_id": p.ToolCallID,
				"name": p.ToolName, "arguments": args, "status": "completed",
			})
		}
	}
	if text != "" || len(output) == 0 {
		// Message item first, then function calls.
		msg := map[string]any{
			"type":   "message",
			"id":     fmt.Sprintf("msg_%d", nowNanos()),
			"role":   "assistant",
			"status": "completed",
			"content": []any{map[string]any{
				"type": "output_text", "text": text, "annotations": []any{},
			}},
		}
		output = append([]any{msg}, output...)
	}
	id := r.ID
	if id == "" || len(id) < 5 || id[:5] != "resp_" {
		id = fmt.Sprintf("resp_%d", nowNanos())
	}
	status := "completed"
	var incomplete any
	if r.FinishReason == "length" {
		status = "incomplete"
		incomplete = map[string]any{"reason": "max_output_tokens"}
	}
	obj := map[string]any{
		"id":         id,
		"object":     "response",
		"created_at": nowUnix(),
		"status":     status,
		"model":      model,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  r.Usage.InputTokens,
			"output_tokens": r.Usage.OutputTokens,
			"total_tokens":  r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	}
	if incomplete != nil {
		obj["incomplete_details"] = incomplete
	} else {
		obj["incomplete_details"] = nil
	}
	return json.Marshal(obj)
}
