package protocol

import (
	"net/http"
	"strings"

	"github.com/viberouter/viberouter/internal/config"
)

// Detect determines the client protocol from the request path.
func Detect(r *http.Request) string {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/chat/completions"):
		return config.ProtocolChatCompletions
	case strings.HasSuffix(p, "/responses"):
		return config.ProtocolResponses
	case strings.HasSuffix(p, "/messages"):
		return config.ProtocolAnthropic
	}
	return ""
}

// EndpointFor returns the upstream request path for a protocol.
func EndpointFor(proto string) string {
	switch proto {
	case config.ProtocolChatCompletions:
		return "/v1/chat/completions"
	case config.ProtocolResponses:
		return "/v1/responses"
	case config.ProtocolAnthropic:
		return "/v1/messages"
	}
	return ""
}
