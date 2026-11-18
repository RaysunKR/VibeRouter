package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/webtools"
)

const (
	protocolVersionLatest = "2025-11-25"
	protocolVersionJune   = "2025-06-18"
	protocolVersionMarch  = "2025-03-26"
)

var supportedProtocolVersions = map[string]struct{}{
	protocolVersionLatest: {},
	protocolVersionJune:   {},
	protocolVersionMarch:  {},
}

type handler struct {
	cfg     *config.Store
	service *webtools.Service
}

func NewHandler(cfg *config.Store, service *webtools.Service) http.Handler {
	return &handler{cfg: cfg, service: service}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Snapshot().WebTools.MCPEnabled {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if !validOrigin(r) {
		http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if !validAccept(r.Header.Values("Accept")) {
		http.Error(w, http.StatusText(http.StatusNotAcceptable), http.StatusNotAcceptable)
		return
	}
	if version := r.Header.Get("MCP-Protocol-Version"); version != "" {
		if _, ok := supportedProtocolVersions[version]; !ok {
			http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
			return
		}
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil || !json.Valid(body) {
		writeRPCError(w, nullID, -32700, "Parse error")
		return
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(body, &message); err != nil || message == nil {
		writeRPCError(w, nullID, -32600, "Invalid Request")
		return
	}

	id, hasID := message["id"]
	if hasID && !validID(id) {
		writeRPCError(w, nullID, -32600, "Invalid Request")
		return
	}
	if !hasID {
		id = nullID
	}
	var jsonrpc string
	if raw, ok := message["jsonrpc"]; !ok || json.Unmarshal(raw, &jsonrpc) != nil || jsonrpc != "2.0" {
		writeRPCError(w, id, -32600, "Invalid Request")
		return
	}

	rawMethod, hasMethod := message["method"]
	if !hasMethod {
		if hasID && (message["result"] != nil || message["error"] != nil) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPCError(w, id, -32600, "Invalid Request")
		return
	}
	var method string
	if json.Unmarshal(rawMethod, &method) != nil {
		writeRPCError(w, id, -32600, "Invalid Request")
		return
	}
	if !hasID {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, rpcErr := h.handleRequest(r.Context(), method, message["params"])
	if rpcErr != nil {
		writeRPCError(w, id, rpcErr.Code, rpcErr.Message)
		return
	}
	writeRPCResponse(w, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (h *handler) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var input struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeInitializeParams(params, &input); err != nil {
			return nil, invalidParams()
		}
		version := input.ProtocolVersion
		if _, ok := supportedProtocolVersions[version]; !ok {
			version = protocolVersionLatest
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{"name": "viberouter", "version": "0.1.0"},
		}, nil
	case "notifications/initialized":
		return map[string]any{}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return h.listTools(), nil
	case "tools/call":
		return h.callTool(ctx, params)
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

type listedTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

func (h *handler) listTools() any {
	definitions := h.service.Definitions()
	tools := make([]listedTool, len(definitions))
	for i, definition := range definitions {
		tools[i] = listedTool{
			Name:         definition.Name,
			Description:  definition.Description,
			InputSchema:  definition.InputSchema,
			OutputSchema: definition.OutputSchema,
		}
	}
	return map[string]any{"tools": tools}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content           []toolContent  `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError"`
}

func (h *handler) callTool(ctx context.Context, raw json.RawMessage) (any, *rpcError) {
	var params callParams
	if err := decodeObject(raw, &params, false); err != nil || params.Name == "" {
		return nil, invalidParams()
	}

	var result any
	switch params.Name {
	case webtools.WebSearchName:
		var input webtools.SearchRequest
		if err := decodeObject(params.Arguments, &input, true); err != nil {
			return nil, invalidParams()
		}
		output, err := h.service.Search(ctx, input)
		if err != nil {
			return h.executionError(params.Name, err), nil
		}
		result = output
	case webtools.WebFetchName:
		var input webtools.FetchRequest
		if err := decodeObject(params.Arguments, &input, true); err != nil {
			return nil, invalidParams()
		}
		output, err := h.service.Fetch(ctx, input)
		if err != nil {
			return h.executionError(params.Name, err), nil
		}
		result = output
	default:
		return nil, invalidParams()
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return h.executionError(params.Name, err), nil
	}
	var structured map[string]any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return h.executionError(params.Name, err), nil
	}
	return toolResult{
		Content:           []toolContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: structured,
	}, nil
}

func (h *handler) executionError(name string, err error) toolResult {
	message := h.redact(err.Error())
	return toolResult{
		Content: []toolContent{{Type: "text", Text: name + ": " + message}},
		IsError: true,
	}
}

func (h *handler) redact(message string) string {
	for _, provider := range h.cfg.Snapshot().WebTools.Providers {
		if provider.APIKey == "" {
			continue
		}
		message = strings.ReplaceAll(message, provider.APIKey, "[REDACTED]")
		message = strings.ReplaceAll(message, url.QueryEscape(provider.APIKey), "[REDACTED]")
	}
	return message
}

func decodeInitializeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("expected object")
	}
	return json.Unmarshal(trimmed, target)
}

func decodeObject(raw json.RawMessage, target any, emptyAllowed bool) error {
	if len(raw) == 0 {
		if !emptyAllowed {
			return errors.New("missing object")
		}
		raw = json.RawMessage("{}")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("expected object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected data after object")
	}
	return nil
}

var nullID = json.RawMessage("null")

func validID(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var id any
	if decoder.Decode(&id) != nil {
		return false
	}
	switch id.(type) {
	case nil, string, json.Number:
		return true
	default:
		return false
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func invalidParams() *rpcError {
	return &rpcError{Code: -32602, Message: "Invalid params"}
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeRPCResponse(w, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	})
}

func writeRPCResponse(w http.ResponseWriter, response rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func validOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	origin, err := url.Parse(strings.TrimSpace(values[0]))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	if origin.Path != "" && origin.Path != "/" {
		return false
	}
	return strings.EqualFold(origin.Host, r.Host)
}

func validAccept(values []string) bool {
	var jsonAccepted, eventsAccepted bool
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil || qualityZero(params["q"]) {
				continue
			}
			switch strings.ToLower(mediaType) {
			case "*/*":
				jsonAccepted, eventsAccepted = true, true
			case "application/json":
				jsonAccepted = true
			case "text/event-stream":
				eventsAccepted = true
			}
		}
	}
	return jsonAccepted && eventsAccepted
}

func qualityZero(value string) bool {
	if value == "" {
		return false
	}
	quality, err := strconv.ParseFloat(value, 64)
	return err != nil || quality <= 0
}
