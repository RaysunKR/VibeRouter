package webtools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
)

func newTestService(t *testing.T, provider, baseURL string) *Service {
	t.Helper()
	return newTestServiceWithConfig(t, config.WebToolsConfig{
		Enabled:        true,
		SearchProvider: provider,
		FetchProvider:  provider,
		TimeoutSec:     12,
		Providers: []config.WebToolProviderConfig{{
			ID: provider, Enabled: true, APIKey: "test-key", BaseURL: baseURL,
		}},
	})
}

func newTestServiceWithConfig(t *testing.T, webTools config.WebToolsConfig) *Service {
	t.Helper()
	store, err := config.Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if err := store.Mutate(func(cfg *config.Config) error {
		cfg.WebTools = webTools
		return nil
	}); err != nil {
		t.Fatalf("config.Mutate() error = %v", err)
	}
	return New(store)
}

func newProviderServer(t *testing.T, path string, expectedBody any, status int, responseBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != path {
			t.Errorf("path = %q, want %q", r.URL.Path, path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if expectedBody != nil {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read request body: %v", err)
			}
			assertJSONEqual(t, body, expectedBody)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := io.WriteString(w, responseBody); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
}

func unexpectedRequestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
}

func assertJSONEqual(t *testing.T, got []byte, want any) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Errorf("request body is not JSON: %v; body = %s", err, got)
		return
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal expected JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal(wantJSON, &wantValue); err != nil {
		t.Fatalf("unmarshal expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("request JSON = %#v, want %#v", gotValue, wantValue)
	}
}

func TestDefinitions(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.WebToolsConfig
		want []string
	}{
		{
			name: "enabled default providers",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "kimi", FetchProvider: "glm",
				Providers: []config.WebToolProviderConfig{
					{ID: "kimi", Enabled: true, APIKey: "kimi-key"},
					{ID: "glm", Enabled: true, APIKey: "glm-key"},
				},
			},
			want: []string{WebSearchName, WebFetchName},
		},
		{
			name: "MCP only",
			cfg: config.WebToolsConfig{
				MCPEnabled: true, SearchProvider: "kimi", FetchProvider: "kimi",
				Providers: []config.WebToolProviderConfig{{ID: "kimi", Enabled: true, APIKey: "kimi-key"}},
			},
			want: []string{WebSearchName, WebFetchName},
		},
		{
			name: "bocha has search only",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "bocha", FetchProvider: "bocha",
				Providers: []config.WebToolProviderConfig{{ID: "bocha", Enabled: true, APIKey: "bocha-key"}},
			},
			want: []string{WebSearchName},
		},
		{
			name: "globally disabled",
			cfg: config.WebToolsConfig{
				Enabled: false, SearchProvider: "kimi", FetchProvider: "kimi",
				Providers: []config.WebToolProviderConfig{{ID: "kimi", Enabled: true, APIKey: "kimi-key"}},
			},
		},
		{
			name: "selected search provider disabled",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "kimi", FetchProvider: "glm",
				Providers: []config.WebToolProviderConfig{
					{ID: "kimi", Enabled: false, APIKey: "kimi-key"},
					{ID: "glm", Enabled: true, APIKey: "glm-key"},
				},
			},
			want: []string{WebFetchName},
		},
		{
			name: "selected fetch provider missing key",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "kimi", FetchProvider: "glm",
				Providers: []config.WebToolProviderConfig{
					{ID: "kimi", Enabled: true, APIKey: "kimi-key"},
					{ID: "glm", Enabled: true},
				},
			},
			want: []string{WebSearchName},
		},
		{
			name: "other configured provider is not exposed",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "deepseek", FetchProvider: "bocha",
				Providers: []config.WebToolProviderConfig{
					{ID: "deepseek", Enabled: true},
					{ID: "kimi", Enabled: true, APIKey: "kimi-key"},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestServiceWithConfig(t, test.cfg)
			definitions := service.Definitions()
			got := make([]string, 0, len(definitions))
			for _, definition := range definitions {
				got = append(got, definition.Name)
				if definition.Description == "" || definition.InputSchema == nil || definition.OutputSchema == nil {
					t.Errorf("incomplete definition: %#v", definition)
				}
			}
			if !reflect.DeepEqual(got, test.want) && !(len(got) == 0 && len(test.want) == 0) {
				t.Fatalf("definition names = %v, want %v", got, test.want)
			}
		})
	}

	if definitions := New(nil).Definitions(); len(definitions) != 0 {
		t.Fatalf("New(nil).Definitions() = %#v, want none", definitions)
	}
}

func TestUnavailableConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.WebToolsConfig
		wantErr string
	}{
		{
			name: "globally disabled",
			cfg: config.WebToolsConfig{
				SearchProvider: "deepseek", FetchProvider: "deepseek",
				Providers: []config.WebToolProviderConfig{{ID: "deepseek", Enabled: true, APIKey: "key"}},
			},
			wantErr: "disabled",
		},
		{
			name: "provider disabled",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "deepseek", FetchProvider: "deepseek",
				Providers: []config.WebToolProviderConfig{{ID: "deepseek", Enabled: false, APIKey: "key"}},
			},
			wantErr: "disabled",
		},
		{
			name: "missing API key",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "deepseek", FetchProvider: "deepseek",
				Providers: []config.WebToolProviderConfig{{ID: "deepseek", Enabled: true}},
			},
			wantErr: "API key",
		},
		{
			name:    "provider not configured",
			cfg:     config.WebToolsConfig{Enabled: true, SearchProvider: "deepseek", FetchProvider: "deepseek"},
			wantErr: "not configured",
		},
		{
			name: "unknown provider",
			cfg: config.WebToolsConfig{
				Enabled: true, SearchProvider: "unknown", FetchProvider: "unknown",
				Providers: []config.WebToolProviderConfig{{ID: "unknown", Enabled: true, APIKey: "key"}},
			},
			wantErr: "unsupported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newTestServiceWithConfig(t, test.cfg)
			if _, err := service.Search(context.Background(), SearchRequest{Query: "query"}); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Search() error = %v, want containing %q", err, test.wantErr)
			}
			if _, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com"}); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Fetch() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}

	service := New(nil)
	if _, err := service.Search(context.Background(), SearchRequest{Query: "query"}); err == nil {
		t.Fatal("New(nil).Search() error = nil")
	}
	if _, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com"}); err == nil {
		t.Fatal("New(nil).Fetch() error = nil")
	}
}

func TestSearchInputValidation(t *testing.T) {
	server := unexpectedRequestServer(t)
	defer server.Close()
	service := newTestService(t, "deepseek", server.URL)
	tests := []struct {
		name string
		req  SearchRequest
	}{
		{name: "empty query", req: SearchRequest{Query: " \t\n "}},
		{name: "negative count", req: SearchRequest{Query: "query", Count: -1}},
		{name: "count too large", req: SearchRequest{Query: "query", Count: 51}},
		{name: "conflicting domain filters", req: SearchRequest{Query: "query", AllowedDomains: []string{"example.com"}, BlockedDomains: []string{"other.test"}}},
		{name: "invalid allowed domain", req: SearchRequest{Query: "query", AllowedDomains: []string{"https://example.com"}}},
		{name: "invalid blocked domain", req: SearchRequest{Query: "query", BlockedDomains: []string{"bad domain.test"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Search(context.Background(), test.req); err == nil {
				t.Fatal("Search() error = nil")
			}
		})
	}
}

func TestFetchURLValidation(t *testing.T) {
	server := unexpectedRequestServer(t)
	defer server.Close()
	service := newTestService(t, "deepseek", server.URL)
	for _, rawURL := range []string{
		"",
		"ftp://example.com/file",
		"https://user:password@example.com/file",
		"https:///missing-host",
		"://invalid",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := service.Fetch(context.Background(), FetchRequest{URL: rawURL}); err == nil {
				t.Fatal("Fetch() error = nil")
			}
		})
	}
}

func TestFetchDomainValidation(t *testing.T) {
	t.Run("allowed domain mismatch", func(t *testing.T) {
		server := unexpectedRequestServer(t)
		defer server.Close()
		service := newTestService(t, "deepseek", server.URL)
		_, err := service.Fetch(context.Background(), FetchRequest{URL: "https://other.test/page", AllowedDomains: []string{"example.com"}})
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("Fetch() error = %v", err)
		}
	})

	t.Run("blocked subdomain", func(t *testing.T) {
		server := unexpectedRequestServer(t)
		defer server.Close()
		service := newTestService(t, "deepseek", server.URL)
		_, err := service.Fetch(context.Background(), FetchRequest{URL: "https://bad.example.com/page", BlockedDomains: []string{"example.com"}})
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Fatalf("Fetch() error = %v", err)
		}
	})

	t.Run("conflicting filters", func(t *testing.T) {
		service := newTestService(t, "deepseek", "http://unused.test")
		_, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com", AllowedDomains: []string{"example.com"}, BlockedDomains: []string{"other.test"}})
		if err == nil || !strings.Contains(err.Error(), "cannot be used together") {
			t.Fatalf("Fetch() error = %v", err)
		}
	})

	t.Run("invalid domain", func(t *testing.T) {
		service := newTestService(t, "deepseek", "http://unused.test")
		_, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com", AllowedDomains: []string{"https://example.com"}})
		if err == nil || !strings.Contains(err.Error(), "invalid allowed_domains") {
			t.Fatalf("Fetch() error = %v", err)
		}
	})

	t.Run("allowed subdomain", func(t *testing.T) {
		server := newProviderServer(t, "/v1/tools/fetch", map[string]any{"url": "https://news.example.com/page"}, http.StatusOK, `{"title":"allowed","content":"content"}`)
		defer server.Close()
		service := newTestService(t, "deepseek", server.URL)
		if _, err := service.Fetch(context.Background(), FetchRequest{URL: "https://news.example.com/page", AllowedDomains: []string{"example.com"}}); err != nil {
			t.Fatalf("Fetch() error = %v", err)
		}
	})
}

func TestDefaultTimeout(t *testing.T) {
	server := newProviderServer(t, "/api/paas/v4/reader", map[string]any{
		"url": "https://example.com", "timeout": 30, "return_format": "markdown", "retain_images": false,
	}, http.StatusOK, `{"reader_result":{"url":"https://example.com","title":"title","content":"content"}}`)
	defer server.Close()
	service := newTestServiceWithConfig(t, config.WebToolsConfig{
		Enabled: true, FetchProvider: "glm",
		Providers: []config.WebToolProviderConfig{{ID: "glm", Enabled: true, APIKey: "test-key", BaseURL: server.URL}},
	})
	if _, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com"}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
}

type endlessZeroReader struct{}

func (endlessZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestResponseSizeLimit(t *testing.T) {
	body := io.LimitReader(endlessZeroReader{}, maxResponseBytes+1)
	if _, err := readResponseBody(body); err == nil || !strings.Contains(err.Error(), "16 MiB") {
		t.Fatalf("readResponseBody() error = %v, want 16 MiB limit error", err)
	}
}
