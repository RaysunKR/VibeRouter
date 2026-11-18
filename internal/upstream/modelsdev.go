package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viberouter/viberouter/internal/config"
)

const (
	modelsDevEndpoint        = "https://models.dev/api.json"
	modelsDevCacheTTL        = time.Hour
	modelsDevResponseMaxSize = 32 << 20
)

// PresetCatalog combines models.dev providers with the built-in presets.
type PresetCatalog struct {
	endpoint string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	cache     []ProviderPreset
	fetchedAt time.Time
}

// NewPresetCatalog returns a catalog backed by models.dev or the optional
// VR_MODELS_DEV_URL mirror.
func NewPresetCatalog() *PresetCatalog {
	endpoint := modelsDevEndpoint
	if mirror := os.Getenv("VR_MODELS_DEV_URL"); mirror != "" {
		endpoint = mirror
	}
	return newPresetCatalog(endpoint, &http.Client{Timeout: 10 * time.Second}, time.Now)
}

func newPresetCatalog(endpoint string, client *http.Client, now func() time.Time) *PresetCatalog {
	return &PresetCatalog{endpoint: endpoint, client: client, now: now}
}

// List returns a copy of the merged catalog. If refreshing fails, it returns
// the most recent cached copy, or the built-in presets, together with the error.
func (c *PresetCatalog) List(ctx context.Context, force bool) ([]ProviderPreset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && c.cache != nil && c.now().Sub(c.fetchedAt) < modelsDevCacheTTL {
		return cloneProviderPresets(c.cache), nil
	}

	remote, err := c.fetch(ctx)
	if err != nil {
		if c.cache != nil {
			return cloneProviderPresets(c.cache), err
		}
		return cloneProviderPresets(Presets()), err
	}

	merged := mergeProviderPresets(Presets(), remote)
	c.cache = cloneProviderPresets(merged)
	c.fetchedAt = c.now()
	return cloneProviderPresets(merged), nil
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	NPM    string                    `json:"npm"`
	API    string                    `json:"api"`
	Models map[string]modelsDevModel `json:"models"`
}

type modelsDevModel struct {
	ID         string               `json:"id"`
	Modalities *modelsDevModalities `json:"modalities"`
	Limit      *modelsDevLimit      `json:"limit"`
}

type modelsDevModalities struct {
	Input *[]string `json:"input"`
}

type modelsDevLimit struct {
	Context *int `json:"context"`
}

func (c *PresetCatalog) fetch(ctx context.Context) ([]ProviderPreset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create models.dev request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models.dev presets: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, modelsDevResponseMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("read models.dev presets: %w", err)
	}
	if len(body) > modelsDevResponseMaxSize {
		return nil, fmt.Errorf("models.dev response exceeds %d bytes", modelsDevResponseMaxSize)
	}

	var providers map[string]modelsDevProvider
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, fmt.Errorf("decode models.dev presets: %w", err)
	}

	keys := make([]string, 0, len(providers))
	for key := range providers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	presets := make([]ProviderPreset, 0, len(providers))
	seen := make(map[string]struct{}, len(providers))
	for _, key := range keys {
		provider := providers[key]
		id := provider.ID
		if id == "" {
			id = key
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}

		name := provider.Name
		if name == "" {
			name = id
		}
		protocol, protocolOK := modelsDevProtocol(provider.NPM)
		baseURL := ""
		if protocolOK && provider.API != "" {
			var endpointOK bool
			baseURL, endpointOK = normalizeModelsDevURL(provider.API, protocol)
			if !endpointOK {
				protocol = ""
				baseURL = ""
			}
		}
		if !protocolOK {
			protocol = ""
		}
		presets = append(presets, ProviderPreset{
			ID:       id,
			Name:     name,
			Protocol: protocol,
			BaseURL:  baseURL,
			Models:   parseModelsDevModels(provider.Models),
		})
	}
	return presets, nil
}

func parseModelsDevModels(models map[string]modelsDevModel) map[string]ModelPreset {
	if models == nil {
		return nil
	}
	keys := make([]string, 0, len(models))
	for key := range models {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	presets := make(map[string]ModelPreset, len(models)*2)
	for _, key := range keys {
		model := models[key]
		id := model.ID
		if id == "" {
			id = key
		}
		if id == "" {
			continue
		}

		preset := ModelPreset{ID: id}
		if model.Modalities != nil && model.Modalities.Input != nil {
			multimodal := false
			for _, modality := range *model.Modalities.Input {
				if modality == "image" || strings.HasPrefix(modality, "image/") {
					multimodal = true
					break
				}
			}
			preset.Multimodal = &multimodal
		}
		if model.Limit != nil && model.Limit.Context != nil {
			contextTokens := *model.Limit.Context
			preset.MaxContextTokens = &contextTokens
		}

		if key != "" && key != id {
			if _, exists := presets[key]; !exists {
				presets[key] = cloneModelPreset(preset)
			}
		}
		presets[id] = preset
	}
	return presets
}

func modelsDevProtocol(npm string) (string, bool) {
	switch npm {
	case "@ai-sdk/openai-compatible":
		return config.ProtocolChatCompletions, true
	case "@ai-sdk/openai":
		return config.ProtocolResponses, true
	case "@ai-sdk/anthropic":
		return config.ProtocolAnthropic, true
	default:
		return "", false
	}
}

func normalizeModelsDevURL(rawURL, protocol string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(rawURL, "#") {
		return "", false
	}
	if !strings.EqualFold(u.Scheme, "http") && !strings.EqualFold(u.Scheme, "https") {
		return "", false
	}

	baseURL := strings.TrimRight(u.String(), "/")
	switch protocol {
	case config.ProtocolChatCompletions:
		baseURL = strings.TrimSuffix(baseURL, "/chat/completions")
	case config.ProtocolResponses:
		baseURL = strings.TrimSuffix(baseURL, "/responses")
	case config.ProtocolAnthropic:
		baseURL = strings.TrimSuffix(baseURL, "/messages")
	}
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return baseURL, true
}

func mergeProviderPresets(builtIn, remote []ProviderPreset) []ProviderPreset {
	byID := make(map[string]ProviderPreset, len(builtIn)+len(remote))
	for _, preset := range builtIn {
		byID[preset.ID] = cloneProviderPreset(preset)
	}
	for _, preset := range remote {
		preset = cloneProviderPreset(preset)
		if existing, ok := byID[preset.ID]; ok {
			if preset.Protocol == "" || preset.BaseURL == "" {
				preset.Protocol = existing.Protocol
				preset.BaseURL = existing.BaseURL
			}
			preset.Balance = existing.Balance
			preset.Models = mergeModelPresets(existing.Models, preset.Models)
		} else if preset.Protocol == "" || preset.BaseURL == "" {
			continue
		}
		byID[preset.ID] = preset
	}

	merged := make([]ProviderPreset, 0, len(byID))
	for _, preset := range byID {
		merged = append(merged, preset)
	}
	sort.Slice(merged, func(i, j int) bool {
		left := strings.ToLower(merged[i].Name)
		right := strings.ToLower(merged[j].Name)
		if left == right {
			return merged[i].ID < merged[j].ID
		}
		return left < right
	})
	return merged
}

func mergeModelPresets(builtIn, remote map[string]ModelPreset) map[string]ModelPreset {
	if builtIn == nil && remote == nil {
		return nil
	}
	merged := cloneModelPresets(builtIn)
	if merged == nil {
		merged = make(map[string]ModelPreset, len(remote))
	}
	for id, preset := range remote {
		merged[id] = cloneModelPreset(preset)
	}
	return merged
}

func cloneProviderPresets(presets []ProviderPreset) []ProviderPreset {
	if presets == nil {
		return nil
	}
	cloned := make([]ProviderPreset, len(presets))
	for i, preset := range presets {
		cloned[i] = cloneProviderPreset(preset)
	}
	return cloned
}

func cloneProviderPreset(preset ProviderPreset) ProviderPreset {
	cloned := preset
	if preset.Balance != nil {
		balance := *preset.Balance
		cloned.Balance = &balance
	}
	cloned.Models = cloneModelPresets(preset.Models)
	return cloned
}

func cloneModelPresets(models map[string]ModelPreset) map[string]ModelPreset {
	if models == nil {
		return nil
	}
	cloned := make(map[string]ModelPreset, len(models))
	for id, preset := range models {
		cloned[id] = cloneModelPreset(preset)
	}
	return cloned
}

func cloneModelPreset(preset ModelPreset) ModelPreset {
	cloned := preset
	if preset.Multimodal != nil {
		multimodal := *preset.Multimodal
		cloned.Multimodal = &multimodal
	}
	if preset.MaxContextTokens != nil {
		contextTokens := *preset.MaxContextTokens
		cloned.MaxContextTokens = &contextTokens
	}
	return cloned
}
