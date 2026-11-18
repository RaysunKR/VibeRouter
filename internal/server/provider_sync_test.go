package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/upstream"
)

func newProviderSyncTestServer(t *testing.T) (*httptest.Server, *config.Store, *Server, string) {
	t.Helper()
	up := fakeUpstream(t, chatHandler)
	ts, cfg, srv := newTestServer(t, up.URL)
	token, err := srv.auth.Issue("admin")
	if err != nil {
		t.Fatal(err)
	}
	return ts, cfg, srv, token
}

func TestProviderSyncPresetUpdatesInstanceAndModels(t *testing.T) {
	ts, cfg, srv, token := newProviderSyncTestServer(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Providers = append(c.Providers,
			config.Provider{ID: "target", Name: "Custom target", Protocol: config.ProtocolChatCompletions, BaseURL: "http://old-target", APIKey: "target-key", PresetID: "preset", Balance: config.BalanceConfig{Type: "deepseek", URL: "http://custom-balance"}},
			config.Provider{ID: "other", Name: "Other account", Protocol: config.ProtocolChatCompletions, BaseURL: "http://old-other", APIKey: "other-key", PresetID: "preset"},
		)
		c.Models = append(c.Models,
			config.Model{Name: "target-default", Provider: "target", UpstreamName: "known"},
			config.Model{Name: "target-override", Provider: "target", UpstreamName: "known", APIKey: "model-key", BaseURL: "http://model-override", Protocol: config.ProtocolResponses},
			config.Model{Name: "target-unknown", Provider: "target", UpstreamName: "unknown", Multimodal: true, LongContext: true, MaxTokens: 500},
			config.Model{Name: "other-model", Provider: "other", UpstreamName: "known"},
		)
		c.Groups = append(c.Groups, config.Group{
			Name: "unchanged-group", Mode: config.ModeSmart, DispatcherModel: "target-default",
			Advanced: []config.GroupMember{{Model: "target-default", Priority: 1}},
			Basic:    []config.GroupMember{{Model: "target-override", Priority: 2}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := cfg.Snapshot()
	multimodal := true
	contextTokens := 200001
	catalog := &fakePresetCatalog{presets: []upstream.ProviderPreset{{
		ID: "preset", Name: "Updated preset", Protocol: config.ProtocolAnthropic, BaseURL: "https://new-preset",
		Models: map[string]upstream.ModelPreset{
			"known": {ID: "known", Multimodal: &multimodal, MaxContextTokens: &contextTokens},
		},
	}}}
	srv.presets = catalog

	status, body := adminRequest(t, http.MethodPost, ts.URL+"/api/providers/target/sync-preset", token)
	if status != http.StatusOK {
		t.Fatalf("sync preset: status=%d body=%s", status, body)
	}
	if !catalog.force {
		t.Fatal("provider sync did not force a preset refresh")
	}
	var result struct {
		Provider      config.Provider `json:"provider"`
		ModelsUpdated int             `json:"models_updated"`
		ModelsSkipped []string        `json:"models_skipped"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if result.ModelsUpdated != 2 || !reflect.DeepEqual(result.ModelsSkipped, []string{"target-unknown"}) {
		t.Fatalf("sync result: updated=%d skipped=%v", result.ModelsUpdated, result.ModelsSkipped)
	}

	after := cfg.Snapshot()
	target := after.FindProvider("target")
	if target == nil || target.Protocol != config.ProtocolAnthropic || target.BaseURL != "https://new-preset" {
		t.Fatalf("provider endpoint was not updated: %+v", target)
	}
	if target.ID != "target" || target.Name != "Custom target" || target.APIKey != "target-key" || target.PresetID != "preset" || target.Balance != before.FindProvider("target").Balance {
		t.Fatalf("provider identity, key, binding, or balance changed: %+v", target)
	}
	for _, name := range []string{"target-default", "target-override"} {
		model := after.FindModel(name)
		if model == nil || !model.Multimodal || !model.LongContext || model.MaxTokens != contextTokens {
			t.Fatalf("model %s capabilities not updated: %+v", name, model)
		}
	}
	override := after.FindModel("target-override")
	if override.APIKey != "model-key" || override.BaseURL != "http://model-override" || override.Protocol != config.ProtocolResponses {
		t.Fatalf("model overrides changed: %+v", override)
	}
	resolved, err := after.ResolveModel(after.FindModel("target-default"))
	if err != nil || resolved.EffectiveProtocol != config.ProtocolAnthropic || resolved.EffectiveBaseURL != "https://new-preset" || resolved.EffectiveAPIKey != "target-key" {
		t.Fatalf("default model resolution: resolved=%+v err=%v", resolved, err)
	}
	resolved, err = after.ResolveModel(override)
	if err != nil || resolved.EffectiveProtocol != config.ProtocolResponses || resolved.EffectiveBaseURL != "http://model-override" || resolved.EffectiveAPIKey != "model-key" {
		t.Fatalf("override model resolution: resolved=%+v err=%v", resolved, err)
	}
	if !reflect.DeepEqual(after.Groups, before.Groups) {
		t.Fatalf("groups changed: before=%+v after=%+v", before.Groups, after.Groups)
	}
	if !reflect.DeepEqual(*after.FindProvider("other"), *before.FindProvider("other")) ||
		!reflect.DeepEqual(*after.FindModel("other-model"), *before.FindModel("other-model")) ||
		!reflect.DeepEqual(*after.FindModel("target-unknown"), *before.FindModel("target-unknown")) {
		t.Fatal("unrelated provider or models changed")
	}
}

func TestProviderSyncPresetConflictIsAtomic(t *testing.T) {
	ts, cfg, srv, token := newProviderSyncTestServer(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Providers = append(c.Providers,
			config.Provider{ID: "sync-source", Name: "Source", Protocol: config.ProtocolChatCompletions, BaseURL: "http://old", APIKey: "shared-key", PresetID: "preset"},
			config.Provider{ID: "sync-conflict", Name: "Conflict", Protocol: config.ProtocolAnthropic, BaseURL: "https://new", APIKey: "shared-key"},
		)
		c.Models = append(c.Models, config.Model{Name: "sync-model", Provider: "sync-source", UpstreamName: "known", MaxTokens: 100})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	multimodal := true
	srv.presets = &fakePresetCatalog{presets: []upstream.ProviderPreset{{
		ID: "preset", Protocol: config.ProtocolAnthropic, BaseURL: "https://new",
		Models: map[string]upstream.ModelPreset{"known": {ID: "known", Multimodal: &multimodal}},
	}}}
	before := cfg.Snapshot()
	status, body := adminRequest(t, http.MethodPost, ts.URL+"/api/providers/sync-source/sync-preset", token)
	if status != http.StatusConflict {
		t.Fatalf("conflicting sync: status=%d body=%s", status, body)
	}
	after := cfg.Snapshot()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed sync changed config: before=%+v after=%+v", before, after)
	}
}

func TestProviderUpdatePresetBindingCompatibility(t *testing.T) {
	ts, cfg, token, upstreamURL := newProviderTestServer(t)
	provider := config.Provider{ID: "bound", Name: "Bound", Protocol: config.ProtocolChatCompletions, BaseURL: upstreamURL, APIKey: "bound-key", PresetID: "old-preset"}
	status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
	if status != http.StatusOK {
		t.Fatalf("create bound provider: status=%d body=%s", status, body)
	}

	legacyPayload := map[string]any{"name": "Renamed", "protocol": provider.Protocol, "base_url": provider.BaseURL, "api_key": provider.APIKey}
	status, body = adminJSONRequest(t, http.MethodPut, ts.URL+"/api/providers/bound", token, legacyPayload)
	if status != http.StatusOK {
		t.Fatalf("legacy update: status=%d body=%s", status, body)
	}
	snapshot := cfg.Snapshot()
	if got := snapshot.FindProvider("bound").PresetID; got != "old-preset" {
		t.Fatalf("legacy update cleared preset binding: %q", got)
	}

	for _, binding := range []string{"new-preset", ""} {
		payload := map[string]any{"name": "Renamed", "protocol": provider.Protocol, "base_url": provider.BaseURL, "api_key": provider.APIKey, "preset_id": binding}
		status, body = adminJSONRequest(t, http.MethodPut, ts.URL+"/api/providers/bound", token, payload)
		if status != http.StatusOK {
			t.Fatalf("update binding to %q: status=%d body=%s", binding, status, body)
		}
		snapshot = cfg.Snapshot()
		if got := snapshot.FindProvider("bound").PresetID; got != binding {
			t.Fatalf("preset binding = %q, want %q", got, binding)
		}
	}
}

func TestProviderSyncPresetErrors(t *testing.T) {
	ts, cfg, srv, token := newProviderSyncTestServer(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Providers = append(c.Providers,
			config.Provider{ID: "unbound", Name: "Unbound", Protocol: config.ProtocolChatCompletions, BaseURL: "http://unbound", APIKey: "unbound-key"},
			config.Provider{ID: "bound-error", Name: "Bound", Protocol: config.ProtocolChatCompletions, BaseURL: "http://bound-error", APIKey: "bound-error-key", PresetID: "missing"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := cfg.Snapshot()
	catalog := &fakePresetCatalog{}
	srv.presets = catalog

	status, _ := adminRequest(t, http.MethodPost, ts.URL+"/api/providers/bound-error/sync-preset", "")
	if status != http.StatusUnauthorized || catalog.calls != 0 {
		t.Fatalf("unauthenticated sync: status=%d calls=%d", status, catalog.calls)
	}
	status, _ = adminRequest(t, http.MethodPost, ts.URL+"/api/providers/unbound/sync-preset", token)
	if status != http.StatusBadRequest || catalog.calls != 0 {
		t.Fatalf("unbound sync: status=%d calls=%d", status, catalog.calls)
	}
	status, _ = adminRequest(t, http.MethodPost, ts.URL+"/api/providers/not-found/sync-preset", token)
	if status != http.StatusNotFound || catalog.calls != 0 {
		t.Fatalf("missing provider sync: status=%d calls=%d", status, catalog.calls)
	}
	status, _ = adminRequest(t, http.MethodPost, ts.URL+"/api/providers/bound-error/sync-preset", token)
	if status != http.StatusNotFound || catalog.calls != 1 || !catalog.force {
		t.Fatalf("missing preset sync: status=%d calls=%d force=%v", status, catalog.calls, catalog.force)
	}
	catalog.err = errors.New("offline")
	status, _ = adminRequest(t, http.MethodPost, ts.URL+"/api/providers/bound-error/sync-preset", token)
	if status != http.StatusBadGateway || catalog.calls != 2 {
		t.Fatalf("failed catalog sync: status=%d calls=%d", status, catalog.calls)
	}
	if after := cfg.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("error paths changed config: before=%+v after=%+v", before, after)
	}
}
