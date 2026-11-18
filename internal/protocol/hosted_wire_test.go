package protocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAppendToolExchangeChatCompletions(t *testing.T) {
	body := []byte(`{"model":"gpt-original","messages":[{"role":"user","content":"find x"}],"custom":{"keep":true}}`)
	response := &CanonicalResponse{Parts: []Part{
		{Type: PartText, Text: "I will look it up."},
		{Type: PartToolCall, ToolCallID: "call_1", ToolName: "lookup", ToolInput: json.RawMessage(`{"q":"x"}`)},
	}}
	results := []Part{{Type: PartToolResult, ToolResultID: "call_1", ToolResultContent: `{"answer":42}`}}
	got, err := AppendToolExchange("chat_completions", body, response, results)
	if err != nil {
		t.Fatal(err)
	}
	root := hostedTestObject(t, got)
	if root["model"] != "gpt-original" || !reflect.DeepEqual(root["custom"], map[string]any{"keep": true}) {
		t.Fatalf("original fields not preserved: %+v", root)
	}
	messages := hostedTestArray(t, root, "messages")
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	assistant := hostedTestMap(t, messages[1])
	if assistant["role"] != "assistant" || assistant["content"] != "I will look it up." {
		t.Fatalf("assistant = %+v", assistant)
	}
	calls := assistant["tool_calls"].([]any)
	call := hostedTestMap(t, calls[0])
	function := hostedTestMap(t, call["function"])
	if call["id"] != "call_1" || call["type"] != "function" || function["name"] != "lookup" || function["arguments"] != `{"q":"x"}` {
		t.Fatalf("tool call = %+v", call)
	}
	tool := hostedTestMap(t, messages[2])
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != `{"answer":42}` {
		t.Fatalf("tool result = %+v", tool)
	}
}

func TestAppendToolExchangeResponses(t *testing.T) {
	body := []byte(`{"model":"responses-original","input":"find x","instructions":"keep me","custom":17}`)
	response := &CanonicalResponse{Parts: []Part{
		{Type: PartText, Text: "Searching."},
		{Type: PartToolCall, ToolCallID: "call_resp", ToolName: "lookup", ToolInput: json.RawMessage(`{"q":"x"}`)},
	}}
	results := []Part{{Type: PartToolResult, ToolResultID: "call_resp", ToolResultContent: "result text"}}
	got, err := AppendToolExchange("responses", body, response, results)
	if err != nil {
		t.Fatal(err)
	}
	root := hostedTestObject(t, got)
	if root["model"] != "responses-original" || root["instructions"] != "keep me" || root["custom"] != float64(17) {
		t.Fatalf("original fields not preserved: %+v", root)
	}
	input := hostedTestArray(t, root, "input")
	if len(input) != 4 {
		t.Fatalf("input = %d items, want 4: %+v", len(input), input)
	}
	user := hostedTestMap(t, input[0])
	if user["type"] != "message" || user["role"] != "user" {
		t.Fatalf("converted string input = %+v", user)
	}
	userContent := hostedTestMap(t, user["content"].([]any)[0])
	if userContent["type"] != "input_text" || userContent["text"] != "find x" {
		t.Fatalf("converted content = %+v", userContent)
	}
	assistant := hostedTestMap(t, input[1])
	assistantContent := hostedTestMap(t, assistant["content"].([]any)[0])
	if assistant["role"] != "assistant" || assistantContent["type"] != "output_text" || assistantContent["text"] != "Searching." {
		t.Fatalf("assistant item = %+v", assistant)
	}
	call := hostedTestMap(t, input[2])
	if call["type"] != "function_call" || call["id"] != "call_resp" || call["call_id"] != "call_resp" ||
		call["name"] != "lookup" || call["arguments"] != `{"q":"x"}` {
		t.Fatalf("function call = %+v", call)
	}
	output := hostedTestMap(t, input[3])
	if output["type"] != "function_call_output" || output["call_id"] != "call_resp" || output["output"] != "result text" {
		t.Fatalf("function output = %+v", output)
	}
}

func TestAppendToolExchangeAnthropic(t *testing.T) {
	body := []byte(`{"model":"claude-original","max_tokens":100,"messages":[{"role":"user","content":"find x"}],"custom":"kept"}`)
	response := &CanonicalResponse{Parts: []Part{
		{Type: PartText, Text: "I will look it up."},
		{Type: PartToolCall, ToolCallID: "toolu_1", ToolName: "lookup", ToolInput: json.RawMessage(`{"q":"x"}`)},
	}}
	results := []Part{{Type: PartToolResult, ToolResultID: "toolu_1", ToolResultContent: "42"}}
	got, err := AppendToolExchange("anthropic", body, response, results)
	if err != nil {
		t.Fatal(err)
	}
	root := hostedTestObject(t, got)
	if root["model"] != "claude-original" || root["custom"] != "kept" || root["max_tokens"] != float64(100) {
		t.Fatalf("original fields not preserved: %+v", root)
	}
	messages := hostedTestArray(t, root, "messages")
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(messages))
	}
	assistant := hostedTestMap(t, messages[1])
	content := assistant["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("assistant content = %+v", content)
	}
	text := hostedTestMap(t, content[0])
	call := hostedTestMap(t, content[1])
	if text["type"] != "text" || text["text"] != "I will look it up." {
		t.Fatalf("text block = %+v", text)
	}
	if call["type"] != "tool_use" || call["id"] != "toolu_1" || call["name"] != "lookup" ||
		!reflect.DeepEqual(call["input"], map[string]any{"q": "x"}) {
		t.Fatalf("tool_use = %+v", call)
	}
	user := hostedTestMap(t, messages[2])
	result := hostedTestMap(t, user["content"].([]any)[0])
	if user["role"] != "user" || result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" || result["content"] != "42" {
		t.Fatalf("tool result message = %+v", user)
	}
}

func TestRenderHostedResponseResponses(t *testing.T) {
	body := []byte(`{"id":"resp_1","object":"response","status":"completed","model":"m","output":[{"type":"message","id":"msg_1","content":[]}],"custom":{"keep":true}}`)
	executions := []HostedExecution{
		{
			Kind: HostedWebSearch, CallID: "search-call", Input: json.RawMessage(`{"query":"vibe router"}`),
			Result: json.RawMessage(`{"provider":"test","query":"vibe router","results":[{"title":"Example","url":"https://example.com","snippet":"snippet","content":"content","site_name":"Example","published_at":"2026-01-02"}]}`),
		},
		{Kind: HostedWebFetch, CallID: "fetch-call", Input: json.RawMessage(`{"url":"https://example.com"}`), Error: "offline"},
	}
	got, err := RenderHostedResponse("responses", body, executions)
	if err != nil {
		t.Fatal(err)
	}
	root := hostedTestObject(t, got)
	if root["id"] != "resp_1" || root["model"] != "m" || !reflect.DeepEqual(root["custom"], map[string]any{"keep": true}) {
		t.Fatalf("original fields not preserved: %+v", root)
	}
	output := hostedTestArray(t, root, "output")
	if len(output) != 3 {
		t.Fatalf("output = %d items, want 3", len(output))
	}
	search := hostedTestMap(t, output[0])
	if search["type"] != "web_search_call" || search["status"] != "completed" || search["id"] != hostedTestNativeID("ws_", "search-call") {
		t.Fatalf("search call = %+v", search)
	}
	action := hostedTestMap(t, search["action"])
	if action["type"] != "search" || action["query"] != "vibe router" {
		t.Fatalf("search action = %+v", action)
	}
	source := hostedTestMap(t, action["sources"].([]any)[0])
	if source["type"] != "url" || source["url"] != "https://example.com" || source["title"] != "Example" {
		t.Fatalf("source = %+v", source)
	}
	fetch := hostedTestMap(t, output[1])
	fetchAction := hostedTestMap(t, fetch["action"])
	if fetch["type"] != "web_search_call" || fetch["status"] != "failed" || fetchAction["type"] != "open_page" || fetchAction["url"] != "https://example.com" {
		t.Fatalf("failed fetch = %+v", fetch)
	}
	original := hostedTestMap(t, output[2])
	if original["id"] != "msg_1" {
		t.Fatalf("native records were not prepended: %+v", output)
	}
}

func TestRenderHostedResponseAnthropic(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"c","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2},"custom":"kept"}`)
	executions := []HostedExecution{
		{
			Kind: HostedWebSearch, CallID: "search-ok", Input: json.RawMessage(`{"query":"golang"}`),
			Result: json.RawMessage(`{"provider":"test","query":"golang","results":[{"title":"Go","url":"https://go.dev","snippet":"Go snippet","content":"Go content","site_name":"Go","published_at":"2026-02-03"}]}`),
		},
		{
			Kind: HostedWebFetch, CallID: "fetch-ok", Input: json.RawMessage(`{"url":"https://go.dev"}`),
			Result: json.RawMessage(`{"provider":"test","url":"https://go.dev","title":"The Go Programming Language","content":"# Go\n","format":"markdown"}`),
		},
		{Kind: HostedWebSearch, CallID: "search-error", Input: json.RawMessage(`{"query":"bad"}`), Error: "unavailable"},
		{Kind: HostedWebFetch, CallID: "fetch-error", Input: json.RawMessage(`{"url":"https://bad.example"}`), Error: "unavailable"},
	}
	got, err := RenderHostedResponse("anthropic", body, executions)
	if err != nil {
		t.Fatal(err)
	}
	root := hostedTestObject(t, got)
	if root["custom"] != "kept" || root["stop_reason"] != "end_turn" {
		t.Fatalf("original fields not preserved: %+v", root)
	}
	content := hostedTestArray(t, root, "content")
	if len(content) != 9 {
		t.Fatalf("content = %d blocks, want 9", len(content))
	}
	searchUse := hostedTestMap(t, content[0])
	searchResult := hostedTestMap(t, content[1])
	if searchUse["type"] != "server_tool_use" || searchUse["name"] != "web_search" ||
		searchUse["id"] != hostedTestNativeID("srvtoolu_", "search-ok") || searchResult["tool_use_id"] != searchUse["id"] ||
		!reflect.DeepEqual(searchUse["input"], map[string]any{"query": "golang"}) {
		t.Fatalf("search use/result = %+v / %+v", searchUse, searchResult)
	}
	searchItems := searchResult["content"].([]any)
	item := hostedTestMap(t, searchItems[0])
	if item["type"] != "web_search_result" || item["url"] != "https://go.dev" || item["title"] != "Go" || item["page_age"] != "2026-02-03" {
		t.Fatalf("web search result = %+v", item)
	}
	encoded := item["encrypted_content"].(string)
	if !strings.HasPrefix(encoded, "viberouter:") {
		t.Fatalf("encrypted_content = %q", encoded)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "viberouter:"))
	if err != nil {
		t.Fatal(err)
	}
	var unified map[string]any
	if err := json.Unmarshal(decoded, &unified); err != nil {
		t.Fatal(err)
	}
	if unified["snippet"] != "Go snippet" || unified["content"] != "Go content" || unified["site_name"] != "Go" || unified["published_at"] != "2026-02-03" {
		t.Fatalf("encoded unified item = %+v", unified)
	}

	fetchUse := hostedTestMap(t, content[2])
	fetchResult := hostedTestMap(t, content[3])
	fetchContent := hostedTestMap(t, fetchResult["content"])
	document := hostedTestMap(t, fetchContent["content"])
	source := hostedTestMap(t, document["source"])
	if fetchUse["name"] != "web_fetch" || fetchResult["type"] != "web_fetch_tool_result" ||
		fetchContent["type"] != "web_fetch_result" || fetchContent["url"] != "https://go.dev" ||
		fetchContent["retrieved_at"] == "" || document["title"] != "The Go Programming Language" ||
		document["type"] != "document" || source["type"] != "text" || source["media_type"] != "text/plain" || source["data"] != "# Go\n" {
		t.Fatalf("fetch result = %+v", fetchResult)
	}

	for _, tc := range []struct {
		index      int
		resultType string
	}{
		{5, "web_search_tool_result"},
		{7, "web_fetch_tool_result"},
	} {
		result := hostedTestMap(t, content[tc.index])
		errorContent := hostedTestMap(t, result["content"])
		if result["type"] != tc.resultType || errorContent["type"] != tc.resultType+"_error" || errorContent["error_code"] != "unavailable" {
			t.Fatalf("error block %d = %+v", tc.index, result)
		}
	}
	if hostedTestMap(t, content[8])["text"] != "done" {
		t.Fatalf("native blocks were not prepended: %+v", content)
	}
}

func TestRenderHostedResponseIDsAreDeterministicAndUnique(t *testing.T) {
	body := []byte(`{"id":"resp_1","output":[]}`)
	executions := []HostedExecution{
		{Kind: HostedWebFetch, CallID: "same", Input: json.RawMessage(`{"url":"https://one.example"}`)},
		{Kind: HostedWebFetch, CallID: "same", Input: json.RawMessage(`{"url":"https://two.example"}`)},
	}
	first, err := RenderHostedResponse("responses", body, executions)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderHostedResponse("responses", body, executions)
	if err != nil {
		t.Fatal(err)
	}
	if !bytesEqualJSON(first, second) {
		t.Fatalf("IDs are not deterministic:\n%s\n%s", first, second)
	}
	output := hostedTestArray(t, hostedTestObject(t, first), "output")
	firstID := hostedTestMap(t, output[0])["id"]
	secondID := hostedTestMap(t, output[1])["id"]
	if firstID == secondID {
		t.Fatalf("duplicate native ID %v", firstID)
	}
}

func TestWriteHostedStreamResponses(t *testing.T) {
	base := []byte(`{"id":"resp_stream","object":"response","created_at":123,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]},{"type":"function_call","id":"call_1","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Paris\"}","status":"completed"}],"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}`)
	body, err := RenderHostedResponse("responses", base, []HostedExecution{{
		Kind: HostedWebSearch, CallID: "stream-search", Input: json.RawMessage(`{"query":"hello"}`),
		Result: json.RawMessage(`{"provider":"test","query":"hello","results":[{"title":"Example","url":"https://example.com","snippet":"s"}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := WriteHostedStream(recorder, "responses", body); err != nil {
		t.Fatal(err)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" || !recorder.Flushed {
		t.Fatalf("SSE headers/flush missing: headers=%v flushed=%v", recorder.Header(), recorder.Flushed)
	}
	events := hostedTestSSE(t, recorder.Body.String())
	if len(events) == 0 || events[0].data["type"] != "response.created" {
		t.Fatalf("first event = %+v", events)
	}
	created := hostedTestMap(t, events[0].data["response"])
	if len(created["output"].([]any)) != 0 {
		t.Fatalf("response.created output = %+v", created["output"])
	}
	wanted := []string{
		"response.output_item.added", "response.output_item.done",
		"response.content_part.added", "response.content_part.done",
		"response.output_text.delta", "response.output_text.completed",
		"response.web_search_call.in_progress", "response.web_search_call.searching", "response.web_search_call.completed",
		"response.function_call_arguments.delta",
	}
	for _, eventType := range wanted {
		if !hostedTestHasEvent(events, eventType) {
			t.Errorf("missing %s in\n%s", eventType, recorder.Body.String())
		}
	}
	last := events[len(events)-1]
	if last.data["type"] != "response.completed" {
		t.Fatalf("last event = %+v", last)
	}
	var wantResponse, gotResponse any
	if err := json.Unmarshal(body, &wantResponse); err != nil {
		t.Fatal(err)
	}
	rawResponse, err := json.Marshal(last.data["response"])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawResponse, &gotResponse); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotResponse, wantResponse) {
		t.Fatalf("completed response differs\ngot:  %+v\nwant: %+v", gotResponse, wantResponse)
	}
}

func TestWriteHostedStreamAnthropic(t *testing.T) {
	base := []byte(`{"id":"msg_stream","type":"message","role":"assistant","model":"c","content":[{"type":"text","text":"hello"},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Paris"}}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":4}}`)
	body, err := RenderHostedResponse("anthropic", base, []HostedExecution{{
		Kind: HostedWebSearch, CallID: "stream-search", Input: json.RawMessage(`{"query":"hello"}`),
		Result: json.RawMessage(`{"provider":"test","query":"hello","results":[{"title":"Example","url":"https://example.com","snippet":"s"}]}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := WriteHostedStream(recorder, "anthropic", body); err != nil {
		t.Fatal(err)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" || !recorder.Flushed {
		t.Fatalf("SSE headers/flush missing: headers=%v flushed=%v", recorder.Header(), recorder.Flushed)
	}
	events := hostedTestSSE(t, recorder.Body.String())
	if len(events) == 0 || events[0].data["type"] != "message_start" {
		t.Fatalf("first event = %+v", events)
	}
	start := hostedTestMap(t, events[0].data["message"])
	if len(start["content"].([]any)) != 0 || start["id"] != "msg_stream" {
		t.Fatalf("message_start = %+v", start)
	}
	for _, eventType := range []string{"content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !hostedTestHasEvent(events, eventType) {
			t.Errorf("missing %s in\n%s", eventType, recorder.Body.String())
		}
	}
	var textDelta, serverInputDelta, regularInputDelta bool
	for _, event := range events {
		if event.data["type"] != "content_block_delta" {
			continue
		}
		delta := hostedTestMap(t, event.data["delta"])
		switch delta["type"] {
		case "text_delta":
			textDelta = delta["text"] == "hello"
		case "input_json_delta":
			partial := delta["partial_json"].(string)
			serverInputDelta = serverInputDelta || strings.Contains(partial, "hello")
			regularInputDelta = regularInputDelta || strings.Contains(partial, "Paris")
		}
	}
	if !textDelta || !serverInputDelta || !regularInputDelta {
		t.Fatalf("deltas: text=%v server=%v regular=%v\n%s", textDelta, serverInputDelta, regularInputDelta, recorder.Body.String())
	}
	if events[len(events)-1].data["type"] != "message_stop" {
		t.Fatalf("last event = %+v", events[len(events)-1])
	}
	messageDelta := events[len(events)-2]
	delta := hostedTestMap(t, messageDelta.data["delta"])
	usage := hostedTestMap(t, messageDelta.data["usage"])
	if messageDelta.data["type"] != "message_delta" || delta["stop_reason"] != "end_turn" || usage["output_tokens"] != float64(4) {
		t.Fatalf("message_delta = %+v", messageDelta.data)
	}
}

type hostedTestSSEEvent struct {
	name string
	data map[string]any
}

func hostedTestSSE(t *testing.T, stream string) []hostedTestSSEEvent {
	t.Helper()
	ch := make(chan sseEvent, 128)
	go scanSSE(strings.NewReader(stream), ch)
	var events []hostedTestSSEEvent
	for event := range ch {
		var data map[string]any
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatalf("invalid SSE data %q: %v", event.Data, err)
		}
		events = append(events, hostedTestSSEEvent{name: event.Event, data: data})
	}
	return events
}

func hostedTestHasEvent(events []hostedTestSSEEvent, eventType string) bool {
	for _, event := range events {
		if event.data["type"] == eventType && event.name == eventType {
			return true
		}
	}
	return false
}

func hostedTestObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func hostedTestArray(t *testing.T, object map[string]any, field string) []any {
	t.Helper()
	array, ok := object[field].([]any)
	if !ok {
		t.Fatalf("%s is not an array: %+v", field, object[field])
	}
	return array
}

func hostedTestMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %+v", value)
	}
	return object
}

func hostedTestNativeID(prefix, callID string) string {
	sum := sha256.Sum256([]byte(callID))
	return prefix + hex.EncodeToString(sum[:12])
}

func bytesEqualJSON(a, b []byte) bool {
	var av, bv any
	return json.Unmarshal(a, &av) == nil && json.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}
