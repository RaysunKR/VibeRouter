package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"viberouter/internal/model"
)

// RequestTransformer handles OpenAI <-> Anthropic request/response transformations
type RequestTransformer struct{}

// NewRequestTransformer creates a new request transformer
func NewRequestTransformer() *RequestTransformer {
	return &RequestTransformer{}
}

// TransformRequest converts request from detected style to backend provider style
func (t *RequestTransformer) TransformRequest(body []byte, clientStyle, backendProvider model.Provider) ([]byte, error) {
	if clientStyle == backendProvider {
		return body, nil // No transformation needed
	}

	if clientStyle == model.ProviderOpenAI && backendProvider == model.ProviderAnthropic {
		return t.OpenAIRequestToAnthropic(body)
	}

	if clientStyle == model.ProviderAnthropic && backendProvider == model.ProviderOpenAI {
		return t.AnthropicRequestToOpenAI(body)
	}

	return body, nil
}

// OpenAIRequestToAnthropic converts OpenAI chat completions request to Anthropic messages format
func (t *RequestTransformer) OpenAIRequestToAnthropic(body []byte) ([]byte, error) {
	var openAIReq map[string]interface{}
	if err := json.Unmarshal(body, &openAIReq); err != nil {
		return nil, err
	}

	anthropicReq := make(map[string]interface{})

	// model
	if model, ok := openAIReq["model"].(string); ok {
		anthropicReq["model"] = t.mapModelName(model)
	}

	// max_tokens (required for Anthropic)
	if maxTokens, ok := openAIReq["max_tokens"].(float64); ok {
		anthropicReq["max_tokens"] = int(maxTokens)
	} else {
		anthropicReq["max_tokens"] = 4096 // default
	}

	// system message
	if systemMsg := t.extractSystemMessage(openAIReq); systemMsg != "" {
		anthropicReq["system"] = systemMsg
	}

	// messages - convert format
	if messages, ok := openAIReq["messages"].([]interface{}); ok {
		anthropicReq["messages"] = t.convertMessagesToAnthropic(messages)
	}

	// temperature
	if temp, ok := openAIReq["temperature"].(float64); ok {
		anthropicReq["temperature"] = temp
	}

	// top_p
	if topP, ok := openAIReq["top_p"].(float64); ok {
		anthropicReq["top_p"] = topP
	}

	// stop_sequences
	if stop, ok := openAIReq["stop"]; ok {
		if str, ok := stop.(string); ok && str != "" {
			anthropicReq["stop_sequences"] = []string{str}
		} else if arr, ok := stop.([]interface{}); ok {
			var stops []string
			for _, s := range arr {
				if str, ok := s.(string); ok {
					stops = append(stops, str)
				}
			}
			anthropicReq["stop_sequences"] = stops
		}
	}

	// tools
	if tools, ok := openAIReq["tools"].([]interface{}); ok {
		anthropicReq["tools"] = t.convertToolsToAnthropic(tools)
	}

	// tool_choice
	if toolChoice, ok := openAIReq["tool_choice"]; ok {
		anthropicReq["tool_choice"] = t.convertToolChoiceToAnthropic(toolChoice)
	}

	// stream
	if stream, ok := openAIReq["stream"].(bool); ok {
		anthropicReq["stream"] = stream
	}

	return json.Marshal(anthropicReq)
}

// AnthropicRequestToOpenAI converts Anthropic messages request to OpenAI chat completions format
func (t *RequestTransformer) AnthropicRequestToOpenAI(body []byte) ([]byte, error) {
	var anthropicReq map[string]interface{}
	if err := json.Unmarshal(body, &anthropicReq); err != nil {
		return nil, err
	}

	openAIReq := make(map[string]interface{})

	// model
	if model, ok := anthropicReq["model"].(string); ok {
		openAIReq["model"] = t.mapModelNameToOpenAI(model)
	}

	// max_tokens
	if maxTokens, ok := anthropicReq["max_tokens"].(float64); ok {
		openAIReq["max_tokens"] = int(maxTokens)
	}

	// system message -> prepend to messages
	messages := make([]interface{}, 0)
	if systemMsg := t.extractAnthropicSystemMessage(anthropicReq); systemMsg != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": systemMsg,
		})
	}

	// messages
	if msgs, ok := anthropicReq["messages"].([]interface{}); ok {
		messages = append(messages, t.convertMessagesToOpenAI(msgs)...)
	}
	openAIReq["messages"] = messages

	// temperature
	if temp, ok := anthropicReq["temperature"].(float64); ok {
		openAIReq["temperature"] = temp
	}

	// top_p
	if topP, ok := anthropicReq["top_p"].(float64); ok {
		openAIReq["top_p"] = topP
	}

	// stop -> convert stop_sequences
	if stops, ok := anthropicReq["stop_sequences"].([]interface{}); ok && len(stops) > 0 {
		if len(stops) == 1 {
			openAIReq["stop"] = stops[0]
		} else {
			openAIReq["stop"] = stops
		}
	}

	// tools
	if tools, ok := anthropicReq["tools"].([]interface{}); ok {
		openAIReq["tools"] = t.convertToolsToOpenAI(tools)
	}

	// tool_choice
	if toolChoice, ok := anthropicReq["tool_choice"]; ok {
		openAIReq["tool_choice"] = t.convertToolChoiceToOpenAI(toolChoice)
	}

	// stream
	if stream, ok := anthropicReq["stream"].(bool); ok {
		openAIReq["stream"] = stream
	}

	return json.Marshal(openAIReq)
}

func (t *RequestTransformer) extractSystemMessage(req map[string]interface{}) string {
	if messages, ok := req["messages"].([]interface{}); ok {
		for i, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if role, ok := msgMap["role"].(string); ok && role == "system" {
					if content, ok := msgMap["content"].(string); ok {
						// Remove system message from array
						newMessages := make([]interface{}, 0)
						newMessages = append(newMessages, messages[:i]...)
						newMessages = append(newMessages, messages[i+1:]...)
						req["messages"] = newMessages
						return content
					}
				}
			}
		}
	}
	return ""
}

func (t *RequestTransformer) extractAnthropicSystemMessage(req map[string]interface{}) string {
	if system, ok := req["system"].(string); ok {
		delete(req, "system")
		return system
	}
	return ""
}

func (t *RequestTransformer) convertMessagesToAnthropic(messages []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, msg := range messages {
		if msgMap, ok := msg.(map[string]interface{}); ok {
			role, _ := msgMap["role"].(string)
			content := msgMap["content"]

			switch role {
			case "system":
				// Already extracted
			case "user":
				result = append(result, map[string]interface{}{
					"role":    "user",
					"content": t.contentToAnthropic(content),
				})
			case "assistant":
				// Handle tool calls
				if toolCalls, ok := msgMap["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
					contentBlocks := make([]interface{}, 0)
					if text, ok := content.(string); ok && text != "" {
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type": "text",
							"text": text,
						})
					}
					for _, tc := range toolCalls {
						if tcMap, ok := tc.(map[string]interface{}); ok {
							funcPtr := tcMap["function"].(map[string]interface{})
							contentBlocks = append(contentBlocks, map[string]interface{}{
								"type": "tool_use",
								"id":   tcMap["id"],
								"name": funcPtr["name"],
								"input": funcPtr["arguments"],
							})
						}
					}
					result = append(result, map[string]interface{}{
						"role":    "assistant",
						"content": contentBlocks,
					})
				} else {
					result = append(result, map[string]interface{}{
						"role":    "assistant",
						"content": t.contentToAnthropic(content),
					})
				}
			case "tool":
				// Anthropic uses tool_result type
				if toolContent, ok := content.(string); ok {
					result = append(result, map[string]interface{}{
						"role": "user",
						"content": []interface{}{
							map[string]interface{}{
								"type":       "tool_result",
								"tool_use_id": msgMap["tool_call_id"],
								"content":     toolContent,
							},
						},
					})
				}
			default:
				result = append(result, msg)
			}
		}
	}
	return result
}

func (t *RequestTransformer) convertMessagesToOpenAI(messages []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, msg := range messages {
		if msgMap, ok := msg.(map[string]interface{}); ok {
			role, _ := msgMap["role"].(string)
			content := msgMap["content"]

			switch role {
			case "user":
				result = append(result, map[string]interface{}{
					"role":    "user",
					"content": t.contentToOpenAI(content),
				})
			case "assistant":
				result = append(result, map[string]interface{}{
					"role":    "assistant",
					"content": t.contentToOpenAI(content),
				})
			default:
				result = append(result, msg)
			}
		}
	}
	return result
}

func (t *RequestTransformer) contentToAnthropic(content interface{}) interface{} {
	if str, ok := content.(string); ok {
		return str
	}
	if arr, ok := content.([]interface{}); ok {
		result := make([]interface{}, 0)
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemMap["type"] == "image_url" {
					if urlData, ok := itemMap["image_url"].(map[string]interface{}); ok {
						url, _ := urlData["url"].(string)
						detail := "high"
						if urlData["detail"] != nil {
							detail, _ = urlData["detail"].(string)
						}
						_ = detail // detail available for future use
						result = append(result, map[string]interface{}{
							"type":      "image",
							"source":     map[string]interface{}{"type": "url", "url": url},
						})
					}
				}
			}
		}
		if len(result) == 0 {
			return content
		}
		return result
	}
	return content
}

func (t *RequestTransformer) contentToOpenAI(content interface{}) interface{} {
	if str, ok := content.(string); ok {
		return str
	}
	if arr, ok := content.([]interface{}); ok {
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemMap["type"] == "image" {
					return content // Keep as array for vision
				}
			}
		}
	}
	return content
}

func (t *RequestTransformer) convertToolsToAnthropic(tools []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, tool := range tools {
		if toolMap, ok := tool.(map[string]interface{}); ok {
			anthropicTool := map[string]interface{}{
				"name":        toolMap["name"],
				"description": toolMap["description"],
				"input_schema": toolMap["parameters"],
			}
			result = append(result, anthropicTool)
		}
	}
	return result
}

func (t *RequestTransformer) convertToolsToOpenAI(tools []interface{}) []interface{} {
	result := make([]interface{}, 0)
	for _, tool := range tools {
		if toolMap, ok := tool.(map[string]interface{}); ok {
			openAITool := map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        toolMap["name"],
					"description": toolMap["description"],
					"parameters": toolMap["input_schema"],
				},
			}
			result = append(result, openAITool)
		}
	}
	return result
}

func (t *RequestTransformer) convertToolChoiceToAnthropic(toolChoice interface{}) interface{} {
	if choiceMap, ok := toolChoice.(map[string]interface{}); ok {
		if choiceMap["type"] == "function" {
			if fn, ok := choiceMap["function"].(map[string]interface{}); ok {
				return map[string]interface{}{
					"type":  "tool",
					"index": fn["name"],
				}
			}
		}
	}
	if str, ok := toolChoice.(string); ok {
		if str == "auto" {
			return map[string]interface{}{"type": "auto"}
		}
		if str == "none" {
			return map[string]interface{}{"type": "none"}
		}
	}
	return toolChoice
}

func (t *RequestTransformer) convertToolChoiceToOpenAI(toolChoice interface{}) interface{} {
	if choiceMap, ok := toolChoice.(map[string]interface{}); ok {
		if choiceMap["type"] == "tool" {
			if name, ok := choiceMap["index"].(string); ok {
				return map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name": name,
					},
				}
			}
		}
	}
	return toolChoice
}

func (t *RequestTransformer) mapModelName(model string) string {
	// Map common model names between providers
	mappings := map[string]string{
		"gpt-4":              "claude-3-opus-20240229",
		"gpt-4-turbo":        "claude-3-sonnet-20240229",
		"gpt-4o":             "claude-3-sonnet-20240229",
		"gpt-3.5-turbo":      "claude-3-haiku-20240307",
	}
	if mapped, ok := mappings[model]; ok {
		return mapped
	}
	if strings.HasPrefix(model, "claude-") {
		return model // Already Anthropic format
	}
	return model // Pass through
}

func (t *RequestTransformer) mapModelNameToOpenAI(model string) string {
	mappings := map[string]string{
		"claude-3-opus-20240229": "gpt-4",
		"claude-3-sonnet-20240229": "gpt-4-turbo",
		"claude-3-haiku-20240307": "gpt-3.5-turbo",
	}
	if mapped, ok := mappings[model]; ok {
		return mapped
	}
	if strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "o1-") {
		return model // Already OpenAI format
	}
	return model
}

// TransformResponse transforms response from backend to client style
func (t *RequestTransformer) TransformResponse(body []byte, clientStyle, backendProvider model.Provider) ([]byte, error) {
	if clientStyle == backendProvider {
		return body, nil
	}

	// First check if this is an error response that needs format conversion
	if errorResp := t.DetectAndTransformError(body, clientStyle, backendProvider); errorResp != nil {
		return errorResp, nil
	}

	// For streaming responses, we handle chunk-by-chunk in handlers
	// This is for non-streaming responses
	if clientStyle == model.ProviderOpenAI && backendProvider == model.ProviderAnthropic {
		return t.AnthropicResponseToOpenAI(body)
	}

	if clientStyle == model.ProviderAnthropic && backendProvider == model.ProviderOpenAI {
		return t.OpenAIResponseToAnthropic(body)
	}

	return body, nil
}

// DetectAndTransformError checks if body contains an error and transforms error format
// Returns transformed error body if error detected, nil otherwise
func (t *RequestTransformer) DetectAndTransformError(body []byte, clientStyle, backendProvider model.Provider) []byte {
	if clientStyle == backendProvider {
		return nil
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}

	// Check for Anthropic error format: { "type": "error", "error": { "type": "...", "message": "..." } }
	if backendProvider == model.ProviderAnthropic && clientStyle == model.ProviderOpenAI {
		if resp["type"] == "error" || resp["error"] != nil {
			return t.TransformAnthropicErrorToOpenAI(body)
		}
	}

	// Check for OpenAI error format: { "error": { "code": "...", "message": "..." } }
	if backendProvider == model.ProviderOpenAI && clientStyle == model.ProviderAnthropic {
		if resp["error"] != nil || resp["code"] != nil {
			return t.TransformOpenAIErrorToAnthropic(body)
		}
	}

	return nil
}

func (t *RequestTransformer) TransformAnthropicErrorToOpenAI(body []byte) []byte {
	var anthropicErrResp map[string]interface{}
	if err := json.Unmarshal(body, &anthropicErrResp); err != nil {
		return body
	}

	// Extract error object
	var errObj map[string]interface{}
	if err, ok := anthropicErrResp["error"].(map[string]interface{}); ok {
		errObj = err
	} else {
		errObj = anthropicErrResp
	}

	// Build OpenAI error format
	openAIErr := map[string]interface{}{
		"error": map[string]interface{}{
			"message": errObj["message"],
			"type":    errObj["type"],
		},
	}

	// Copy additional fields
	if code := errObj["type"]; code != nil {
		openAIErr["error"].(map[string]interface{})["code"] = code
	}
	if param := errObj["param"]; param != nil {
		openAIErr["error"].(map[string]interface{})["param"] = param
	}

	jsonData, _ := json.Marshal(openAIErr)
	return jsonData
}

func (t *RequestTransformer) TransformOpenAIErrorToAnthropic(body []byte) []byte {
	var openAIErrResp map[string]interface{}
	if err := json.Unmarshal(body, &openAIErrResp); err != nil {
		return body
	}

	// Extract error object
	var errObj map[string]interface{}
	if err, ok := openAIErrResp["error"].(map[string]interface{}); ok {
		errObj = err
	} else {
		errObj = openAIErrResp
	}

	// Build Anthropic error format
	anthropicErr := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    errObj["type"],
			"message": errObj["message"],
		},
	}

	// Copy additional fields
	if code := errObj["code"]; code != nil {
		anthropicErr["error"].(map[string]interface{})["type"] = code
	}
	if param := errObj["param"]; param != nil {
		anthropicErr["error"].(map[string]interface{})["param"] = param
	}

	jsonData, _ := json.Marshal(anthropicErr)
	return jsonData
}

// FormatError formats an error message according to the target API style
// This is used for VibeRouter's own error responses (not backend errors)
func (t *RequestTransformer) FormatError(message, errType string, style model.Provider) []byte {
	if style == model.ProviderAnthropic {
		anthropicErr := map[string]interface{}{
			"type": "error",
			"error": map[string]interface{}{
				"type":    errType,
				"message": message,
			},
		}
		data, _ := json.Marshal(anthropicErr)
		return data
	}

	// OpenAI format
	openAIErr := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errType,
		},
	}
	data, _ := json.Marshal(openAIErr)
	return data
}

func (t *RequestTransformer) AnthropicResponseToOpenAI(body []byte) ([]byte, error) {
	var anthropicResp map[string]interface{}
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return body, nil
	}

	openAIResp := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", anthropicResp["id"]),
		"object":  "chat.completion",
		"created": anthropicResp["created"],
		"model":   anthropicResp["model"],
		"choices": []interface{}{
			map[string]interface{}{
				"index": 0,
				"message": map[string]interface{}{
					"role":    "assistant",
					"content": t.extractAnthropicContent(anthropicResp),
				},
				"finish_reason": anthropicResp["stop_reason"],
			},
		},
		"usage": anthropicResp["usage"],
	}

	return json.Marshal(openAIResp)
}

func (t *RequestTransformer) OpenAIResponseToAnthropic(body []byte) ([]byte, error) {
	var openAIResp map[string]interface{}
	if err := json.Unmarshal(body, &openAIResp); err != nil {
		return body, nil
	}

	anthropicResp := map[string]interface{}{
		"id":      openAIResp["id"],
		"type":    "message",
		"role":    "assistant",
		"model":   openAIResp["model"],
		"content": t.extractOpenAIContent(openAIResp),
		"stop_reason": t.extractFinishReason(openAIResp),
		"usage": map[string]interface{}{
			"input_tokens":  openAIResp["usage"].(map[string]interface{})["prompt_tokens"],
			"output_tokens": openAIResp["usage"].(map[string]interface{})["completion_tokens"],
		},
	}

	return json.Marshal(anthropicResp)
}

func (t *RequestTransformer) extractAnthropicContent(resp map[string]interface{}) interface{} {
	if content, ok := resp["content"].([]interface{}); ok {
		var textParts []string
		for _, block := range content {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockMap["type"] == "text" {
					if text, ok := blockMap["text"].(string); ok {
						textParts = append(textParts, text)
					}
				}
			}
		}
		if len(textParts) > 0 {
			return strings.Join(textParts, "")
		}
	}
	if text, ok := resp["content"].(string); ok {
		return text
	}
	return ""
}

func (t *RequestTransformer) extractOpenAIContent(resp map[string]interface{}) interface{} {
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				return msg["content"]
			}
		}
	}
	return ""
}

func (t *RequestTransformer) extractFinishReason(resp map[string]interface{}) string {
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if reason, ok := choice["finish_reason"].(string); ok {
				return reason
			}
		}
	}
	return "end_turn"
}
