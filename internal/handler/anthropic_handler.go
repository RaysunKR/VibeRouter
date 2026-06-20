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

	username, exists := c.Get("username")
	if !exists {
		c.Data(http.StatusUnauthorized, "application/json", h.transformer.FormatError("unauthorized", "authentication_error", model.ProviderAnthropic))
		return
	}
	usernameStr := username.(string)

	isStreaming := msgReq.Stream

	// Client API style: only meaningful for "auto".
	clientStyle := model.ProviderAnthropic
	if msgReq.Model == "auto" {
		style := service.DetectAPIStyle(c.Request, body)
		if style == service.StyleOpenAI {
			clientStyle = model.ProviderOpenAI
		}
	}

	// Route: complexity -> tier, long-context filter, priority failover order.
	candidates, info, rerr := h.lb.Route(routeInput(msgReq.Model, c.Request.Header, body, len(msgReq.Messages), len(msgReq.Tools) > 0))
	if rerr != nil {
		c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(rerr.Error(), "internal_error", model.ProviderAnthropic))
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

		resp, err := getAnthropicHTTPClient().Do(upstreamReq)
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
			h.handleStreamingResponse(c, resp, &selected, selected.Provider, usernameStr, info, startTime)
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
		transformedResp, _ := h.transformer.TransformResponse(respBody, model.ProviderAnthropic, selected.Provider)
		h.logCall(c, usernameStr, &selected, clientStyle, info, respStatus, string(transformedResp), "", startTime)

		for k, v := range resp.Header {
			c.Header(k, v[0])
		}
		c.Header("anthropic-rid", generateAnthropicRID())
		c.Data(resp.StatusCode, "application/json", transformedResp)
		return
	}

	c.Data(http.StatusServiceUnavailable, "application/json", h.transformer.FormatError(errString(lastErr), "internal_error", model.ProviderAnthropic))
}

func (h *AnthropicHandler) handleStreamingResponse(c *gin.Context, resp *http.Response, selected *model.BackendModel, backendProvider model.Provider, username string, info service.RouteInfo, startTime time.Time) {
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
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "event:") {
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
			eventType, transformed := h.transformToAnthropicStream(data, backendProvider)
			if eventType != "" {
				c.Writer.Write([]byte("event: " + eventType + "\n"))
			}
			c.Writer.Write([]byte("data: " + transformed + "\n\n"))
			flusher.Flush()
		}
	}

	resp.Body.Close()
	h.logCall(c, username, selected, selected.Provider, info, 200, "", "", startTime)
}

func (h *AnthropicHandler) transformToAnthropicStream(chunk string, provider model.Provider) (string, string) {
	if provider == model.ProviderOpenAI {
		return h.convertOpenAIStream(chunk)
	}
	return "", chunk
}

func (h *AnthropicHandler) convertOpenAIStream(chunk string) (string, string) {
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
	models := h.lb.GetAllModels()
	data := make([]gin.H, 0, len(models)+3)
	for _, v := range []string{"auto", "auto-advanced", "auto-basic"} {
		data = append(data, gin.H{"id": v, "type": "model", "display_name": v})
	}
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		data = append(data, gin.H{
			"id":           m.TechnicalName,
			"type":         "model",
			"display_name": m.Name,
			"version":      "1.0",
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
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

func (h *AnthropicHandler) logCall(c *gin.Context, username string, backend *model.BackendModel, clientStyle model.Provider, info service.RouteInfo, status int, response, errorMsg string, start time.Time) {
	latency := int(time.Since(start).Milliseconds())
	apiStyle := "anthropic"
	if clientStyle == model.ProviderOpenAI {
		apiStyle = "openai"
	}
	h.callLogSvc.Log(&model.CallLog{
		Username:         username,
		ClientIP:         getRealIP(c),
		Provider:         backend.Provider,
		ModelName:        backend.TechnicalName,
		ModelDisplayName: backend.Name,
		Tier:             info.Tier,
		IsLongContext:    info.IsLongContext,
		ApiStyle:         apiStyle,
		RequestPath:      c.Request.URL.Path,
		RequestMethod:    c.Request.Method,
		StatusCode:       status,
		ErrorMessage:     errorMsg,
		LatencyMs:        latency,
	})
}

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
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}
