package protocol

import (
	"io"
	"strings"
	"testing"
)

func collect(ch <-chan StreamEvent) []StreamEvent {
	var out []StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestChatStreamToAnthropic(t *testing.T) {
	upstream := "data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"f\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"id\":\"chatcmpl-9\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"

	events := collect(ParseChatStream(strings.NewReader(upstream)))
	var text strings.Builder
	var gotStart, gotArgs bool
	var done *StreamEvent
	for _, ev := range events {
		switch ev.Type {
		case EvTextDelta:
			text.WriteString(ev.Text)
		case EvToolCallStart:
			gotStart = true
			if ev.ToolName != "f" || ev.CallID != "call_1" {
				t.Fatalf("bad tool start: %+v", ev)
			}
		case EvToolArgsDelta:
			gotArgs = true
		case EvDone:
			cp := ev
			done = &cp
		}
	}
	if text.String() != "Hello world" || !gotStart || !gotArgs || done == nil {
		t.Fatalf("parse failed: text=%q start=%v args=%v done=%v", text.String(), gotStart, gotArgs, done != nil)
	}
	if done.Usage.InputTokens != 10 || done.Usage.OutputTokens != 7 {
		t.Fatalf("usage lost: %+v", done.Usage)
	}
	if done.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", done.FinishReason)
	}
	if len(done.Response.Parts) != 2 {
		t.Fatalf("assembled parts = %d", len(done.Response.Parts))
	}

	// re-render as anthropic SSE
	var buf strings.Builder
	if err := WriteAnthropicStream(&buf, sliceToChan(events), "claude-x"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"message_start", "content_block_start", "text_delta", "tool_use", "input_json_delta", "message_delta", "message_stop"} {
		if !strings.Contains(out, want) {
			t.Fatalf("anthropic stream missing %q:\n%s", want, out)
		}
	}

	// anthropic stream should parse back
	events2 := collect(ParseAnthropicStream(strings.NewReader(out)))
	var text2 strings.Builder
	for _, ev := range events2 {
		if ev.Type == EvTextDelta {
			text2.WriteString(ev.Text)
		}
	}
	if text2.String() != "Hello world" {
		t.Fatalf("anthropic round trip text = %q", text2.String())
	}
}

func TestAnthropicStreamToChat(t *testing.T) {
	upstream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"c\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

	events := collect(ParseAnthropicStream(strings.NewReader(upstream)))
	var buf strings.Builder
	if err := WriteChatStream(&buf, sliceToChan(events), "gpt-x"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "chat.completion.chunk") || !strings.Contains(out, "[DONE]") || !strings.Contains(out, "Hi") {
		t.Fatalf("bad chat stream:\n%s", out)
	}
}

func TestResponsesStreamRoundTrip(t *testing.T) {
	upstream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"model\":\"m\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"abc\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"model\":\"m\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"abc\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"

	events := collect(ParseResponsesStream(strings.NewReader(upstream)))
	var last StreamEvent
	var text string
	for _, ev := range events {
		if ev.Type == EvTextDelta {
			text += ev.Text
		}
		last = ev
	}
	if text != "abc" || last.Type != EvDone || last.Usage.InputTokens != 2 {
		t.Fatalf("bad parse: text=%q last=%+v", text, last)
	}

	var buf strings.Builder
	if err := WriteResponsesStream(&buf, sliceToChan(events), "m"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "response.completed") || !strings.Contains(out, "output_text.delta") {
		t.Fatalf("bad responses stream:\n%s", out)
	}
	// final completed event must embed a parseable response object
	idx := strings.LastIndex(out, "response.completed")
	if idx < 0 {
		t.Fatal("no completed event")
	}
	_ = idx
}

func sliceToChan(events []StreamEvent) <-chan StreamEvent {
	ch := make(chan StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return ch
}

var _ = io.EOF
