package webtools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) searchBocha(ctx context.Context, provider providerRuntime, req SearchRequest) (SearchResult, error) {
	payload := map[string]any{
		"query":   req.Query,
		"count":   req.Count,
		"summary": req.IncludeContent,
	}
	if req.Freshness != "" {
		payload["freshness"] = req.Freshness
	}
	if len(req.AllowedDomains) > 0 {
		payload["include"] = strings.Join(req.AllowedDomains, ",")
	}
	if len(req.BlockedDomains) > 0 {
		payload["exclude"] = strings.Join(req.BlockedDomains, ",")
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			WebPages struct {
				Value []struct {
					Name          string `json:"name"`
					URL           string `json:"url"`
					Snippet       string `json:"snippet"`
					Summary       string `json:"summary"`
					SiteName      string `json:"siteName"`
					DatePublished string `json:"datePublished"`
				} `json:"value"`
			} `json:"webPages"`
		} `json:"data"`
	}
	if err := s.postJSON(ctx, provider, "/v1/web-search", payload, &response); err != nil {
		return SearchResult{}, err
	}
	if response.Code != 200 {
		return SearchResult{}, fmt.Errorf("bocha response code is %d, want 200", response.Code)
	}
	result := SearchResult{Provider: provider.id, Query: req.Query, Results: make([]SearchItem, 0, len(response.Data.WebPages.Value))}
	for _, item := range response.Data.WebPages.Value {
		result.Results = append(result.Results, SearchItem{
			Title:       item.Name,
			URL:         item.URL,
			Snippet:     item.Snippet,
			Content:     item.Summary,
			SiteName:    item.SiteName,
			PublishedAt: item.DatePublished,
		})
	}
	return result, nil
}

func (s *Service) searchKimi(ctx context.Context, provider providerRuntime, req SearchRequest) (SearchResult, error) {
	if req.Freshness != "" {
		return SearchResult{}, errors.New("kimi does not support freshness filtering")
	}
	payload := map[string]any{
		"text_query":      req.Query,
		"limit":           req.Count,
		"include_content": req.IncludeContent,
	}
	var response struct {
		SearchResults []struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Snippet  string `json:"snippet"`
			Text     string `json:"text"`
			SiteName string `json:"site_name"`
			Date     string `json:"date"`
		} `json:"search_results"`
	}
	if err := s.postJSON(ctx, provider, "/v1/tools/search", payload, &response); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Provider: provider.id, Query: req.Query, Results: make([]SearchItem, 0, len(response.SearchResults))}
	for _, item := range response.SearchResults {
		result.Results = append(result.Results, SearchItem{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Snippet,
			Content:     item.Text,
			SiteName:    item.SiteName,
			PublishedAt: item.Date,
		})
	}
	return result, nil
}

func (s *Service) searchDeepSeek(ctx context.Context, provider providerRuntime, req SearchRequest) (SearchResult, error) {
	if req.Freshness != "" {
		return SearchResult{}, errors.New("deepseek does not support freshness filtering")
	}
	payload := map[string]any{
		"query":        req.Query,
		"count":        req.Count,
		"need_content": req.IncludeContent,
	}
	var response struct {
		Results []struct {
			Title    string `json:"title"`
			URL      string `json:"url"`
			Snippet  string `json:"snippet"`
			Summary  string `json:"summary"`
			Content  string `json:"content"`
			SiteName string `json:"site_name"`
			Date     string `json:"date"`
		} `json:"results"`
	}
	if err := s.postJSON(ctx, provider, "/v1/tools/search", payload, &response); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Provider: provider.id, Query: req.Query, Results: make([]SearchItem, 0, len(response.Results))}
	for _, item := range response.Results {
		snippet := item.Snippet
		if snippet == "" {
			snippet = item.Summary
		}
		result.Results = append(result.Results, SearchItem{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     snippet,
			Content:     item.Content,
			SiteName:    item.SiteName,
			PublishedAt: item.Date,
		})
	}
	return result, nil
}

func (s *Service) searchGLM(ctx context.Context, provider providerRuntime, req SearchRequest) (SearchResult, error) {
	if len(req.AllowedDomains) > 1 {
		return SearchResult{}, errors.New("glm supports only one allowed domain per search")
	}
	payload := map[string]any{
		"search_query":  req.Query,
		"search_engine": "search_pro",
		"search_intent": false,
		"count":         req.Count,
		"content_size":  "medium",
	}
	if req.IncludeContent {
		payload["content_size"] = "high"
	}
	if len(req.AllowedDomains) == 1 {
		payload["search_domain_filter"] = req.AllowedDomains[0]
	}
	if req.Freshness != "" {
		payload["search_recency_filter"] = req.Freshness
	}
	var response struct {
		SearchResult []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			Content     string `json:"content"`
			Media       string `json:"media"`
			PublishDate string `json:"publish_date"`
		} `json:"search_result"`
	}
	if err := s.postJSON(ctx, provider, "/api/paas/v4/web_search", payload, &response); err != nil {
		return SearchResult{}, err
	}
	result := SearchResult{Provider: provider.id, Query: req.Query, Results: make([]SearchItem, 0, len(response.SearchResult))}
	for _, item := range response.SearchResult {
		result.Results = append(result.Results, SearchItem{
			Title:       item.Title,
			URL:         item.Link,
			Snippet:     item.Content,
			Content:     item.Content,
			SiteName:    item.Media,
			PublishedAt: item.PublishDate,
		})
	}
	return result, nil
}

func (s *Service) fetchKimi(ctx context.Context, provider providerRuntime, req FetchRequest) (FetchResult, error) {
	var response struct {
		URL      string `json:"url"`
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	if err := s.postJSON(ctx, provider, "/v1/tools/fetch", map[string]any{"url": req.URL}, &response); err != nil {
		return FetchResult{}, err
	}
	if response.URL == "" {
		response.URL = req.URL
	}
	return FetchResult{Provider: provider.id, URL: response.URL, Title: response.Title, Content: response.Markdown, Format: "markdown"}, nil
}

func (s *Service) fetchDeepSeek(ctx context.Context, provider providerRuntime, req FetchRequest) (FetchResult, error) {
	var response struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := s.postJSON(ctx, provider, "/v1/tools/fetch", map[string]any{"url": req.URL}, &response); err != nil {
		return FetchResult{}, err
	}
	return FetchResult{Provider: provider.id, URL: req.URL, Title: response.Title, Content: response.Content, Format: "markdown"}, nil
}

func (s *Service) fetchGLM(ctx context.Context, provider providerRuntime, req FetchRequest) (FetchResult, error) {
	payload := map[string]any{
		"url":           req.URL,
		"timeout":       int(provider.timeout / time.Second),
		"return_format": "markdown",
		"retain_images": false,
	}
	var response struct {
		ReaderResult struct {
			Content     string `json:"content"`
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"reader_result"`
	}
	if err := s.postJSON(ctx, provider, "/api/paas/v4/reader", payload, &response); err != nil {
		return FetchResult{}, err
	}
	if response.ReaderResult.URL == "" {
		response.ReaderResult.URL = req.URL
	}
	return FetchResult{Provider: provider.id, URL: response.ReaderResult.URL, Title: response.ReaderResult.Title, Content: response.ReaderResult.Content, Format: "markdown"}, nil
}
