// mockupstream is a deterministic test double implementing all three upstream
// protocols (chat completions / responses / anthropic) with streaming, tool
// calls, provider web tools, models.dev sync, request inspection, and error
// simulation for E2E tests.
//
// Behavior contract:
//   - reply text: "MOCK[<proto>] model=<wire model> echo: <last user text>"
//   - dispatcher system prompt → JSON tier; "invalid-json" simulates bad output
//   - web_search/web_fetch function → hosted tool call, then final reply after tool result
//   - DeepSeek, Kimi, Bocha, and GLM web endpoints use provider-specific keys and schemas
//   - /api.json serves a models.dev-compatible DeepSeek catalog using the request host
//   - user text containing "toolcall" → emits tool call get_weather({"city":"beijing"})
//   - request containing an image → reply prefixed with "[image seen]"
//   - Authorization "Bearer badkey" → 402 insufficient balance
//   - fail500/fail429/fail403/fail408/fail400 models → corresponding HTTP errors
//   - model containing "slow" → delays the response by about two seconds
//   - stream: word-by-word deltas + usage
//   - GET /__mock/requests lists non-control requests; POST /__mock/reset clears them
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

func main() {
	port := flag.Int("port", 19000, "listen port")
	flag.Parse()
	recorder := &requestRecorder{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/v1/responses", responsesHandler)
	mux.HandleFunc("/v1/messages", anthropicHandler)
	mux.HandleFunc("/api.json", modelsDevHandler)
	mux.HandleFunc("/v1/tools/search", toolsSearchHandler)
	mux.HandleFunc("/v1/tools/fetch", toolsFetchHandler)
	mux.HandleFunc("/v1/web-search", bochaSearchHandler)
	mux.HandleFunc("/api/paas/v4/web_search", glmSearchHandler)
	mux.HandleFunc("/api/paas/v4/reader", glmReaderHandler)
	mux.HandleFunc("/__mock/requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"requests": recorder.list()})
	})
	mux.HandleFunc("/__mock/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		recorder.reset()
		writeJSON(w, map[string]any{"ok": true})
	})
	// deepseek-style balance endpoint for balance-probe tests
	mux.HandleFunc("/user/balance", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"is_available":  true,
			"balance_infos": []any{map[string]any{"currency": "CNY", "total_balance": "88.50"}},
		})
	})
	log.Printf("mockupstream on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), recorder.middleware(mux)))
}

type recordedRequest struct {
	Method        string          `json:"method"`
	Path          string          `json:"path"`
	Authorization string          `json:"authorization"`
	Body          json.RawMessage `json:"body"`
}

type requestRecorder struct {
	mu       sync.RWMutex
	requests []recordedRequest
}

func (r *requestRecorder) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/__mock/requests" || req.URL.Path == "/__mock/reset" {
			next.ServeHTTP(w, req)
			return
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusBadRequest)
			return
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		var rawBody json.RawMessage
		if len(body) > 0 {
			rawBody = json.RawMessage(body)
		}
		r.mu.Lock()
		r.requests = append(r.requests, recordedRequest{
			Method:        req.Method,
			Path:          req.URL.Path,
			Authorization: req.Header.Get("Authorization"),
			Body:          rawBody,
		})
		r.mu.Unlock()
		next.ServeHTTP(w, req)
	})
}

func (r *requestRecorder) list() []recordedRequest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	requests := make([]recordedRequest, len(r.requests))
	copy(requests, r.requests)
	for i := range requests {
		requests[i].Body = json.RawMessage(bytes.Clone(requests[i].Body))
	}
	return requests
}

func (r *requestRecorder) reset() {
	r.mu.Lock()
	r.requests = nil
	r.mu.Unlock()
}

func modelsDevHandler(w http.ResponseWriter, r *http.Request) {
	model := func(id, name string) map[string]any {
		return map[string]any{
			"id": id, "name": name, "family": "deepseek", "attachment": true,
			"reasoning": false, "tool_call": true, "temperature": true,
			"modalities": map[string]any{"input": []string{"text", "image"}, "output": []string{"text"}},
			"limit":      map[string]any{"context": 200001, "output": 8192},
		}
	}
	writeJSON(w, map[string]any{
		"deepseek": map[string]any{
			"id": "deepseek", "env": []string{"DEEPSEEK_API_KEY"},
			"npm": "@ai-sdk/openai-compatible", "api": "http://" + r.Host,
			"name": "DeepSeek", "doc": "https://api-docs.deepseek.com/quick_start/pricing",
			"models": map[string]any{
				"real-chat": model("real-chat", "Real Chat"),
				"real-resp": model("real-resp", "Real Responses"),
				"real-anth": model("real-anth", "Real Anthropic"),
			},
		},
	})
}

func toolsSearchHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("Authorization") {
	case "Bearer web-key":
		writeJSON(w, map[string]any{"results": []any{map[string]any{
			"title": "Mock DeepSeek search result", "url": "https://example.com/search",
			"snippet": "DeepSeek search snippet", "content": "DeepSeek search content",
		}}})
	case "Bearer web-kimi-key":
		writeJSON(w, map[string]any{"search_results": []any{map[string]any{
			"title": "Mock Kimi search result", "url": "https://example.com/kimi/search",
			"snippet": "Kimi search snippet", "text": "Kimi search content",
			"site_name": "Kimi", "date": "2026-01-02",
		}}})
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func toolsFetchHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Header.Get("Authorization") {
	case "Bearer web-key":
		writeJSON(w, map[string]any{
			"title": "Mock DeepSeek fetch result", "content": "# DeepSeek fetched content",
		})
	case "Bearer web-kimi-key":
		writeJSON(w, map[string]any{
			"url": "https://example.com/kimi/fetch", "title": "Mock Kimi fetch result",
			"markdown": "# Kimi fetched content",
		})
	default:
		w.WriteHeader(http.StatusUnauthorized)
	}
}

func bochaSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer web-bocha-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{
		"code": 200,
		"data": map[string]any{"webPages": map[string]any{"value": []any{map[string]any{
			"name": "Mock Bocha search result", "url": "https://example.com/bocha/search",
			"snippet": "Bocha search snippet", "summary": "Bocha search summary",
			"siteName": "Bocha", "datePublished": "2026-01-03",
		}}}},
	})
}

func glmSearchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer web-glm-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"search_result": []any{map[string]any{
		"title": "Mock GLM search result", "link": "https://example.com/glm/search",
		"content": "GLM search content", "media": "GLM", "publish_date": "2026-01-04",
	}}})
}

func glmReaderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer web-glm-key" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"reader_result": map[string]any{
		"url": "https://example.com/glm/fetch", "title": "Mock GLM reader result",
		"content": "# GLM reader content", "description": "GLM reader description",
	}})
}

// ---- shared request decoding (just enough of each protocol) ----

type inbound struct {
	Model         string
	Stream        bool
	Text          string // concatenated last-user text
	System        string
	Image         bool
	HostedTool    string
	HasToolResult bool
}

func decodeChat(body []byte) inbound {
	var r struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		Msgs   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream}
	for _, tool := range r.Tools {
		if tool.Function.Name == "web_search" || tool.Function.Name == "web_fetch" {
			in.HostedTool = tool.Function.Name
		}
	}
	for _, m := range r.Msgs {
		if m.Role == "tool" {
			in.HasToolResult = true
		}
		if m.Role == "system" {
			var system string
			if json.Unmarshal(m.Content, &system) == nil {
				in.System += system
			}
		}
	}
	for i := len(r.Msgs) - 1; i >= 0; i-- {
		m := r.Msgs[i]
		if m.Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			in.Text = s
		} else {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(m.Content, &parts) == nil {
				for _, p := range parts {
					if p.Type == "text" {
						in.Text += p.Text
					}
					if p.Type == "image_url" {
						in.Image = true
					}
				}
			}
		}
		break
	}
	return in
}

func decodeAnthropic(body []byte) inbound {
	var r struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		System string `json:"system"`
		Msgs   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream, System: r.System}
	for i := len(r.Msgs) - 1; i >= 0; i-- {
		m := r.Msgs[i]
		if m.Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			in.Text = s
		} else {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(m.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "text" {
						in.Text += b.Text
					}
					if b.Type == "image" {
						in.Image = true
					}
				}
			}
		}
		break
	}
	return in
}

func decodeResponses(body []byte) inbound {
	var r struct {
		Model        string          `json:"model"`
		Stream       bool            `json:"stream"`
		Instructions string          `json:"instructions"`
		Input        json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream, System: r.Instructions}
	var s string
	if json.Unmarshal(r.Input, &s) == nil {
		in.Text = s
		return in
	}
	var items []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(r.Input, &items) == nil {
		for i := len(items) - 1; i >= 0; i-- {
			if items[i].Role == "user" || items[i].Type == "message" {
				for _, c := range items[i].Content {
					if c.Type == "input_text" || c.Type == "output_text" {
						in.Text += c.Text
					}
					if c.Type == "input_image" {
						in.Image = true
					}
				}
				break
			}
		}
	}
	return in
}

func replyText(proto string, in inbound) string {
	if strings.Contains(in.System, "请求复杂度分类器") {
		if strings.Contains(in.Text, "invalid-json") {
			return "advanced without JSON"
		}
		tier := "basic"
		if strings.Contains(in.Text, "advanced") || strings.Contains(in.Text, "多步推理") {
			tier = "advanced"
		}
		result, _ := json.Marshal(map[string]string{"tier": tier})
		return string(result)
	}
	t := fmt.Sprintf("MOCK[%s] model=%s echo: %s", proto, in.Model, in.Text)
	if in.Image {
		t = "[image seen] " + t
	}
	return t
}

func checkSimulatedError(w http.ResponseWriter, r *http.Request, model string) bool {
	if r.Header.Get("Authorization") == "Bearer badkey" ||
		(model == "" && r.Header.Get("x-api-key") == "badkey") {
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = io.WriteString(w, `{"error":{"message":"Insufficient Balance"}}`)
		return true
	}
	if strings.Contains(model, "fail500") {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
		return true
	}
	if strings.Contains(model, "fail429") {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
		return true
	}
	if strings.Contains(model, "fail403") {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"forbidden"}}`)
		return true
	}
	if strings.Contains(model, "fail408") {
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = io.WriteString(w, `{"error":{"message":"request timeout"}}`)
		return true
	}
	if strings.Contains(model, "fail400") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad request"}}`)
		return true
	}
	if strings.Contains(model, "slow") {
		time.Sleep(2 * time.Second)
	}
	return false
}

func wantsToolCall(in inbound) bool { return strings.Contains(in.Text, "toolcall") }

// ---- chat completions ----

func chatHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	in := decodeChat(body)
	if checkSimulatedError(w, r, in.Model) {
		return
	}
	if in.HostedTool != "" && !in.HasToolResult {
		arguments := `{"query":"latest news","count":1}`
		if in.HostedTool == "web_fetch" {
			arguments = `{"url":"https://example.com/article"}`
		}
		writeJSON(w, map[string]any{
			"id": "chatcmpl-hosted", "object": "chat.completion", "created": time.Now().Unix(), "model": in.Model,
			"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
				"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
					"id": "call_hosted", "type": "function", "function": map[string]any{"name": in.HostedTool, "arguments": arguments},
				}},
			}}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
		return
	}
	text := replyText("chat", in)
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		chunk := func(delta map[string]any, finish any) {
			payload := map[string]any{
				"id": "chatcmpl-mock", "object": "chat.completion.chunk",
				"created": time.Now().Unix(), "model": in.Model,
				"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
			}
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			if f != nil {
				f.Flush()
			}
		}
		chunk(map[string]any{"role": "assistant"}, nil)
		if wantsToolCall(in) {
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "id": "call_mock1", "type": "function",
				"function": map[string]any{"name": "get_weather", "arguments": ""}}}}, nil)
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `{"city":`}}}}, nil)
			chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": 0, "function": map[string]any{"arguments": `"beijing"}`}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
		} else {
			for _, word := range strings.Split(text, " ") {
				chunk(map[string]any{"content": word + " "}, nil)
			}
			chunk(map[string]any{}, "stop")
		}
		usage := map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": in.Model, "choices": []any{},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		}
		data, _ := json.Marshal(usage)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		return
	}

	msg := map[string]any{"role": "assistant", "content": text}
	finish := "stop"
	if wantsToolCall(in) {
		msg["content"] = nil
		msg["tool_calls"] = []any{map[string]any{
			"id": "call_mock1", "type": "function",
			"function": map[string]any{"name": "get_weather", "arguments": `{"city":"beijing"}`},
		}}
		finish = "tool_calls"
	}
	resp := map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion", "created": time.Now().Unix(),
		"model":   in.Model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
	}
	writeJSON(w, resp)
}

// ---- anthropic ----

func anthropicHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	in := decodeAnthropic(body)
	if checkSimulatedError(w, r, in.Model) {
		return
	}
	text := replyText("anthropic", in)
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		send := func(event string, payload any) {
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
			if f != nil {
				f.Flush()
			}
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_mock1", "type": "message", "role": "assistant", "model": in.Model,
			"content": []any{}, "stop_reason": nil, "usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
		}})
		if wantsToolCall(in) {
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "tool_use", "id": "toolu_mock1", "name": "get_weather", "input": map[string]any{}}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"city":"beijing"}`}})
			send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			send("message_delta", map[string]any{"type": "message_delta",
				"delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 5}})
		} else {
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "text", "text": ""}})
			for _, word := range strings.Split(text, " ") {
				send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
					"delta": map[string]any{"type": "text_delta", "text": word + " "}})
			}
			send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
			send("message_delta", map[string]any{"type": "message_delta",
				"delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 5}})
		}
		send("message_stop", map[string]any{"type": "message_stop"})
		return
	}

	var content []any
	stopReason := "end_turn"
	if wantsToolCall(in) {
		content = []any{map[string]any{"type": "tool_use", "id": "toolu_mock1",
			"name": "get_weather", "input": map[string]any{"city": "beijing"}}}
		stopReason = "tool_use"
	} else {
		content = []any{map[string]any{"type": "text", "text": text}}
	}
	writeJSON(w, map[string]any{
		"id": "msg_mock1", "type": "message", "role": "assistant", "model": in.Model,
		"content": content, "stop_reason": stopReason, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
}

// ---- responses ----

func responsesHandler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	in := decodeResponses(body)
	if checkSimulatedError(w, r, in.Model) {
		return
	}
	text := replyText("responses", in)
	respObj := func(status string) map[string]any {
		var output []any
		if wantsToolCall(in) {
			output = []any{map[string]any{
				"type": "function_call", "id": "fc_mock1", "call_id": "fc_mock1",
				"name": "get_weather", "arguments": `{"city":"beijing"}`, "status": "completed",
			}}
		} else {
			output = []any{map[string]any{
				"type": "message", "id": "msg_mock1", "role": "assistant", "status": "completed",
				"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
			}}
		}
		return map[string]any{
			"id": "resp_mock1", "object": "response", "created_at": time.Now().Unix(),
			"status": status, "model": in.Model, "output": output,
			"incomplete_details": nil,
			"usage":              map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		}
	}
	if in.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		seq := 0
		send := func(payload map[string]any) {
			payload["sequence_number"] = seq
			seq++
			t, _ := payload["type"].(string)
			data, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", t, data)
			if f != nil {
				f.Flush()
			}
		}
		created := respObj("in_progress")
		created["output"] = []any{}
		send(map[string]any{"type": "response.created", "response": created})
		if wantsToolCall(in) {
			send(map[string]any{"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"id": "fc_mock1", "type": "function_call", "call_id": "fc_mock1",
					"name": "get_weather", "arguments": "", "status": "in_progress"}})
			send(map[string]any{"type": "response.function_call_arguments.delta",
				"item_id": "fc_mock1", "output_index": 0, "delta": `{"city":"beijing"}`})
		} else {
			send(map[string]any{"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{"id": "msg_mock1", "type": "message", "role": "assistant",
					"status": "in_progress", "content": []any{}}})
			send(map[string]any{"type": "response.content_part.added", "item_id": "msg_mock1",
				"output_index": 0, "content_index": 0,
				"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			for _, word := range strings.Split(text, " ") {
				send(map[string]any{"type": "response.output_text.delta", "item_id": "msg_mock1",
					"output_index": 0, "content_index": 0, "delta": word + " "})
			}
		}
		send(map[string]any{"type": "response.completed", "response": respObj("completed")})
		return
	}
	writeJSON(w, respObj("completed"))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
