package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderPresetIDCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	legacy := []byte(`
server: {addr: ":0"}
providers:
  - {id: legacy, name: Legacy, protocol: chat_completions, base_url: "https://example.test", api_key: test-key}
`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := cfg.Snapshot()
	if got := snapshot.FindProvider("legacy").PresetID; got != "" {
		t.Fatalf("legacy preset_id = %q, want empty", got)
	}
	if err := cfg.Mutate(func(c *Config) error {
		c.FindProvider("legacy").PresetID = "preset"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = reloaded.Snapshot()
	provider := snapshot.FindProvider("legacy")
	if provider == nil || provider.PresetID != "preset" || provider.APIKey != "test-key" {
		t.Fatalf("preset binding round trip: %+v", provider)
	}
}
