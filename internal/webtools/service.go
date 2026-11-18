// Package webtools provides a unified interface to supported web search and
// fetch providers.
package webtools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/viberouter/viberouter/internal/config"
)

const (
	WebSearchName = "web_search"
	WebFetchName  = "web_fetch"
)

type SearchRequest struct {
	Query          string   `json:"query"`
	Count          int      `json:"count,omitempty"`
	IncludeContent bool     `json:"include_content,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	Freshness      string   `json:"freshness,omitempty"`
}

type SearchItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Content     string `json:"content,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

type SearchResult struct {
	Provider string       `json:"provider"`
	Query    string       `json:"query"`
	Results  []SearchItem `json:"results"`
}

type FetchRequest struct {
	URL            string   `json:"url"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

type FetchResult struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Content  string `json:"content"`
	Format   string `json:"format"`
}

type ToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
}

type Service struct {
	cfg    *config.Store
	client *http.Client
}

type providerRuntime struct {
	id      string
	baseURL string
	apiKey  string
	timeout time.Duration
}

var defaultBaseURLs = map[string]string{
	"bocha":    "https://api.bocha.cn",
	"kimi":     "https://api.moonshot.cn",
	"deepseek": "https://api.deepseek.com",
	"glm":      "https://open.bigmodel.cn",
}

func New(cfg *config.Store) *Service {
	return &Service{cfg: cfg, client: http.DefaultClient}
}

func (s *Service) Definitions() []ToolDefinition {
	if s == nil || s.cfg == nil {
		return nil
	}
	cfg := s.cfg.Snapshot().WebTools
	if !cfg.Enabled && !cfg.MCPEnabled {
		return nil
	}

	definitions := make([]ToolDefinition, 0, 2)
	if providerEnabled(cfg, cfg.SearchProvider) && supportsSearch(cfg.SearchProvider) {
		definitions = append(definitions, searchDefinition())
	}
	if providerEnabled(cfg, cfg.FetchProvider) && supportsFetch(cfg.FetchProvider) {
		definitions = append(definitions, fetchDefinition())
	}
	return definitions
}

func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	var zero SearchResult
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return zero, errors.New("web search query must not be empty")
	}
	if req.Count == 0 {
		req.Count = 10
	}
	if req.Count < 1 || req.Count > 50 {
		return zero, errors.New("web search count must be between 1 and 50")
	}
	req.Freshness = strings.TrimSpace(req.Freshness)
	allowed, blocked, err := normalizeDomainFilters(req.AllowedDomains, req.BlockedDomains)
	if err != nil {
		return zero, err
	}
	req.AllowedDomains, req.BlockedDomains = allowed, blocked

	provider, err := s.provider("search")
	if err != nil {
		return zero, err
	}
	if provider.id == "kimi" && req.Count > 20 {
		req.Count = 20
	}

	var result SearchResult
	switch provider.id {
	case "bocha":
		result, err = s.searchBocha(ctx, provider, req)
	case "kimi":
		result, err = s.searchKimi(ctx, provider, req)
	case "deepseek":
		result, err = s.searchDeepSeek(ctx, provider, req)
	case "glm":
		result, err = s.searchGLM(ctx, provider, req)
	default:
		err = fmt.Errorf("unsupported web search provider %q", provider.id)
	}
	if err != nil {
		return zero, err
	}
	result.Results = filterSearchItems(result.Results, req.AllowedDomains, req.BlockedDomains)
	return result, nil
}

func (s *Service) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	var zero FetchResult
	req.URL = strings.TrimSpace(req.URL)
	host, err := validateFetchURL(req.URL)
	if err != nil {
		return zero, err
	}
	allowed, blocked, err := normalizeDomainFilters(req.AllowedDomains, req.BlockedDomains)
	if err != nil {
		return zero, err
	}
	if !domainAllowed(host, allowed, blocked) {
		return zero, fmt.Errorf("fetch URL host %q is not allowed by the domain filters", host)
	}

	provider, err := s.provider("fetch")
	if err != nil {
		return zero, err
	}
	switch provider.id {
	case "kimi":
		return s.fetchKimi(ctx, provider, req)
	case "deepseek":
		return s.fetchDeepSeek(ctx, provider, req)
	case "glm":
		return s.fetchGLM(ctx, provider, req)
	case "bocha":
		return zero, errors.New("bocha does not support web fetch")
	default:
		return zero, fmt.Errorf("unsupported web fetch provider %q", provider.id)
	}
}

func (s *Service) provider(operation string) (providerRuntime, error) {
	if s == nil || s.cfg == nil {
		return providerRuntime{}, errors.New("web tools are not configured")
	}
	cfg := s.cfg.Snapshot().WebTools
	if !cfg.Enabled && !cfg.MCPEnabled {
		return providerRuntime{}, errors.New("web tools are disabled")
	}

	selected := cfg.SearchProvider
	if operation == "fetch" {
		selected = cfg.FetchProvider
	}
	id := strings.ToLower(strings.TrimSpace(selected))
	if id == "" {
		return providerRuntime{}, fmt.Errorf("web %s provider is not configured", operation)
	}
	if operation == "fetch" && id == "bocha" {
		return providerRuntime{}, errors.New("bocha does not support web fetch")
	}
	if !supportsSearch(id) {
		return providerRuntime{}, fmt.Errorf("unsupported web %s provider %q", operation, id)
	}

	var provider *config.WebToolProviderConfig
	for i := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(cfg.Providers[i].ID), id) {
			provider = &cfg.Providers[i]
			break
		}
	}
	if provider == nil {
		return providerRuntime{}, fmt.Errorf("web %s provider %q is not configured", operation, id)
	}
	if !provider.Enabled {
		return providerRuntime{}, fmt.Errorf("web %s provider %q is disabled", operation, id)
	}
	apiKey := strings.TrimSpace(provider.APIKey)
	if apiKey == "" {
		return providerRuntime{}, fmt.Errorf("web %s provider %q API key is not configured", operation, id)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURLs[id]
	}
	timeout := cfg.TimeoutSec
	if timeout <= 0 {
		timeout = 30
	}
	return providerRuntime{id: id, baseURL: baseURL, apiKey: apiKey, timeout: time.Duration(timeout) * time.Second}, nil
}

func providerEnabled(cfg config.WebToolsConfig, selected string) bool {
	id := strings.ToLower(strings.TrimSpace(selected))
	for _, provider := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(provider.ID), id) {
			return provider.Enabled && strings.TrimSpace(provider.APIKey) != ""
		}
	}
	return false
}

func supportsSearch(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "bocha", "kimi", "deepseek", "glm":
		return true
	default:
		return false
	}
}

func supportsFetch(id string) bool {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "kimi", "deepseek", "glm":
		return true
	default:
		return false
	}
}

func searchDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        WebSearchName,
		Description: "Search the web and return matching pages.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":           map[string]any{"type": "string", "description": "Search query."},
				"count":           map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 10},
				"include_content": map[string]any{"type": "boolean", "default": false},
				"allowed_domains": stringArraySchema(),
				"blocked_domains": stringArraySchema(),
				"freshness":       map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"provider": map[string]any{"type": "string"},
				"query":    map[string]any{"type": "string"},
				"results": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"title":        map[string]any{"type": "string"},
							"url":          map[string]any{"type": "string"},
							"snippet":      map[string]any{"type": "string"},
							"content":      map[string]any{"type": "string"},
							"site_name":    map[string]any{"type": "string"},
							"published_at": map[string]any{"type": "string"},
						},
					},
				},
			},
			"required": []string{"provider", "query", "results"},
		},
	}
}

func fetchDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        WebFetchName,
		Description: "Fetch a web page and return its readable content.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":             map[string]any{"type": "string", "description": "HTTP or HTTPS page URL."},
				"allowed_domains": stringArraySchema(),
				"blocked_domains": stringArraySchema(),
			},
			"required": []string{"url"},
		},
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"provider": map[string]any{"type": "string"},
				"url":      map[string]any{"type": "string"},
				"title":    map[string]any{"type": "string"},
				"content":  map[string]any{"type": "string"},
				"format":   map[string]any{"type": "string"},
			},
			"required": []string{"provider", "url", "title", "content", "format"},
		},
	}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}
