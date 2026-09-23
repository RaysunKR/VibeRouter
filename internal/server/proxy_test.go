package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viberouter/viberouter/internal/auth"
	"github.com/viberouter/viberouter/internal/config"
	"github.com/viberouter/viberouter/internal/logger"
	"github.com/viberouter/viberouter/internal/router"
	"github.com/viberouter/viberouter/internal/upstream"
)

// fakeUpstream mimics a chat.completions provider.
func fakeUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func newTestServer(t *testing.T, upstreamURL string) (*httptest.Server, *config.Store, *Server) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	envPath := filepath.Join(dir, ".env")
	logPath := filepath.Join(dir, "logs", "router.log")

	yaml := strings.ReplaceAll(`
server: {addr: ":0"}
routing: {long_context_threshold: 200000, dispatcher_timeout_sec: 5, strategy: round_robin}
providers:
  - {id: up, name: Up, protocol: chat_completions, base_url: "UPURL", api_key: upkey}
  - {id: upbad, name: UpBad, protocol: chat_completions, base_url: "http://127.0.0.1:1", api_key: x}
models:
  - {name: direct, provider: up, upstream_model: real-model, multimodal: true, long_context: true, max_context_tokens: 1000000}
groups:
  - name: failover
    mode: balance
    members:
      - {model: bad, priority: 1}
      - {model: direct, priority: 2}
keys:
  - {id: k1, key: vr-testkey, name: test, enabled: true}
logs: {file: "LOGPATH"}
`, "UPURL", upstreamURL)
	yaml = strings.ReplaceAll(yaml, "LOGPATH", filepath.ToSlash(logPath))

	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// register the broken-priority model referencing dead provider
	err = cfg.Mutate(func(c *config.Config) error {
		c.Models = append(c.Models, config.Model{Name: "bad", Provider: "upbad", UpstreamName: "x", MaxTokens: 1000})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	am, _, err := auth.LoadEnv(envPath)
	if err != nil {
		t.Fatal(err)
	}
	fl, err := logger.Open(logPath, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fl.Close() })

	rt := router.New(cfg, upstream.Dispatcher(cfg))
	srv := New(cfg, rt, am, fl)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, cfg, srv
}

const okChatResponse = `{"id":"chatcmpl-ok","object":"chat.completion","created":1,"model":"real-model","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`

func chatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer upkey" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, okChatResponse)
}

func doReq(t *testing.T, method, url, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestProxyPassthrough(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, _ := newTestServer(t, up.URL)

	status, body := doReq(t, "POST", ts.URL+"/v1/chat/completions", "vr-testkey",
		`{"model":"direct","messages":[{"role":"user","content":"ping"}]}`)
	if status != 200 || !strings.Contains(body, "pong") {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestProxyKeyAuth(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, _ := newTestServer(t, up.URL)
	status, _ := doReq(t, "POST", ts.URL+"/v1/chat/completions", "vr-wrong",
		`{"model":"direct","messages":[{"role":"user","content":"ping"}]}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d", status)
	}
}

func TestFailover(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, _ := newTestServer(t, up.URL)
	// "failover" group: first member has dead upstream (127.0.0.1:1) → must fall through to "direct"
	status, body := doReq(t, "POST", ts.URL+"/v1/chat/completions", "vr-testkey",
		`{"model":"failover","messages":[{"role":"user","content":"ping"}]}`)
	if status != 200 || !strings.Contains(body, "pong") {
		t.Fatalf("failover failed: status=%d body=%s", status, body)
	}
}

func TestAnthropicToChatConversion(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, _ := newTestServer(t, up.URL)

	// client speaks anthropic; upstream speaks chat completions
	status, body := doReq(t, "POST", ts.URL+"/v1/messages", "vr-testkey",
		`{"model":"direct","max_tokens":100,"messages":[{"role":"user","content":"ping"}]}`)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(body, `"type":"message"`) || !strings.Contains(body, "pong") {
		t.Fatalf("not an anthropic response: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Fatalf("bad stop_reason: %s", body)
	}
}

func TestAnthropicStreamFromChat(t *testing.T) {
	up := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"real-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"he\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"real-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"llo\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"real-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	})
	ts, _, _ := newTestServer(t, up.URL)

	status, body := doReq(t, "POST", ts.URL+"/v1/messages", "vr-testkey",
		`{"model":"direct","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	for _, want := range []string{"message_start", "text_delta", "he", "llo", "message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream missing %q:\n%s", want, body)
		}
	}
}

func TestCaptchaEndpoint(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, _ := newTestServer(t, up.URL)

	status, body := doReq(t, "GET", ts.URL+"/api/captcha", "", "")
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var resp struct {
		CaptchaID string `json:"captcha_id"`
		Image     string `json:"image"`
	}
	if err := jsonUnmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("bad json: %s", body)
	}
	if resp.CaptchaID == "" || !strings.HasPrefix(resp.Image, "data:image/png;base64,") {
		t.Fatalf("unexpected captcha response: %s", body)
	}
}

func TestAdminFlow(t *testing.T) {
	up := fakeUpstream(t, chatHandler)
	ts, _, srv := newTestServer(t, up.URL)

	// login requires a valid one-time captcha
	status, body := doReq(t, "POST", ts.URL+"/api/login", "", `{"username":"admin","password":"admin"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("login without captcha: status=%d body=%s", status, body)
	}

	capID, capCode, _, err := srv.captcha.Generate()
	if err != nil {
		t.Fatal(err)
	}
	loginBody := `{"username":"admin","password":"admin","captcha_id":"` + capID + `","captcha_code":"` + capCode + `"}`

	// correct captcha + credentials logs in
	status, body = doReq(t, "POST", ts.URL+"/api/login", "", loginBody)
	if status != 200 {
		t.Fatalf("login: status=%d body=%s", status, body)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := jsonUnmarshal([]byte(body), &tok); err != nil || tok.Token == "" {
		t.Fatalf("no token: %s", body)
	}

	// the same captcha cannot be reused
	status, body = doReq(t, "POST", ts.URL+"/api/login", "", loginBody)
	if status != http.StatusBadRequest {
		t.Fatalf("login reusing captcha: status=%d body=%s", status, body)
	}

	adminReq := func(method, path, payload string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// create model
	status, body = adminReq("POST", "/api/models",
		`{"name":"m-x","provider":"up","upstream_model":"ux","multimodal":false,"long_context":false,"max_context_tokens":1000}`)
	if status != 200 {
		t.Fatalf("create model: %d %s", status, body)
	}
	// config persisted?
	status, body = adminReq("GET", "/api/config", "")
	if status != 200 || !strings.Contains(body, "m-x") {
		t.Fatalf("config: %d %s", status, body)
	}
	// create key
	status, body = adminReq("POST", "/api/keys", `{"name":"k2"}`)
	if status != 200 || !strings.Contains(body, "vr-") {
		t.Fatalf("create key: %d %s", status, body)
	}
	// unauthorized admin call rejected
	status, _ = doReq(t, "GET", ts.URL+"/api/config", "", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("admin without token: %d", status)
	}
}

func jsonUnmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}
