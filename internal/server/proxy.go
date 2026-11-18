package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/protocol"
	"github.com/viberouter/viberouter/internal/upstream"
)

// handleProxy serves POST /v1/chat/completions, /v1/responses, /v1/messages.
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	clientProto := protocol.Detect(r)
	if clientProto == "" {
		writeError(w, http.StatusNotFound, "unknown endpoint")
		return
	}
	key := extractKey(r)
	keyName, ok := s.authorizeKey(key)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body failed")
		return
	}

	// Parse into canonical form (needed for routing filters and conversion).
	canonical, err := parseByProtocol(clientProto, rawBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	requestModel := canonical.Model

	entry := logger.Entry{
		KeyName:      keyName,
		ClientProto:  clientProto,
		RequestModel: requestModel,
		Stream:       canonical.Stream,
	}
	defer func() {
		entry.LatencyMs = time.Since(start).Milliseconds()
		s.log.Log(entry)
	}()

	forcedTier := r.Header.Get("X-VibeRouter-Tier")
	candidates, dec, err := s.router.Route(r.Context(), requestModel, canonical, forcedTier)
	if err != nil {
		entry.Error = err.Error()
		entry.Status = http.StatusBadGateway
		if strings.Contains(err.Error(), "no model") {
			writeError(w, http.StatusNotFound, err.Error())
			entry.Status = http.StatusNotFound
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	if dec != nil {
		entry.Mode, entry.Group, entry.Tier, entry.Forced, entry.DispErr = dec.Mode, dec.Group, dec.Tier, dec.Forced, dec.DispErr
	}
	if len(canonical.HostedTools) > 0 && s.cfg.Snapshot().WebTools.Enabled {
		s.handleHostedProxy(w, r, clientProto, canonical, rawBody, candidates, &entry)
		return
	}

	var lastErr string
	for _, cand := range candidates {
		entry.Attempts++
		entry.RouteModel = cand.Name
		entry.Upstream = cand.UpstreamName
		entry.Provider = cand.Provider

		upBody, err := s.buildUpstreamBody(clientProto, cand, canonical, rawBody)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		resp, err := upstream.Do(r.Context(), cand, upBody, canonical.Stream)
		if err != nil {
			lastErr = err.Error()
			continue // network error → fail over
		}
		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if upstream.ShouldFailOver(resp.StatusCode) {
				lastErr = "upstream " + itoa(resp.StatusCode) + ": " + truncate(string(errBody), 200)
				continue
			}
			// Client error (4xx): relay verbatim.
			entry.Status = resp.StatusCode
			entry.Error = truncate(string(errBody), 500)
			relayRaw(w, resp.StatusCode, resp.Header.Get("Content-Type"), errBody)
			return
		}

		entry.Status = http.StatusOK
		if canonical.Stream {
			s.relayStream(w, r, clientProto, cand, resp, &entry)
		} else {
			s.relayUnary(w, clientProto, cand, resp, &entry)
		}
		return
	}

	entry.Status = http.StatusBadGateway
	entry.Error = lastErr
	writeError(w, http.StatusBadGateway, "all upstreams failed: "+lastErr)
}

func (s *Server) buildUpstreamBody(clientProto string, cand *config.ResolvedModel, canonical *protocol.CanonicalRequest, rawBody []byte) ([]byte, error) {
	if cand.EffectiveProtocol == clientProto {
		// same protocol: passthrough, only swap the model name
		return protocol.RewriteModel(rawBody, cand.UpstreamName)
	}
	return upstream.BuildRequest(cand.EffectiveProtocol, canonical, cand.UpstreamName)
}

// relayUnary handles non-streaming responses.
func (s *Server) relayUnary(w http.ResponseWriter, clientProto string, cand *config.ResolvedModel, resp *http.Response, entry *logger.Entry) {
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		entry.Error = err.Error()
		entry.Status = http.StatusBadGateway
		writeError(w, http.StatusBadGateway, "read upstream response failed")
		return
	}
	if cand.EffectiveProtocol == clientProto {
		// best-effort usage extraction for logs
		if cr, err := upstream.ParseResponse(clientProto, body); err == nil {
			entry.InputTokens = cr.Usage.InputTokens
			entry.OutputTokens = cr.Usage.OutputTokens
		}
		relayRaw(w, http.StatusOK, "application/json", body)
		return
	}
	cr, err := upstream.ParseResponse(cand.EffectiveProtocol, body)
	if err != nil {
		entry.Error = "convert response: " + err.Error()
		entry.Status = http.StatusBadGateway
		writeError(w, http.StatusBadGateway, entry.Error)
		return
	}
	entry.InputTokens = cr.Usage.InputTokens
	entry.OutputTokens = cr.Usage.OutputTokens
	out, err := writeByProtocol(clientProto, cr, cand.Name)
	if err != nil {
		entry.Error = err.Error()
		entry.Status = http.StatusBadGateway
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	relayRaw(w, http.StatusOK, "application/json", out)
}

// relayStream handles SSE responses, converting when protocols differ.
func (s *Server) relayStream(w http.ResponseWriter, r *http.Request, clientProto string, cand *config.ResolvedModel, resp *http.Response, entry *logger.Entry) {
	defer func() { _ = resp.Body.Close() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	if cand.EffectiveProtocol == clientProto {
		copyFlush(w, resp.Body)
		return
	}
	events := parseStreamByProtocol(cand.EffectiveProtocol, resp.Body)
	// capture usage from the done event for logging
	tapped := make(chan protocol.StreamEvent, 64)
	go func() {
		defer close(tapped)
		for ev := range events {
			if ev.Type == protocol.EvDone {
				entry.InputTokens = ev.Usage.InputTokens
				entry.OutputTokens = ev.Usage.OutputTokens
			}
			tapped <- ev
		}
	}()
	var werr error
	switch clientProto {
	case config.ProtocolChatCompletions:
		werr = protocol.WriteChatStream(w, tapped, cand.Name)
	case config.ProtocolResponses:
		werr = protocol.WriteResponsesStream(w, tapped, cand.Name)
	case config.ProtocolAnthropic:
		werr = protocol.WriteAnthropicStream(w, tapped, cand.Name)
	}
	if werr != nil {
		entry.Error = "stream write: " + werr.Error()
		// drain remaining events so the parser goroutine can exit
		go func() {
			for range tapped {
			}
		}()
	}
}

func copyFlush(w io.Writer, r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// --- helpers ---

func parseByProtocol(proto string, body []byte) (*protocol.CanonicalRequest, error) {
	switch proto {
	case config.ProtocolChatCompletions:
		return protocol.ParseChatRequest(body)
	case config.ProtocolResponses:
		return protocol.ParseResponsesRequest(body)
	case config.ProtocolAnthropic:
		return protocol.ParseAnthropicRequest(body)
	}
	return nil, http.ErrNotSupported
}

func writeByProtocol(proto string, r *protocol.CanonicalResponse, model string) ([]byte, error) {
	switch proto {
	case config.ProtocolChatCompletions:
		return protocol.WriteChatResponse(r, model)
	case config.ProtocolResponses:
		return protocol.WriteResponsesResponse(r, model)
	case config.ProtocolAnthropic:
		return protocol.WriteAnthropicResponse(r, model)
	}
	return nil, http.ErrNotSupported
}

func parseStreamByProtocol(proto string, r io.Reader) <-chan protocol.StreamEvent {
	switch proto {
	case config.ProtocolChatCompletions:
		return protocol.ParseChatStream(r)
	case config.ProtocolResponses:
		return protocol.ParseResponsesStream(r)
	case config.ProtocolAnthropic:
		return protocol.ParseAnthropicStream(r)
	}
	ch := make(chan protocol.StreamEvent, 1)
	close(ch)
	return ch
}

func extractKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return k
	}
	return ""
}

func relayRaw(w http.ResponseWriter, status int, contentType string, body []byte) {
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": msg, "type": "viberouter_error"},
	})
	relayRaw(w, status, "application/json", body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
