package service

import (
	"crypto/tls"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"viberouter/internal/config"
	"viberouter/internal/model"
)

// CircuitBreaker manages circuit breaker state per backend model key.
type CircuitBreaker struct {
	mu           sync.RWMutex
	failureCount int
	state        model.CircuitState
	lastFailure  time.Time
	threshold    int
	timeoutSec   int
}

func NewCircuitBreaker(threshold, timeoutSec int) *CircuitBreaker {
	return &CircuitBreaker{
		state:      model.CircuitClosed,
		threshold:  threshold,
		timeoutSec: timeoutSec,
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount = 0
	cb.state = model.CircuitClosed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	if cb.state == model.CircuitHalfOpen {
		cb.state = model.CircuitOpen
		cb.lastFailure = now
		return
	}
	cb.failureCount++
	cb.lastFailure = now
	if cb.failureCount >= cb.threshold {
		cb.state = model.CircuitOpen
	}
}

// IsOpen returns true when the circuit is open and the cool-down has not
// elapsed. It transitions open -> half_open once the cool-down passes.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state != model.CircuitOpen {
		return false
	}
	if time.Since(cb.lastFailure) >= time.Duration(cb.timeoutSec)*time.Second {
		cb.state = model.CircuitHalfOpen
		return false
	}
	return true
}

func (cb *CircuitBreaker) GetState() model.CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// RouteRequest carries the signals extracted from an inbound request that the
// router needs to pick a tier and filter by long context.
type RouteRequest struct {
	ModelName      string
	Header         http.Header
	MessageTurns   int
	EstInputTokens int
	HasTools       bool
	HasCode        bool
}

// RouteInfo describes the routing decision made for a request (for logging).
type RouteInfo struct {
	Tier          model.Tier
	IsLongContext bool
	Direct        bool // client named a concrete model
}

// LoadBalancer selects backend models based on tier, long-context capability
// and priority, and tracks circuit-breaker state per model.
type LoadBalancer struct {
	mu         sync.RWMutex
	cfg        *config.Config
	breakers   map[string]*CircuitBreaker
	httpClient *http.Client
	rrCounters sync.Map // group key -> *int64 (round-robin within a priority group)
}

var (
	lbInstance *LoadBalancer
	lbOnce     sync.Once
)

func InitLoadBalancer(cfg *config.Config) {
	lbOnce.Do(func() {
		lbInstance = &LoadBalancer{
			cfg:      cfg,
			breakers: make(map[string]*CircuitBreaker),
			httpClient: &http.Client{
				Timeout: time.Duration(cfg.Retry.TimeoutMs) * time.Millisecond,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			},
		}
	})
}

func GetLoadBalancer() *LoadBalancer { return lbInstance }

// Reload swaps in a new config so web-UI edits take effect without restart.
func (lb *LoadBalancer) Reload(cfg *config.Config) {
	lb.mu.Lock()
	lb.cfg = cfg
	if lb.httpClient != nil && cfg != nil {
		lb.httpClient.Timeout = time.Duration(cfg.Retry.TimeoutMs) * time.Millisecond
	}
	lb.mu.Unlock()
}

func (lb *LoadBalancer) getCfg() *config.Config {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return lb.cfg
}

func (lb *LoadBalancer) GetBreaker(key string) *CircuitBreaker {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	cb, ok := lb.breakers[key]
	if !ok {
		threshold, timeout := 5, 30
		if lb.cfg != nil {
			threshold = lb.cfg.CircuitBreaker.Threshold
			timeout = lb.cfg.CircuitBreaker.TimeoutSec
		}
		cb = NewCircuitBreaker(threshold, timeout)
		lb.breakers[key] = cb
	}
	return cb
}

func (lb *LoadBalancer) RecordSuccess(key string) { lb.GetBreaker(key).RecordSuccess() }
func (lb *LoadBalancer) RecordFailure(key string) { lb.GetBreaker(key).RecordFailure() }

// flatten returns every configured model with its Tier and Key filled in.
func flatten(tiers map[string]config.TierConfig) []model.BackendModel {
	out := make([]model.BackendModel, 0, 16)
	for tierName, tc := range tiers {
		for _, m := range tc.Models {
			m.Tier = model.Tier(tierName)
			if m.Key == "" {
				m.Key = fmt.Sprintf("%s:%s", tierName, m.Name)
			}
			if m.Priority == 0 {
				m.Priority = 1
			}
			out = append(out, m)
		}
	}
	return out
}

// GetAllModels returns every configured model (any tier, enabled or not).
func (lb *LoadBalancer) GetAllModels() []model.BackendModel {
	cfg := lb.getCfg()
	if cfg == nil {
		return nil
	}
	return flatten(cfg.Tiers)
}

// FindModel looks up a model by technical name or display name across tiers.
func (lb *LoadBalancer) FindModel(name string) (model.BackendModel, bool) {
	for _, m := range lb.GetAllModels() {
		if m.TechnicalName == name || m.Name == name {
			return m, true
		}
	}
	return model.BackendModel{}, false
}

// Route resolves the ordered failover list for a request.
// Order: complexity -> tier, then long-context capability filter, then
// priority (ascending); within a priority group the load-balance strategy
// picks the start point (round-robin / random). The first usable, non-open
// model is first; lower-priority models follow as failover candidates.
func (lb *LoadBalancer) Route(req RouteRequest) ([]model.BackendModel, RouteInfo, error) {
	cfg := lb.getCfg()
	if cfg == nil {
		return nil, RouteInfo{}, fmt.Errorf("no config loaded")
	}
	all := flatten(cfg.Tiers)

	info := RouteInfo{}

	// 1. Direct: client named a concrete configured model.
	if req.ModelName != "" && req.ModelName != "auto" && !isAlias(cfg, req.ModelName) {
		for _, m := range all {
			if (m.TechnicalName == req.ModelName || m.Name == req.ModelName) && m.Enabled {
				info.Direct = true
				info.Tier = m.Tier
				info.IsLongContext = req.EstInputTokens > cfg.Routing.LongContextThreshold && m.LongContext
				if lb.GetBreaker(m.Key).IsOpen() {
					return nil, info, fmt.Errorf("requested model %s circuit is open", req.ModelName)
				}
				return []model.BackendModel{m}, info, nil
			}
		}
	}

	// 2. Resolve tier (override > complexity rules > default).
	info.IsLongContext = req.EstInputTokens > cfg.Routing.LongContextThreshold
	tier := resolveTier(cfg, req)

	// 3. Collect enabled models in the chosen tier.
	pool := make([]model.BackendModel, 0)
	for _, m := range all {
		if m.Tier == tier && m.Enabled {
			pool = append(pool, m)
		}
	}

	// 4. Long-context capability filter.
	if info.IsLongContext {
		pool = filterLongContext(pool, req.EstInputTokens)
		if len(pool) == 0 {
			// Escalate to advanced tier's long-context models.
			log.Printf("[LB] no long-context model in tier %s, escalating to advanced", tier)
			for _, m := range all {
				if m.Tier == model.TierAdvanced && m.Enabled && m.LongContext &&
					(m.MaxContextTokens == 0 || m.MaxContextTokens >= req.EstInputTokens) {
					pool = append(pool, m)
				}
			}
			if len(pool) == 0 {
				return nil, info, fmt.Errorf("no long-context model available for %d tokens", req.EstInputTokens)
			}
			tier = model.TierAdvanced
		}
	}
	info.Tier = tier

	if len(pool) == 0 {
		return nil, info, fmt.Errorf("no models configured in tier %s", tier)
	}

	// 5. Drop circuit-open models.
	available := make([]model.BackendModel, 0, len(pool))
	for _, m := range pool {
		if !lb.GetBreaker(m.Key).IsOpen() {
			available = append(available, m)
		}
	}
	if len(available) == 0 {
		return nil, info, fmt.Errorf("all models in tier %s are circuit-open", tier)
	}

	// 6. Order by priority, with strategy-based tie-break inside each group.
	ordered := orderByPriority(available, cfg.LoadBalance.Strategy, &lb.rrCounters)
	return ordered, info, nil
}

func isAlias(cfg *config.Config, name string) bool {
	_, ok := cfg.Routing.Override.ModelAlias[name]
	return ok
}

func resolveTier(cfg *config.Config, req RouteRequest) model.Tier {
	// Explicit header override.
	if hdr := cfg.Routing.Override.Header; hdr != "" {
		if v := strings.TrimSpace(req.Header.Get(hdr)); v != "" {
			if t := model.Tier(v); t.Valid() {
				return t
			}
		}
	}
	// Model alias override (auto-advanced / auto-basic).
	if t, ok := cfg.Routing.Override.ModelAlias[req.ModelName]; ok {
		if tier := model.Tier(t); tier.Valid() {
			return tier
		}
	}
	// Complexity heuristic.
	if matchesComplexity(cfg, req) {
		return model.TierAdvanced
	}
	return model.Tier(cfg.Routing.Complexity.DefaultTier)
}

func matchesComplexity(cfg *config.Config, req RouteRequest) bool {
	for _, rule := range cfg.Routing.Complexity.Rules {
		if ruleMatches(rule, req) {
			return true
		}
	}
	return false
}

func ruleMatches(rule config.ComplexityRule, req RouteRequest) bool {
	var fieldVal int
	switch rule.Field {
	case "message_turns":
		fieldVal = req.MessageTurns
	case "est_input_tokens", "prompt_length":
		fieldVal = req.EstInputTokens
	case "has_tools":
		if req.HasTools {
			fieldVal = 1
		}
	case "has_code":
		if req.HasCode {
			fieldVal = 1
		}
	default:
		return false
	}
	switch rule.Op {
	case "gte":
		return fieldVal >= rule.Value
	case "gt":
		return fieldVal > rule.Value
	case "eq":
		return fieldVal == rule.Value
	default:
		return false
	}
}

func filterLongContext(pool []model.BackendModel, estTokens int) []model.BackendModel {
	out := make([]model.BackendModel, 0, len(pool))
	for _, m := range pool {
		if m.LongContext && (m.MaxContextTokens == 0 || m.MaxContextTokens >= estTokens) {
			out = append(out, m)
		}
	}
	return out
}

// orderByPriority sorts models by priority ascending and, within each priority
// group, rotates the start index (round-robin) or shuffles (random) so traffic
// spreads across same-priority models. The result is the failover order.
func orderByPriority(models []model.BackendModel, strategy string, counters *sync.Map) []model.BackendModel {
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Priority < models[j].Priority
	})

	out := make([]model.BackendModel, 0, len(models))
	i := 0
	for i < len(models) {
		j := i + 1
		for j < len(models) && models[j].Priority == models[i].Priority {
			j++
		}
		group := models[i:j]
		out = append(out, orderGroup(group, strategy, counters)...)
		i = j
	}
	return out
}

func orderGroup(group []model.BackendModel, strategy string, counters *sync.Map) []model.BackendModel {
	if len(group) == 1 {
		return group
	}
	switch strategy {
	case "random":
		// Deterministic-enough shuffle using time entropy; keeps failover intact.
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		shuffled := make([]model.BackendModel, len(group))
		copy(shuffled, group)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		return shuffled
	default: // round_robin / weighted_round_robin
		key := fmt.Sprintf("%s:%d:%d", group[0].Tier, group[0].Priority, len(group))
		var counter *int64
		if v, ok := counters.Load(key); ok {
			counter = v.(*int64)
		} else {
			counter = new(int64)
			actual, _ := counters.LoadOrStore(key, counter)
			counter = actual.(*int64)
		}
		start := int(atomic.AddInt64(counter, 1)-1) % len(group)
		if start < 0 {
			start += len(group)
		}
		rotated := make([]model.BackendModel, len(group))
		for k := range group {
			rotated[k] = group[(start+k)%len(group)]
		}
		return rotated
	}
}

// HTTPClient exposes the shared upstream client used by handlers.
func (lb *LoadBalancer) HTTPClient() *http.Client { return lb.httpClient }

// ProxyResponse is retained for compatibility with any external callers.
type ProxyResponse struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}
