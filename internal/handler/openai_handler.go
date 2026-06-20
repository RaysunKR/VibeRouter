package handler

import (
	"bufio"
	"bytes"
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

// routeInput builds the routing signals from an OpenAI-style request body.
func routeInput(modelName string, header http.Header, body []byte, turns int, hasTools bool) service.RouteRequest {
	return service.RouteRequest{
		ModelName:      modelName,
		Header:         header,
		MessageTurns:   turns,
		EstInputTokens: len(body) / 4,
		HasTools:       hasTools,
		HasCode:        bytes.Contains(body, []byte("```")),
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

	username, exists := c.Get("username")
	if !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderOpenAI))
		return
	}
	usernameStr := username.(string)

	isStreaming := chatReq.Stream

	// Client API style: only meaningful for "auto".
	clientStyle := model.ProviderOpenAI
	if chatReq.Model == "auto" {
		style := service.DetectAPIStyle(c.Request, body)
		if style == service.StyleAnthropic {
			clientStyle = model.ProviderAnthropic
		}
	}

	// Route: complexity -> tier, long-context filter, priority failover order.
	candidates, info, rerr := h.lb.Route(routeInput(chatReq.Model, c.Request.Header, body, len(chatReq.Messages), len(chatReq.Tools) > 0))
	if rerr != nil {
		c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(rerr.Error(), "internal_error", model.ProviderOpenAI))
		return
	}

	var lastErr error
	for _, selected := range candidates {
		transformedBody, _ := h.transformer.TransformRequest(body, clientStyle, selected.Provider)
		transformedBody, _ = h.replaceModelName(transformedBody, selected.TechnicalName)
		upstreamReq, err := h.buildUpstreamRequest(c, transformedBody, &selected)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := getSharedHTTPClient().Do(upstreamReq)
		if err != nil {
			lastErr = err
			h.lb.RecordFailure(selected.Key)
			continue
		}

		respStatus := resp.StatusCode

		if isStreaming {
			if respStatus >= 500 || respStatus == 401 || respStatus == 404 {
				resp.Body.Close()
				lastErr = fmt.Errorf("upstream error: %d", respStatus)
				h.lb.RecordFailure(selected.Key)
				continue
			}
			h.lb.RecordSuccess(selected.Key)
			h.handleStreamingResponse(c, resp, &selected, clientStyle, usernameStr, info, startTime)
			return
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if respStatus >= 500 || respStatus == 401 || respStatus == 404 {
			lastErr = fmt.Errorf("upstream error: %d", respStatus)
			h.lb.RecordFailure(selected.Key)
			continue
		}

		h.lb.RecordSuccess(selected.Key)
		transformedResp, _ := h.transformer.TransformResponse(respBody, model.ProviderOpenAI, selected.Provider)
		h.logCall(c, usernameStr, &selected, clientStyle, info, respStatus, string(transformedResp), "", startTime)

		for k, v := range resp.Header {
			c.Header(k, v[0])
		}
		c.Header("X-Request-Id", generateRequestID())
		c.Data(respStatus, "application/json", transformedResp)
		return
	}

	c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(errString(lastErr), "internal_error", model.ProviderOpenAI))
}

func (h *OpenAIHandler) handleStreamingResponse(c *gin.Context, resp *http.Response, selected *model.BackendModel, clientStyle model.Provider, username string, info service.RouteInfo, startTime time.Time) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Request-Id", generateRequestID())

	backendProvider := selected.Provider

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

			if strings.HasPrefix(line, "data:") {
				data := strings.TrimPrefix(line, "data:")
				data = strings.TrimSpace(data)

				if data == "[DONE]" {
					c.Writer.Write([]byte("data: [DONE]\n\n"))
					flusher.Flush()
					break Flusher
				}

				transformed := h.transformStreamChunk(data, backendProvider)
				c.Writer.Write([]byte("data: " + transformed + "\n\n"))
				flusher.Flush()
			} else if strings.HasPrefix(line, "event:") {
				c.Writer.Write([]byte(line + "\n"))
				flusher.Flush()
			}
		}
	}

	resp.Body.Close()
	h.logCall(c, username, selected, clientStyle, info, 200, "", "", startTime)
}

func (h *OpenAIHandler) transformStreamChunk(chunk string, backendProvider model.Provider) string {
	if backendProvider == model.ProviderAnthropic {
		return h.convertAnthropicToOpenAIStream(chunk)
	}
	return chunk
}

func (h *OpenAIHandler) convertAnthropicToOpenAIStream(chunk string) string {
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

// buildUpstreamURL joins baseURL and path.
func buildUpstreamURL(baseURL, path string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	path = strings.TrimPrefix(path, "/")
	return baseURL + "/" + path
}

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
	h.ChatCompletions(c)
}

// Embeddings handles POST /v1/embeddings
func (h *OpenAIHandler) Embeddings(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Data(http.StatusBadRequest, "application/json", h.transformer.FormatError("failed to read request body", "invalid_request_error", model.ProviderOpenAI))
		return
	}

	if _, exists := c.Get("username"); !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderOpenAI))
		return
	}

	var embReq struct {
		Model string `json:"model"`
	}
	json.Unmarshal(body, &embReq)

	candidates, _, rerr := h.lb.Route(routeInput(embReq.Model, c.Request.Header, body, 0, false))
	if rerr != nil || len(candidates) == 0 {
		c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError("no available models", "internal_error", model.ProviderOpenAI))
		return
	}
	selected := candidates[0]

	upstreamReq, _ := h.buildUpstreamRequest(c, body, &selected)
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
	models := h.lb.GetAllModels()

	data := make([]gin.H, 0, len(models)+3)
	for _, v := range []string{"auto", "auto-advanced", "auto-basic"} {
		data = append(data, gin.H{
			"id":           v,
			"object":       "model",
			"created":      time.Now().Unix(),
			"owned_by":     "viberouter",
			"capabilities": []string{"openai", "anthropic"},
		})
	}
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		data = append(data, gin.H{
			"id":       m.TechnicalName,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": m.Provider,
		})
	}

	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

// GetModel handles GET /v1/models/:model
func (h *OpenAIHandler) GetModel(c *gin.Context) {
	modelName := strings.TrimPrefix(c.Param("model"), "/")

	m, ok := h.lb.FindModel(modelName)
	if !ok {
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

	delete(req.Header, "Connection")
	delete(req.Header, "Keep-Alive")
	delete(req.Header, "Transfer-Encoding")

	return req, nil
}

func (h *OpenAIHandler) logCall(c *gin.Context, username string, backend *model.BackendModel, clientStyle model.Provider, info service.RouteInfo, status int, response, errorMsg string, start time.Time) {
	latency := int(time.Since(start).Milliseconds())
	h.callLogSvc.Log(&model.CallLog{
		Username:         username,
		ClientIP:         getRealIP(c),
		Provider:         backend.Provider,
		ModelName:        backend.TechnicalName,
		ModelDisplayName: backend.Name,
		Tier:             info.Tier,
		IsLongContext:    info.IsLongContext,
		ApiStyle:         "openai",
		RequestPath:      c.Request.URL.Path,
		RequestMethod:    c.Request.Method,
		StatusCode:       status,
		ErrorMessage:     errorMsg,
		LatencyMs:        latency,
	})
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

func errString(err error) string {
	if err == nil {
		return "no available models"
	}
	return err.Error()
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
	Role         string            `json:"role"`
	Content      interface{}       `json:"content"`
	Name         string            `json:"name,omitempty"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	ToolFunction *ToolFunctionCall `json:"tool_function,omitempty"`
}

type ToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
