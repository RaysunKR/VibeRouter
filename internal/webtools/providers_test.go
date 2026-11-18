package webtools

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestBochaSearch(t *testing.T) {
	server := newProviderServer(t, "/v1/web-search", map[string]any{
		"query":     "bocha query",
		"count":     10,
		"freshness": "oneWeek",
		"summary":   true,
		"include":   "example.com",
	}, http.StatusOK, `{
		"code": 200,
		"data": {"webPages": {"value": [
			{"name":"Bocha title","url":"https://news.example.com/a","snippet":"snippet","summary":"summary","siteName":"Example","datePublished":"2026-01-02"},
			{"name":"Other","url":"https://other.test/a","snippet":"other"}
		]}}
	}`)
	defer server.Close()

	service := newTestService(t, "bocha", server.URL)
	result, err := service.Search(context.Background(), SearchRequest{
		Query:          "  bocha query  ",
		IncludeContent: true,
		AllowedDomains: []string{"Example.COM"},
		Freshness:      "oneWeek",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	want := SearchResult{Provider: "bocha", Query: "bocha query", Results: []SearchItem{{
		Title: "Bocha title", URL: "https://news.example.com/a", Snippet: "snippet", Content: "summary", SiteName: "Example", PublishedAt: "2026-01-02",
	}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Search() = %#v, want %#v", result, want)
	}
}

func TestBochaSearchBlockedDomains(t *testing.T) {
	server := newProviderServer(t, "/v1/web-search", map[string]any{
		"query":   "blocked",
		"count":   2,
		"summary": false,
		"exclude": "blocked.example,ads.test",
	}, http.StatusOK, `{
		"code":200,
		"data":{"webPages":{"value":[
			{"name":"Allowed","url":"https://ok.test/page"},
			{"name":"Blocked","url":"https://sub.blocked.example/page"},
			{"name":"Ads","url":"https://ads.test/page"}
		]}}
	}`)
	defer server.Close()

	service := newTestService(t, "bocha", server.URL)
	result, err := service.Search(context.Background(), SearchRequest{
		Query: "blocked", Count: 2, BlockedDomains: []string{"blocked.example", "ads.test"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].URL != "https://ok.test/page" {
		t.Fatalf("filtered results = %#v", result.Results)
	}
}

func TestKimiSearch(t *testing.T) {
	server := newProviderServer(t, "/v1/tools/search", map[string]any{
		"text_query":      "kimi query",
		"limit":           20,
		"include_content": true,
	}, http.StatusOK, `{
		"search_results":[
			{"title":"Kimi title","url":"https://example.com/k","snippet":"k snippet","text":"k text","site_name":"Kimi Site","date":"2026-02-03"},
			{"title":"Other","url":"https://other.test/k"}
		]
	}`)
	defer server.Close()

	service := newTestService(t, "kimi", server.URL)
	result, err := service.Search(context.Background(), SearchRequest{
		Query: "kimi query", Count: 30, IncludeContent: true, AllowedDomains: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	want := SearchResult{Provider: "kimi", Query: "kimi query", Results: []SearchItem{{
		Title: "Kimi title", URL: "https://example.com/k", Snippet: "k snippet", Content: "k text", SiteName: "Kimi Site", PublishedAt: "2026-02-03",
	}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Search() = %#v, want %#v", result, want)
	}
}

func TestDeepSeekSearch(t *testing.T) {
	server := newProviderServer(t, "/v1/tools/search", map[string]any{
		"query":        "deepseek query",
		"count":        7,
		"need_content": true,
	}, http.StatusOK, `{
		"results":[
			{"title":"DeepSeek title","url":"https://good.test/d","snippet":"","summary":"d summary","content":"d content","site_name":"Deep Site","date":"2026-03-04"},
			{"title":"Blocked","url":"https://bad.example/d"}
		]
	}`)
	defer server.Close()

	service := newTestService(t, "deepseek", server.URL)
	result, err := service.Search(context.Background(), SearchRequest{
		Query: "deepseek query", Count: 7, IncludeContent: true, BlockedDomains: []string{"bad.example"},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	want := SearchResult{Provider: "deepseek", Query: "deepseek query", Results: []SearchItem{{
		Title: "DeepSeek title", URL: "https://good.test/d", Snippet: "d summary", Content: "d content", SiteName: "Deep Site", PublishedAt: "2026-03-04",
	}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Search() = %#v, want %#v", result, want)
	}
}

func TestGLMSearch(t *testing.T) {
	server := newProviderServer(t, "/api/paas/v4/web_search", map[string]any{
		"search_query":          "glm query",
		"search_engine":         "search_pro",
		"search_intent":         false,
		"count":                 5,
		"content_size":          "high",
		"search_domain_filter":  "example.com",
		"search_recency_filter": "week",
	}, http.StatusOK, `{
		"search_result":[
			{"title":"GLM title","link":"https://example.com/g","content":"g content","media":"GLM Media","publish_date":"2026-04-05"},
			{"title":"Other","link":"https://other.test/g","content":"other"}
		]
	}`)
	defer server.Close()

	service := newTestService(t, "glm", server.URL)
	result, err := service.Search(context.Background(), SearchRequest{
		Query: "glm query", Count: 5, IncludeContent: true, AllowedDomains: []string{"EXAMPLE.COM."}, Freshness: "week",
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	want := SearchResult{Provider: "glm", Query: "glm query", Results: []SearchItem{{
		Title: "GLM title", URL: "https://example.com/g", Snippet: "g content", Content: "g content", SiteName: "GLM Media", PublishedAt: "2026-04-05",
	}}}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Search() = %#v, want %#v", result, want)
	}
}

func TestKimiFetch(t *testing.T) {
	server := newProviderServer(t, "/v1/tools/fetch", map[string]any{
		"url": "https://example.com/kimi",
	}, http.StatusOK, `{"url":"https://example.com/kimi-redirect","title":"Kimi fetch","markdown":"# Kimi"}`)
	defer server.Close()

	service := newTestService(t, "kimi", server.URL)
	result, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com/kimi"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want := FetchResult{Provider: "kimi", URL: "https://example.com/kimi-redirect", Title: "Kimi fetch", Content: "# Kimi", Format: "markdown"}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Fetch() = %#v, want %#v", result, want)
	}
}

func TestDeepSeekFetch(t *testing.T) {
	server := newProviderServer(t, "/v1/tools/fetch", map[string]any{
		"url": "https://example.com/deepseek",
	}, http.StatusOK, `{"url":"https://ignored.test/","title":"DeepSeek fetch","content":"plain content"}`)
	defer server.Close()

	service := newTestService(t, "deepseek", server.URL)
	result, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com/deepseek"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want := FetchResult{Provider: "deepseek", URL: "https://example.com/deepseek", Title: "DeepSeek fetch", Content: "plain content", Format: "markdown"}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Fetch() = %#v, want %#v", result, want)
	}
}

func TestGLMFetch(t *testing.T) {
	server := newProviderServer(t, "/api/paas/v4/reader", map[string]any{
		"url":           "https://example.com/glm",
		"timeout":       12,
		"return_format": "markdown",
		"retain_images": false,
	}, http.StatusOK, `{"reader_result":{"content":"# GLM","title":"GLM fetch","url":"https://example.com/glm-final","description":"description"}}`)
	defer server.Close()

	service := newTestService(t, "glm", server.URL)
	result, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com/glm"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	want := FetchResult{Provider: "glm", URL: "https://example.com/glm-final", Title: "GLM fetch", Content: "# GLM", Format: "markdown"}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("Fetch() = %#v, want %#v", result, want)
	}
}

func TestBochaFetchUnsupported(t *testing.T) {
	server := unexpectedRequestServer(t)
	defer server.Close()
	service := newTestService(t, "bocha", server.URL)
	_, err := service.Fetch(context.Background(), FetchRequest{URL: "https://example.com"})
	if err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("Fetch() error = %v, want unsupported error", err)
	}
}

func TestProviderBusinessErrorsAndKeyRedaction(t *testing.T) {
	tests := []struct {
		provider string
		path     string
		body     string
	}{
		{provider: "bocha", path: "/v1/web-search", body: `{"code":500,"msg":"test-key bocha failure"}`},
		{provider: "kimi", path: "/v1/tools/search", body: `{"error":{"message":"test-key kimi failure"}}`},
		{provider: "deepseek", path: "/v1/tools/search", body: `{"error":"test-key deepseek failure"}`},
		{provider: "glm", path: "/api/paas/v4/web_search", body: `{"success":false,"message":"test-key glm failure"}`},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			server := newProviderServer(t, test.path, nil, http.StatusOK, test.body)
			defer server.Close()
			service := newTestService(t, test.provider, server.URL)
			_, err := service.Search(context.Background(), SearchRequest{Query: "query"})
			if err == nil {
				t.Fatal("Search() error = nil, want business error")
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Fatalf("Search() error leaked API key: %v", err)
			}
		})
	}
}

func TestHTTPStatusErrorAndKeyRedaction(t *testing.T) {
	server := newProviderServer(t, "/v1/tools/search", nil, http.StatusUnauthorized, `{"error":{"message":"invalid key test-key"}}`)
	defer server.Close()
	service := newTestService(t, "deepseek", server.URL)
	_, err := service.Search(context.Background(), SearchRequest{Query: "query"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("Search() error = %v, want HTTP 401 error", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("Search() error leaked API key: %v", err)
	}
}

func TestUnsupportedSearchFilters(t *testing.T) {
	for _, provider := range []string{"kimi", "deepseek"} {
		t.Run(provider+" freshness", func(t *testing.T) {
			server := unexpectedRequestServer(t)
			defer server.Close()
			service := newTestService(t, provider, server.URL)
			_, err := service.Search(context.Background(), SearchRequest{Query: "query", Freshness: "day"})
			if err == nil || !strings.Contains(err.Error(), "does not support freshness") {
				t.Fatalf("Search() error = %v", err)
			}
		})
	}

	t.Run("glm multiple allowed domains", func(t *testing.T) {
		server := unexpectedRequestServer(t)
		defer server.Close()
		service := newTestService(t, "glm", server.URL)
		_, err := service.Search(context.Background(), SearchRequest{Query: "query", AllowedDomains: []string{"one.test", "two.test"}})
		if err == nil || !strings.Contains(err.Error(), "only one allowed domain") {
			t.Fatalf("Search() error = %v", err)
		}
	})
}
