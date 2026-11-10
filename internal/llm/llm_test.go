package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "knack", "llm.json")
	cfg, err := Load(path)
	if err != nil || cfg.Enabled || cfg.Protocol != Anthropic || cfg.Model != DefaultModel {
		t.Fatalf("unset connection: %+v %v", cfg, err)
	}
	want := Config{Enabled: true, Protocol: OpenAI, BaseURL: "https://llm.example/v1", Model: "m", APIKey: "$KNACK_TEST_KEY"}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", st.Mode().Perm(), err)
	}
	got, err := Load(path)
	if err != nil || got != want {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	t.Setenv("KNACK_TEST_KEY", "from-env")
	if got.key() != "from-env" || got.Host() != "llm.example" || !got.Ready() {
		t.Fatalf("key=%q host=%q", got.key(), got.Host())
	}
	if (Config{Protocol: Anthropic}).Host() != "api.anthropic.com" {
		t.Fatal("default host")
	}
}

func TestAnthropicClient(t *testing.T) {
	var seen map[string]any
	var key string
	status := http.StatusOK
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","stop_reason":"end_turn",
		"content":[{"type":"thinking","thinking":"","signature":"s"},{"type":"text","text":"ok"}],
		"usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":2,"output_tokens":3}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		// The SDK sends the key as a bearer token.
		key = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &seen)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	c, err := New(Config{Protocol: Anthropic, BaseURL: srv.URL, Model: "claude-opus-5-5", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Complete(context.Background(), Request{System: "be a judge", Prompt: "hello"})
	if err != nil || r.Text != "ok" || r.Input != 17 || r.Output != 3 {
		t.Fatalf("reply %+v %v", r, err)
	}
	if key != "Bearer k" || seen["model"] != "claude-opus-5-5" || seen["thinking"] != nil || seen["temperature"] != nil {
		t.Fatalf("request key=%q body=%v", key, seen)
	}
	if system, _ := seen["system"].([]any); len(system) != 1 || system[0].(map[string]any)["cache_control"] != nil {
		t.Fatalf("system without cache: %v", seen["system"])
	}
	if _, err := c.Complete(context.Background(), Request{System: "skill list", Prompt: "q", Cache: true}); err != nil {
		t.Fatal(err)
	}
	if system, _ := seen["system"].([]any); len(system) != 1 || system[0].(map[string]any)["cache_control"] == nil {
		t.Fatalf("cached system: %v", seen["system"])
	}
	if err := Test(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	body = `{"id":"msg_2","type":"message","role":"assistant","model":"m","stop_reason":"refusal","content":[],"usage":{"input_tokens":1,"output_tokens":0}}`
	if _, err := c.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("refusal: %v", err)
	}

	status, body = http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`
	if _, err := c.Complete(context.Background(), Request{Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "密钥无效") {
		t.Fatalf("auth error: %v", err)
	}
}

func TestOpenAIClient(t *testing.T) {
	retryAfter = time.Millisecond
	var calls int
	var auth string
	var seen map[string]any
	fail := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &seen)
		if fail > 0 {
			fail--
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"slow down"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":1}}`)
	}))
	defer srv.Close()
	c, err := New(Config{Protocol: OpenAI, BaseURL: srv.URL + "/v1/", Model: "gpt-x", APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Complete(context.Background(), Request{System: "s", Prompt: "p"})
	if err != nil || r.Text != "ok" || r.Input != 7 || r.Output != 1 || auth != "Bearer k" {
		t.Fatalf("reply %+v auth=%q err=%v", r, auth, err)
	}
	if msgs, _ := seen["messages"].([]any); len(msgs) != 2 || len(seen) != 2 {
		t.Fatalf("request carries more than model and messages: %v", seen)
	}
	// A throttled call is retried once, then reported.
	calls, fail = 0, 1
	if _, err := c.Complete(context.Background(), Request{Prompt: "p"}); err != nil || calls != 2 {
		t.Fatalf("retry: calls=%d err=%v", calls, err)
	}
	calls, fail = 0, 5
	if _, err := c.Complete(context.Background(), Request{Prompt: "p"}); err == nil || calls != 2 || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("gave up: calls=%d err=%v", calls, err)
	}
	if _, err := New(Config{Protocol: OpenAI, BaseURL: "llm.example", Model: "m"}); err == nil {
		t.Fatal("accepted an address without a scheme")
	}
	if _, err := New(Config{Protocol: OpenAI}); err == nil {
		t.Fatal("accepted a connection without a model")
	}
}
