package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/upstream"
)

type fakePresetCatalog struct {
	presets []upstream.ProviderPreset
	err     error
	calls   int
	force   bool
}

func (f *fakePresetCatalog) List(_ context.Context, force bool) ([]upstream.ProviderPreset, error) {
	f.calls++
	f.force = force
	return f.presets, f.err
}

func adminRequest(t *testing.T, method, url, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestPresetEndpoints(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, srv := newTestServer(t, up.URL)
	token, err := srv.auth.Issue("admin")
	if err != nil {
		t.Fatal(err)
	}
	catalog := &fakePresetCatalog{presets: []upstream.ProviderPreset{{
		ID: "remote", Name: "Remote", Protocol: config.ProtocolChatCompletions,
		BaseURL: "https://example.com", Balance: &config.BalanceConfig{Type: "deepseek"},
	}}}
	srv.presets = catalog

	status, _ := adminRequest(t, http.MethodPost, ts.URL+"/api/presets/refresh", "")
	if status != http.StatusUnauthorized || catalog.calls != 0 {
		t.Fatalf("unauthenticated refresh: status=%d calls=%d", status, catalog.calls)
	}

	status, body := adminRequest(t, http.MethodGet, ts.URL+"/api/presets", token)
	if status != http.StatusOK || catalog.force {
		t.Fatalf("list presets: status=%d force=%v body=%s", status, catalog.force, body)
	}
	var presets []upstream.ProviderPreset
	if err := json.Unmarshal(body, &presets); err != nil {
		t.Fatal(err)
	}
	if len(presets) != 1 || presets[0].Balance == nil || presets[0].Balance.Type != "deepseek" {
		t.Fatalf("unexpected presets: %s", body)
	}

	status, body = adminRequest(t, http.MethodPost, ts.URL+"/api/presets/refresh", token)
	if status != http.StatusOK || !catalog.force {
		t.Fatalf("refresh presets: status=%d force=%v body=%s", status, catalog.force, body)
	}

	catalog.err = errors.New("offline")
	status, _ = adminRequest(t, http.MethodGet, ts.URL+"/api/presets", token)
	if status != http.StatusOK {
		t.Fatalf("list with fallback: status=%d", status)
	}
	status, _ = adminRequest(t, http.MethodPost, ts.URL+"/api/presets/refresh", token)
	if status != http.StatusBadGateway {
		t.Fatalf("failed refresh: status=%d", status)
	}
}

func TestCreateProviderFromPresetBalance(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, cfg, srv := newTestServer(t, up.URL)
	token, err := srv.auth.Issue("admin")
	if err != nil {
		t.Fatal(err)
	}
	var preset upstream.ProviderPreset
	for _, candidate := range upstream.Presets() {
		if candidate.ID == "deepseek" {
			preset = candidate
			break
		}
	}
	if preset.Balance == nil {
		t.Fatal("DeepSeek preset has no balance probe")
	}
	payload, err := json.Marshal(config.Provider{
		ID: preset.ID, Name: preset.Name, Protocol: preset.Protocol, BaseURL: preset.BaseURL,
		Balance: *preset.Balance,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/providers", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create provider from preset: status=%d body=%s", resp.StatusCode, body)
	}
	snapshot := cfg.Snapshot()
	provider := snapshot.FindProvider(preset.ID)
	if provider == nil || provider.Balance.Type != "deepseek" {
		t.Fatalf("balance probe was not preserved: %+v", provider)
	}
}
