// Package protocol defines the canonical intermediate representation used to
// convert between OpenAI Chat Completions, OpenAI Responses, and Anthropic
// Messages APIs, plus per-protocol parsers/serializers for streaming and
// non-streaming traffic.
package protocol

import (
	"encoding/json"
	"strings"
)

// Part types inside a canonical message.
const (
	PartText       = "text"
	PartImage      = "image"       // ImageURL holds http(s):// or data: URL
	PartToolCall   = "tool_call"   // assistant requesting a tool
	PartToolResult = "tool_result" // user/tool supplying a result
)

type Part struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	ImageURL string `json:"image_url,omitempty"`

	// tool_call
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`

	// tool_result
	ToolResultID      string `json:"tool_result_id,omitempty"` // id of the call this answers
	ToolResultContent string `json:"tool_result_content,omitempty"`
}

type Message struct {
	Role  string `json:"role"` // system | user | assistant | tool
	Parts []Part `json:"parts"`
}

// Text concatenates the text parts of a message.
func (m *Message) Text() string {
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == PartText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ToolChoice: "auto" | "none" | "required" | specific tool name.
type ToolChoice struct {
	Mode string `json:"mode"`
	Tool string `json:"tool,omitempty"`
}

type HostedTool struct {
	Kind           string   `json:"kind"` // web_search | web_fetch
	MaxUses        int      `json:"max_uses,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type CanonicalRequest struct {
	Model         string       `json:"model"`
	Messages      []Message    `json:"messages"`
	System        string       `json:"system,omitempty"`
	Tools         []Tool       `json:"tools,omitempty"`
	HostedTools   []HostedTool `json:"hosted_tools,omitempty"`
	HostedHistory bool         `json:"hosted_history,omitempty"`
	ToolChoice    *ToolChoice  `json:"tool_choice,omitempty"`
	MaxTokens     int          `json:"max_tokens,omitempty"`
	Temperature   *float64     `json:"temperature,omitempty"`
	TopP          *float64     `json:"top_p,omitempty"`
	Stop          []string     `json:"stop,omitempty"`
	Stream        bool         `json:"stream,omitempty"`
}

// HasMultimodal reports whether any message contains a non-text part.
func (r *CanonicalRequest) HasMultimodal() bool {
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			if p.Type == PartImage {
				return true
			}
		}
	}
	return false
}

// EstimateTokens approximates input tokens as len(text)/4 plus a fixed cost
// per image, sufficient for the long-context routing threshold.
func (r *CanonicalRequest) EstimateTokens() int {
	n := len(r.System) / 4
	for _, m := range r.Messages {
		for _, p := range m.Parts {
			switch p.Type {
			case PartImage:
				n += 1000
			default:
				n += len(p.Text)/4 + len(p.ToolInput)/4 + len(p.ToolResultContent)/4
			}
		}
	}
	return n
}

// Usage is normalized across protocols.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type CanonicalResponse struct {
	ID           string `json:"id"`
	Model        string `json:"model"`
	Parts        []Part `json:"parts"`         // assistant message parts (text / tool_call)
	FinishReason string `json:"finish_reason"` // stop | length | tool_calls
	Usage        Usage  `json:"usage"`
}

// --- streaming ---

// Stream event types.
const (
	EvTextDelta     = "text_delta"
	EvToolCallStart = "tool_call_start"
	EvToolArgsDelta = "tool_args_delta"
	EvDone          = "done"
	EvError         = "error"
)

type StreamEvent struct {
	Type string

	// text delta
	Text string

	// tool call deltas (Index correlates start/args within one response)
	Index    int
	CallID   string
	ToolName string
	Args     string // JSON fragment

	// done
	FinishReason string
	Usage        Usage
	Response     *CanonicalResponse // full assembled response (done only)

	// error
	Err string
}
