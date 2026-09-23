// mockupstream is a deterministic test double implementing all three upstream
// protocols (chat completions / responses / anthropic) with streaming, tool
// calls, and error simulation for failover tests.
//
// Behavior contract:
//   - reply text: "MOCK[<proto>] model=<wire model> echo: <last user text>"
//   - user text containing "toolcall" → emits tool call get_weather({"city":"beijing"})
//   - request containing an image → reply prefixed with "[image seen]"
//   - Authorization "Bearer badkey" → 402 insufficient balance
//   - model containing "fail500" → 500; containing "fail429" → 429
//   - stream: word-by-word deltas + usage
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	port := flag.Int("port", 19000, "listen port")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", chatHandler)
	mux.HandleFunc("/v1/responses", responsesHandler)
	mux.HandleFunc("/v1/messages", anthropicHandler)
	// deepseek-style balance endpoint for balance-probe tests
	mux.HandleFunc("/user/balance", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{
			"is_available":   true,
			"balance_infos":  []any{map[string]any{"currency": "CNY", "total_balance": "88.50"}},
		})
	})
	log.Printf("mockupstream on :%d", *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), mux))
}

// ---- shared request decoding (just enough of each protocol) ----

type inbound struct {
	Model  string
	Stream bool
	Text   string // concatenated last-user text
	Image  bool
}

func decodeChat(body []byte) inbound {
	var r struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		Msgs   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream}
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
		Msgs   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream}
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
		Model  string          `json:"model"`
		Stream bool            `json:"stream"`
		Input  json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(body, &r)
	in := inbound{Model: r.Model, Stream: r.Stream}
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
		"model":    in.Model,
		"choices":  []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":    map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
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
