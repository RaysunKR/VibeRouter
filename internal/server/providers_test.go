package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
)

func newProviderTestServer(t *testing.T) (*httptest.Server, *config.Store, string, string) {
	t.Helper()
	up := fakeUpstream(t, chatHandler)
	ts, cfg, srv := newTestServer(t, up.URL)
	token, err := srv.auth.Issue("admin")
	if err != nil {
		t.Fatal(err)
	}
	return ts, cfg, token, up.URL
}

func adminJSONRequest(t *testing.T, method, url, token string, payload any) (int, string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return doReq(t, method, url, token, string(body))
}

func TestProviderMultipleAccountInstances(t *testing.T) {
	ts, cfg, token, upstreamURL := newProviderTestServer(t)
	providers := []config.Provider{
		{
			ID:       "account-one",
			Name:     "Account One",
			Protocol: config.ProtocolChatCompletions,
			BaseURL:  upstreamURL,
			APIKey:   "account-one-key",
		},
		{
			ID:       "account-two",
			Name:     "Account Two",
			Protocol: config.ProtocolChatCompletions,
			BaseURL:  upstreamURL,
			APIKey:   "account-two-key",
		},
	}
	for _, provider := range providers {
		status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
		if status != http.StatusOK {
			t.Fatalf("create provider %s: status=%d body=%s", provider.ID, status, body)
		}
	}

	models := []config.Model{
		{Name: "account-one-model", Provider: providers[0].ID, UpstreamName: "real-model"},
		{Name: "account-two-model", Provider: providers[1].ID, UpstreamName: "real-model"},
	}
	for _, model := range models {
		status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/models", token, model)
		if status != http.StatusOK {
			t.Fatalf("create model %s: status=%d body=%s", model.Name, status, body)
		}
	}

	snapshot := cfg.Snapshot()
	for i, model := range models {
		configured := snapshot.FindModel(model.Name)
		if configured == nil {
			t.Fatalf("model %s was not persisted", model.Name)
		}
		resolved, err := snapshot.ResolveModel(configured)
		if err != nil {
			t.Fatalf("resolve model %s: %v", model.Name, err)
		}
		if resolved.EffectiveAPIKey != providers[i].APIKey {
			t.Errorf("model %s resolved key %q, want %q", model.Name, resolved.EffectiveAPIKey, providers[i].APIKey)
		}
	}
}

func TestProviderCreateRejectsDuplicateID(t *testing.T) {
	ts, _, token, upstreamURL := newProviderTestServer(t)
	provider := config.Provider{
		ID:       "up",
		Name:     "Duplicate ID",
		Protocol: config.ProtocolChatCompletions,
		BaseURL:  upstreamURL,
		APIKey:   "different-key",
	}
	status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
	if status != http.StatusConflict {
		t.Fatalf("create provider with duplicate ID: status=%d body=%s", status, body)
	}
}

func TestProviderCreateRejectsDuplicateCredentials(t *testing.T) {
	ts, _, token, upstreamURL := newProviderTestServer(t)
	provider := config.Provider{
		ID:       "duplicate-credentials",
		Name:     "Duplicate Credentials",
		Protocol: config.ProtocolChatCompletions,
		BaseURL:  upstreamURL + "///",
		APIKey:   "upkey",
	}
	status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
	if status != http.StatusConflict {
		t.Fatalf("create provider with duplicate credentials: status=%d body=%s", status, body)
	}
	if strings.Contains(body, provider.APIKey) {
		t.Fatalf("conflict response contains API key: %s", body)
	}
}

func TestProviderUpdateRejectsOtherInstanceCredentials(t *testing.T) {
	ts, cfg, token, upstreamURL := newProviderTestServer(t)
	providers := []config.Provider{
		{
			ID:       "update-account-one",
			Name:     "Update Account One",
			Protocol: config.ProtocolChatCompletions,
			BaseURL:  upstreamURL,
			APIKey:   "update-account-one-key",
		},
		{
			ID:       "update-account-two",
			Name:     "Update Account Two",
			Protocol: config.ProtocolChatCompletions,
			BaseURL:  upstreamURL,
			APIKey:   "update-account-two-key",
		},
	}
	for _, provider := range providers {
		status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
		if status != http.StatusOK {
			t.Fatalf("create provider %s: status=%d body=%s", provider.ID, status, body)
		}
	}

	selfUpdate := providers[0]
	selfUpdate.Name = "Renamed Account One"
	selfUpdate.BaseURL += "/"
	status, body := adminJSONRequest(t, http.MethodPut, ts.URL+"/api/providers/"+selfUpdate.ID, token, selfUpdate)
	if status != http.StatusOK {
		t.Fatalf("update provider with its own credentials: status=%d body=%s", status, body)
	}

	conflictingUpdate := providers[1]
	conflictingUpdate.BaseURL = upstreamURL + "///"
	conflictingUpdate.APIKey = selfUpdate.APIKey
	status, body = adminJSONRequest(t, http.MethodPut, ts.URL+"/api/providers/"+conflictingUpdate.ID, token, conflictingUpdate)
	if status != http.StatusConflict {
		t.Fatalf("update provider with other instance credentials: status=%d body=%s", status, body)
	}
	if strings.Contains(body, conflictingUpdate.APIKey) {
		t.Fatalf("conflict response contains API key: %s", body)
	}
	snapshot := cfg.Snapshot()
	unchanged := snapshot.FindProvider(conflictingUpdate.ID)
	if unchanged == nil || unchanged.APIKey != providers[1].APIKey {
		t.Fatalf("rejected update changed provider: %+v", unchanged)
	}
}

func TestProviderAccountKeysReachGroupedUpstream(t *testing.T) {
	keys := make(chan string, 2)
	up := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		keys <- r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(okChatResponse))
	})
	ts, _, srv := newTestServer(t, up.URL)
	token, err := srv.auth.Issue("admin")
	if err != nil {
		t.Fatal(err)
	}

	providers := []config.Provider{
		{ID: "group-account-one", Name: "Group Account One", Protocol: config.ProtocolChatCompletions, BaseURL: up.URL, APIKey: "group-account-one-key"},
		{ID: "group-account-two", Name: "Group Account Two", Protocol: config.ProtocolChatCompletions, BaseURL: up.URL, APIKey: "group-account-two-key"},
	}
	for _, provider := range providers {
		status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/providers", token, provider)
		if status != http.StatusOK {
			t.Fatalf("create provider %s: status=%d body=%s", provider.ID, status, body)
		}
	}
	models := []config.Model{
		{Name: "group-account-one-model", Provider: providers[0].ID, UpstreamName: "real-model"},
		{Name: "group-account-two-model", Provider: providers[1].ID, UpstreamName: "real-model"},
	}
	for _, model := range models {
		status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/models", token, model)
		if status != http.StatusOK {
			t.Fatalf("create model %s: status=%d body=%s", model.Name, status, body)
		}
	}
	group := config.Group{
		Name: "account-group", Mode: config.ModeBalance, Strategy: "round_robin",
		Members: []config.GroupMember{{Model: models[0].Name, Priority: 1}, {Model: models[1].Name, Priority: 1}},
	}
	status, body := adminJSONRequest(t, http.MethodPost, ts.URL+"/api/groups", token, group)
	if status != http.StatusOK {
		t.Fatalf("create group: status=%d body=%s", status, body)
	}

	for i := 0; i < 2; i++ {
		status, body = doReq(t, http.MethodPost, ts.URL+"/v1/chat/completions", "vr-testkey",
			`{"model":"account-group","messages":[{"role":"user","content":"hello"}]}`)
		if status != http.StatusOK {
			t.Fatalf("grouped request %d: status=%d body=%s", i, status, body)
		}
	}
	first, second := <-keys, <-keys
	if first == second || first != "Bearer "+providers[0].APIKey && second != "Bearer "+providers[0].APIKey ||
		first != "Bearer "+providers[1].APIKey && second != "Bearer "+providers[1].APIKey {
		t.Fatalf("upstream keys = %q, %q; want both account keys", first, second)
	}
}
