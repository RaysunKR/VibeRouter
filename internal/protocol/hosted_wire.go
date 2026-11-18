package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func AppendToolExchange(proto string, body []byte, response *CanonicalResponse, results []Part) ([]byte, error) {
	if response == nil {
		return nil, fmt.Errorf("append %s tool exchange: nil response", proto)
	}
	root, err := hostedJSONObject(body)
	if err != nil {
		return nil, fmt.Errorf("append %s tool exchange: %w", proto, err)
	}

	switch proto {
	case "chat_completions":
		err = appendChatToolExchange(root, response, results)
	case "responses":
		err = appendResponsesToolExchange(root, response, results)
	case "anthropic":
		err = appendAnthropicToolExchange(root, response, results)
	default:
		return nil, fmt.Errorf("append tool exchange: unsupported protocol %q", proto)
	}
	if err != nil {
		return nil, fmt.Errorf("append %s tool exchange: %w", proto, err)
	}
	return json.Marshal(root)
}

func appendChatToolExchange(root map[string]json.RawMessage, response *CanonicalResponse, results []Part) error {
	messages, err := hostedJSONArrayField(root, "messages")
	if err != nil {
		return err
	}
	var text string
	calls := make([]any, 0)
	for _, part := range response.Parts {
		switch part.Type {
		case PartText:
			text += part.Text
		case PartToolCall:
			calls = append(calls, map[string]any{
				"id":   part.ToolCallID,
				"type": "function",
				"function": map[string]any{
					"name":      part.ToolName,
					"arguments": hostedCallArguments(part.ToolInput),
				},
			})
		}
	}
	assistant := map[string]any{"role": "assistant", "content": text}
	if len(calls) > 0 {
		assistant["tool_calls"] = calls
		if text == "" {
			assistant["content"] = nil
		}
	}
	messages = hostedAppendJSON(messages, assistant)
	for _, result := range results {
		messages = hostedAppendJSON(messages, map[string]any{
			"role":         "tool",
			"tool_call_id": result.ToolResultID,
			"content":      result.ToolResultContent,
		})
	}
	return hostedSetJSONArray(root, "messages", messages)
}

func appendResponsesToolExchange(root map[string]json.RawMessage, response *CanonicalResponse, results []Part) error {
	input, err := hostedResponsesInput(root)
	if err != nil {
		return err
	}
	var text string
	calls := make([]json.RawMessage, 0)
	for _, part := range response.Parts {
		switch part.Type {
		case PartText:
			text += part.Text
		case PartToolCall:
			calls = hostedAppendJSON(calls, map[string]any{
				"type":      "function_call",
				"id":        part.ToolCallID,
				"call_id":   part.ToolCallID,
				"name":      part.ToolName,
				"arguments": hostedCallArguments(part.ToolInput),
			})
		}
	}
	if text != "" {
		input = hostedAppendJSON(input, map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": text,
			}},
		})
	}
	input = append(input, calls...)
	for _, result := range results {
		input = hostedAppendJSON(input, map[string]any{
			"type":    "function_call_output",
			"call_id": result.ToolResultID,
			"output":  result.ToolResultContent,
		})
	}
	return hostedSetJSONArray(root, "input", input)
}

func hostedResponsesInput(root map[string]json.RawMessage) ([]json.RawMessage, error) {
	raw, ok := root["input"]
	if !ok || hostedJSONNull(raw) {
		return []json.RawMessage{}, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		item := hostedMustMarshalJSON(map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": text,
			}},
		})
		return []json.RawMessage{item}, nil
	}
	return hostedJSONArray(raw, "input")
}

func appendAnthropicToolExchange(root map[string]json.RawMessage, response *CanonicalResponse, results []Part) error {
	messages, err := hostedJSONArrayField(root, "messages")
	if err != nil {
		return err
	}
	content := make([]any, 0, len(response.Parts))
	for _, part := range response.Parts {
		switch part.Type {
		case PartText:
			content = append(content, map[string]any{"type": "text", "text": part.Text})
		case PartToolCall:
			input := hostedRawObject(part.ToolInput)
			if !json.Valid(input) {
				return fmt.Errorf("invalid arguments for tool call %q", part.ToolCallID)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    part.ToolCallID,
				"name":  part.ToolName,
				"input": input,
			})
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": ""})
	}
	messages = hostedAppendJSON(messages, map[string]any{"role": "assistant", "content": content})
	if len(results) > 0 {
		toolResults := make([]any, 0, len(results))
		for _, result := range results {
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": result.ToolResultID,
				"content":     result.ToolResultContent,
			})
		}
		messages = hostedAppendJSON(messages, map[string]any{"role": "user", "content": toolResults})
	}
	return hostedSetJSONArray(root, "messages", messages)
}

type hostedSearchItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Content     string `json:"content,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type hostedSearchResult struct {
	Provider string             `json:"provider"`
	Query    string             `json:"query"`
	Results  []hostedSearchItem `json:"results"`
}

type hostedFetchResult struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Format   string `json:"format"`
}

type hostedExecutionInput struct {
	Query string `json:"query"`
	URL   string `json:"url"`
}

func RenderHostedResponse(proto string, body []byte, executions []HostedExecution) ([]byte, error) {
	root, err := hostedJSONObject(body)
	if err != nil {
		return nil, fmt.Errorf("render %s hosted response: %w", proto, err)
	}
	switch proto {
	case "responses":
		err = renderResponsesHosted(root, executions)
	case "anthropic":
		err = renderAnthropicHosted(root, executions)
	default:
		return nil, fmt.Errorf("render hosted response: unsupported protocol %q", proto)
	}
	if err != nil {
		return nil, fmt.Errorf("render %s hosted response: %w", proto, err)
	}
	return json.Marshal(root)
}

func renderResponsesHosted(root map[string]json.RawMessage, executions []HostedExecution) error {
	output, err := hostedJSONArrayField(root, "output")
	if err != nil {
		return err
	}
	ids := newHostedNativeIDs("ws_", output)
	native := make([]json.RawMessage, 0, len(executions))
	for i, execution := range executions {
		item, err := responsesHostedExecution(execution, ids.next(execution.CallID))
		if err != nil {
			return fmt.Errorf("execution %d: %w", i, err)
		}
		native = append(native, item)
	}
	return hostedSetJSONArray(root, "output", append(native, output...))
}

func responsesHostedExecution(execution HostedExecution, id string) (json.RawMessage, error) {
	var input hostedExecutionInput
	if err := hostedDecodeJSON(execution.Input, &input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	status := "completed"
	if execution.Error != "" {
		status = "failed"
	}
	var action map[string]any
	switch HostedWebToolKind(execution.Kind) {
	case HostedWebSearch:
		var result hostedSearchResult
		if err := hostedDecodeJSON(execution.Result, &result); err != nil {
			return nil, fmt.Errorf("invalid search result: %w", err)
		}
		sources := make([]any, 0, len(result.Results))
		for _, item := range result.Results {
			sources = append(sources, map[string]any{
				"type":  "url",
				"url":   item.URL,
				"title": item.Title,
			})
		}
		action = map[string]any{"type": "search", "query": input.Query, "sources": sources}
	case HostedWebFetch:
		action = map[string]any{"type": "open_page", "url": input.URL}
	default:
		return nil, fmt.Errorf("unsupported hosted tool kind %q", execution.Kind)
	}
	return json.Marshal(map[string]any{
		"type":   "web_search_call",
		"id":     id,
		"status": status,
		"action": action,
	})
}

func renderAnthropicHosted(root map[string]json.RawMessage, executions []HostedExecution) error {
	content, err := hostedJSONArrayField(root, "content")
	if err != nil {
		return err
	}
	ids := newHostedNativeIDs("srvtoolu_", content)
	native := make([]json.RawMessage, 0, len(executions)*2)
	for i, execution := range executions {
		blocks, err := anthropicHostedExecution(execution, ids.next(execution.CallID))
		if err != nil {
			return fmt.Errorf("execution %d: %w", i, err)
		}
		native = append(native, blocks...)
	}
	return hostedSetJSONArray(root, "content", append(native, content...))
}

func anthropicHostedExecution(execution HostedExecution, id string) ([]json.RawMessage, error) {
	var input hostedExecutionInput
	if err := hostedDecodeJSON(execution.Input, &input); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	kind := HostedWebToolKind(execution.Kind)
	if kind == "" {
		return nil, fmt.Errorf("unsupported hosted tool kind %q", execution.Kind)
	}
	toolInput := map[string]any{"query": input.Query}
	if kind == HostedWebFetch {
		toolInput = map[string]any{"url": input.URL}
	}
	toolUse := hostedMustMarshalJSON(map[string]any{
		"type":  "server_tool_use",
		"id":    id,
		"name":  kind,
		"input": toolInput,
	})
	resultType := kind + "_tool_result"
	if execution.Error != "" {
		errorCode := "unavailable"
		if execution.Error == "max_uses_exceeded" {
			errorCode = "max_uses_exceeded"
		}
		result := hostedMustMarshalJSON(map[string]any{
			"type":        resultType,
			"tool_use_id": id,
			"content": map[string]any{
				"type":       resultType + "_error",
				"error_code": errorCode,
			},
		})
		return []json.RawMessage{toolUse, result}, nil
	}

	var result json.RawMessage
	switch kind {
	case HostedWebSearch:
		var search hostedSearchResult
		if err := hostedDecodeJSON(execution.Result, &search); err != nil {
			return nil, fmt.Errorf("invalid search result: %w", err)
		}
		items := make([]any, 0, len(search.Results))
		for _, item := range search.Results {
			encoded, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			items = append(items, map[string]any{
				"type":              "web_search_result",
				"url":               item.URL,
				"title":             item.Title,
				"page_age":          item.PublishedAt,
				"encrypted_content": "viberouter:" + base64.RawURLEncoding.EncodeToString(encoded),
			})
		}
		result = hostedMustMarshalJSON(map[string]any{
			"type":        resultType,
			"tool_use_id": id,
			"content":     items,
		})
	case HostedWebFetch:
		var fetch hostedFetchResult
		if err := hostedDecodeJSON(execution.Result, &fetch); err != nil {
			return nil, fmt.Errorf("invalid fetch result: %w", err)
		}
		if fetch.URL == "" {
			fetch.URL = input.URL
		}
		result = hostedMustMarshalJSON(map[string]any{
			"type":        resultType,
			"tool_use_id": id,
			"content": map[string]any{
				"type":         "web_fetch_result",
				"url":          fetch.URL,
				"retrieved_at": time.Now().UTC().Format(time.RFC3339),
				"content": map[string]any{
					"type":  "document",
					"title": fetch.Title,
					"source": map[string]any{
						"type":       "text",
						"media_type": "text/plain",
						"data":       fetch.Content,
					},
				},
			},
		})
	}
	return []json.RawMessage{toolUse, result}, nil
}

type hostedNativeIDs struct {
	prefix string
	used   map[string]bool
}

func newHostedNativeIDs(prefix string, existing []json.RawMessage) *hostedNativeIDs {
	ids := &hostedNativeIDs{prefix: prefix, used: make(map[string]bool)}
	for _, raw := range existing {
		var item struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &item) == nil && item.ID != "" {
			ids.used[item.ID] = true
		}
	}
	return ids
}

func (ids *hostedNativeIDs) next(callID string) string {
	candidate := ids.hash(callID)
	for n := 2; ids.used[candidate]; n++ {
		candidate = ids.hash(fmt.Sprintf("%s#%d", callID, n))
	}
	ids.used[candidate] = true
	return candidate
}

func (ids *hostedNativeIDs) hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return ids.prefix + hex.EncodeToString(sum[:12])
}

type hostedStreamEvent struct {
	name    string
	payload map[string]any
}

func WriteHostedStream(w http.ResponseWriter, proto string, body []byte) error {
	var (
		events []hostedStreamEvent
		err    error
	)
	switch proto {
	case "responses":
		events, err = hostedResponsesStream(body)
	case "anthropic":
		events, err = hostedAnthropicStream(body)
	default:
		return fmt.Errorf("write hosted stream: unsupported protocol %q", proto)
	}
	if err != nil {
		return fmt.Errorf("write %s hosted stream: %w", proto, err)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for i, event := range events {
		if proto == "responses" {
			event.payload["sequence_number"] = i
		}
		if err := writeSSE(w, event.name, event.payload); err != nil {
			return err
		}
		flushIf(w)
	}
	return nil
}

func hostedResponsesStream(body []byte) ([]hostedStreamEvent, error) {
	root, err := hostedJSONObject(body)
	if err != nil {
		return nil, err
	}
	output, err := hostedJSONArrayField(root, "output")
	if err != nil {
		return nil, err
	}
	items := make([]map[string]json.RawMessage, len(output))
	for i, raw := range output {
		items[i], err = hostedJSONObject(raw)
		if err != nil {
			return nil, fmt.Errorf("output item %d: %w", i, err)
		}
	}

	created := hostedCloneObject(root)
	created["status"] = hostedRawValue("in_progress")
	created["output"] = json.RawMessage("[]")
	events := []hostedStreamEvent{hostedEvent("response.created", "response", created)}
	for i, item := range items {
		itemType := hostedJSONString(item["type"])
		itemID := hostedJSONString(item["id"])
		added := hostedCloneObject(item)
		switch itemType {
		case "message":
			added["status"] = hostedRawValue("in_progress")
			added["content"] = json.RawMessage("[]")
		case "function_call", "web_search_call":
			added["status"] = hostedRawValue("in_progress")
			if itemType == "function_call" {
				added["arguments"] = hostedRawValue("")
			}
		}
		events = append(events, hostedEvent("response.output_item.added", "output_index", i, "item", added))

		switch itemType {
		case "message":
			content, err := hostedJSONArrayField(item, "content")
			if err != nil {
				return nil, fmt.Errorf("output item %d content: %w", i, err)
			}
			for contentIndex, rawPart := range content {
				part, err := hostedJSONObject(rawPart)
				if err != nil {
					return nil, fmt.Errorf("output item %d content %d: %w", i, contentIndex, err)
				}
				startedPart := hostedCloneObject(part)
				if hostedJSONString(part["type"]) == "output_text" {
					startedPart["text"] = hostedRawValue("")
				}
				events = append(events, hostedEvent("response.content_part.added",
					"item_id", itemID, "output_index", i, "content_index", contentIndex, "part", startedPart))
				if hostedJSONString(part["type"]) == "output_text" {
					text := hostedJSONString(part["text"])
					if text != "" {
						events = append(events, hostedEvent("response.output_text.delta",
							"item_id", itemID, "output_index", i, "content_index", contentIndex, "delta", text))
					}
					events = append(events,
						hostedEvent("response.output_text.done", "item_id", itemID, "output_index", i,
							"content_index", contentIndex, "text", text),
						hostedEvent("response.output_text.completed", "item_id", itemID, "output_index", i,
							"content_index", contentIndex, "text", text),
					)
				}
				events = append(events, hostedEvent("response.content_part.done",
					"item_id", itemID, "output_index", i, "content_index", contentIndex, "part", part))
			}
		case "web_search_call":
			for _, phase := range []string{"in_progress", "searching", "completed"} {
				events = append(events, hostedEvent("response.web_search_call."+phase,
					"item_id", itemID, "output_index", i))
			}
		case "function_call":
			arguments := hostedJSONString(item["arguments"])
			if arguments != "" {
				events = append(events, hostedEvent("response.function_call_arguments.delta",
					"item_id", itemID, "output_index", i, "delta", arguments))
			}
			events = append(events, hostedEvent("response.function_call_arguments.done",
				"item_id", itemID, "output_index", i, "arguments", arguments))
		}
		events = append(events, hostedEvent("response.output_item.done", "output_index", i, "item", item))
	}
	events = append(events, hostedEvent("response.completed", "response", root))
	return events, nil
}

func hostedAnthropicStream(body []byte) ([]hostedStreamEvent, error) {
	root, err := hostedJSONObject(body)
	if err != nil {
		return nil, err
	}
	content, err := hostedJSONArrayField(root, "content")
	if err != nil {
		return nil, err
	}
	blocks := make([]map[string]json.RawMessage, len(content))
	for i, raw := range content {
		blocks[i], err = hostedJSONObject(raw)
		if err != nil {
			return nil, fmt.Errorf("content block %d: %w", i, err)
		}
	}

	start := hostedCloneObject(root)
	start["content"] = json.RawMessage("[]")
	start["stop_reason"] = json.RawMessage("null")
	start["stop_sequence"] = json.RawMessage("null")
	usage := make(map[string]json.RawMessage)
	if raw := root["usage"]; len(raw) > 0 && !hostedJSONNull(raw) {
		if err := json.Unmarshal(raw, &usage); err != nil {
			return nil, fmt.Errorf("usage: %w", err)
		}
	}
	usage["output_tokens"] = json.RawMessage("0")
	start["usage"] = hostedMustMarshalJSON(usage)
	events := []hostedStreamEvent{hostedEvent("message_start", "message", start)}
	for i, block := range blocks {
		blockType := hostedJSONString(block["type"])
		switch blockType {
		case "text":
			started := hostedCloneObject(block)
			started["text"] = hostedRawValue("")
			events = append(events, hostedEvent("content_block_start", "index", i, "content_block", started))
			text := hostedJSONString(block["text"])
			if text != "" {
				events = append(events, hostedEvent("content_block_delta", "index", i,
					"delta", map[string]any{"type": "text_delta", "text": text}))
			}
		case "tool_use", "server_tool_use":
			started := hostedCloneObject(block)
			started["input"] = json.RawMessage("{}")
			events = append(events, hostedEvent("content_block_start", "index", i, "content_block", started))
			input := hostedRawObject(block["input"])
			events = append(events, hostedEvent("content_block_delta", "index", i,
				"delta", map[string]any{"type": "input_json_delta", "partial_json": string(input)}))
		default:
			events = append(events, hostedEvent("content_block_start", "index", i, "content_block", block))
		}
		events = append(events, hostedEvent("content_block_stop", "index", i))
	}

	stopReason := json.RawMessage("null")
	if raw, ok := root["stop_reason"]; ok {
		stopReason = raw
	}
	stopSequence := json.RawMessage("null")
	if raw, ok := root["stop_sequence"]; ok {
		stopSequence = raw
	}
	outputTokens := json.RawMessage("0")
	if raw := usage["output_tokens"]; len(raw) > 0 {
		outputTokens = raw
	}
	if raw := root["usage"]; len(raw) > 0 && !hostedJSONNull(raw) {
		var finalUsage map[string]json.RawMessage
		if err := json.Unmarshal(raw, &finalUsage); err == nil && len(finalUsage["output_tokens"]) > 0 {
			outputTokens = finalUsage["output_tokens"]
		}
	}
	events = append(events,
		hostedEvent("message_delta",
			"delta", map[string]json.RawMessage{"stop_reason": stopReason, "stop_sequence": stopSequence},
			"usage", map[string]json.RawMessage{"output_tokens": outputTokens}),
		hostedEvent("message_stop"),
	)
	return events, nil
}

func hostedEvent(name string, fields ...any) hostedStreamEvent {
	payload := map[string]any{"type": name}
	for i := 0; i < len(fields); i += 2 {
		payload[fields[i].(string)] = fields[i+1]
	}
	return hostedStreamEvent{name: name, payload: payload}
}

func hostedJSONObject(body []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return object, nil
}

func hostedCloneObject(object map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		clone[key] = value
	}
	return clone
}

func hostedJSONArrayField(object map[string]json.RawMessage, field string) ([]json.RawMessage, error) {
	raw, ok := object[field]
	if !ok || hostedJSONNull(raw) {
		return []json.RawMessage{}, nil
	}
	return hostedJSONArray(raw, field)
}

func hostedJSONArray(raw json.RawMessage, field string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%s must be an array", field)
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	return items, nil
}

func hostedSetJSONArray(object map[string]json.RawMessage, field string, items []json.RawMessage) error {
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	object[field] = raw
	return nil
}

func hostedAppendJSON(items []json.RawMessage, value any) []json.RawMessage {
	return append(items, hostedMustMarshalJSON(value))
}

func hostedMustMarshalJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}

func hostedRawValue(value any) json.RawMessage {
	return hostedMustMarshalJSON(value)
}

func hostedRawObject(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage("{}")
	}
	return raw
}

func hostedCallArguments(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "{}"
	}
	return string(raw)
}

func hostedJSONNull(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func hostedJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func hostedDecodeJSON(raw json.RawMessage, target any) error {
	if hostedJSONNull(raw) {
		return nil
	}
	return json.Unmarshal(raw, target)
}
