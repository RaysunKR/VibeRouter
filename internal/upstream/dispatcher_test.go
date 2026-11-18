package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/protocol"
)

func TestTruncateRunes(t *testing.T) {
	exact := strings.Repeat("界", 4000)
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{name: "empty", text: "", limit: 4000, want: ""},
		{name: "short mixed text", text: "a界🙂", limit: 4000, want: "a界🙂"},
		{name: "exactly 4000 runes", text: exact, limit: 4000, want: exact},
		{name: "over 4000 runes", text: exact + "🙂", limit: 4000, want: exact},
		{name: "zero limit", text: "界", limit: 0, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := truncateRunes(test.text, test.limit)
			if got != test.want {
				t.Fatalf("truncateRunes() returned %d runes, want %d", utf8.RuneCountInString(got), utf8.RuneCountInString(test.want))
			}
			if !utf8.ValidString(got) {
				t.Fatal("truncateRunes() returned invalid UTF-8")
			}
		})
	}
}

func TestDispatcherBuildsRequestFromTruncatedLastUserText(t *testing.T) {
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s, want /v1/chat/completions", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			http.Error(w, "read failed", http.StatusInternalServerError)
			return
		}
		requests <- body
		writeDispatcherChatResponse(w, `{"tier":"advanced"}`)
	}))
	defer server.Close()

	cfg, group := newDispatcherTestConfig(t, server.URL)
	latest := "latest:" + strings.Repeat("界", 3993) + "🙂"
	originalTemperature := 0.9
	req := &protocol.CanonicalRequest{
		Model:       "client-model",
		System:      "original-system-not-sent",
		Temperature: &originalTemperature,
		MaxTokens:   77,
		Stream:      true,
		Messages: []protocol.Message{
			{Role: "user", Parts: []protocol.Part{{Type: protocol.PartText, Text: "old-user-not-sent"}}},
			{Role: "assistant", Parts: []protocol.Part{{Type: protocol.PartText, Text: "assistant-history-not-sent"}}},
			{Role: "user", Parts: []protocol.Part{
				{Type: protocol.PartText, Text: latest},
				{Type: protocol.PartImage, ImageURL: "data:image/png;base64,image-not-sent"},
			}},
			{Role: "assistant", Parts: []protocol.Part{{Type: protocol.PartText, Text: "trailing-assistant-not-sent"}}},
		},
	}

	tier, err := Dispatcher(cfg)(context.Background(), group, req)
	if err != nil {
		t.Fatalf("Dispatcher() error = %v", err)
	}
	if tier != config.TierAdvanced {
		t.Fatalf("Dispatcher() tier = %q, want advanced", tier)
	}

	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
		Temperature *float64 `json:"temperature"`
		MaxTokens   int      `json:"max_tokens"`
		Stream      bool     `json:"stream"`
	}
	if err := json.Unmarshal(<-requests, &got); err != nil {
		t.Fatalf("decode upstream request: %v", err)
	}
	if got.Model != "dispatcher-upstream" {
		t.Errorf("model = %q, want dispatcher-upstream", got.Model)
	}
	if got.Temperature == nil || *got.Temperature != 0 {
		t.Errorf("temperature = %v, want explicit 0", got.Temperature)
	}
	if got.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", got.MaxTokens)
	}
	if got.Stream {
		t.Error("stream = true, want false")
	}
	if len(got.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want system and one user message", len(got.Messages))
	}
	if got.Messages[0].Role != "system" || got.Messages[1].Role != "user" {
		t.Fatalf("message roles = %q, %q; want system, user", got.Messages[0].Role, got.Messages[1].Role)
	}
	var systemPrompt string
	if err := json.Unmarshal(got.Messages[0].Content, &systemPrompt); err != nil {
		t.Fatalf("decode system content: %v", err)
	}
	if systemPrompt != "test dispatcher prompt" {
		t.Errorf("system prompt = %q, want test dispatcher prompt", systemPrompt)
	}
	var text string
	if err := json.Unmarshal(got.Messages[1].Content, &text); err != nil {
		t.Fatalf("decode user content: %v", err)
	}
	want := strings.TrimSuffix(latest, "🙂")
	if text != want {
		t.Errorf("user text differs: got %d runes, want %d", utf8.RuneCountInString(text), utf8.RuneCountInString(want))
	}
	if utf8.RuneCountInString(text) != 4000 || !utf8.ValidString(text) {
		t.Errorf("user text rune count/UTF-8 = %d/%v, want 4000/true", utf8.RuneCountInString(text), utf8.ValidString(text))
	}
}

func TestDispatcherEmptyTextSkipsUpstream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeDispatcherChatResponse(w, `{"tier":"advanced"}`)
	}))
	defer server.Close()
	cfg, group := newDispatcherTestConfig(t, server.URL)

	for _, text := range []string{"", " \n\t "} {
		req := &protocol.CanonicalRequest{Messages: []protocol.Message{{Role: "user", Parts: []protocol.Part{
			{Type: protocol.PartText, Text: text},
			{Type: protocol.PartImage, ImageURL: "data:image/png;base64,unused"},
		}}}}
		tier, err := Dispatcher(cfg)(context.Background(), group, req)
		if err != nil {
			t.Fatalf("Dispatcher(%q) error = %v", text, err)
		}
		if tier != config.TierBasic {
			t.Errorf("Dispatcher(%q) tier = %q, want basic", text, tier)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}

func TestParseDispatcherTier(t *testing.T) {
	valid := []struct {
		name string
		out  string
		want string
	}{
		{name: "advanced", out: `{"tier":"advanced"}`, want: config.TierAdvanced},
		{name: "basic with whitespace", out: " \n\t" + `{"tier":"basic"}` + "\r\n ", want: config.TierBasic},
		{name: "tier case and whitespace", out: `{"tier":"  AdVaNcEd  "}`, want: config.TierAdvanced},
		{name: "JSON code block", out: "```json\n{\"tier\":\"advanced\"}\n```", want: config.TierAdvanced},
		{name: "unlabelled code block", out: " \n```\n{\"tier\":\" BASIC \"}\n```\n", want: config.TierBasic},
	}
	for _, test := range valid {
		t.Run("valid "+test.name, func(t *testing.T) {
			got, err := parseDispatcherTier(test.out)
			if err != nil {
				t.Fatalf("parseDispatcherTier() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("parseDispatcherTier() = %q, want %q", got, test.want)
			}
		})
	}

	invalid := []struct {
		name string
		out  string
	}{
		{name: "empty", out: ""},
		{name: "bare tier", out: "advanced"},
		{name: "misleading explanation", out: "The request is advanced because it needs reasoning."},
		{name: "explanation before JSON", out: "Here is the result:\n{\"tier\":\"basic\"}"},
		{name: "explanation after JSON", out: "{\"tier\":\"advanced\"}\nThis is advanced."},
		{name: "explanation after code block", out: "```json\n{\"tier\":\"advanced\"}\n```\nDone."},
		{name: "multiple JSON objects", out: `{"tier":"advanced"} {"tier":"basic"}`},
		{name: "multiple code blocks", out: "```json\n{\"tier\":\"advanced\"}\n```\n```json\n{\"tier\":\"basic\"}\n```"},
		{name: "missing tier", out: `{"result":"advanced"}`},
		{name: "empty tier", out: `{"tier":""}`},
		{name: "null tier", out: `{"tier":null}`},
		{name: "non-string tier", out: `{"tier":1}`},
		{name: "other tier", out: `{"tier":"expert"}`},
		{name: "array", out: `["advanced"]`},
		{name: "malformed JSON", out: `{"tier":"advanced"`},
		{name: "unterminated code block", out: "```json\n{\"tier\":\"advanced\"}"},
		{name: "non-JSON code block", out: "```json\nadvanced\n```"},
	}
	for _, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			got, err := parseDispatcherTier(test.out)
			if err == nil {
				t.Fatalf("parseDispatcherTier() = %q, nil; want error", got)
			}
			if got != "" {
				t.Fatalf("parseDispatcherTier() tier = %q on error, want empty", got)
			}
		})
	}
}

func TestClassifyRejectsNonJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDispatcherChatResponse(w, "The answer is advanced")
	}))
	defer server.Close()
	dm := &config.ResolvedModel{
		Model:             config.Model{UpstreamName: "dispatcher-upstream"},
		EffectiveProtocol: config.ProtocolChatCompletions,
		EffectiveBaseURL:  server.URL,
	}

	tier, err := classify(context.Background(), dm, "prompt", "request", 1)
	if err == nil {
		t.Fatalf("classify() = %q, nil; want error", tier)
	}
	if tier != "" {
		t.Fatalf("classify() tier = %q on error, want empty", tier)
	}
}

func newDispatcherTestConfig(t *testing.T, baseURL string) (*config.Store, *config.Group) {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.Mutate(func(c *config.Config) error {
		c.Providers = []config.Provider{{
			ID:       "test",
			Protocol: config.ProtocolChatCompletions,
			BaseURL:  baseURL,
		}}
		c.Models = []config.Model{{
			Name:         "dispatcher",
			Provider:     "test",
			UpstreamName: "dispatcher-upstream",
		}}
		c.Routing.DispatcherTimeoutSec = 2
		return nil
	}); err != nil {
		t.Fatalf("configure dispatcher: %v", err)
	}
	return cfg, &config.Group{
		Name:             "smart",
		DispatcherModel:  "dispatcher",
		DispatcherPrompt: "test dispatcher prompt",
	}
}

func writeDispatcherChatResponse(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
		}},
	})
}
