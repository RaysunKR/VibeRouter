package protocol

import (
	"encoding/json"
	"fmt"
	"io"
)

// ---------- Responses API streaming: parser ----------

// ParseResponsesStream converts an upstream Responses SSE stream into
// canonical events. The final EvDone carries the assembled full response.
func ParseResponsesStream(r io.Reader) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		events := make(chan sseEvent, 16)
		go scanSSE(r, events)

		var id, model string
		var text string
		calls := map[int]*callAccumulator{}
		callByItemID := map[string]int{}
		finish := "stop"
		var usage Usage

		for ev := range events {
			var env struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(ev.Data, &env) != nil {
				continue
			}
			switch env.Type {
			case "response.created", "response.in_progress":
				var e struct {
					Response struct {
						ID    string `json:"id"`
						Model string `json:"model"`
					} `json:"response"`
				}
				if json.Unmarshal(ev.Data, &e) == nil {
					if e.Response.ID != "" {
						id = e.Response.ID
					}
					if e.Response.Model != "" {
						model = e.Response.Model
					}
				}
			case "response.output_item.added":
				var e struct {
					OutputIndex int `json:"output_index"`
					Item        struct {
						ID     string `json:"id"`
						Type   string `json:"type"`
						CallID string `json:"call_id"`
						Name   string `json:"name"`
					} `json:"item"`
				}
				if json.Unmarshal(ev.Data, &e) == nil && e.Item.Type == "function_call" {
					callIdx := len(calls)
					cid := e.Item.CallID
					if cid == "" {
						cid = e.Item.ID
					}
					calls[callIdx] = &callAccumulator{id: cid, name: e.Item.Name}
					callByItemID[e.Item.ID] = callIdx
					out <- StreamEvent{Type: EvToolCallStart, Index: callIdx, CallID: cid, ToolName: e.Item.Name}
				}
			case "response.output_text.delta":
				var e struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(ev.Data, &e) == nil && e.Delta != "" {
					text += e.Delta
					out <- StreamEvent{Type: EvTextDelta, Text: e.Delta}
				}
			case "response.function_call_arguments.delta":
				var e struct {
					ItemID string `json:"item_id"`
					Delta  string `json:"delta"`
				}
				if json.Unmarshal(ev.Data, &e) == nil {
					if ci, ok := callByItemID[e.ItemID]; ok {
						calls[ci].args.WriteString(e.Delta)
						out <- StreamEvent{Type: EvToolArgsDelta, Index: ci, Args: e.Delta}
					}
				}
			case "response.completed", "response.incomplete":
				var e struct {
					Response responsesObject `json:"response"`
				}
				if json.Unmarshal(ev.Data, &e) == nil {
					if e.Response.Usage != nil {
						usage = Usage{InputTokens: e.Response.Usage.InputTokens, OutputTokens: e.Response.Usage.OutputTokens}
					}
					if e.Response.IncompleteDetails != nil && e.Response.IncompleteDetails.Reason == "max_output_tokens" {
						finish = "length"
					}
				}
			case "response.failed", "error":
				var e struct {
					Response *struct {
						Error *struct {
							Message string `json:"message"`
						} `json:"error"`
					} `json:"response"`
					Message string `json:"message"`
				}
				if json.Unmarshal(ev.Data, &e) == nil {
					msg := e.Message
					if e.Response != nil && e.Response.Error != nil {
						msg = e.Response.Error.Message
					}
					out <- StreamEvent{Type: EvError, Err: msg}
				}
			}
		}
		if len(calls) > 0 && finish == "stop" {
			finish = "tool_calls"
		}
		out <- StreamEvent{Type: EvDone, FinishReason: finish, Usage: usage,
			Response: assembleResponse(id, model, text, calls, finish, usage)}
	}()
	return out
}

// ---------- Responses API streaming: writer ----------

// WriteResponsesStream renders canonical events as a Responses SSE stream.
func WriteResponsesStream(w io.Writer, events <-chan StreamEvent, model string) error {
	respID := fmt.Sprintf("resp_%d", nowNanos())
	created := nowUnix()
	started := false

	seq := 0
	send := func(payload map[string]any) error {
		payload["sequence_number"] = seq
		seq++
		t, _ := payload["type"].(string)
		if err := writeSSE(w, t, payload); err != nil {
			return err
		}
		flushIf(w)
		return nil
	}
	skeleton := func(status string) map[string]any {
		return map[string]any{
			"id": respID, "object": "response", "created_at": created,
			"status": status, "model": model, "output": []any{},
			"incomplete_details": nil,
			"usage": nil,
		}
	}
	ensureStarted := func() error {
		if started {
			return nil
		}
		started = true
		return send(map[string]any{"type": "response.created", "response": skeleton("in_progress")})
	}

	textItemOpen := false
	textItemID := ""
	outputIndex := 0
	toolItems := map[int]int{} // canonical call index -> output_index

	for ev := range events {
		switch ev.Type {
		case EvTextDelta:
			if err := ensureStarted(); err != nil {
				return err
			}
			if !textItemOpen {
				textItemOpen = true
				textItemID = fmt.Sprintf("msg_%d", nowNanos())
				if err := send(map[string]any{
					"type": "response.output_item.added", "output_index": outputIndex,
					"item": map[string]any{
						"id": textItemID, "type": "message", "role": "assistant",
						"status": "in_progress", "content": []any{},
					},
				}); err != nil {
					return err
				}
				if err := send(map[string]any{
					"type": "response.content_part.added", "item_id": textItemID,
					"output_index": outputIndex, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
				}); err != nil {
					return err
				}
			}
			if err := send(map[string]any{
				"type":          "response.output_text.delta",
				"item_id":       textItemID,
				"output_index":  0, "content_index": 0,
				"delta":         ev.Text,
			}); err != nil {
				return err
			}
		case EvToolCallStart:
			if err := ensureStarted(); err != nil {
				return err
			}
			// text item is intentionally left open: finalize happens at Done
			outputIndex++
			toolItems[ev.Index] = outputIndex
			if err := send(map[string]any{
				"type": "response.output_item.added", "output_index": outputIndex,
				"item": map[string]any{
					"id": ev.CallID, "type": "function_call", "call_id": ev.CallID,
					"name": ev.ToolName, "arguments": "", "status": "in_progress",
				},
			}); err != nil {
				return err
			}
		case EvToolArgsDelta:
			if oi, ok := toolItems[ev.Index]; ok {
				if err := send(map[string]any{
					"type": "response.function_call_arguments.delta",
					"item_id": ev.CallID, "output_index": oi,
					"delta": ev.Args,
				}); err != nil {
					return err
				}
			}
		case EvDone:
			if err := ensureStarted(); err != nil {
				return err
			}
			// Build the completed response object from the assembled canonical response.
			full, err := WriteResponsesResponse(ev.Response, model)
			if err != nil {
				return err
			}
			var obj map[string]any
			if err := json.Unmarshal(full, &obj); err != nil {
				return err
			}
			obj["id"] = respID
			obj["created_at"] = created
			t := "response.completed"
			if obj["status"] == "incomplete" {
				t = "response.incomplete"
			}
			if err := send(map[string]any{"type": t, "response": obj}); err != nil {
				return err
			}
			return nil
		case EvError:
			_ = ensureStarted()
			_ = send(map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id": respID, "object": "response", "created_at": created,
					"status": "failed", "model": model, "output": []any{},
					"error": map[string]any{"code": "upstream_error", "message": ev.Err},
				},
			})
			return nil
		}
	}
	return nil
}
