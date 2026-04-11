package service

import (
	"encoding/json"
	"net/http"
	"strings"
)

type APIStyle string

const (
	StyleOpenAI    APIStyle = "openai"
	StyleAnthropic APIStyle = "anthropic"
	StyleUnknown   APIStyle = "unknown"
)

// DetectAPIStyle determines the API style from request headers and body
func DetectAPIStyle(r *http.Request, body []byte) APIStyle {
	path := r.URL.Path

	// URL-based detection
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions"),
		strings.HasPrefix(path, "/v1/completions"),
		strings.HasPrefix(path, "/v1/embeddings"),
		strings.HasPrefix(path, "/v1/audio"),
		strings.HasPrefix(path, "/v1/fine_tuning"),
		strings.HasPrefix(path, "/v1/moderations"):
		return StyleOpenAI
	case strings.HasPrefix(path, "/v1/messages"):
		// /v1/messages is ambiguous - need further detection
	}

	// Header-based detection (most reliable)
	authHeader := r.Header.Get("Authorization")
	apiKeyHeader := r.Header.Get("x-api-key")

	if strings.HasPrefix(authHeader, "Bearer sk-") {
		return StyleOpenAI
	}
	if strings.HasPrefix(apiKeyHeader, "sk-ant-") {
		return StyleAnthropic
	}

	// Body-based detection
	if len(body) == 0 {
		// Default based on model prefix if no body
		model := r.URL.Query().Get("model")
		if strings.HasPrefix(model, "claude-") {
			return StyleAnthropic
		}
		return StyleOpenAI
	}

	var rawBody map[string]interface{}
	if err := json.Unmarshal(body, &rawBody); err != nil {
		return StyleOpenAI // Default to OpenAI on parse error
	}

	// Anthropic indicators
	if _, ok := rawBody["max_tokens"]; ok && path == "/v1/messages" {
		return StyleAnthropic
	}
	if _, ok := rawBody["stop_sequences"]; ok {
		return StyleAnthropic
	}

	// OpenAI indicators
	if _, ok := rawBody["tools"]; ok {
		return StyleOpenAI
	}
	if _, ok := rawBody["functions"]; ok {
		return StyleOpenAI
	}

	// Check message roles
	if messages, ok := rawBody["messages"].([]interface{}); ok {
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if role, ok := msgMap["role"].(string); ok && role == "tool" {
					return StyleOpenAI
				}
			}
		}
	}

	// Check model prefix as fallback
	if model, ok := rawBody["model"].(string); ok {
		if strings.HasPrefix(model, "claude-") {
			return StyleAnthropic
		}
	}

	return StyleOpenAI
}
