package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/webtools"
)

const (
	testAccept = "application/json, text/event-stream"
	testAPIKey = "test-secret"
)

type testRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type observedRequest struct {
	Path          string
	Authorization string
	Body          map[string]any
}

func newTestHandler(t *testing.T, providerHandler http.HandlerFunc) (*config.Store, http.Handler) {
	t.Helper()
	if providerHandler == nil {
		providerHandler = successfulProvider
	}
	provider := httptest.NewServer(providerHandler)
	t.Cleanup(provider.Close)

	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Mutate(func(c *config.Config) error {
		c.WebTools.Enabled = true
		c.WebTools.MCPEnabled = true
		c.WebTools.SearchProvider = "deepseek"
		c.WebTools.FetchProvider = "deepseek"
		c.WebTools.TimeoutSec = 5
		c.WebTools.Providers = []config.WebToolProviderConfig{{
			ID:      "deepseek",
			Enabled: true,
			APIKey:  testAPIKey,
			BaseURL: provider.URL,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return cfg, NewHandler(cfg, webtools.New(cfg))
}

func successfulProvider(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/tools/search":
		_, _ = w.Write([]byte(`{"results":[]}`))
	case "/v1/tools/fetch":
		_, _ = w.Write([]byte(`{"title":"Example","content":"Content"}`))
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func makeRequest(t *testing.T, method string, id, params any) string {
	t.Helper()
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func makeToolCall(t *testing.T, name string, arguments any) string {
	t.Helper()
	return makeRequest(t, "tools/call", "call-1", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
}

func post(t *testing.T, handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://mcp.test/mcp", strings.NewReader(body))
	request.Header.Set("Accept", testAccept)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRPCResponse(t *testing.T, response *httptest.ResponseRecorder) testRPCResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	var decoded testRPCResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
	if decoded.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc = %q, want 2.0", decoded.JSONRPC)
	}
	return decoded
}

func requireRPCError(t *testing.T, response *httptest.ResponseRecorder, code int) testRPCResponse {
	t.Helper()
	decoded := decodeRPCResponse(t, response)
	if decoded.Error == nil || decoded.Error.Code != code {
		t.Fatalf("error = %+v, want code %d; body = %s", decoded.Error, code, response.Body.String())
	}
	return decoded
}

func TestInitialize(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	for _, version := range []string{protocolVersionLatest, protocolVersionJune, protocolVersionMarch} {
		t.Run(version, func(t *testing.T) {
			body := makeRequest(t, "initialize", "init-"+version, map[string]any{
				"protocolVersion": version,
				"capabilities":    map[string]any{"roots": map[string]any{"listChanged": true}},
				"clientInfo":      map[string]any{"name": "test-client", "version": "1.0.0"},
			})
			response := post(t, handler, body, nil)
			decoded := decodeRPCResponse(t, response)
			if decoded.Error != nil {
				t.Fatalf("unexpected error: %+v", decoded.Error)
			}
			if string(decoded.ID) != `"init-`+version+`"` {
				t.Fatalf("id = %s", decoded.ID)
			}
			var result struct {
				ProtocolVersion string `json:"protocolVersion"`
				Capabilities    struct {
					Tools struct {
						ListChanged bool `json:"listChanged"`
					} `json:"tools"`
				} `json:"capabilities"`
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}
			if err := json.Unmarshal(decoded.Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.ProtocolVersion != version {
				t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, version)
			}
			if result.Capabilities.Tools.ListChanged {
				t.Error("capabilities.tools.listChanged = true, want false")
			}
			if result.ServerInfo.Name != "viberouter" || result.ServerInfo.Version != "0.1.0" {
				t.Errorf("serverInfo = %+v", result.ServerInfo)
			}
			if session := response.Header().Get("MCP-Session-Id"); session != "" {
				t.Errorf("unexpected MCP-Session-Id = %q", session)
			}
		})
	}
}

func TestPingAndIDTypes(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	tests := []struct {
		name   string
		id     any
		wantID string
	}{
		{name: "string", id: "request-id", wantID: `"request-id"`},
		{name: "number", id: 42, wantID: `42`},
		{name: "null", id: nil, wantID: `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := decodeRPCResponse(t, post(t, handler, makeRequest(t, "ping", test.id, nil), nil))
			if decoded.Error != nil || string(decoded.Result) != "{}" {
				t.Fatalf("ping response = %s", decoded.Result)
			}
			if string(decoded.ID) != test.wantID {
				t.Errorf("id = %s, want %s", decoded.ID, test.wantID)
			}
		})
	}
}

func TestToolsList(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	decoded := decodeRPCResponse(t, post(t, handler, makeRequest(t, "tools/list", 1, nil), nil))
	if decoded.Error != nil {
		t.Fatalf("unexpected error: %+v", decoded.Error)
	}
	var result struct {
		Tools []struct {
			Name         string         `json:"name"`
			Description  string         `json:"description"`
			InputSchema  map[string]any `json:"inputSchema"`
			OutputSchema map[string]any `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2; result = %s", len(result.Tools), decoded.Result)
	}
	for i, wantName := range []string{webtools.WebSearchName, webtools.WebFetchName} {
		tool := result.Tools[i]
		if tool.Name != wantName || tool.Description == "" {
			t.Errorf("tool %d = %+v", i, tool)
		}
		if tool.InputSchema["type"] != "object" || tool.OutputSchema["type"] != "object" {
			t.Errorf("tool %q schemas = %+v / %+v", tool.Name, tool.InputSchema, tool.OutputSchema)
		}
	}
}

func TestSearchCall(t *testing.T) {
	observed := make(chan observedRequest, 1)
	_, handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		observed <- observedRequest{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Result","url":"https://example.com/article","snippet":"Snippet","content":"Full content","site_name":"Example","date":"2026-01-02"}]}`))
	})
	body := makeToolCall(t, webtools.WebSearchName, map[string]any{
		"query":           "golang testing",
		"count":           2,
		"include_content": true,
		"allowed_domains": []string{"example.com"},
	})
	decoded := decodeRPCResponse(t, post(t, handler, body, nil))
	if decoded.Error != nil {
		t.Fatalf("unexpected error: %+v", decoded.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tool result = %s", decoded.Result)
	}
	var structured webtools.SearchResult
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatal(err)
	}
	if structured.Provider != "deepseek" || structured.Query != "golang testing" || len(structured.Results) != 1 {
		t.Fatalf("structuredContent = %+v", structured)
	}
	if structured.Results[0].Title != "Result" || structured.Results[0].Content != "Full content" || structured.Results[0].PublishedAt != "2026-01-02" {
		t.Errorf("search item = %+v", structured.Results[0])
	}
	assertTextMatchesStructured(t, result.Content[0].Text, result.StructuredContent)

	request := <-observed
	if request.Path != "/v1/tools/search" || request.Authorization != "Bearer "+testAPIKey {
		t.Errorf("provider request = %+v", request)
	}
	if request.Body["query"] != "golang testing" || request.Body["count"] != float64(2) || request.Body["need_content"] != true {
		t.Errorf("provider body = %+v", request.Body)
	}
}

func TestFetchCall(t *testing.T) {
	observed := make(chan observedRequest, 1)
	_, handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		observed <- observedRequest{Path: r.URL.Path, Authorization: r.Header.Get("Authorization"), Body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Fetched title","content":"Fetched content"}`))
	})
	body := makeToolCall(t, webtools.WebFetchName, map[string]any{
		"url":             "http://page.test/article",
		"allowed_domains": []string{"page.test"},
	})
	decoded := decodeRPCResponse(t, post(t, handler, body, nil))
	if decoded.Error != nil {
		t.Fatalf("unexpected error: %+v", decoded.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tool result = %s", decoded.Result)
	}
	var structured webtools.FetchResult
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatal(err)
	}
	if structured.Provider != "deepseek" || structured.URL != "http://page.test/article" || structured.Title != "Fetched title" || structured.Content != "Fetched content" || structured.Format != "markdown" {
		t.Fatalf("structuredContent = %+v", structured)
	}
	assertTextMatchesStructured(t, result.Content[0].Text, result.StructuredContent)

	request := <-observed
	if request.Path != "/v1/tools/fetch" || request.Authorization != "Bearer "+testAPIKey {
		t.Errorf("provider request = %+v", request)
	}
	if request.Body["url"] != "http://page.test/article" {
		t.Errorf("provider body = %+v", request.Body)
	}
}

func assertTextMatchesStructured(t *testing.T, text string, structured json.RawMessage) {
	t.Helper()
	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil {
		t.Fatalf("content text is not JSON: %v", err)
	}
	if err := json.Unmarshal(structured, &fromStructured); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromText, fromStructured) {
		t.Errorf("text = %s, structuredContent = %s", text, structured)
	}
}

func TestNotificationsAndResponsesAccepted(t *testing.T) {
	_, handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("notification unexpectedly called provider: %s", r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	})
	bodies := []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"unknown/notification"}`,
		`{"jsonrpc":"2.0","id":"response-1","result":{}}`,
		`{"jsonrpc":"2.0","id":9,"error":{"code":-32000,"message":"client error"}}`,
		`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"web_search","arguments":{"query":"not executed"}}}`,
	}
	for _, body := range bodies {
		response := post(t, handler, body, nil)
		if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
			t.Errorf("body %s: status = %d, response body = %q", body, response.Code, response.Body.String())
		}
		if session := response.Header().Get("MCP-Session-Id"); session != "" {
			t.Errorf("unexpected MCP-Session-Id = %q", session)
		}
	}
}

func TestGetAndDeleteNotAllowed(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		request := httptest.NewRequest(method, "http://mcp.test/mcp", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, response.Code)
		}
		if allow := response.Header().Get("Allow"); allow != http.MethodPost {
			t.Errorf("%s Allow = %q, want POST", method, allow)
		}
	}
}

func TestOriginAndAcceptValidation(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	body := makeRequest(t, "ping", 1, nil)
	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{name: "no origin", want: http.StatusOK},
		{name: "same HTTP origin", headers: map[string]string{"Origin": "http://mcp.test"}, want: http.StatusOK},
		{name: "same HTTPS origin", headers: map[string]string{"Origin": "https://mcp.test"}, want: http.StatusOK},
		{name: "different origin host", headers: map[string]string{"Origin": "https://other.test"}, want: http.StatusForbidden},
		{name: "different origin port", headers: map[string]string{"Origin": "http://mcp.test:8080"}, want: http.StatusForbidden},
		{name: "invalid origin scheme", headers: map[string]string{"Origin": "file://mcp.test"}, want: http.StatusForbidden},
		{name: "origin with path", headers: map[string]string{"Origin": "http://mcp.test/path"}, want: http.StatusForbidden},
		{name: "wildcard accept", headers: map[string]string{"Accept": "*/*"}, want: http.StatusOK},
		{name: "accept with parameters", headers: map[string]string{"Accept": "application/json;charset=utf-8, text/event-stream;q=0.5"}, want: http.StatusOK},
		{name: "JSON only", headers: map[string]string{"Accept": "application/json"}, want: http.StatusNotAcceptable},
		{name: "events only", headers: map[string]string{"Accept": "text/event-stream"}, want: http.StatusNotAcceptable},
		{name: "missing accept", headers: map[string]string{"Accept": ""}, want: http.StatusNotAcceptable},
		{name: "zero quality wildcard", headers: map[string]string{"Accept": "*/*;q=0"}, want: http.StatusNotAcceptable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := post(t, handler, body, test.headers)
			if response.Code != test.want {
				t.Errorf("status = %d, want %d; body = %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestProtocolVersionHeader(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	body := makeRequest(t, "ping", 1, nil)
	for _, version := range []string{protocolVersionLatest, protocolVersionJune, protocolVersionMarch} {
		response := post(t, handler, body, map[string]string{"MCP-Protocol-Version": version})
		if response.Code != http.StatusOK {
			t.Errorf("version %s: status = %d, want 200", version, response.Code)
		}
	}
	response := post(t, handler, body, map[string]string{"MCP-Protocol-Version": "2024-01-01"})
	if response.Code != http.StatusBadRequest {
		t.Errorf("unsupported version status = %d, want 400", response.Code)
	}
}

func TestMCPDisabled(t *testing.T) {
	cfg, handler := newTestHandler(t, nil)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.WebTools.MCPEnabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := post(t, handler, makeRequest(t, "ping", 1, nil), nil)
	if response.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", response.Code)
	}
}

func TestExecutionValidationError(t *testing.T) {
	_, handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid tool input unexpectedly called provider")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	})
	decoded := decodeRPCResponse(t, post(t, handler, makeToolCall(t, webtools.WebSearchName, map[string]any{"query": "  "}), nil))
	if decoded.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", decoded.Error)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "query must not be empty") {
		t.Fatalf("result = %s", decoded.Result)
	}
}

func TestProviderErrorIsToolResultAndRedacted(t *testing.T) {
	_, handler := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream rejected test-secret"}`))
	})
	response := post(t, handler, makeToolCall(t, webtools.WebSearchName, map[string]any{"query": "test"}), nil)
	if strings.Contains(response.Body.String(), testAPIKey) {
		t.Fatalf("response leaked API key: %s", response.Body.String())
	}
	decoded := decodeRPCResponse(t, response)
	if decoded.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", decoded.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(decoded.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || !strings.Contains(result.Content[0].Text, "upstream rejected") {
		t.Fatalf("result = %s", decoded.Result)
	}
	if len(result.StructuredContent) != 0 {
		t.Errorf("error structuredContent = %s, want omitted", result.StructuredContent)
	}
}

func TestInvalidToolCalls(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown tool", body: makeToolCall(t, "unknown", map[string]any{})},
		{name: "unknown argument", body: makeToolCall(t, webtools.WebSearchName, map[string]any{"query": "test", "unexpected": true})},
		{name: "wrong argument type", body: makeToolCall(t, webtools.WebSearchName, map[string]any{"query": "test", "count": "two"})},
		{name: "array arguments", body: makeToolCall(t, webtools.WebSearchName, []any{"test"})},
		{name: "null arguments", body: makeToolCall(t, webtools.WebFetchName, nil)},
		{name: "missing params", body: makeRequest(t, "tools/call", 1, nil)},
		{name: "array params", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":[]}`},
		{name: "unknown params field", body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"test"},"unexpected":true}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireRPCError(t, post(t, handler, test.body, nil), -32602)
		})
	}
}

func TestInvalidJSONRPCMessages(t *testing.T) {
	_, handler := newTestHandler(t, nil)
	tests := []struct {
		name string
		body string
		code int
	}{
		{name: "malformed JSON", body: `{"jsonrpc":`, code: -32700},
		{name: "batch", body: `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, code: -32600},
		{name: "scalar", body: `"request"`, code: -32600},
		{name: "wrong JSON-RPC version", body: `{"jsonrpc":"1.0","id":1,"method":"ping"}`, code: -32600},
		{name: "boolean ID", body: `{"jsonrpc":"2.0","id":true,"method":"ping"}`, code: -32600},
		{name: "object ID", body: `{"jsonrpc":"2.0","id":{},"method":"ping"}`, code: -32600},
		{name: "unknown method", body: makeRequest(t, "unknown/method", 1, nil), code: -32601},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoded := requireRPCError(t, post(t, handler, test.body, nil), test.code)
			if test.code == -32700 && string(decoded.ID) != "null" {
				t.Errorf("parse error id = %s, want null", decoded.ID)
			}
		})
	}
}
