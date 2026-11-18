package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/viberouter/viberouter/internal/config"
)

func TestNewPresetCatalogDefaults(t *testing.T) {
	catalog := NewPresetCatalog()
	if catalog.endpoint != "https://models.dev/api.json" {
		t.Fatalf("endpoint = %q", catalog.endpoint)
	}
	if catalog.client == nil || catalog.client.Timeout != 10*time.Second {
		t.Fatalf("client timeout = %v, want 10s", catalog.client.Timeout)
	}
	if catalog.now == nil {
		t.Fatal("clock is nil")
	}
}

func TestPresetCatalogListMapsModelsDevProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"compatible-key": {
				"name": "Compatible",
				"npm": "@ai-sdk/openai-compatible",
				"api": "https://chat.example.test/v1/chat/completions/",
				"models": {}
			},
			"responses-key": {
				"id": "remote-openai",
				"npm": "@ai-sdk/openai",
				"api": "https://responses.example.test/v1/responses"
			},
			"anthropic-key": {
				"id": "remote-anthropic",
				"name": "Remote Anthropic",
				"npm": "@ai-sdk/anthropic",
				"api": "https://anthropic.example.test/v1/messages/"
			}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	got, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != len(Presets())+3 {
		t.Fatalf("len(List()) = %d, want %d", len(got), len(Presets())+3)
	}

	tests := []struct {
		id       string
		name     string
		protocol string
		baseURL  string
	}{
		{"compatible-key", "Compatible", config.ProtocolChatCompletions, "https://chat.example.test"},
		{"remote-openai", "remote-openai", config.ProtocolResponses, "https://responses.example.test"},
		{"remote-anthropic", "Remote Anthropic", config.ProtocolAnthropic, "https://anthropic.example.test"},
	}
	for _, test := range tests {
		preset, ok := findPresetForTest(got, test.id)
		if !ok {
			t.Errorf("preset %q is missing", test.id)
			continue
		}
		if preset.Name != test.name || preset.Protocol != test.protocol || preset.BaseURL != test.baseURL {
			t.Errorf("preset %q = %+v, want name %q, protocol %q, base URL %q", test.id, preset, test.name, test.protocol, test.baseURL)
		}
	}
}

func TestPresetCatalogListSkipsUnsupportedAndInvalidProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"unsupported": {"npm":"@ai-sdk/google","api":"https://example.test"},
			"missing-api": {"npm":"@ai-sdk/openai-compatible"},
			"malformed": {"npm":"@ai-sdk/openai-compatible","api":"://bad"},
			"wrong-scheme": {"npm":"@ai-sdk/openai-compatible","api":"ftp://example.test"},
			"relative": {"npm":"@ai-sdk/openai-compatible","api":"/v1"},
			"userinfo": {"npm":"@ai-sdk/openai-compatible","api":"https://user:pass@example.test/v1"},
			"query": {"npm":"@ai-sdk/openai-compatible","api":"https://example.test/v1?x=1"},
			"fragment": {"npm":"@ai-sdk/openai-compatible","api":"https://example.test/v1#models"},
			"missing-host": {"npm":"@ai-sdk/openai-compatible","api":"https:///v1"},
			"valid": {"npm":"@ai-sdk/openai-compatible","api":"https://valid.example.test/v1"}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	got, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != len(Presets())+1 {
		t.Fatalf("len(List()) = %d, want %d", len(got), len(Presets())+1)
	}
	for _, id := range []string{"unsupported", "missing-api", "malformed", "wrong-scheme", "relative", "userinfo", "query", "fragment", "missing-host"} {
		if _, ok := findPresetForTest(got, id); ok {
			t.Errorf("invalid provider %q was included", id)
		}
	}
	if _, ok := findPresetForTest(got, "valid"); !ok {
		t.Error("valid provider was skipped")
	}
}

func TestNormalizeModelsDevURL(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		protocol string
		want     string
	}{
		{"chat completions", "https://example.test/v1/chat/completions///", config.ProtocolChatCompletions, "https://example.test"},
		{"responses", "https://example.test/v1/responses/", config.ProtocolResponses, "https://example.test"},
		{"messages with path", "https://example.test/api/v1/messages/", config.ProtocolAnthropic, "https://example.test/api"},
		{"version only", "https://example.test/v1/", config.ProtocolChatCompletions, "https://example.test"},
		{"other path", "https://example.test/api/", config.ProtocolChatCompletions, "https://example.test/api"},
		{"http", "http://example.test/v1/responses", config.ProtocolResponses, "http://example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := normalizeModelsDevURL(test.rawURL, test.protocol)
			if !ok || got != test.want {
				t.Fatalf("normalizeModelsDevURL(%q) = %q, %v; want %q, true", test.rawURL, got, ok, test.want)
			}
		})
	}

	invalid := []string{
		"",
		"://bad",
		"example.test",
		"ftp://example.test",
		"https:///v1",
		"https://user@example.test/v1",
		"https://example.test/v1?x=1",
		"https://example.test/v1?",
		"https://example.test/v1#models",
		"https://example.test/v1#",
	}
	for _, rawURL := range invalid {
		t.Run("invalid "+rawURL, func(t *testing.T) {
			if got, ok := normalizeModelsDevURL(rawURL, config.ProtocolChatCompletions); ok {
				t.Fatalf("normalizeModelsDevURL(%q) = %q, true; want rejection", rawURL, got)
			}
		})
	}
}

func TestPresetCatalogListMergesBalancesAndSorts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"deepseek-key": {
				"id":"deepseek",
				"name":"DeepSeek Remote",
				"npm":"@ai-sdk/openai-compatible",
				"api":"https://new.deepseek.example.test/v1/chat/completions"
			},
			"z-remote": {
				"name":"alpha",
				"npm":"@ai-sdk/openai",
				"api":"https://z.example.test/v1/responses"
			},
			"a-remote": {
				"name":"Alpha",
				"npm":"@ai-sdk/anthropic",
				"api":"https://a.example.test/v1/messages"
			}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	got, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != len(Presets())+2 {
		t.Fatalf("len(List()) = %d, want %d", len(got), len(Presets())+2)
	}

	deepseek, ok := findPresetForTest(got, "deepseek")
	if !ok {
		t.Fatal("deepseek preset is missing")
	}
	if deepseek.Name != "DeepSeek Remote" || deepseek.Protocol != config.ProtocolChatCompletions || deepseek.BaseURL != "https://new.deepseek.example.test" {
		t.Errorf("remote deepseek fields were not applied: %+v", deepseek)
	}
	if deepseek.Balance == nil || deepseek.Balance.Type != "deepseek" {
		t.Errorf("built-in deepseek balance was not preserved: %+v", deepseek.Balance)
	}

	special, ok := findPresetForTest(got, "deepseek-anthropic")
	if !ok || special.BaseURL != "https://api.deepseek.com/anthropic" || special.Balance == nil || special.Balance.Type != "deepseek" {
		t.Errorf("built-in special preset was not retained: %+v", special)
	}

	seen := make(map[string]struct{}, len(got))
	for _, preset := range got {
		if _, exists := seen[preset.ID]; exists {
			t.Errorf("duplicate preset ID %q", preset.ID)
		}
		seen[preset.ID] = struct{}{}
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		left := strings.ToLower(got[i].Name)
		right := strings.ToLower(got[j].Name)
		if left == right {
			return got[i].ID < got[j].ID
		}
		return left < right
	}) {
		t.Error("presets are not sorted by case-insensitive name and ID")
	}
	aIndex := presetIndexForTest(got, "a-remote")
	zIndex := presetIndexForTest(got, "z-remote")
	if aIndex < 0 || zIndex < 0 || aIndex > zIndex {
		t.Errorf("equal folded names are not sorted by ID: a-remote=%d, z-remote=%d", aIndex, zIndex)
	}
}

func TestPresetCatalogListCacheTTLAndForce(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		_, _ = fmt.Fprintf(w, `{"remote":{"name":"Remote %d","npm":"@ai-sdk/openai-compatible","api":"https://example.test/v1"}}`, requests)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), func() time.Time { return now })
	assertRemoteName := func(force bool, want string) {
		t.Helper()
		got, err := catalog.List(context.Background(), force)
		if err != nil {
			t.Fatalf("List(force=%v) error = %v", force, err)
		}
		preset, ok := findPresetForTest(got, "remote")
		if !ok || preset.Name != want {
			t.Fatalf("remote preset = %+v, %v; want name %q", preset, ok, want)
		}
	}

	assertRemoteName(false, "Remote 1")
	now = now.Add(59 * time.Minute)
	assertRemoteName(false, "Remote 1")
	if requests != 1 {
		t.Fatalf("requests after cached List = %d, want 1", requests)
	}

	assertRemoteName(true, "Remote 2")
	if requests != 2 {
		t.Fatalf("requests after forced List = %d, want 2", requests)
	}
	now = now.Add(59 * time.Minute)
	assertRemoteName(false, "Remote 2")
	if requests != 2 {
		t.Fatalf("requests before forced refresh TTL expiry = %d, want 2", requests)
	}
	now = now.Add(time.Minute)
	assertRemoteName(false, "Remote 3")
	if requests != 3 {
		t.Fatalf("requests after TTL expiry = %d, want 3", requests)
	}
}

func TestPresetCatalogListFailureFallsBackToBuiltIns(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", http.StatusServiceUnavailable, `{"remote":{}}`},
		{"bad JSON", http.StatusOK, `{"remote":`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()

			catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
			got, err := catalog.List(context.Background(), false)
			if err == nil {
				t.Fatal("List() error = nil, want refresh error")
			}
			if !reflect.DeepEqual(got, Presets()) {
				t.Errorf("List() fallback differs from built-in presets")
			}
		})
	}

	t.Run("offline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		endpoint := server.URL
		client := server.Client()
		server.Close()

		catalog := newPresetCatalog(endpoint, client, time.Now)
		got, err := catalog.List(context.Background(), false)
		if err == nil {
			t.Fatal("List() error = nil, want offline error")
		}
		if !reflect.DeepEqual(got, Presets()) {
			t.Errorf("List() offline fallback differs from built-in presets")
		}
	})
}

func TestPresetCatalogListFailureReturnsRecentCache(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	fail := false
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"remote":{"name":"Cached Remote","npm":"@ai-sdk/openai-compatible","api":"https://example.test/v1"}}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), func() time.Time { return now })
	if _, err := catalog.List(context.Background(), false); err != nil {
		t.Fatalf("initial List() error = %v", err)
	}
	fail = true
	got, err := catalog.List(context.Background(), true)
	if err == nil {
		t.Fatal("forced List() error = nil, want refresh error")
	}
	preset, ok := findPresetForTest(got, "remote")
	if !ok || preset.Name != "Cached Remote" {
		t.Fatalf("forced List() did not return cached preset: %+v, %v", preset, ok)
	}
	if len(got) != len(Presets())+1 {
		t.Errorf("cached result length = %d, want %d", len(got), len(Presets())+1)
	}

	got, err = catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("cached List() error = %v", err)
	}
	if requests != 2 {
		t.Errorf("requests after ordinary cached List = %d, want 2", requests)
	}
	preset, ok = findPresetForTest(got, "remote")
	if !ok || preset.Name != "Cached Remote" {
		t.Errorf("ordinary List() did not return cached preset: %+v, %v", preset, ok)
	}
}

func TestPresetCatalogListReturnsIndependentCopies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"deepseek-key":{"id":"deepseek","name":"Cached DeepSeek","npm":"@ai-sdk/openai-compatible","api":"https://cached.example.test/v1"}}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	first, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	index := presetIndexForTest(first, "deepseek")
	if index < 0 || first[index].Balance == nil {
		t.Fatalf("deepseek preset or balance is missing: %+v", first)
	}
	original := first[index]
	originalBalance := *first[index].Balance
	balancePointer := first[index].Balance

	first[index].Name = "Mutated"
	first[index].Protocol = "mutated"
	first[index].BaseURL = "https://mutated.example.test"
	first[index].Balance.Type = "mutated"
	first[index].Balance.URL = "https://mutated.example.test/balance"

	second, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	got := second[index]
	if got.ID != original.ID || got.Name != original.Name || got.Protocol != original.Protocol || got.BaseURL != original.BaseURL {
		t.Errorf("returned slice mutation changed cached preset: got %+v, want %+v", got, original)
	}
	if got.Balance == nil || *got.Balance != originalBalance {
		t.Errorf("returned balance mutation changed cache: got %+v, want %+v", got.Balance, originalBalance)
	}
	if got.Balance == balancePointer {
		t.Error("Balance pointers alias across returned copies")
	}
}

func TestPresetCatalogListParsesModelMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"remote": {
				"npm": "@ai-sdk/openai-compatible",
				"api": "https://models.example.test/v1",
				"models": {
					"fallback-key": {
						"modalities": {"input": ["text"]},
						"limit": {"context": 4096}
					},
					"alias-key": {
						"id": "canonical-image",
						"modalities": {"input": ["text", "image"]},
						"limit": {"context": 128000}
					},
					"mime-key": {
						"id": "mime-image",
						"modalities": {"input": ["image/png"]},
						"limit": {"context": 64000}
					},
					"documents": {
						"id": "text-documents",
						"modalities": {"input": ["text", "attachment", "pdf"]},
						"limit": {"context": 8192}
					},
					"unknown-modalities": {
						"limit": {"context": 2048}
					},
					"missing-input": {
						"modalities": {"output": ["text"]},
						"limit": {"context": 1024}
					},
					"unknown-limit": {
						"modalities": {"input": []}
					},
					"missing-context": {
						"modalities": {"input": ["text"]},
						"limit": {"output": 256}
					}
				}
			}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	got, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	preset, ok := findPresetForTest(got, "remote")
	if !ok {
		t.Fatal("remote preset is missing")
	}

	imageAlias := ModelPreset{ID: "canonical-image", Multimodal: modelBoolForTest(true), MaxContextTokens: modelIntForTest(128000)}
	tests := []struct {
		key  string
		want ModelPreset
	}{
		{"fallback-key", ModelPreset{ID: "fallback-key", Multimodal: modelBoolForTest(false), MaxContextTokens: modelIntForTest(4096)}},
		{"alias-key", imageAlias},
		{"canonical-image", imageAlias},
		{"mime-image", ModelPreset{ID: "mime-image", Multimodal: modelBoolForTest(true), MaxContextTokens: modelIntForTest(64000)}},
		{"text-documents", ModelPreset{ID: "text-documents", Multimodal: modelBoolForTest(false), MaxContextTokens: modelIntForTest(8192)}},
		{"unknown-modalities", ModelPreset{ID: "unknown-modalities", MaxContextTokens: modelIntForTest(2048)}},
		{"missing-input", ModelPreset{ID: "missing-input", MaxContextTokens: modelIntForTest(1024)}},
		{"unknown-limit", ModelPreset{ID: "unknown-limit", Multimodal: modelBoolForTest(false)}},
		{"missing-context", ModelPreset{ID: "missing-context", Multimodal: modelBoolForTest(false)}},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			model, exists := preset.Models[test.key]
			if !exists {
				t.Fatalf("model %q is missing from %+v", test.key, preset.Models)
			}
			if !reflect.DeepEqual(model, test.want) {
				t.Errorf("model %q = %+v, want %+v", test.key, model, test.want)
			}
		})
	}

	payload, err := json.Marshal(preset)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(payload)), `"models"`) || strings.Contains(string(payload), "canonical-image") {
		t.Errorf("preset JSON exposes model metadata: %s", payload)
	}
}

func TestPresetCatalogListMergesModelsWithoutUsableRemoteEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"deepseek-key": {
				"id": "deepseek",
				"name": "DeepSeek Metadata",
				"npm": "@ai-sdk/openai-compatible",
				"models": {
					"deepseek-chat": {
						"modalities": {"input": ["text", "image"]},
						"limit": {"context": 65536}
					}
				}
			},
			"openrouter-key": {
				"id": "openrouter",
				"npm": "@ai-sdk/google",
				"api": "https://ignored.example.test/v1",
				"models": {
					"openrouter-model": {
						"modalities": {"input": ["text"]},
						"limit": {"context": 32768}
					}
				}
			},
			"new-no-api": {
				"npm": "@ai-sdk/openai-compatible",
				"models": {"orphan": {}}
			},
			"new-unsupported": {
				"npm": "@ai-sdk/google",
				"api": "https://new.example.test/v1",
				"models": {"orphan": {}}
			}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	got, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != len(Presets()) {
		t.Fatalf("len(List()) = %d, want only %d built-in presets", len(got), len(Presets()))
	}
	for _, id := range []string{"new-no-api", "new-unsupported"} {
		if _, exists := findPresetForTest(got, id); exists {
			t.Errorf("new provider %q without a usable endpoint was included", id)
		}
	}

	deepseek, ok := findPresetForTest(got, "deepseek")
	if !ok {
		t.Fatal("deepseek preset is missing")
	}
	if deepseek.Protocol != config.ProtocolChatCompletions || deepseek.BaseURL != "https://api.deepseek.com" {
		t.Errorf("deepseek built-in endpoint was not preserved: %+v", deepseek)
	}
	if deepseek.Balance == nil || deepseek.Balance.Type != "deepseek" {
		t.Errorf("deepseek built-in balance was not preserved: %+v", deepseek.Balance)
	}
	model := deepseek.Models["deepseek-chat"]
	if model.ID != "deepseek-chat" || model.Multimodal == nil || !*model.Multimodal || model.MaxContextTokens == nil || *model.MaxContextTokens != 65536 {
		t.Errorf("deepseek model metadata was not merged: %+v", model)
	}

	openrouter, ok := findPresetForTest(got, "openrouter")
	if !ok {
		t.Fatal("openrouter preset is missing")
	}
	if openrouter.Protocol != config.ProtocolAnthropic || openrouter.BaseURL != "https://openrouter.ai/api" {
		t.Errorf("openrouter built-in endpoint was not preserved: %+v", openrouter)
	}
	if openrouter.Balance == nil || openrouter.Balance.Type != "openrouter" {
		t.Errorf("openrouter built-in balance was not preserved: %+v", openrouter.Balance)
	}
	model = openrouter.Models["openrouter-model"]
	if model.ID != "openrouter-model" || model.Multimodal == nil || *model.Multimodal || model.MaxContextTokens == nil || *model.MaxContextTokens != 32768 {
		t.Errorf("openrouter model metadata was not merged: %+v", model)
	}
}

func TestMergeProviderPresetsDoesNotShareMutableState(t *testing.T) {
	builtInMultimodal := false
	builtInContext := 100
	remoteMultimodal := true
	remoteContext := 200
	builtIn := []ProviderPreset{{
		ID: "shared", Name: "Built In", Protocol: config.ProtocolChatCompletions,
		BaseURL: "https://built-in.example.test", Balance: &config.BalanceConfig{Type: "built-in"},
		Models: map[string]ModelPreset{
			"built-in": {ID: "built-in", Multimodal: &builtInMultimodal, MaxContextTokens: &builtInContext},
		},
	}}
	remote := []ProviderPreset{{
		ID: "shared", Name: "Remote Metadata",
		Models: map[string]ModelPreset{
			"remote": {ID: "remote", Multimodal: &remoteMultimodal, MaxContextTokens: &remoteContext},
		},
	}}

	merged := mergeProviderPresets(builtIn, remote)
	if len(merged) != 1 {
		t.Fatalf("len(mergeProviderPresets()) = %d, want 1", len(merged))
	}
	builtInMultimodal = true
	builtInContext = 1
	remoteMultimodal = false
	remoteContext = 2
	builtIn[0].Balance.Type = "mutated"
	builtIn[0].Models["extra"] = ModelPreset{ID: "extra"}
	remote[0].Models["extra"] = ModelPreset{ID: "extra"}

	got := merged[0]
	if got.Protocol != config.ProtocolChatCompletions || got.BaseURL != "https://built-in.example.test" || got.Balance == nil || got.Balance.Type != "built-in" {
		t.Errorf("built-in fields were not preserved: %+v", got)
	}
	if len(got.Models) != 2 {
		t.Errorf("merged models = %+v, want built-in and remote entries", got.Models)
	}
	builtInModel := got.Models["built-in"]
	if builtInModel.Multimodal == nil || *builtInModel.Multimodal || builtInModel.MaxContextTokens == nil || *builtInModel.MaxContextTokens != 100 {
		t.Errorf("merged built-in model aliases its input: %+v", builtInModel)
	}
	remoteModel := got.Models["remote"]
	if remoteModel.Multimodal == nil || !*remoteModel.Multimodal || remoteModel.MaxContextTokens == nil || *remoteModel.MaxContextTokens != 200 {
		t.Errorf("merged remote model aliases its input: %+v", remoteModel)
	}
}

func TestPresetCatalogListReturnsIndependentModelCopies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{
			"deepseek-key": {
				"id": "deepseek",
				"npm": "@ai-sdk/openai-compatible",
				"api": "https://cached.example.test/v1",
				"models": {
					"model-key": {
						"id": "model-id",
						"modalities": {"input": ["text", "image"]},
						"limit": {"context": 12345}
					}
				}
			}
		}`)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), time.Now)
	first, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("first List() error = %v", err)
	}
	firstPreset, ok := findPresetForTest(first, "deepseek")
	if !ok {
		t.Fatal("deepseek preset is missing")
	}
	firstModel, ok := firstPreset.Models["model-id"]
	if !ok || firstModel.Multimodal == nil || firstModel.MaxContextTokens == nil {
		t.Fatalf("model metadata is incomplete: %+v", firstPreset.Models)
	}
	multimodalPointer := firstModel.Multimodal
	contextPointer := firstModel.MaxContextTokens
	aliasModel := firstPreset.Models["model-key"]
	if aliasModel.Multimodal == multimodalPointer || aliasModel.MaxContextTokens == contextPointer {
		t.Error("model ID aliases share mutable capability pointers")
	}

	*firstModel.Multimodal = false
	*firstModel.MaxContextTokens = 1
	firstModel.ID = "mutated"
	firstPreset.Models["model-id"] = firstModel
	firstPreset.Models["extra"] = ModelPreset{ID: "extra"}
	if aliasModel.Multimodal == nil || !*aliasModel.Multimodal || aliasModel.MaxContextTokens == nil || *aliasModel.MaxContextTokens != 12345 {
		t.Errorf("mutating canonical model changed its alias: %+v", aliasModel)
	}

	second, err := catalog.List(context.Background(), false)
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	secondPreset, ok := findPresetForTest(second, "deepseek")
	if !ok {
		t.Fatal("cached deepseek preset is missing")
	}
	secondModel, ok := secondPreset.Models["model-id"]
	if !ok || secondModel.ID != "model-id" || secondModel.Multimodal == nil || !*secondModel.Multimodal || secondModel.MaxContextTokens == nil || *secondModel.MaxContextTokens != 12345 {
		t.Errorf("returned model mutation changed cache: %+v", secondPreset.Models)
	}
	if _, exists := secondPreset.Models["extra"]; exists {
		t.Error("returned Models map mutation changed cache")
	}
	if secondModel.Multimodal == multimodalPointer || secondModel.MaxContextTokens == contextPointer {
		t.Error("model capability pointers alias across returned copies")
	}
}

func TestPresetCatalogListForceRefreshesModelMetadata(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		modalities := `"text"`
		if requests > 1 {
			modalities = `"text","image"`
		}
		_, _ = fmt.Fprintf(w, `{"remote":{"npm":"@ai-sdk/openai-compatible","api":"https://example.test/v1","models":{"model":{"modalities":{"input":[%s]},"limit":{"context":%d}}}}}`, modalities, requests*1000)
	}))
	defer server.Close()

	catalog := newPresetCatalog(server.URL, server.Client(), func() time.Time { return now })
	assertModel := func(force bool, wantMultimodal bool, wantContext int) {
		t.Helper()
		got, err := catalog.List(context.Background(), force)
		if err != nil {
			t.Fatalf("List(force=%v) error = %v", force, err)
		}
		preset, ok := findPresetForTest(got, "remote")
		if !ok {
			t.Fatal("remote preset is missing")
		}
		model := preset.Models["model"]
		if model.Multimodal == nil || *model.Multimodal != wantMultimodal || model.MaxContextTokens == nil || *model.MaxContextTokens != wantContext {
			t.Errorf("model = %+v, want multimodal %v and context %d", model, wantMultimodal, wantContext)
		}
	}

	assertModel(false, false, 1000)
	assertModel(false, false, 1000)
	if requests != 1 {
		t.Fatalf("requests after cached List = %d, want 1", requests)
	}
	assertModel(true, true, 2000)
	if requests != 2 {
		t.Fatalf("requests after forced List = %d, want 2", requests)
	}
}

func modelBoolForTest(value bool) *bool {
	return &value
}

func modelIntForTest(value int) *int {
	return &value
}

func findPresetForTest(presets []ProviderPreset, id string) (ProviderPreset, bool) {
	index := presetIndexForTest(presets, id)
	if index < 0 {
		return ProviderPreset{}, false
	}
	return presets[index], true
}

func presetIndexForTest(presets []ProviderPreset, id string) int {
	for i := range presets {
		if presets[i].ID == id {
			return i
		}
	}
	return -1
}
