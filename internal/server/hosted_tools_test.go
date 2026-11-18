package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

func TestHostedWebToolsEndToEnd(t *testing.T) {
	for _, test := range []struct {
		name        string
		clientProto string
		tool        string
		stream      bool
	}{
		{"Responses search", config.ProtocolResponses, "web_search", false},
		{"Anthropic search", config.ProtocolAnthropic, "web_search", false},
		{"Responses fetch stream", config.ProtocolResponses, "web_fetch", true},
		{"Anthropic fetch stream", config.ProtocolAnthropic, "web_fetch", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			toolRequests := make(chan map[string]any, 1)
			toolCalls := atomic.Int32{}
			toolUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				toolCalls.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if r.Header.Get("Authorization") != "Bearer web-key" {
					t.Errorf("tool authorization = %q", r.Header.Get("Authorization"))
				}
				toolRequests <- map[string]any{"path": r.URL.Path, "body": body}
				w.Header().Set("Content-Type", "application/json")
				if test.tool == "web_search" {
					if r.URL.Path != "/v1/tools/search" {
						t.Errorf("search path = %s", r.URL.Path)
					}
					_, _ = io.WriteString(w, `{"results":[{"title":"Example","url":"https://example.com/news","snippet":"News snippet","content":"Full news"}]}`)
					return
				}
				if r.URL.Path != "/v1/tools/fetch" {
					t.Errorf("fetch path = %s", r.URL.Path)
				}
				_, _ = io.WriteString(w, `{"title":"Example page","content":"# Fetched body"}`)
			}))
			defer toolUpstream.Close()

			modelRequests := make(chan map[string]any, 2)
			modelCalls := atomic.Int32{}
			modelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				modelRequests <- body
				if body["stream"] == true {
					t.Error("hosted loop upstream request must be non-streaming")
				}
				w.Header().Set("Content-Type", "application/json")
				if modelCalls.Add(1) == 1 {
					arguments := `{"query":"latest news","count":1}`
					if test.tool == "web_fetch" {
						arguments = `{"url":"https://example.com/article"}`
					}
					writeHostedChatResponse(t, w, test.tool, arguments, false)
					return
				}
				writeHostedChatResponse(t, w, "", "", true)
			}))
			defer modelUpstream.Close()

			ts, cfg, _ := newTestServer(t, modelUpstream.URL)
			if err := cfg.Mutate(func(c *config.Config) error {
				c.WebTools.Enabled = true
				c.WebTools.SearchProvider = "deepseek"
				c.WebTools.FetchProvider = "deepseek"
				c.WebTools.MaxCalls = 4
				c.WebTools.Providers = []config.WebToolProviderConfig{{ID: "deepseek", Enabled: true, APIKey: "web-key", BaseURL: toolUpstream.URL}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}

			var request string
			if test.clientProto == config.ProtocolResponses {
				request = `{"model":"direct","input":"Use the web","stream":` + jsonBool(test.stream) + `,"tools":[{"type":"` + test.tool + `"}]}`
			} else {
				wireType := "web_search_20250305"
				if test.tool == "web_fetch" {
					wireType = "web_fetch_20250910"
				}
				request = `{"model":"direct","max_tokens":500,"stream":` + jsonBool(test.stream) + `,"messages":[{"role":"user","content":"Use the web"}],"tools":[{"type":"` + wireType + `","name":"` + test.tool + `"}]}`
			}
			path := "/v1/responses"
			if test.clientProto == config.ProtocolAnthropic {
				path = "/v1/messages"
			}
			status, responseBody := doReq(t, http.MethodPost, ts.URL+path, "vr-testkey", request)
			if status != http.StatusOK {
				t.Fatalf("hosted request: status=%d body=%s", status, responseBody)
			}
			if toolCalls.Load() != 1 || modelCalls.Load() != 2 {
				t.Fatalf("tool calls=%d model calls=%d", toolCalls.Load(), modelCalls.Load())
			}

			firstModelRequest := <-modelRequests
			secondModelRequest := <-modelRequests
			toolsJSON, _ := json.Marshal(firstModelRequest["tools"])
			if !strings.Contains(string(toolsJSON), `"type":"function"`) || !strings.Contains(string(toolsJSON), test.tool) {
				t.Fatalf("model tools = %s", toolsJSON)
			}
			messagesJSON, _ := json.Marshal(secondModelRequest["messages"])
			if !strings.Contains(string(messagesJSON), `"role":"tool"`) || !strings.Contains(string(messagesJSON), "Example") {
				t.Fatalf("tool exchange was not appended: %s", messagesJSON)
			}
			toolRequest := <-toolRequests
			toolBody := toolRequest["body"].(map[string]any)
			if test.tool == "web_search" && toolBody["query"] != "latest news" || test.tool == "web_fetch" && toolBody["url"] != "https://example.com/article" {
				t.Fatalf("tool request = %+v", toolRequest)
			}

			if test.stream {
				if !strings.Contains(responseBody, "Final answer") || !strings.Contains(responseBody, "web_") {
					t.Fatalf("hosted stream = %s", responseBody)
				}
				if test.clientProto == config.ProtocolResponses && !strings.Contains(responseBody, "response.completed") {
					t.Fatalf("Responses stream missing completion: %s", responseBody)
				}
				if test.clientProto == config.ProtocolAnthropic && !strings.Contains(responseBody, "message_stop") {
					t.Fatalf("Anthropic stream missing completion: %s", responseBody)
				}
				return
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(responseBody), &decoded); err != nil {
				t.Fatal(err)
			}
			if test.clientProto == config.ProtocolResponses {
				output := decoded["output"].([]any)
				if output[0].(map[string]any)["type"] != "web_search_call" || !strings.Contains(responseBody, "Final answer") {
					t.Fatalf("Responses hosted output = %s", responseBody)
				}
			} else {
				content := decoded["content"].([]any)
				if content[0].(map[string]any)["type"] != "server_tool_use" || !strings.Contains(responseBody, "Final answer") {
					t.Fatalf("Anthropic hosted output = %s", responseBody)
				}
			}
		})
	}
}

func TestHostedMaxUses(t *testing.T) {
	s := &Server{}
	call := protocolPartForHostedTest("web_search", `{"query":"again"}`)
	result, execution := s.executeHostedCall(&http.Request{}, []protocol.HostedTool{{Kind: "web_search", MaxUses: 1}}, call, 2)
	if execution.Error != "max_uses_exceeded" || !strings.Contains(result.ToolResultContent, "max_uses_exceeded") {
		t.Fatalf("max uses result=%+v execution=%+v", result, execution)
	}
}

func TestMCPRouteUsesClientKey(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, cfg, _ := newTestServer(t, up.URL)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.WebTools.MCPEnabled = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`
	status, _ := doReq(t, http.MethodPost, ts.URL+"/mcp", "", body)
	if status != http.StatusUnauthorized {
		t.Fatalf("MCP without key: status=%d", status)
	}
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer vr-testkey")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP with client key: status=%d", resp.StatusCode)
	}
}

func writeHostedChatResponse(t *testing.T, w http.ResponseWriter, tool, arguments string, final bool) {
	t.Helper()
	message := map[string]any{"role": "assistant", "content": "Final answer with https://example.com"}
	finish := "stop"
	if !final {
		message["content"] = nil
		message["tool_calls"] = []any{map[string]any{
			"id": "call_web_1", "type": "function",
			"function": map[string]any{"name": tool, "arguments": arguments},
		}}
		finish = "tool_calls"
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-hosted", "object": "chat.completion", "created": 1, "model": "real-model",
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	})
}

func jsonBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func protocolPartForHostedTest(name, input string) protocol.Part {
	return protocol.Part{Type: protocol.PartToolCall, ToolCallID: "call-1", ToolName: name, ToolInput: json.RawMessage(input)}
}
