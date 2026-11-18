// Package router turns a client-facing model name into an ordered list of
// upstream candidates, applying modality/long-context filters, tier
// classification (smart groups), and priority-based failover ordering.
package router

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

var ErrNoModel = errors.New("no model available for this request")

// Classifier judges request complexity for smart groups. Implemented by the
// dispatcher package; returns config.TierAdvanced or config.TierBasic.
type Classifier func(ctx context.Context, group *config.Group, req *protocol.CanonicalRequest) (tier string, err error)

type Router struct {
	cfg        *config.Store
	classifier Classifier

	mu       sync.Mutex
	rrCursor map[string]int // round-robin cursors, key: group|priority
}

func New(cfg *config.Store, classifier Classifier) *Router {
	return &Router{cfg: cfg, classifier: classifier, rrCursor: map[string]int{}}
}

// Decision describes the routing outcome for logging.
type Decision struct {
	Group   string // group name when routed via a group
	Mode    string // direct | balance | smart
	Tier    string // smart mode only
	Forced  bool   // tier forced via header
	DispErr string // dispatcher failure reason when degraded
}

type requestConstraints struct {
	multimodal  bool
	tokens      int
	longContext bool
}

func constraintsFor(req *protocol.CanonicalRequest, snap *config.Config) requestConstraints {
	tokens := req.EstimateTokens()
	return requestConstraints{
		multimodal:  req.HasMultimodal(),
		tokens:      tokens,
		longContext: tokens > snap.Routing.LongContextThreshold,
	}
}

type memberEntry struct {
	model    *config.ResolvedModel
	priority int
}

// Route returns the ordered candidate list to try (failover order) plus a
// decision record. forcedTier may be "" | "advanced" | "basic" (header override).
func (rt *Router) Route(ctx context.Context, modelName string, req *protocol.CanonicalRequest, forcedTier string) ([]*config.ResolvedModel, *Decision, error) {
	snap := rt.cfg.Snapshot()
	constraints := constraintsFor(req, &snap)
	dec := &Decision{}

	// 1. direct model
	if m := snap.FindModel(modelName); m != nil {
		r, err := snap.ResolveModel(m)
		if err != nil {
			return nil, nil, err
		}
		if err := checkCaps(r, constraints); err != nil {
			return nil, nil, err
		}
		dec.Mode = "direct"
		return []*config.ResolvedModel{r}, dec, nil
	}

	// 2. group
	g := snap.FindGroup(modelName)
	if g == nil {
		return nil, nil, ErrNoModel
	}
	dec.Group = g.Name

	switch g.Mode {
	case config.ModeBalance:
		dec.Mode = "balance"
		entries := filterMembers(&snap, g.Members, constraints)
		cands := rt.orderMembers(&snap, g, "balance", entries)
		if len(cands) == 0 {
			return nil, dec, ErrNoModel
		}
		return cands, dec, nil
	case config.ModeSmart:
		dec.Mode = "smart"
		basic := filterMembers(&snap, g.Basic, constraints)
		advanced := filterMembers(&snap, g.Advanced, constraints)
		tier := forcedTier
		dec.Forced = forcedTier != ""

		if tier == "" {
			switch {
			case len(basic) == 0 && len(advanced) == 0:
				return nil, dec, ErrNoModel
			case len(basic) == 0:
				tier = config.TierAdvanced
			case len(advanced) == 0:
				tier = config.TierBasic
			default:
				var err error
				if rt.classifier == nil {
					err = errors.New("smart group has no classifier")
				} else {
					tier, err = rt.classifier(ctx, g, req)
				}
				if err != nil {
					tier = config.TierBasic
					dec.DispErr = err.Error()
				}
			}
		}
		if tier != config.TierAdvanced {
			tier = config.TierBasic
		}
		if tier == config.TierBasic && len(basic) == 0 && len(advanced) > 0 {
			tier = config.TierAdvanced
		}
		dec.Tier = tier

		if tier == config.TierAdvanced {
			if len(advanced) == 0 {
				return nil, dec, ErrNoModel
			}
			return rt.orderMembers(&snap, g, config.TierAdvanced, advanced), dec, nil
		}
		if len(basic) == 0 {
			return nil, dec, ErrNoModel
		}
		return rt.orderMembers(&snap, g, config.TierBasic, basic), dec, nil
	default:
		return nil, nil, errors.New("unknown group mode: " + g.Mode)
	}
}

// filterMembers resolves models that satisfy the request's modality and context
// constraints without changing load-balancing state.
func filterMembers(snap *config.Config, members []config.GroupMember, constraints requestConstraints) []memberEntry {
	var entries []memberEntry
	for _, member := range members {
		model := snap.FindModel(member.Model)
		if model == nil {
			continue
		}
		if constraints.multimodal && !model.Multimodal {
			continue
		}
		if constraints.longContext && !model.LongContext {
			continue
		}
		if model.MaxTokens > 0 && constraints.tokens > model.MaxTokens {
			continue
		}
		resolved, err := snap.ResolveModel(model)
		if err != nil {
			continue
		}
		entries = append(entries, memberEntry{model: resolved, priority: member.Priority})
	}
	return entries
}

// orderMembers sorts by priority and rotates only the selected mode or tier.
func (rt *Router) orderMembers(snap *config.Config, g *config.Group, scope string, entries []memberEntry) []*config.ResolvedModel {
	if len(entries) == 0 {
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].priority < entries[j].priority })

	strategy := g.Strategy
	if strategy == "" {
		strategy = snap.Routing.Strategy
	}
	var out []*config.ResolvedModel
	for i := 0; i < len(entries); {
		j := i
		for j < len(entries) && entries[j].priority == entries[i].priority {
			j++
		}
		bucket := entries[i:j]
		if len(bucket) > 1 && strategy == "round_robin" {
			rt.mu.Lock()
			key := g.Name + "|" + scope + "|" + itoa(entries[i].priority)
			off := rt.rrCursor[key] % len(bucket)
			rt.rrCursor[key] = rt.rrCursor[key] + 1
			rt.mu.Unlock()
			bucket = append(append([]memberEntry{}, bucket[off:]...), bucket[:off]...)
		}
		for _, entry := range bucket {
			out = append(out, entry.model)
		}
		i = j
	}
	return out
}

// checkCaps validates capability constraints for direct mode.
func checkCaps(r *config.ResolvedModel, constraints requestConstraints) error {
	if constraints.multimodal && !r.Multimodal {
		return errors.New("model " + r.Name + " does not support multimodal input")
	}
	if constraints.longContext && !r.LongContext {
		return errors.New("model " + r.Name + " does not support long context")
	}
	if r.MaxTokens > 0 && constraints.tokens > r.MaxTokens {
		return errors.New("request exceeds context limit of model " + r.Name)
	}
	return nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
