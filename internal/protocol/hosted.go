package protocol

import (
	"encoding/json"
	"strings"
)

type HostedExecution struct {
	Kind   string
	CallID string
	Input  json.RawMessage
	Result json.RawMessage
	Error  string
}

const (
	HostedWebSearch = "web_search"
	HostedWebFetch  = "web_fetch"
)

func HostedWebToolKind(wireType string) string {
	switch {
	case wireType == HostedWebSearch || strings.HasPrefix(wireType, "web_search_"):
		return HostedWebSearch
	case wireType == HostedWebFetch || strings.HasPrefix(wireType, "web_fetch_"):
		return HostedWebFetch
	default:
		return ""
	}
}
