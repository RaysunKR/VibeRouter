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

// Shared HTTP client for Anthropic handler
var (
	anthropicClient     *http.Client
	anthropicClientOnce sync.Once
)

func getAnthropicHTTPClient() *http.Client {
	anthropicClientOnce.Do(func() {
		anthropicClient = &http.Client{
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
	return anthropicClient
}

// AnthropicHandler handles Anthropic-compatible API requests
type AnthropicHandler struct {
	lb          *service.LoadBalancer
	callLogSvc  *service.CallLogService
	transformer *service.RequestTransformer
}

func NewAnthropicHandler(lb *service.LoadBalancer, cls *service.CallLogService) *AnthropicHandler {
	return &AnthropicHandler{
		lb:          lb,
		callLogSvc:  cls,
		transformer: service.NewRequestTransformer(),
	}
}

// Messages handles POST /v1/messages
func (h *AnthropicHandler) Messages(c *gin.Context) {
	startTime := time.Now()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("failed to read request body", "invalid_request_error", model.ProviderAnthropic))
		return
	}

	var msgReq AnthropicMessageRequest
	if err := json.Unmarshal(body, &msgReq); err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("invalid JSON body", "invalid_request_error", model.ProviderAnthropic))
		return
	}

	// Get username from context (set by auth middleware)
	username, exists := c.Get("username")
	if !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderAnthropic))
		return
	}
	usernameStr := username.(string)

	isStreaming := msgReq.Stream

	// Detect API style for auto model selection
	clientStyle := model.ProviderAnthropic
	if msgReq.Model == "auto" {
		style := service.DetectAPIStyle(c.Request, body)
		if style == service.StyleOpenAI {
			clientStyle = model.ProviderOpenAI
		}
	}

	var lastErr error
	maxRetries := 2
	if isStreaming {
		maxRetries = 1
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Select backend model - filter by client style (for auto) or Anthropic
		models := h.lb.GetBackendModels(clientStyle)
		selected := h.lb.SelectModel(models, "round_robin")
		if selected == nil {
			modelType := "Anthropic"
			if clientStyle == model.ProviderOpenAI {
				modelType = "OpenAI"
			}
			c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError("no available "+modelType+" models", "internal_error", model.ProviderAnthropic))
			return
		}

		transformedBody, _ := h.transformer.TransformRequest(body, clientStyle, selected.Provider)
		// Replace model name with backend's technical_name for the upstream request
		transformedBody, _ = h.replaceModelName(transformedBody, selected.TechnicalName)
		upstreamReq, err := h.buildUpstreamRequest(c, transformedBody, selected)
		if err != nil {
			c.Data(http.StatusInternalServerError, "application/json", h.transformer.FormatError("failed to build request", "internal_error", model.ProviderAnthropic))
			return
		}

		resp, err := getAnthropicHTTPClient().Do(upstreamReq)
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
			h.handleStreamingResponse(c, resp, selected, selected.Provider, usernameStr, startTime)
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
		transformedResp, _ := h.transformer.TransformResponse(respBody, model.ProviderAnthropic, selected.Provider)

		h.logCall(c, usernameStr, selected, clientStyle, respStatus, string(transformedResp), "", startTime)

		for k, v := range resp.Header {
			c.Header(k, v[0])
		}
		c.Header("anthropic-rid", generateAnthropicRID())
		c.Data(resp.StatusCode, "application/json", transformedResp)
		return
	}

	c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(lastErr.Error(), "internal_error", model.ProviderAnthropic))
}

func (h *AnthropicHandler) handleStreamingResponse(c *gin.Context, resp *http.Response, selected *model.BackendModel, backendProvider model.Provider, username string, startTime time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("anthropic-rid", generateAnthropicRID())
	c.Header("anthropic-version", "2023-06-01")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	reader := bufio.NewReader(resp.Body)

	// Forward all SSE events from upstream
	// For Anthropic backends: forward as-is
	// For OpenAI backends: transform to Anthropic format
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Collect event type if present
		var eventType string
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimPrefix(line, "event:")
			eventType = strings.TrimSpace(eventType)
			c.Writer.Write([]byte(line + "\n"))
			flusher.Flush()
			continue
		}

		if strings.HasPrefix(line, "data:") {
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)

			if data == "[DONE]" {
				break
			}

			// Transform to Anthropic format if needed
			eventType, transformed := h.transformToAnthropicStream(data, backendProvider)
			if eventType != "" {
				c.Writer.Write([]byte("event: " + eventType + "\n"))
			}
			c.Writer.Write([]byte("data: " + transformed + "\n\n"))
			flusher.Flush()
		}
	}

	resp.Body.Close()

	// Log streaming request after it completes
	h.logCall(c, username, selected, selected.Provider, 200, "", "", startTime)
}

func (h *AnthropicHandler) transformToAnthropicStream(chunk string, provider model.Provider) (string, string) {
	if provider == model.ProviderOpenAI {
		return h.convertOpenAIStream(chunk)
	}
	return "", chunk
}

func (h *AnthropicHandler) convertOpenAIStream(chunk string) (string, string) {
	// OpenAI: data: {"id":"...","choices":[{"delta":{"content":"..."}}]}
	// Anthropic: event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text","text":"..."}}

	var openAIData map[string]interface{}
	if err := json.Unmarshal([]byte(chunk), &openAIData); err != nil {
		return "", chunk
	}

	choices, ok := openAIData["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", chunk
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return "", chunk
	}

	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return "", chunk
	}

	content, hasContent := delta["content"].(string)
	finishReason, hasFinish := choice["finish_reason"].(string)

	if hasContent && content != "" {
		return "content_block_delta", fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text","text":%s}}`,
			escapeJSONString(content))
	}

	if hasFinish && finishReason == "stop" {
		// Extract usage info if available
		usage, _ := openAIData["usage"].(map[string]interface{})
		outputTokens := 0
		if usage != nil {
			if ct, ok := usage["completion_tokens"].(float64); ok {
				outputTokens = int(ct)
			}
		}
		if outputTokens == 0 {
			outputTokens = 1
		}
		return "message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":%d}}`, outputTokens)
	}

	return "", chunk
}

// ListModels handles GET /v1/models
func (h *AnthropicHandler) ListModels(c *gin.Context) {
	models := h.lb.GetBackendModels(model.ProviderAnthropic)

	response := gin.H{
		"data": make([]gin.H, 0),
	}
	for _, m := range models {
		modelInfo := gin.H{
			"id":          m.TechnicalName,
			"type":        "model",
			"display_name": m.DisplayName,
			"version":     "1.0",
		}
		response["data"] = append(response["data"].([]gin.H), modelInfo)
	}

	c.JSON(http.StatusOK, response)
}

func (h *AnthropicHandler) buildUpstreamRequest(c *gin.Context, body []byte, backend *model.BackendModel) (*http.Request, error) {
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
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	delete(req.Header, "Connection")

	return req, nil
}

func (h *AnthropicHandler) logCall(c *gin.Context, username string, backend *model.BackendModel, clientStyle model.Provider, status int, response, errorMsg string, start time.Time) {
	latency := int(time.Since(start).Milliseconds())
	apiStyle := "anthropic"
	if clientStyle == model.ProviderOpenAI {
		apiStyle = "openai"
	}
	log := &model.CallLog{
		AdminUsername:    username,
		ClientIP:         getRealIP(c),
		Provider:         backend.Provider,
		ModelName:        backend.TechnicalName,
		ModelDisplayName: backend.DisplayName,
		ApiStyle:         apiStyle,
		RequestPath:      c.Request.URL.Path,
		RequestMethod:    c.Request.Method,
		StatusCode:       status,
		ErrorMessage:     errorMsg,
		LatencyMs:        latency,
		CreatedAt:        time.Now(),
	}
	h.callLogSvc.Log(log)
}

// replaceModelName replaces the model name in the request body with the backend's technical name
func (h *AnthropicHandler) replaceModelName(body []byte, technicalName string) ([]byte, error) {
	var req map[string]interface{}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, err
	}
	req["model"] = technicalName
	return json.Marshal(req)
}

func generateAnthropicRID() string {
	return fmt.Sprintf("rid_%d", time.Now().UnixNano())
}

// AnthropicMessageRequest represents Anthropic Messages API request
type AnthropicMessageRequest struct {
	Model         string                   `json:"model"`
	Messages      []AnthropicMessage       `json:"messages"`
	MaxTokens     int                      `json:"max_tokens"`
	System        interface{}              `json:"system,omitempty"`
	Temperature   float64                  `json:"temperature,omitempty"`
	TopP          float64                  `json:"top_p,omitempty"`
	TopK          int                      `json:"top_k,omitempty"`
	StopSequences []string                 `json:"stop_sequences,omitempty"`
	Stream        bool                     `json:"stream,omitempty"`
	Tools         []map[string]interface{} `json:"tools,omitempty"`
	ToolChoice    interface{}             `json:"tool_choice,omitempty"`
	Thinking      *map[string]interface{} `json:"thinking,omitempty"`
	Betas         []string                 `json:"betas,omitempty"`
	Metadata      *map[string]interface{} `json:"metadata,omitempty"`
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content interface{} `json:"content"`
}

type MessageStartEvent struct {
	Type    string     `json:"type"`
	Message MessageInfo `json:"message"`
}

type MessageInfo struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Role       string      `json:"role"`
	Content    []interface{} `json:"content"`
	Model      string      `json:"model"`
	StopReason *string     `json:"stop_reason,omitempty"`
}

type ContentBlockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta Delta  `json:"delta"`
}

type Delta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}
