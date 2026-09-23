package router

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

func testConfig(t *testing.T) *config.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
server: {addr: ":0"}
routing:
  long_context_threshold: 5000
  dispatcher_timeout_sec: 1
  strategy: round_robin
providers:
  - {id: p1, name: P1, protocol: chat_completions, base_url: http://up1}
models:
  - {name: m1, provider: p1, upstream_model: u1, multimodal: false, long_context: false, max_context_tokens: 1000}
  - {name: m2, provider: p1, upstream_model: u2, multimodal: true, long_context: false, max_context_tokens: 1000}
  - {name: m3, provider: p1, upstream_model: u3, multimodal: true, long_context: true, max_context_tokens: 1000000}
  - {name: m4, provider: p1, upstream_model: u4, multimodal: false, long_context: true, max_context_tokens: 1000000}
groups:
  - name: lb
    mode: balance
    members:
      - {model: m1, priority: 1}
      - {model: m2, priority: 1}
      - {model: m3, priority: 2}
  - name: smart
    mode: smart
    dispatcher_model: m1
    advanced:
      - {model: m3, priority: 1}
    basic:
      - {model: m1, priority: 1}
      - {model: m4, priority: 2}
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func textReq(text string) *protocol.CanonicalRequest {
	return &protocol.CanonicalRequest{Messages: []protocol.Message{
		{Role: "user", Parts: []protocol.Part{{Type: protocol.PartText, Text: text}}},
	}}
}

func TestDirectRoute(t *testing.T) {
	rt := New(testConfig(t), nil)
	cands, dec, err := rt.Route(context.Background(), "m1", textReq("hi"), "")
	if err != nil || len(cands) != 1 || cands[0].UpstreamName != "u1" {
		t.Fatalf("direct: cands=%v err=%v", cands, err)
	}
	if dec.Mode != "direct" {
		t.Fatalf("mode = %q", dec.Mode)
	}
}

func TestDirectMultimodalRejected(t *testing.T) {
	rt := New(testConfig(t), nil)
	req := &protocol.CanonicalRequest{Messages: []protocol.Message{
		{Role: "user", Parts: []protocol.Part{{Type: protocol.PartImage, ImageURL: "http://x/y.png"}}},
	}}
	_, _, err := rt.Route(context.Background(), "m1", req, "")
	if err == nil {
		t.Fatal("expected capability error")
	}
}

func TestBalanceRoundRobin(t *testing.T) {
	rt := New(testConfig(t), nil)
	first, _, err := rt.Route(context.Background(), "lb", textReq("hi"), "")
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := rt.Route(context.Background(), "lb", textReq("hi"), "")
	// priority 1 bucket has m1,m2 — round robin should alternate heads
	if first[0].Name == second[0].Name {
		t.Fatalf("round robin did not rotate: %s vs %s", first[0].Name, second[0].Name)
	}
	// priority 2 always appended last
	if first[len(first)-1].Name != "m3" {
		t.Fatalf("priority order broken: %+v", first)
	}
}

func TestBalanceMultimodalFilter(t *testing.T) {
	rt := New(testConfig(t), nil)
	req := &protocol.CanonicalRequest{Messages: []protocol.Message{
		{Role: "user", Parts: []protocol.Part{{Type: protocol.PartImage, ImageURL: "http://x/y.png"}}},
	}}
	cands, _, err := rt.Route(context.Background(), "lb", req, "")
	if err != nil {
		t.Fatal(err)
	}
	// only multimodal models survive: m2 (pri 1) then m3 (pri 2)
	if len(cands) != 2 || cands[0].Name != "m2" || cands[1].Name != "m3" {
		t.Fatalf("bad filter: %+v", cands)
	}
}

func TestSmartTierAndEscalation(t *testing.T) {
	cfg := testConfig(t)
	var classifyCalls int
	rt := New(cfg, func(ctx context.Context, g *config.Group, req *protocol.CanonicalRequest) (string, error) {
		classifyCalls++
		return config.TierBasic, nil
	})
	cands, dec, err := rt.Route(context.Background(), "smart", textReq("short"), "")
	if err != nil {
		t.Fatal(err)
	}
	if classifyCalls != 1 || dec.Tier != config.TierBasic {
		t.Fatalf("smart basic: tier=%q calls=%d", dec.Tier, classifyCalls)
	}
	if cands[0].Name != "m1" {
		t.Fatalf("basic tier first = %s", cands[0].Name)
	}

	// long context forces long_context models; basic tier has m4 (long), so no escalation
	long := textReq(strings.Repeat("x", 30000)) // est ~7500 tokens > 5000 threshold
	cands, _, err = rt.Route(context.Background(), "smart", long, "")
	if err != nil {
		t.Fatal(err)
	}
	if cands[0].Name != "m4" {
		t.Fatalf("long ctx basic should pick m4, got %s", cands[0].Name)
	}

	// multimodal + long context: basic tier has none → escalate to advanced (m3)
	mm := &protocol.CanonicalRequest{Messages: []protocol.Message{{Role: "user", Parts: []protocol.Part{
		{Type: protocol.PartText, Text: strings.Repeat("x", 30000)},
		{Type: protocol.PartImage, ImageURL: "http://x/y.png"},
	}}}}
	cands, dec, err = rt.Route(context.Background(), "smart", mm, "")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Tier != config.TierAdvanced || cands[0].Name != "m3" {
		t.Fatalf("escalation failed: tier=%s cands=%v", dec.Tier, cands)
	}
}

func TestSmartForcedTier(t *testing.T) {
	rt := New(testConfig(t), func(ctx context.Context, g *config.Group, req *protocol.CanonicalRequest) (string, error) {
		t.Fatal("classifier must not be called when tier forced")
		return "", nil
	})
	_, dec, err := rt.Route(context.Background(), "smart", textReq("hi"), "advanced")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Tier != config.TierAdvanced || !dec.Forced {
		t.Fatalf("forced tier: %+v", dec)
	}
}

func TestDispatcherFailureDegrades(t *testing.T) {
	rt := New(testConfig(t), func(ctx context.Context, g *config.Group, req *protocol.CanonicalRequest) (string, error) {
		return "", context.DeadlineExceeded
	})
	cands, dec, err := rt.Route(context.Background(), "smart", textReq("hi"), "")
	if err != nil {
		t.Fatal(err)
	}
	if dec.Tier != config.TierBasic || dec.DispErr == "" {
		t.Fatalf("degradation: %+v", dec)
	}
	if cands[0].Name != "m1" {
		t.Fatalf("should use basic tier on dispatcher failure")
	}
}
