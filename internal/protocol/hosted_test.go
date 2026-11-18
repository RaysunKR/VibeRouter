package protocol

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseHostedWebTools(t *testing.T) {
	responses := []byte(`{
		"model":"test","input":"search","tools":[
			{"type":"web_search","filters":{"allowed_domains":["example.com"]}},
			{"type":"web_fetch","max_uses":3},
			{"type":"function","name":"weather","parameters":{"type":"object"}}
		]
	}`)
	req, err := ParseResponsesRequest(responses)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.HostedTools) != 2 || len(req.Tools) != 1 {
		t.Fatalf("hosted=%+v regular=%+v", req.HostedTools, req.Tools)
	}
	if req.HostedTools[0].Kind != HostedWebSearch || req.HostedTools[0].AllowedDomains[0] != "example.com" ||
		req.HostedTools[1].Kind != HostedWebFetch || req.HostedTools[1].MaxUses != 3 {
		t.Fatalf("unexpected Responses hosted tools: %+v", req.HostedTools)
	}

	anthropic := []byte(`{
		"model":"test","max_tokens":100,"messages":[{"role":"user","content":"search"}],"tools":[
			{"type":"web_search_20250305","name":"web_search","max_uses":2,"blocked_domains":["bad.example"]},
			{"type":"web_fetch_20250910","name":"web_fetch"},
			{"name":"weather","input_schema":{"type":"object"}}
		]
	}`)
	req, err = ParseAnthropicRequest(anthropic)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.HostedTools) != 2 || len(req.Tools) != 1 || req.HostedTools[0].MaxUses != 2 ||
		req.HostedTools[0].BlockedDomains[0] != "bad.example" || req.HostedTools[1].Kind != HostedWebFetch {
		t.Fatalf("unexpected Anthropic hosted tools: %+v regular=%+v", req.HostedTools, req.Tools)
	}
}

func TestParseHostedWebHistory(t *testing.T) {
	responses := []byte(`{"model":"test","input":[
		{"type":"web_search_call","action":{"type":"search","query":"news"},"sources":[{"type":"url","url":"https://example.com","title":"Example"}]},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Answer"}]}
	]}`)
	req, err := ParseResponsesRequest(responses)
	if err != nil {
		t.Fatal(err)
	}
	if !req.HostedHistory || len(req.Messages) != 2 || !strings.Contains(req.Messages[0].Text(), "https://example.com") {
		t.Fatalf("Responses hosted history: %+v", req)
	}

	encoded := base64.RawURLEncoding.EncodeToString([]byte(`{"title":"Example","url":"https://example.com","content":"details"}`))
	anthropic := []byte(`{"model":"test","max_tokens":100,"messages":[
		{"role":"assistant","content":[
			{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"news"}},
			{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://example.com","title":"Example","encrypted_content":"viberouter:` + encoded + `"}]},
			{"type":"web_fetch_tool_result","content":{"type":"web_fetch_result","url":"https://example.com","content":{"type":"document","title":"Example","source":{"type":"text","data":"Full body"}}}}
		]}
	]}`)
	req, err = ParseAnthropicRequest(anthropic)
	if err != nil {
		t.Fatal(err)
	}
	text := req.Messages[0].Text()
	if !req.HostedHistory || !strings.Contains(text, "details") || !strings.Contains(text, "Full body") {
		t.Fatalf("Anthropic hosted history: %+v", req)
	}
}
