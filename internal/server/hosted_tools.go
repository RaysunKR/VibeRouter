package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/protocol"
	"github.com/viberouter/viberouter/internal/upstream"
	"github.com/viberouter/viberouter/internal/webtools"
)

type hostedRun struct {
	status      int
	contentType string
	body        []byte
	response    *protocol.CanonicalResponse
	executions  []protocol.HostedExecution
	retry       bool
	err         error
}

func (s *Server) handleHostedProxy(w http.ResponseWriter, r *http.Request, clientProto string, canonical *protocol.CanonicalRequest, rawBody []byte, candidates []*config.ResolvedModel, entry *logger.Entry) {
	requested := make(map[string]bool)
	for _, tool := range canonical.HostedTools {
		requested[tool.Kind] = true
	}
	for _, tool := range canonical.Tools {
		if requested[tool.Name] {
			entry.Status = http.StatusBadRequest
			writeError(w, http.StatusBadRequest, "hosted tool name conflicts with a client function: "+tool.Name)
			return
		}
	}
	var definitions []webtools.ToolDefinition
	for _, definition := range s.webTools.Definitions() {
		if requested[definition.Name] {
			definitions = append(definitions, definition)
		}
	}
	if len(definitions) != len(requested) {
		entry.Status = http.StatusBadRequest
		writeError(w, http.StatusBadRequest, "a requested web tool has no enabled provider")
		return
	}

	var lastErr error
	for _, candidate := range candidates {
		entry.Attempts++
		entry.RouteModel = candidate.Name
		entry.Upstream = candidate.UpstreamName
		entry.Provider = candidate.Provider
		run := s.runHostedCandidate(r, clientProto, candidate, canonical, rawBody, definitions)
		if run.retry {
			lastErr = run.err
			continue
		}
		if run.err != nil {
			entry.Status = run.status
			entry.Error = run.err.Error()
			writeError(w, run.status, run.err.Error())
			return
		}
		if run.status != http.StatusOK {
			entry.Status = run.status
			relayRaw(w, run.status, run.contentType, run.body)
			return
		}

		entry.Status = http.StatusOK
		entry.InputTokens = run.response.Usage.InputTokens
		entry.OutputTokens = run.response.Usage.OutputTokens
		body := run.body
		var err error
		if clientProto != candidate.EffectiveProtocol {
			body, err = writeByProtocol(clientProto, run.response, candidate.Name)
			if err == nil && len(run.executions) > 0 {
				body, err = protocol.RenderHostedResponse(clientProto, body, run.executions)
			}
		} else if len(run.executions) > 0 {
			body, err = protocol.RenderHostedResponse(clientProto, body, run.executions)
		}
		if err != nil {
			entry.Status = http.StatusInternalServerError
			entry.Error = err.Error()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if canonical.Stream {
			if err := protocol.WriteHostedStream(w, clientProto, body); err != nil {
				entry.Error = err.Error()
			}
		} else {
			relayRaw(w, http.StatusOK, "application/json", body)
		}
		return
	}
	entry.Status = http.StatusBadGateway
	if lastErr == nil {
		lastErr = errors.New("no upstream available")
	}
	entry.Error = lastErr.Error()
	writeError(w, http.StatusBadGateway, "all upstreams failed: "+lastErr.Error())
}

func (s *Server) runHostedCandidate(r *http.Request, clientProto string, candidate *config.ResolvedModel, canonical *protocol.CanonicalRequest, rawBody []byte, definitions []webtools.ToolDefinition) hostedRun {
	body, err := buildHostedInitialBody(clientProto, candidate, canonical, rawBody, definitions)
	if err != nil {
		return hostedRun{retry: true, err: err}
	}
	settings := s.cfg.Snapshot().WebTools
	maxCalls := settings.MaxCalls
	if maxCalls <= 0 {
		maxCalls = 8
	}
	counts := make(map[string]int)
	var executions []protocol.HostedExecution

	for {
		resp, err := upstream.Do(r.Context(), candidate, body, false)
		if err != nil {
			return hostedRun{retry: true, err: err}
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return hostedRun{retry: true, err: readErr}
		}
		if resp.StatusCode != http.StatusOK {
			if upstream.ShouldFailOver(resp.StatusCode) {
				return hostedRun{retry: true, err: fmt.Errorf("upstream %d: %s", resp.StatusCode, truncate(string(responseBody), 200))}
			}
			return hostedRun{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: responseBody}
		}
		parsed, err := upstream.ParseResponse(candidate.EffectiveProtocol, responseBody)
		if err != nil {
			return hostedRun{retry: true, err: err}
		}

		var webCalls []protocol.Part
		otherCalls := 0
		for _, part := range parsed.Parts {
			if part.Type != protocol.PartToolCall {
				continue
			}
			if part.ToolName == protocol.HostedWebSearch || part.ToolName == protocol.HostedWebFetch {
				webCalls = append(webCalls, part)
			} else {
				otherCalls++
			}
		}
		if len(webCalls) == 0 {
			return hostedRun{status: http.StatusOK, body: responseBody, response: parsed, executions: executions}
		}
		if otherCalls > 0 {
			return hostedRun{status: http.StatusBadRequest, err: errors.New("cannot mix hosted web calls and client tool calls in one upstream response")}
		}
		if len(executions)+len(webCalls) > maxCalls {
			return hostedRun{status: http.StatusBadRequest, err: fmt.Errorf("hosted web tool call limit exceeded (%d)", maxCalls)}
		}

		results := make([]protocol.Part, 0, len(webCalls))
		for _, call := range webCalls {
			counts[call.ToolName]++
			result, execution := s.executeHostedCall(r, canonical.HostedTools, call, counts[call.ToolName])
			results = append(results, result)
			executions = append(executions, execution)
		}
		body, err = protocol.AppendToolExchange(candidate.EffectiveProtocol, body, parsed, results)
		if err != nil {
			return hostedRun{retry: true, err: err}
		}
	}
}

func (s *Server) executeHostedCall(r *http.Request, tools []protocol.HostedTool, call protocol.Part, useCount int) (protocol.Part, protocol.HostedExecution) {
	if len(call.ToolInput) == 0 {
		call.ToolInput = json.RawMessage(`{}`)
	}
	execution := protocol.HostedExecution{Kind: call.ToolName, CallID: call.ToolCallID, Input: call.ToolInput}
	var result any
	var err error
	for _, tool := range tools {
		if tool.Kind == call.ToolName && tool.MaxUses > 0 && useCount > tool.MaxUses {
			err = errors.New("max_uses_exceeded")
			break
		}
	}
	if err == nil {
		switch call.ToolName {
		case protocol.HostedWebSearch:
			var arguments struct {
				Query          string `json:"query"`
				Count          int    `json:"count"`
				IncludeContent bool   `json:"include_content"`
			}
			if err = json.Unmarshal(call.ToolInput, &arguments); err == nil {
				request := webtools.SearchRequest{Query: arguments.Query, Count: arguments.Count, IncludeContent: arguments.IncludeContent}
				request.AllowedDomains, request.BlockedDomains = hostedDomains(tools, call.ToolName)
				result, err = s.webTools.Search(r.Context(), request)
			}
		case protocol.HostedWebFetch:
			var arguments struct {
				URL string `json:"url"`
			}
			if err = json.Unmarshal(call.ToolInput, &arguments); err == nil {
				request := webtools.FetchRequest{URL: arguments.URL}
				request.AllowedDomains, request.BlockedDomains = hostedDomains(tools, call.ToolName)
				result, err = s.webTools.Fetch(r.Context(), request)
			}
		default:
			err = errors.New("unsupported hosted tool")
		}
	}
	if err != nil {
		execution.Error = err.Error()
		result = map[string]string{"error": err.Error()}
	}
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		encoded = []byte(`{"error":"failed to encode tool result"}`)
		execution.Error = marshalErr.Error()
	}
	execution.Result = encoded
	return protocol.Part{Type: protocol.PartToolResult, ToolResultID: call.ToolCallID, ToolResultContent: string(encoded)}, execution
}

func hostedDomains(tools []protocol.HostedTool, kind string) ([]string, []string) {
	for _, tool := range tools {
		if tool.Kind == kind {
			return tool.AllowedDomains, tool.BlockedDomains
		}
	}
	return nil, nil
}

func buildHostedInitialBody(clientProto string, candidate *config.ResolvedModel, canonical *protocol.CanonicalRequest, rawBody []byte, definitions []webtools.ToolDefinition) ([]byte, error) {
	functions := make([]protocol.Tool, 0, len(definitions))
	for _, definition := range definitions {
		schema, err := json.Marshal(definition.InputSchema)
		if err != nil {
			return nil, err
		}
		functions = append(functions, protocol.Tool{Name: definition.Name, Description: definition.Description, InputSchema: schema})
	}
	request := *canonical
	request.HostedTools = nil
	request.Stream = false
	request.Tools = append(append([]protocol.Tool{}, canonical.Tools...), functions...)
	if clientProto != candidate.EffectiveProtocol || canonical.HostedHistory {
		return upstream.BuildRequest(candidate.EffectiveProtocol, &request, candidate.UpstreamName)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, err
	}
	var existing []json.RawMessage
	if tools := body["tools"]; len(tools) > 0 {
		if err := json.Unmarshal(tools, &existing); err != nil {
			return nil, err
		}
	}
	kept := existing[:0]
	for _, raw := range existing {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) == nil && protocol.HostedWebToolKind(header.Type) != "" {
			continue
		}
		kept = append(kept, raw)
	}
	for _, function := range functions {
		var raw json.RawMessage
		var err error
		if candidate.EffectiveProtocol == config.ProtocolAnthropic {
			raw, err = json.Marshal(map[string]any{"name": function.Name, "description": function.Description, "input_schema": json.RawMessage(protocolSchema(function.InputSchema))})
		} else {
			raw, err = json.Marshal(map[string]any{"type": "function", "name": function.Name, "description": function.Description, "parameters": json.RawMessage(protocolSchema(function.InputSchema))})
		}
		if err != nil {
			return nil, err
		}
		kept = append(kept, raw)
	}
	encodedTools, err := json.Marshal(kept)
	if err != nil {
		return nil, err
	}
	body["tools"] = encodedTools
	body["stream"] = json.RawMessage("false")
	if choice := body["tool_choice"]; len(choice) > 0 {
		var selected struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(choice, &selected) == nil && protocol.HostedWebToolKind(selected.Type) != "" {
			if clientProto == config.ProtocolAnthropic {
				body["tool_choice"] = json.RawMessage(`{"type":"auto"}`)
			} else {
				body["tool_choice"] = json.RawMessage(`"auto"`)
			}
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return protocol.RewriteModel(encoded, candidate.UpstreamName)
}

func protocolSchema(schema json.RawMessage) []byte {
	if len(schema) == 0 {
		return []byte(`{"type":"object"}`)
	}
	return schema
}
