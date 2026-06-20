package service

import (
	"net/http"
	"strings"
	"testing"

	"viberouter/internal/config"
	"viberouter/internal/model"
)

func newTestLB(cfg *config.Config) *LoadBalancer {
	return &LoadBalancer{cfg: cfg, breakers: make(map[string]*CircuitBreaker)}
}

// twoTierCfg: advanced has a prio-1 long-context model + a prio-2 fallback;
// basic has a prio-1 plain model + a prio-1 long-context model.
func twoTierCfg() *config.Config {
	return &config.Config{
		CircuitBreaker: config.CircuitBreakerConfig{Threshold: 3, TimeoutSec: 30},
		Routing: config.RoutingConfig{
			LongContextThreshold: 32000,
			Complexity: config.ComplexityConfig{
				DefaultTier: "basic",
				Rules: []config.ComplexityRule{
					{Field: "message_turns", Op: "gte", Value: 8},
					{Field: "est_input_tokens", Op: "gte", Value: 8000},
					{Field: "has_tools", Op: "eq", Value: 1},
					{Field: "has_code", Op: "eq", Value: 1},
				},
			},
			Override: config.OverrideConfig{
				Header:     "X-VibeRouter-Tier",
				ModelAlias: map[string]string{"auto-advanced": "advanced", "auto-basic": "basic"},
			},
		},
		Tiers: map[string]config.TierConfig{
			"advanced": {Models: []model.BackendModel{
				{Name: "modelA", Provider: model.ProviderOpenAI, Priority: 1, LongContext: true, MaxContextTokens: 200000, Enabled: true},
				{Name: "modelB", Provider: model.ProviderOpenAI, Priority: 2, Enabled: true},
			}},
			"basic": {Models: []model.BackendModel{
				{Name: "modelC", Provider: model.ProviderAnthropic, Priority: 1, Enabled: true},
				{Name: "modelD", Provider: model.ProviderAnthropic, Priority: 1, LongContext: true, MaxContextTokens: 200000, Enabled: true},
			}},
		},
	}
}

func names(ms []model.BackendModel) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = string(m.Tier) + ":" + m.Name
	}
	return strings.Join(out, ",")
}

func TestRoute_ComplexitySelectsTier(t *testing.T) {
	lb := newTestLB(twoTierCfg())

	// Short, plain request -> basic (default tier).
	got, info, err := lb.Route(RouteRequest{ModelName: "auto", MessageTurns: 1, EstInputTokens: 100})
	if err != nil || info.Tier != model.TierBasic {
		t.Fatalf("short request: tier=%v err=%v", info.Tier, err)
	}
	for _, m := range got {
		if m.Tier != model.TierBasic {
			t.Fatalf("expected only basic models, got %s", names(got))
		}
	}

	// Long turns -> complex -> advanced.
	got, info, _ = lb.Route(RouteRequest{ModelName: "auto", MessageTurns: 10, EstInputTokens: 100})
	if info.Tier != model.TierAdvanced {
		t.Fatalf("complex request: want advanced tier, got %v", info.Tier)
	}
	if len(got) == 0 || got[0].Tier != model.TierAdvanced {
		t.Fatalf("expected advanced candidate first, got %s", names(got))
	}
}

func TestRoute_TierOverride(t *testing.T) {
	lb := newTestLB(twoTierCfg())
	hdr := http.Header{}
	hdr.Set("X-VibeRouter-Tier", "advanced")

	got, info, _ := lb.Route(RouteRequest{ModelName: "auto", Header: hdr, MessageTurns: 1, EstInputTokens: 100})
	if info.Tier != model.TierAdvanced {
		t.Fatalf("header override: want advanced, got %v", info.Tier)
	}

	// Alias override.
	got, info, _ = lb.Route(RouteRequest{ModelName: "auto-basic", MessageTurns: 10, EstInputTokens: 100})
	if info.Tier != model.TierBasic {
		t.Fatalf("alias override: want basic, got %v", info.Tier)
	}
	_ = got
}

func TestRoute_PriorityOrder(t *testing.T) {
	lb := newTestLB(twoTierCfg())
	got, info, _ := lb.Route(RouteRequest{ModelName: "auto", MessageTurns: 10, EstInputTokens: 100}) // advanced
	if info.Tier != model.TierAdvanced || len(got) != 2 {
		t.Fatalf("want 2 advanced candidates, got %s", names(got))
	}
	if got[0].Name != "modelA" || got[1].Name != "modelB" {
		t.Fatalf("priority order wrong: got %s", names(got))
	}
}

func TestRoute_LongContextFilter(t *testing.T) {
	lb := newTestLB(twoTierCfg())
	// Force basic tier with a huge prompt -> must keep only long-context basic model.
	hdr := http.Header{}
	hdr.Set("X-VibeRouter-Tier", "basic")
	got, info, err := lb.Route(RouteRequest{ModelName: "auto", Header: hdr, EstInputTokens: 50000, MessageTurns: 1})
	if err != nil {
		t.Fatalf("long-context: unexpected err %v", err)
	}
	if !info.IsLongContext {
		t.Fatalf("want IsLongContext=true")
	}
	for _, m := range got {
		if !m.LongContext {
			t.Fatalf("non-long-context model in long-context pool: %s", names(got))
		}
	}
	if len(got) != 1 || got[0].Name != "modelD" {
		t.Fatalf("want only modelD, got %s", names(got))
	}
}

func TestRoute_LongContextEscalation(t *testing.T) {
	// Basic tier has NO long-context model; advanced has one.
	cfg := twoTierCfg()
	cfg.Tiers["basic"] = config.TierConfig{Models: []model.BackendModel{
		{Name: "modelC", Provider: model.ProviderAnthropic, Priority: 1, Enabled: true}, // not long-context
	}}
	lb := newTestLB(cfg)

	got, info, err := lb.Route(RouteRequest{ModelName: "auto", EstInputTokens: 50000, MessageTurns: 1})
	if err != nil {
		t.Fatalf("escalation: unexpected err %v", err)
	}
	if info.Tier != model.TierAdvanced {
		t.Fatalf("escalation: want tier advanced, got %v", info.Tier)
	}
	if len(got) != 1 || got[0].Name != "modelA" {
		t.Fatalf("escalation: want advanced long-context modelA, got %s", names(got))
	}
}

func TestRoute_NoLongContextAvailable(t *testing.T) {
	cfg := twoTierCfg()
	// No long-context models anywhere.
	for tname := range cfg.Tiers {
		models := cfg.Tiers[tname].Models
		for i := range models {
			models[i].LongContext = false
		}
	}
	lb := newTestLB(cfg)
	_, _, err := lb.Route(RouteRequest{ModelName: "auto", EstInputTokens: 50000, MessageTurns: 1})
	if err == nil {
		t.Fatal("want error when no long-context model available")
	}
}

func TestRoute_DirectModel(t *testing.T) {
	lb := newTestLB(twoTierCfg())
	got, info, err := lb.Route(RouteRequest{ModelName: "modelB", MessageTurns: 1, EstInputTokens: 100})
	if err != nil || !info.Direct || len(got) != 1 || got[0].Name != "modelB" {
		t.Fatalf("direct model: info=%+v got=%s err=%v", info, names(got), err)
	}
}

func TestRoute_CircuitOpenSkipped(t *testing.T) {
	lb := newTestLB(twoTierCfg())
	// Force modelA (advanced prio-1) circuit open.
	for i := 0; i < lb.cfg.CircuitBreaker.Threshold; i++ {
		lb.GetBreaker("advanced:modelA").RecordFailure()
	}
	got, info, _ := lb.Route(RouteRequest{ModelName: "auto", MessageTurns: 10, EstInputTokens: 100})
	if info.Tier != model.TierAdvanced {
		t.Fatalf("want advanced tier, got %v", info.Tier)
	}
	for _, m := range got {
		if m.Name == "modelA" {
			t.Fatalf("open-circuit modelA should be skipped, got %s", names(got))
		}
	}
}

func TestRoute_EmptyTierErrors(t *testing.T) {
	cfg := twoTierCfg()
	delete(cfg.Tiers, "advanced")
	lb := newTestLB(cfg)
	hdr := http.Header{}
	hdr.Set("X-VibeRouter-Tier", "advanced")
	_, _, err := lb.Route(RouteRequest{ModelName: "auto", Header: hdr, EstInputTokens: 100, MessageTurns: 1})
	if err == nil {
		t.Fatal("want error for empty advanced tier")
	}
}
