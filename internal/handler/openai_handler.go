package handler

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"viberouter/internal/model"
	"viberouter/internal/service"

	"github.com/gin-gonic/gin"
)

// Shared HTTP client with connection pooling for maximum concurrency
var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

func getSharedHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		httpClient = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        1000,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				DisableKeepAlives:   false,
			},
		}
	})
	return httpClient
}

// getRealIP returns the real client IP, preferring IPv4
func getRealIP(c *gin.Context) string {
	ip := c.ClientIP()
	// If IPv6 localhost, convert to IPv4
	if ip == "::1" {
		ip = "127.0.0.1"
	}
	return ip
}

// OpenAIHandler handles OpenAI-compatible API requests
type OpenAIHandler struct {
	lb          *service.LoadBalancer
	callLogSvc  *service.CallLogService
	transformer *service.RequestTransformer
}

func NewOpenAIHandler(lb *service.LoadBalancer, cls *service.CallLogService) *OpenAIHandler {
	return &OpenAIHandler{
		lb:          lb,
		callLogSvc:  cls,
		transformer: service.NewRequestTransformer(),
	}
}

// ChatCompletions handles POST /v1/chat/completions
func (h *OpenAIHandler) ChatCompletions(c *gin.Context) {
	startTime := time.Now()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("failed to read request body", "invalid_request_error", model.ProviderOpenAI))
		return
	}

	var chatReq ChatCompletionRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("invalid JSON body", "invalid_request_error", model.ProviderOpenAI))
		return
	}

	// Get username from context (set by auth middleware)
	username, exists := c.Get("username")
	if !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderOpenAI))
		return
	}
	usernameStr := username.(string)

	isStreaming := chatReq.Stream
	maxRetries := 2
	if isStreaming {
		maxRetries = 1
	}

	// Detect API style if model is "auto"
	clientStyle := model.ProviderOpenAI
	if chatReq.Model == "auto" {
		style := service.DetectAPIStyle(c.Request, body)
		if style == service.StyleAnthropic {
			clientStyle = model.ProviderAnthropic
		}
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Select backend model based on detected API style
		models := h.lb.GetBackendModels(clientStyle)
		selected := h.lb.SelectModel(models, "round_robin")
		if selected == nil {
			c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError("no available models", "internal_error", model.ProviderOpenAI))
			return
		}

		// Build upstream request with potential transformation
		transformedBody, _ := h.transformer.TransformRequest(body, clientStyle, selected.Provider)
		// Replace model name with backend's technical_name for the upstream request
		transformedBody, _ = h.replaceModelName(transformedBody, selected.TechnicalName)
		upstreamReq, err := h.buildUpstreamRequest(c, transformedBody, selected)
		if err != nil {
			c.Data(http.StatusInternalServerError, "application/json", h.transformer.FormatError("failed to build request", "internal_error", model.ProviderOpenAI))
			return
		}

		resp, err := getSharedHTTPClient().Do(upstreamReq)
		if err != nil {
			lastErr = err
			h.lb.RecordFailure(selected.ID)
			continue
		}

		respStatus := resp.StatusCode

		// For streaming, don't read body here - let streaming handler read it directly
		if isStreaming {
			// For streaming, check status code first
			if respStatus >= 500 || respStatus == 401 || respStatus == 404 {
				resp.Body.Close()
				lastErr = fmt.Errorf("upstream error: %d", respStatus)
				h.lb.RecordFailure(selected.ID)
				continue
			}
			h.lb.RecordSuccess(selected.ID)
			h.handleStreamingResponse(c, resp, selected, clientStyle, usernameStr, startTime)
			return
		}

		// Non-streaming: read body
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Check if we should retry based on status code
		if respStatus >= 500 || respStatus == 401 || respStatus == 404 {
			lastErr = fmt.Errorf("upstream error: %d", respStatus)
			h.lb.RecordFailure(selected.ID)
			continue
		}

		h.lb.RecordSuccess(selected.ID)

		// Transform response if needed (includes error format conversion)
		transformedResp, _ := h.transformer.TransformResponse(respBody, model.ProviderOpenAI, selected.Provider)

		// Log the call
		h.logCall(c, usernameStr, selected, clientStyle, respStatus, string(transformedResp), "", startTime)

		// Copy response
		for k, v := range resp.Header {
			c.Header(k, v[0])
		}
		c.Header("X-Request-Id", generateRequestID())
		c.Data(respStatus, "application/json", transformedResp)
		return
	}

	c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(lastErr.Error(), "internal_error", model.ProviderOpenAI))
}

func (h *OpenAIHandler) handleStreamingResponse(c *gin.Context, resp *http.Response, selected *model.BackendModel, clientStyle model.Provider, username string, startTime time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Request-Id", generateRequestID())

	// backendProvider is what format the upstream returns
	backendProvider := selected.Provider

	// Handle different response formats from upstream
Flusher:
	for {
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			break
		}

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break Flusher
			}
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// SSE data line
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimSpace(data)

				// Check for [DONE]
				if data == "[DONE]" {
					c.Writer.Write([]byte("data: [DONE]\n\n"))
					flusher.Flush()
					break Flusher
				}

				// Transform if needed: backend provider format -> OpenAI format (this endpoint's format)
				transformed := h.transformStreamChunk(data, backendProvider)
				c.Writer.Write([]byte("data: " + transformed + "\n\n"))
				flusher.Flush()
			} else if strings.HasPrefix(line, "event:") {
				// Anthropic-style events need conversion
				c.Writer.Write([]byte(line + "\n"))
				flusher.Flush()
			}
		}
	}

	resp.Body.Close()

	// Log streaming request after it completes
	h.logCall(c, username, selected, clientStyle, 200, "", "", startTime)
}

func (h *OpenAIHandler) transformStreamChunk(chunk string, backendProvider model.Provider) string {
	// OpenAI handler always returns OpenAI format
	// If backend is Anthropic, convert from Anthropic to OpenAI
	if backendProvider == model.ProviderAnthropic {
		return h.convertAnthropicToOpenAIStream(chunk)
	}
	return chunk
}

func (h *OpenAIHandler) convertAnthropicToOpenAIStream(chunk string) string {
	// Anthropic: event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text","text":"..."}}
	// OpenAI: data: {"id":"...","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"..."}}]}

	var anthropicData map[string]interface{}
	if err := json.Unmarshal([]byte(chunk), &anthropicData); err != nil {
		return chunk
	}

	eventType, _ := anthropicData["type"].(string)
	switch eventType {
	case "content_block_delta":
		delta, _ := anthropicData["delta"].(map[string]interface{})
		if delta == nil {
			return chunk
		}
		deltaType, _ := delta["type"].(string)
		if deltaType == "text_delta" {
			text, _ := delta["text"].(string)
			index, _ := anthropicData["index"].(float64)
			return fmt.Sprintf(`{"id":"chatcmpl","object":"chat.completion.chunk","choices":[{"index":%d,"delta":{"content":"%s"},"finish_reason":null}]}`, int(index), escapeJSONString(text))
		}
	case "message_delta":
		usage, _ := anthropicData["usage"].(map[string]interface{})
		if usage != nil {
			outputTokens, _ := usage["output_tokens"].(float64)
			return fmt.Sprintf(`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":%d}}`, int(outputTokens))
		}
	}
	return chunk
}

func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)[1 : len(b)-1]
}

// buildUpstreamURL builds the correct upstream URL by properly joining baseURL and path
func buildUpstreamURL(baseURL, path string) string {
	// Ensure baseURL doesn't end with / and path doesn't start with /
	baseURL = strings.TrimSuffix(baseURL, "/")
	path = strings.TrimPrefix(path, "/")
	return baseURL + "/" + path
}

// replaceModelName replaces the model name in the request body with the backend's technical name
func (h *OpenAIHandler) replaceModelName(body []byte, technicalName string) ([]byte, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}
	req["model"] = technicalName
	return json.Marshal(req)
}

// Completions handles POST /v1/completions (legacy)
func (h *OpenAIHandler) Completions(c *gin.Context) {
	h.ChatCompletions(c) // Same logic
}

// Embeddings handles POST /v1/embeddings
func (h *OpenAIHandler) Embeddings(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("failed to read request body", "invalid_request_error", model.ProviderOpenAI))
		return
	}

	// Embeddings requires API key auth
	_, exists := c.Get("username")
	if !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderOpenAI))
		return
	}

	models := h.lb.GetBackendModels(model.ProviderOpenAI)
	selected := h.lb.SelectModel(models, "round_robin")
	if selected == nil {
		c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError("no available models", "internal_error", model.ProviderOpenAI))
		return
	}

	upstreamReq, _ := h.buildUpstreamRequest(c, body, selected)
	resp, err := getSharedHTTPClient().Do(upstreamReq)
	if err != nil {
		c.Data(http.StatusInternalServerError, "application/json", h.transformer.FormatError(err.Error(), "internal_error", model.ProviderOpenAI))
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	for k, v := range resp.Header {
		c.Header(k, v[0])
	}
	c.Data(resp.StatusCode, "application/json", respBody)
}

// ListModels handles GET /v1/models
func (h *OpenAIHandler) ListModels(c *gin.Context) {
	models := h.lb.GetBackendModels(model.ProviderOpenAI)

	response := gin.H{
		"object": "list",
		"data":   make([]gin.H, 0),
	}

	// Add "auto" model for adaptive API style
	response["data"] = append(response["data"].([]gin.H), gin.H{
		"id":        "auto",
		"object":    "model",
		"created":   time.Now().Unix(),
		"owned_by":  "viberouter",
		"capabilities": []string{"openai", "anthropic"},
	})

	for _, m := range models {
		modelInfo := gin.H{
			"id":       m.TechnicalName,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": m.Provider,
		}
		response["data"] = append(response["data"].([]gin.H), modelInfo)
	}

	c.JSON(http.StatusOK, response)
}

// GetModel handles GET /v1/models/:model
func (h *OpenAIHandler) GetModel(c *gin.Context) {
	modelName := c.Param("model")
	// Strip leading "/" from wildcard parameter
	modelName = strings.TrimPrefix(modelName, "/")

	models := h.lb.GetBackendModels("")
	var m model.BackendModel
	found := false
	for _, model := range models {
		if model.TechnicalName == modelName {
			m = model
			found = true
			break
		}
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "model not found", "type": "invalid_request_error"}})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":       m.TechnicalName,
		"object":   "model",
		"created":  time.Now().Unix(),
		"owned_by": m.Provider,
	})
}

func (h *OpenAIHandler) buildUpstreamRequest(c *gin.Context, body []byte, backend *model.BackendModel) (*http.Request, error) {
	url := buildUpstreamURL(backend.BaseURL, c.Request.URL.Path)

	req, err := http.NewRequest(c.Request.Method, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	req.Header = make(http.Header)
	for k, v := range c.Request.Header {
		req.Header[k] = v
	}

	switch backend.Provider {
	case model.ProviderOpenAI:
		req.Header.Set("Authorization", "Bearer "+backend.APIKey)
	case model.ProviderAnthropic:
		req.Header.Set("x-api-key", backend.APIKey)
	}

	// Remove hop-by-hop headers
	delete(req.Header, "Connection")
	delete(req.Header, "Keep-Alive")
	delete(req.Header, "Transfer-Encoding")

	return req, nil
}

func (h *OpenAIHandler) logCall(c *gin.Context, username string, backend *model.BackendModel, clientStyle model.Provider, status int, response, errorMsg string, start time.Time) {
	latency := int(time.Since(start).Milliseconds())
	log := &model.CallLog{
		AdminUsername:    username,
		ClientIP:         getRealIP(c),
		Provider:         backend.Provider,
		ModelName:        backend.TechnicalName,
		ModelDisplayName: backend.DisplayName,
		ApiStyle:         "openai",
		RequestPath:      c.Request.URL.Path,
		RequestMethod:    c.Request.Method,
		StatusCode:       status,
		ErrorMessage:     errorMsg,
		LatencyMs:        latency,
		CreatedAt:        time.Now(),
	}
	h.callLogSvc.Log(log)
}

func extractAPIKey(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return c.GetHeader("x-api-key")
}

func generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

// ChatCompletionRequest represents OpenAI chat completions request
type ChatCompletionRequest struct {
	Model       string                   `json:"model"`
	Messages    []ChatMessage            `json:"messages"`
	Temperature float64                  `json:"temperature,omitempty"`
	TopP        float64                  `json:"top_p,omitempty"`
	N           int                      `json:"n,omitempty"`
	Stream      bool                     `json:"stream,omitempty"`
	Stop        interface{}              `json:"stop,omitempty"`
	MaxTokens   int                      `json:"max_tokens,omitempty"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
	Functions   []map[string]interface{} `json:"functions,omitempty"`
	ToolChoice  interface{}             `json:"tool_choice,omitempty"`
}

type ChatMessage struct {
	Role         string                  `json:"role"`
	Content      interface{}             `json:"content"`
	Name         string                  `json:"name,omitempty"`
	ToolCallID   string                  `json:"tool_call_id,omitempty"`
	ToolFunction *ToolFunctionCall       `json:"tool_function,omitempty"`
}

type ToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
