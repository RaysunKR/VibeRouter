package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---------- Anthropic streaming: parser ----------

// ParseAnthropicStream converts an upstream Anthropic SSE stream into
// canonical events. The final EvDone carries the assembled full response.
func ParseAnthropicStream(r io.Reader) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		events := make(chan sseEvent, 16)
		go scanSSE(r, events)

		var id, model string
		var text strings.Builder
		calls := map[int]*callAccumulator{}
		blockCall := map[int]int{} // anthropic block index -> call index
		finish := "stop"
		var usage Usage

		for ev := range events {
			switch ev.Event {
			case "message_start":
				var m struct {
					Message struct {
						ID    string `json:"id"`
						Model string `json:"model"`
						Usage *struct {
							InputTokens int `json:"input_tokens"`
						} `json:"usage"`
					} `json:"message"`
				}
				if json.Unmarshal(ev.Data, &m) == nil {
					id, model = m.Message.ID, m.Message.Model
					if m.Message.Usage != nil {
						usage.InputTokens = m.Message.Usage.InputTokens
					}
				}
			case "content_block_start":
				var b struct {
					Index        int `json:"index"`
					ContentBlock struct {
						Type string `json:"type"`
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"content_block"`
				}
				if json.Unmarshal(ev.Data, &b) == nil && b.ContentBlock.Type == "tool_use" {
					callIdx := len(calls)
					calls[callIdx] = &callAccumulator{id: b.ContentBlock.ID, name: b.ContentBlock.Name}
					blockCall[b.Index] = callIdx
					out <- StreamEvent{Type: EvToolCallStart, Index: callIdx, CallID: b.ContentBlock.ID, ToolName: b.ContentBlock.Name}
				}
			case "content_block_delta":
				var d struct {
					Index int `json:"index"`
					Delta struct {
						Type        string `json:"type"` // text_delta | input_json_delta
						Text        string `json:"text"`
						PartialJSON string `json:"partial_json"`
					} `json:"delta"`
				}
				if json.Unmarshal(ev.Data, &d) != nil {
					continue
				}
				switch d.Delta.Type {
				case "text_delta":
					if d.Delta.Text != "" {
						text.WriteString(d.Delta.Text)
						out <- StreamEvent{Type: EvTextDelta, Text: d.Delta.Text}
					}
				case "input_json_delta":
					if ci, ok := blockCall[d.Index]; ok && d.Delta.PartialJSON != "" {
						calls[ci].args.WriteString(d.Delta.PartialJSON)
						out <- StreamEvent{Type: EvToolArgsDelta, Index: ci, Args: d.Delta.PartialJSON}
					}
				}
			case "message_delta":
				var m struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
					Usage *struct {
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				}
				if json.Unmarshal(ev.Data, &m) == nil {
					if m.Delta.StopReason != "" {
						finish = normalizeFinish(m.Delta.StopReason)
					}
					if m.Usage != nil {
						usage.OutputTokens = m.Usage.OutputTokens
					}
				}
			case "error":
				var e struct {
					Error struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(ev.Data, &e) == nil {
					out <- StreamEvent{Type: EvError, Err: e.Error.Message}
				}
			}
		}
		out <- StreamEvent{Type: EvDone, FinishReason: finish, Usage: usage,
			Response: assembleResponse(id, model, text.String(), calls, finish, usage)}
	}()
	return out
}

// ---------- Anthropic streaming: writer ----------

// WriteAnthropicStream renders canonical events as an Anthropic SSE stream.
func WriteAnthropicStream(w io.Writer, events <-chan StreamEvent, model string) error {
	msgID := fmt.Sprintf("msg_%d", nowNanos())
	started := false
	blockOpen := false
	blockKind := "" // "text" | "tool"
	blockIdx := -1
	var inputTokens int

	startMessage := func() error {
		started = true
		return writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": msgID, "type": "message", "role": "assistant", "model": model,
				"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": 0},
			},
		})
	}
	closeBlock := func() error {
		if !blockOpen {
			return nil
		}
		blockOpen = false
		return writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIdx})
	}

	for ev := range events {
		if !started && ev.Type != EvDone && ev.Type != EvError {
			if err := startMessage(); err != nil {
				return err
			}
		}
		switch ev.Type {
		case EvTextDelta:
			if !blockOpen || blockKind != "text" {
				if err := closeBlock(); err != nil {
					return err
				}
				blockIdx++
				blockOpen = true
				blockKind = "text"
				if err := writeSSE(w, "content_block_start", map[string]any{
					"type": "content_block_start", "index": blockIdx,
					"content_block": map[string]any{"type": "text", "text": ""},
				}); err != nil {
					return err
				}
			}
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": blockIdx,
				"delta": map[string]any{"type": "text_delta", "text": ev.Text},
			}); err != nil {
				return err
			}
			flushIf(w)
		case EvToolCallStart:
			if err := closeBlock(); err != nil {
				return err
			}
			blockIdx++
			blockOpen = true
			blockKind = "tool"
			if err := writeSSE(w, "content_block_start", map[string]any{
				"type": "content_block_start", "index": blockIdx,
				"content_block": map[string]any{
					"type": "tool_use", "id": ev.CallID, "name": ev.ToolName, "input": map[string]any{},
				},
			}); err != nil {
				return err
			}
			flushIf(w)
		case EvToolArgsDelta:
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": blockIdx,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": ev.Args},
			}); err != nil {
				return err
			}
			flushIf(w)
		case EvDone:
			if !started {
				inputTokens = ev.Usage.InputTokens
				if err := startMessage(); err != nil {
					return err
				}
			}
			if err := closeBlock(); err != nil {
				return err
			}
			stopReason := "end_turn"
			switch ev.FinishReason {
			case "length":
				stopReason = "max_tokens"
			case "tool_calls":
				stopReason = "tool_use"
			}
			if err := writeSSE(w, "message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
				"usage": map[string]any{"output_tokens": ev.Usage.OutputTokens},
			}); err != nil {
				return err
			}
			if err := writeSSE(w, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
				return err
			}
			flushIf(w)
			return nil
		case EvError:
			_ = writeSSE(w, "error", map[string]any{
				"type":  "error",
				"error": map[string]any{"type": "api_error", "message": ev.Err},
			})
			flushIf(w)
			return nil
		}
	}
	return nil
}
