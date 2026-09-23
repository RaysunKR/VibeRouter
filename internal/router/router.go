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

// Route returns the ordered candidate list to try (failover order) plus a
// decision record. forcedTier may be "" | "advanced" | "basic" (header override).
func (rt *Router) Route(ctx context.Context, modelName string, req *protocol.CanonicalRequest, forcedTier string) ([]*config.ResolvedModel, *Decision, error) {
	snap := rt.cfg.Snapshot()
	dec := &Decision{}

	// 1. direct model
	if m := snap.FindModel(modelName); m != nil {
		r, err := snap.ResolveModel(m)
		if err != nil {
			return nil, nil, err
		}
		if err := checkCaps(r, req, &snap); err != nil {
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

	var members []config.GroupMember
	switch g.Mode {
	case config.ModeBalance:
		dec.Mode = "balance"
		members = g.Members
	case config.ModeSmart:
		dec.Mode = "smart"
		tier := forcedTier
		dec.Forced = forcedTier != ""
		if tier == "" {
			var err error
			tier, err = rt.classifier(ctx, g, req)
			if err != nil {
				tier = config.TierBasic // degrade to basic on dispatcher failure
				dec.DispErr = err.Error()
			}
		}
		if tier != config.TierAdvanced {
			tier = config.TierBasic
		}
		dec.Tier = tier
		members = g.Basic
		if tier == config.TierAdvanced {
			members = g.Advanced
		}
		// Try the chosen tier first; on capability mismatch escalate to advanced.
		cands := rt.pickMembers(&snap, g, members, req)
		if len(cands) > 0 {
			return cands, dec, nil
		}
		if tier == config.TierBasic {
			cands = rt.pickMembers(&snap, g, g.Advanced, req)
			if len(cands) > 0 {
				dec.Tier = config.TierAdvanced // escalated for capability
				return cands, dec, nil
			}
		}
		return nil, dec, ErrNoModel
	default:
		return nil, nil, errors.New("unknown group mode: " + g.Mode)
	}

	cands := rt.pickMembers(&snap, g, members, req)
	if len(cands) == 0 {
		return nil, dec, ErrNoModel
	}
	return cands, dec, nil
}

// pickMembers filters members by modality/context capability, then orders them
// by priority (lower first), rotating same-priority entries per strategy.
func (rt *Router) pickMembers(snap *config.Config, g *config.Group, members []config.GroupMember, req *protocol.CanonicalRequest) []*config.ResolvedModel {
	multi := req.HasMultimodal()
	long := req.EstimateTokens() > snap.Routing.LongContextThreshold

	type entry struct {
		r   *config.ResolvedModel
		pri int
	}
	var ok []entry
	for _, m := range members {
		model := snap.FindModel(m.Model)
		if model == nil {
			continue
		}
		if multi && !model.Multimodal {
			continue
		}
		if long && !model.LongContext {
			continue
		}
		r, err := snap.ResolveModel(model)
		if err != nil {
			continue
		}
		ok = append(ok, entry{r, m.Priority})
	}
	if len(ok) == 0 {
		return nil
	}
	sort.SliceStable(ok, func(i, j int) bool { return ok[i].pri < ok[j].pri })

	// Rotate within each priority bucket according to the strategy.
	strategy := g.Strategy
	if strategy == "" {
		strategy = snap.Routing.Strategy
	}
	var out []*config.ResolvedModel
	for i := 0; i < len(ok); {
		j := i
		for j < len(ok) && ok[j].pri == ok[i].pri {
			j++
		}
		bucket := ok[i:j]
		if len(bucket) > 1 && strategy == "round_robin" {
			rt.mu.Lock()
			key := g.Name + "|" + itoa(ok[i].pri)
			off := rt.rrCursor[key] % len(bucket)
			rt.rrCursor[key] = rt.rrCursor[key] + 1
			rt.mu.Unlock()
			rotated := append(append([]entry{}, bucket[off:]...), bucket[:off]...)
			bucket = rotated
		}
		for _, e := range bucket {
			out = append(out, e.r)
		}
		i = j
	}
	return out
}

// checkCaps validates capability constraints for direct mode.
func checkCaps(r *config.ResolvedModel, req *protocol.CanonicalRequest, snap *config.Config) error {
	if req.HasMultimodal() && !r.Multimodal {
		return errors.New("model " + r.Name + " does not support multimodal input")
	}
	if req.EstimateTokens() > snap.Routing.LongContextThreshold && !r.LongContext {
		return errors.New("model " + r.Name + " does not support long context")
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
