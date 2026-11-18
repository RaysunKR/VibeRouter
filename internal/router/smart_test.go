package router

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

func TestSmartOnlyAdvancedCapableSkipsDispatcher(t *testing.T) {
	calls := 0
	rt := New(testConfig(t), func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierBasic, nil
	})
	req := &protocol.CanonicalRequest{Messages: []protocol.Message{{
		Role: "user", Parts: []protocol.Part{{Type: protocol.PartImage, ImageURL: "https://example.test/image.png"}},
	}}}
	cands, dec, err := rt.Route(context.Background(), "smart", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || dec.Tier != config.TierAdvanced || len(cands) != 1 || cands[0].Name != "m3" {
		t.Fatalf("tier=%s calls=%d candidates=%v", dec.Tier, calls, cands)
	}
}

func TestSmartOnlyBasicAvailableSkipsDispatcher(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Groups = append(c.Groups, config.Group{
			Name: "basic-only", Mode: config.ModeSmart,
			Basic: []config.GroupMember{{Model: "m1", Priority: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierAdvanced, nil
	})
	cands, dec, err := rt.Route(context.Background(), "basic-only", textReq("hello"), "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || dec.Tier != config.TierBasic || len(cands) != 1 || cands[0].Name != "m1" {
		t.Fatalf("tier=%s calls=%d candidates=%v", dec.Tier, calls, cands)
	}
}

func TestSmartNoCapableModelSkipsDispatcher(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.FindModel("m3").MaxTokens = 1000
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierBasic, nil
	})
	req := &protocol.CanonicalRequest{Messages: []protocol.Message{{
		Role: "user", Parts: []protocol.Part{
			{Type: protocol.PartText, Text: strings.Repeat("x", 30000)},
			{Type: protocol.PartImage, ImageURL: "https://example.test/image.png"},
		},
	}}}
	_, _, err := rt.Route(context.Background(), "smart", req, "")
	if !errors.Is(err, ErrNoModel) || calls != 0 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestSmartLongContextEscalatesBeforeDispatch(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Groups = append(c.Groups, config.Group{
			Name: "long-only", Mode: config.ModeSmart,
			Advanced: []config.GroupMember{{Model: "m3", Priority: 1}},
			Basic:    []config.GroupMember{{Model: "m1", Priority: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierBasic, nil
	})
	cands, dec, err := rt.Route(context.Background(), "long-only", textReq(strings.Repeat("x", 30000)), "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || dec.Tier != config.TierAdvanced || cands[0].Name != "m3" {
		t.Fatalf("tier=%s calls=%d candidates=%v", dec.Tier, calls, cands)
	}
}

func TestSmartFiltersActualContextCapacity(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Groups = append(c.Groups, config.Group{
			Name: "capacity", Mode: config.ModeSmart,
			Advanced: []config.GroupMember{{Model: "m3", Priority: 1}},
			Basic:    []config.GroupMember{{Model: "m1", Priority: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierBasic, nil
	})
	req := textReq(strings.Repeat("x", 4100))
	cands, dec, err := rt.Route(context.Background(), "capacity", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || dec.Tier != config.TierAdvanced || cands[0].Name != "m3" {
		t.Fatalf("tier=%s calls=%d candidates=%v", dec.Tier, calls, cands)
	}

	_, _, err = rt.Route(context.Background(), "m1", req, "")
	if err == nil || !strings.Contains(err.Error(), "context limit") {
		t.Fatalf("direct capacity error = %v", err)
	}

	if err := cfg.Mutate(func(c *config.Config) error {
		c.FindModel("m1").MaxTokens = 0
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.Route(context.Background(), "m1", req, ""); err != nil {
		t.Fatalf("zero max_context_tokens should not add a limit: %v", err)
	}
}

func TestSmartForcedTierBoundaries(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Groups = append(c.Groups, config.Group{
			Name: "forced", Mode: config.ModeSmart,
			Basic: []config.GroupMember{{Model: "m1", Priority: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		calls++
		return config.TierBasic, nil
	})
	_, _, err := rt.Route(context.Background(), "forced", textReq("hello"), config.TierAdvanced)
	if !errors.Is(err, ErrNoModel) || calls != 0 {
		t.Fatalf("forced advanced error=%v calls=%d", err, calls)
	}

	req := &protocol.CanonicalRequest{Messages: []protocol.Message{{
		Role: "user", Parts: []protocol.Part{{Type: protocol.PartImage, ImageURL: "https://example.test/image.png"}},
	}}}
	cands, dec, err := rt.Route(context.Background(), "smart", req, config.TierBasic)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 || dec.Tier != config.TierAdvanced || !dec.Forced || cands[0].Name != "m3" {
		t.Fatalf("forced basic escalation: decision=%+v calls=%d candidates=%v", dec, calls, cands)
	}
}

func TestSmartTierRoundRobinIsIndependent(t *testing.T) {
	cfg := testConfig(t)
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Models = append(c.Models, config.Model{
			Name: "m5", Provider: "p1", UpstreamName: "u5", Multimodal: true, LongContext: true, MaxTokens: 1000000,
		})
		c.Groups = append(c.Groups, config.Group{
			Name: "tiered", Mode: config.ModeSmart,
			Advanced: []config.GroupMember{{Model: "m3", Priority: 1}, {Model: "m5", Priority: 1}},
			Basic:    []config.GroupMember{{Model: "m1", Priority: 1}, {Model: "m2", Priority: 1}},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tier := config.TierBasic
	rt := New(cfg, func(context.Context, *config.Group, *protocol.CanonicalRequest) (string, error) {
		return tier, nil
	})
	for _, test := range []struct {
		tier string
		want string
	}{
		{config.TierBasic, "m1"},
		{config.TierAdvanced, "m3"},
		{config.TierBasic, "m2"},
		{config.TierAdvanced, "m5"},
	} {
		tier = test.tier
		cands, _, err := rt.Route(context.Background(), "tiered", textReq("hello"), "")
		if err != nil {
			t.Fatal(err)
		}
		if len(cands) != 2 || cands[0].Name != test.want {
			t.Fatalf("tier=%s first candidate=%v, want %s", test.tier, cands, test.want)
		}
	}
}
