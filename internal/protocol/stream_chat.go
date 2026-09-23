package protocol

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ---------- SSE plumbing ----------

type sseEvent struct {
	Event string // value of "event:" line, may be empty
	Data  []byte // concatenated "data:" lines
}

// scanSSE yields SSE events from r until EOF or error. Terminal "[DONE]"
// payloads are consumed (not emitted).
func scanSSE(r io.Reader, out chan<- sseEvent) {
	defer close(out)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var ev sseEvent
	flush := func() {
		if ev.Data == nil && ev.Event == "" {
			return
		}
		if !bytes.Equal(bytes.TrimSpace(ev.Data), []byte("[DONE]")) {
			out <- ev
		}
		ev = sseEvent{}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line, "data:")
			d = strings.TrimPrefix(d, " ")
			if ev.Data != nil {
				ev.Data = append(ev.Data, '\n')
			}
			ev.Data = append(ev.Data, d...)
		default:
			// comments (": ping") and stray lines ignored
		}
	}
	flush()
}

func writeSSE(w io.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// flushIf flushes w when it supports http.Flusher.
func flushIf(w io.Writer) {
	if f, ok := w.(interface{ Flush() }); ok {
		f.Flush()
	}
}

// ---------- Chat Completions streaming ----------

type chatChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string         `json:"role,omitempty"`
			Content   string         `json:"content,omitempty"`
			ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices,omitempty"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

// ParseChatStream converts an upstream chat.completions SSE stream into
// canonical events. The final EvDone carries the assembled full response.
func ParseChatStream(r io.Reader) <-chan StreamEvent {
	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		events := make(chan sseEvent, 16)
		go scanSSE(r, events)

		var id, model string
		var text strings.Builder
		calls := map[int]*callAccumulator{}
		finish := "stop"
		var usage Usage

		for ev := range events {
			var chunk chatChunk
			if json.Unmarshal(ev.Data, &chunk) != nil {
				continue
			}
			if chunk.ID != "" {
				id = chunk.ID
			}
			if chunk.Model != "" {
				model = chunk.Model
			}
			if chunk.Usage != nil {
				usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
			}
			for _, ch := range chunk.Choices {
				if ch.Delta.Content != "" {
					text.WriteString(ch.Delta.Content)
					out <- StreamEvent{Type: EvTextDelta, Text: ch.Delta.Content}
				}
				for _, tc := range ch.Delta.ToolCalls {
					idx := tc.Index
					acc := calls[idx]
					if acc == nil {
						acc = &callAccumulator{}
						calls[idx] = acc
					}
					if tc.ID != "" {
						acc.id = tc.ID
						acc.name = tc.Function.Name
						out <- StreamEvent{Type: EvToolCallStart, Index: idx, CallID: tc.ID, ToolName: tc.Function.Name}
					}
					if tc.Function.Arguments != "" {
						acc.args.WriteString(tc.Function.Arguments)
						out <- StreamEvent{Type: EvToolArgsDelta, Index: idx, Args: tc.Function.Arguments}
					}
				}
				if ch.FinishReason != nil && *ch.FinishReason != "" {
					finish = normalizeFinish(*ch.FinishReason)
				}
			}
		}
		out <- StreamEvent{Type: EvDone, FinishReason: finish, Usage: usage,
			Response: assembleResponse(id, model, text.String(), calls, finish, usage)}
	}()
	return out
}

type callAccumulator struct {
	id, name string
	args     strings.Builder
}

func assembleResponse(id, model, text string, calls map[int]*callAccumulator, finish string, usage Usage) *CanonicalResponse {
	resp := &CanonicalResponse{ID: id, Model: model, FinishReason: finish, Usage: usage}
	if text != "" {
		resp.Parts = append(resp.Parts, Part{Type: PartText, Text: text})
	}
	for i := 0; i < len(calls); i++ {
		c := calls[i]
		if c == nil {
			continue
		}
		resp.Parts = append(resp.Parts, Part{
			Type: PartToolCall, ToolCallID: c.id, ToolName: c.name,
			ToolInput: json.RawMessage(orEmptyObject(json.RawMessage(c.args.String()))),
		})
	}
	return resp
}

// WriteChatStream renders canonical events as a chat.completions SSE stream.
func WriteChatStream(w io.Writer, events <-chan StreamEvent, model string) error {
	id := fmt.Sprintf("chatcmpl-%d", nowNanos())
	created := nowUnix()
	roleSent := false
	send := func(delta map[string]any, finish *string, usage any) error {
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		if usage != nil {
			chunk["choices"] = []any{}
			chunk["usage"] = usage
		}
		if err := writeSSE(w, "", chunk); err != nil {
			return err
		}
		flushIf(w)
		return nil
	}
	for ev := range events {
		switch ev.Type {
		case EvTextDelta:
			delta := map[string]any{"content": ev.Text}
			if !roleSent {
				delta["role"] = "assistant"
				roleSent = true
			}
			if err := send(delta, nil, nil); err != nil {
				return err
			}
		case EvToolCallStart:
			if !roleSent {
				_ = send(map[string]any{"role": "assistant"}, nil, nil)
				roleSent = true
			}
			if err := send(map[string]any{"tool_calls": []any{map[string]any{
				"index": ev.Index, "id": ev.CallID, "type": "function",
				"function": map[string]any{"name": ev.ToolName, "arguments": ""},
			}}}, nil, nil); err != nil {
				return err
			}
		case EvToolArgsDelta:
			if err := send(map[string]any{"tool_calls": []any{map[string]any{
				"index": ev.Index, "function": map[string]any{"arguments": ev.Args},
			}}}, nil, nil); err != nil {
				return err
			}
		case EvDone:
			finish := ev.FinishReason
			if finish == "" {
				finish = "stop"
			}
			if !roleSent {
				_ = send(map[string]any{"role": "assistant"}, nil, nil)
			}
			if err := send(map[string]any{}, &finish, nil); err != nil {
				return err
			}
			_ = send(nil, nil, map[string]any{
				"prompt_tokens":     ev.Usage.InputTokens,
				"completion_tokens": ev.Usage.OutputTokens,
				"total_tokens":      ev.Usage.InputTokens + ev.Usage.OutputTokens,
			})
			_, err := io.WriteString(w, "data: [DONE]\n\n")
			flushIf(w)
			return err
		case EvError:
			_ = writeSSE(w, "", map[string]any{"error": map[string]any{"message": ev.Err, "type": "upstream_error"}})
			_, err := io.WriteString(w, "data: [DONE]\n\n")
			flushIf(w)
			return err
		}
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	flushIf(w)
	return err
}
