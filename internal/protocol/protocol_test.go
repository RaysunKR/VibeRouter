package protocol

import (
	"strings"
	"testing"
)

const sampleChatReq = `{
  "model": "gpt-4o",
  "stream": false,
  "temperature": 0.7,
  "max_tokens": 1024,
  "messages": [
    {"role": "system", "content": "You are helpful."},
    {"role": "user", "content": [{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},
    {"role": "assistant", "content": null, "tool_calls": [{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},
    {"role": "tool", "tool_call_id": "call_1", "content": "42"}
  ],
  "tools": [{"type":"function","function":{"name":"lookup","description":"look up","parameters":{"type":"object"}}}],
  "tool_choice": "auto"
}`

func TestChatRoundTrip(t *testing.T) {
	cr, err := ParseChatRequest([]byte(sampleChatReq))
	if err != nil {
		t.Fatal(err)
	}
	if cr.System != "You are helpful." {
		t.Fatalf("system not hoisted: %q", cr.System)
	}
	if !cr.HasMultimodal() {
		t.Fatal("expected multimodal")
	}
	if len(cr.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(cr.Messages))
	}
	// assistant tool call
	found := false
	for _, m := range cr.Messages {
		for _, p := range m.Parts {
			if p.Type == PartToolCall && p.ToolName == "lookup" {
				found = true
			}
			if p.Type == PartToolResult && p.ToolResultContent != "42" {
				t.Fatalf("tool result content = %q", p.ToolResultContent)
			}
		}
	}
	if !found {
		t.Fatal("tool call lost")
	}
	if len(cr.Tools) != 1 || cr.Tools[0].Name != "lookup" {
		t.Fatal("tools lost")
	}

	// canonical -> anthropic -> canonical
	ab, err := BuildAnthropicRequest(cr, "claude-test")
	if err != nil {
		t.Fatal(err)
	}
	cr2, err := ParseAnthropicRequest(ab)
	if err != nil {
		t.Fatal(err)
	}
	if cr2.System != cr.System || len(cr2.Messages) != len(cr.Messages) {
		t.Fatalf("anthropic round trip mismatch: %+v", cr2)
	}
	if !cr2.HasMultimodal() {
		t.Fatal("multimodal lost via anthropic")
	}

	// canonical -> responses -> canonical
	rb, err := BuildResponsesRequest(cr, "gpt-test")
	if err != nil {
		t.Fatal(err)
	}
	cr3, err := ParseResponsesRequest(rb)
	if err != nil {
		t.Fatal(err)
	}
	if cr3.System != cr.System || !cr3.HasMultimodal() {
		t.Fatalf("responses round trip mismatch: %+v", cr3)
	}
	found = false
	for _, m := range cr3.Messages {
		for _, p := range m.Parts {
			if p.Type == PartToolCall && p.ToolName == "lookup" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("tool call lost via responses")
	}
}

func TestChatResponseRoundTrip(t *testing.T) {
	upstream := `{"id":"chatcmpl-1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hi","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`
	cr, err := ParseChatResponse([]byte(upstream))
	if err != nil {
		t.Fatal(err)
	}
	if cr.FinishReason != "tool_calls" || cr.Usage.InputTokens != 3 || cr.Usage.OutputTokens != 5 {
		t.Fatalf("bad parse: %+v", cr)
	}

	// -> anthropic response
	ab, err := WriteAnthropicResponse(cr, "m")
	if err != nil {
		t.Fatal(err)
	}
	cr2, err := ParseAnthropicResponse(ab)
	if err != nil {
		t.Fatal(err)
	}
	if cr2.FinishReason != "tool_calls" {
		t.Fatalf("anthropic finish = %q", cr2.FinishReason)
	}

	// -> responses object
	rb, err := WriteResponsesResponse(cr, "m")
	if err != nil {
		t.Fatal(err)
	}
	cr3, err := ParseResponsesResponse(rb)
	if err != nil {
		t.Fatal(err)
	}
	if cr3.FinishReason != "tool_calls" {
		t.Fatalf("responses finish = %q", cr3.FinishReason)
	}
}

func TestEstimateTokens(t *testing.T) {
	cr := &CanonicalRequest{
		System:   strings.Repeat("a", 400),
		Messages: []Message{{Role: "user", Parts: []Part{{Type: PartText, Text: strings.Repeat("b", 4000)}}}},
	}
	est := cr.EstimateTokens()
	if est < 1000 || est > 1200 {
		t.Fatalf("estimate = %d, want ~1100", est)
	}
}
