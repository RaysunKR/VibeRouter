package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
	"viberouter/internal/config"
	"viberouter/internal/model"

	"github.com/spf13/viper"
)

// CircuitBreaker manages circuit breaker state per backend model
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
		state:     model.CircuitClosed,
		threshold: threshold,
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

func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	if cb.state == model.CircuitOpen {
		elapsed := time.Since(cb.lastFailure)
		if elapsed >= time.Duration(cb.timeoutSec)*time.Second {
			cb.mu.RUnlock()
			cb.mu.Lock()
			cb.state = model.CircuitHalfOpen
			cb.mu.Unlock()
			cb.mu.RLock()
			return false
		}
		return true
	}
	return false
}

func (cb *CircuitBreaker) GetState() model.CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// LoadBalancer holds circuit breakers per model
type LoadBalancer struct {
	mu          sync.RWMutex
	breakers    map[uint]*CircuitBreaker
	cfg         *config.Config
	httpClient  *http.Client
	strategies  map[string]Strategy
}

type Strategy interface {
	Select(models []model.BackendModel) *model.BackendModel
}

type RoundRobinStrategy struct {
	mu      sync.Mutex
	current int
}

func (s *RoundRobinStrategy) Select(models []model.BackendModel) *model.BackendModel {
	if len(models) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.current % len(models)
	s.current++
	return &models[idx]
}

type WeightedRoundRobinStrategy struct {
	mu       sync.Mutex
	current  int64
	weights  map[uint]int
}

func (s *WeightedRoundRobinStrategy) Select(models []model.BackendModel) *model.BackendModel {
	if len(models) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Calculate total weight
	totalWeight := 0
	for _, m := range models {
		w := int(m.Weight)
		if w <= 0 {
			w = 1 // Minimum weight of 1
		}
		totalWeight += w
	}

	// Use atomic counter for position
	pos := s.current % int64(totalWeight)
	s.current++

	// Find the model at this position
	 cumulative := int64(0)
	for i := range models {
		w := int(models[i].Weight)
		if w <= 0 {
			w = 1
		}
		cumulative += int64(w)
		if pos < cumulative {
			return &models[i]
		}
	}

	// Fallback (shouldn't happen)
	return &models[0]
}

type RandomStrategy struct{}

func (s *RandomStrategy) Select(models []model.BackendModel) *model.BackendModel {
	if len(models) == 0 {
		return nil
	}
	idx := time.Now().UnixNano() % int64(len(models))
	return &models[idx]
}

var (
	lbInstance *LoadBalancer
	lbOnce     sync.Once
)

func InitLoadBalancer(cfg *config.Config) {
	lbOnce.Do(func() {
		lbInstance = &LoadBalancer{
			breakers: make(map[uint]*CircuitBreaker),
			cfg:      cfg,
			httpClient: &http.Client{
				Timeout: time.Duration(cfg.Retry.TimeoutMs) * time.Millisecond,
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			},
			strategies: map[string]Strategy{
				"round_robin":        &RoundRobinStrategy{},
				"weighted_round_robin": &WeightedRoundRobinStrategy{},
				"random":             &RandomStrategy{},
			},
		}
	})
}

func GetLoadBalancer() *LoadBalancer {
	return lbInstance
}

// GetBackendModels returns backend models from config
func (lb *LoadBalancer) GetBackendModels(provider model.Provider) []model.BackendModel {
	if lb.cfg == nil {
		return nil
	}
	models := make([]model.BackendModel, 0)
	for i, cm := range lb.cfg.BackendModels {
		if provider != "" && model.Provider(cm.Provider) != provider {
			continue
		}
		m := model.BackendModel{
			ID:            uint(i + 1),
			Provider:      model.Provider(cm.Provider),
			DisplayName:   cm.DisplayName,
			TechnicalName: cm.TechnicalName,
			BaseURL:       cm.BaseURL,
			APIKey:        cm.APIKey,
			Weight:        10,
			IsActive:      cm.IsActive,
			FailureCount:  0,
			CircuitState:  model.CircuitClosed,
		}
		models = append(models, m)
	}
	return models
}

func (lb *LoadBalancer) GetBreaker(modelID uint) *CircuitBreaker {
	lb.mu.RLock()
	cb, exists := lb.breakers[modelID]
	lb.mu.RUnlock()
	if !exists {
		lb.mu.Lock()
		cb = NewCircuitBreaker(lb.cfg.CircuitBreaker.Threshold, lb.cfg.CircuitBreaker.TimeoutSec)
		lb.breakers[modelID] = cb
		lb.mu.Unlock()
	}
	return cb
}

func (lb *LoadBalancer) SelectModel(models []model.BackendModel, strategy string) *model.BackendModel {
	active := make([]model.BackendModel, 0)
	for _, m := range models {
		if !m.IsActive {
			continue
		}
		cb := lb.GetBreaker(m.ID)
		state := cb.GetState()
		log.Printf("[LB] Model ID=%d IsActive=%v CircuitState=%v", m.ID, m.IsActive, state)
		if cb.IsOpen() {
			log.Printf("[LB] Model %d circuit is OPEN, skipping", m.ID)
			continue
		}
		active = append(active, m)
	}

	log.Printf("[LB] Active models count: %d", len(active))
	if len(active) == 0 {
		return nil
	}

	// Sort by ID for consistent weighted round-robin behavior
	for i := 0; i < len(active)-1; i++ {
		for j := i + 1; j < len(active); j++ {
			if active[i].ID > active[j].ID {
				active[i], active[j] = active[j], active[i]
			}
		}
	}

	s, ok := lb.strategies[strategy]
	if !ok {
		s = lb.strategies["round_robin"]
	}
	selected := s.Select(active)
	return selected
}

func (lb *LoadBalancer) RecordSuccess(modelID uint) {
	lb.GetBreaker(modelID).RecordSuccess()
}

func (lb *LoadBalancer) RecordFailure(modelID uint) {
	lb.GetBreaker(modelID).RecordFailure()
}

// ProxyRequest forwards request to selected backend model with retry
func (lb *LoadBalancer) ProxyRequest(ctx context.Context, req *http.Request, style APIStyle, isStreaming bool) (*ProxyResponse, error) {
	maxRetries := lb.cfg.Retry.MaxAttempts
	if isStreaming {
		maxRetries = 1 // Don't retry streaming requests
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Select backend model from config (no DB)
		models := lb.GetBackendModels(model.ProviderOpenAI)

		selected := lb.SelectModel(models, viper.GetString("load_balance.strategy"))
		if selected == nil {
			return nil, fmt.Errorf("no available backend models")
		}

		// Prepare upstream request
		upstreamReq, err := lb.buildUpstreamRequest(req, selected, style)
		if err != nil {
			return nil, err
		}

		// Send request
		if isStreaming {
			return lb.handleStreaming(ctx, upstreamReq, selected.ID, style)
		}
		resp, err := lb.httpClient.Do(upstreamReq)
		if err != nil {
			lastErr = err
			lb.RecordFailure(selected.ID)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("upstream error: %d", resp.StatusCode)
			lb.RecordFailure(selected.ID)
			resp.Body.Close()
			continue
		}

		lb.RecordSuccess(selected.ID)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return &ProxyResponse{
			StatusCode: resp.StatusCode,
			Body:       body,
			Header:     resp.Header,
		}, nil
	}
	return nil, lastErr
}

type ProxyResponse struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

func (lb *LoadBalancer) buildUpstreamRequest(req *http.Request, backend *model.BackendModel, style APIStyle) (*http.Request, error) {
	// Transform request based on style if needed
	body, _ := io.ReadAll(req.Body)
	req.Body.Close()

	upstreamURL := backend.BaseURL + req.URL.Path
	newReq, err := http.NewRequest(req.Method, upstreamURL, io.NopCloser(
		&readerWithContent{content: body, consumed: false},
	))
	if err != nil {
		return nil, err
	}

	newReq.Header = make(http.Header)
	for k, v := range req.Header {
		newReq.Header[k] = v
	}

	// Set auth header based on provider
	switch backend.Provider {
	case model.ProviderOpenAI:
		newReq.Header.Set("Authorization", "Bearer "+backend.APIKey)
	case model.ProviderAnthropic:
		newReq.Header.Set("x-api-key", backend.APIKey)
	}

	return newReq, nil
}

type readerWithContent struct {
	content  []byte
	consumed bool
}

func (r *readerWithContent) Read(p []byte) (n int, err error) {
	if r.consumed {
		return 0, io.EOF
	}
	n = copy(p, r.content)
	r.consumed = true
	return n, nil
}

func (lb *LoadBalancer) handleStreaming(ctx context.Context, req *http.Request, modelID uint, style APIStyle) (*ProxyResponse, error) {
	// For streaming, we upgrade to websocket or use chunked response
	// Simplified: just do regular HTTP streaming via SSE
	resp, err := lb.httpClient.Do(req)
	if err != nil {
		lb.RecordFailure(modelID)
		return nil, err
	}

	if resp.StatusCode >= 500 {
		lb.RecordFailure(modelID)
		resp.Body.Close()
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	lb.RecordSuccess(modelID)
	return &ProxyResponse{
		StatusCode: resp.StatusCode,
		Header:     resp.Header,
		Body:       nil, // Streaming body handled separately
	}, nil
}

// SSE streaming helpers
func StreamSSE(w http.ResponseWriter, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", jsonData)
	return err
}

func StreamSSEEvent(w http.ResponseWriter, event string, data interface{}) error {
	_, err := fmt.Fprintf(w, "event: %s\n", event)
	if err != nil {
		return err
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", jsonData)
	return err
}
