// Package llm calls the model the person connected for skill evaluation.
// It is the only place Knack sends anything off the machine besides plugin
// installs, and only after the connection is switched on.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	Anthropic = "anthropic" // the Messages API, through the official SDK
	OpenAI    = "openai"    // any service that speaks /chat/completions
)

// Config is the connection in ~/.config/knack/llm.json.
type Config struct {
	// Enabled records that the person agreed to send session content to
	// this service. Nothing is sent while it is off.
	Enabled  bool   `json:"enabled"`
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseUrl,omitempty"`
	Model    string `json:"model"`
	// APIKey is the key itself, or $NAME to read it from the environment.
	APIKey string `json:"apiKey,omitempty"`
}

// DefaultModel is offered for a new Anthropic connection.
const DefaultModel = "claude-opus-5-5"

// Load reads the connection. A missing file is an unset one.
func Load(path string) (Config, error) {
	cfg := Config{Protocol: Anthropic, Model: DefaultModel}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{Protocol: Anthropic, Model: DefaultModel}, fmt.Errorf("llm.json 无法解析：%w", err)
	}
	if cfg.Protocol != OpenAI {
		cfg.Protocol = Anthropic
	}
	return cfg, nil
}

// Save writes the connection for its owner only: it may hold a key.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Ready reports whether calls may be made.
func (c Config) Ready() bool { return c.Enabled && strings.TrimSpace(c.Model) != "" }

// Host names where requests go, for telling the person before they send.
func (c Config) Host() string {
	base := strings.TrimSpace(c.BaseURL)
	if base == "" {
		if c.Protocol == OpenAI {
			return "api.openai.com"
		}
		return "api.anthropic.com"
	}
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		return u.Host
	}
	return base
}

func (c Config) key() string {
	k := strings.TrimSpace(c.APIKey)
	if name, ok := strings.CutPrefix(k, "$"); ok {
		return os.Getenv(name)
	}
	return k
}

// Request is one question with its instructions.
type Request struct {
	System string
	Prompt string
	// Cache says System repeats across the calls of a run, so a provider
	// that caches by request can bill the repeats at its lower rate.
	Cache bool
}

// Reply is the model's text and what the call used.
type Reply struct {
	Text          string
	Input, Output int64
}

// Client completes requests against the connected model.
type Client interface {
	Complete(ctx context.Context, req Request) (Reply, error)
}

// New makes a client for the connection.
func New(cfg Config) (Client, error) {
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("还没有填写模型")
	}
	if cfg.Protocol == OpenAI {
		return newOpenAI(cfg)
	}
	return newAnthropic(cfg), nil
}

// Test asks the model for one word, to check the address, key and model.
func Test(ctx context.Context, c Client) error {
	r, err := c.Complete(ctx, Request{Prompt: "Reply with the single word: ok"})
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Text) == "" {
		return errors.New("模型返回了空回复")
	}
	return nil
}

// statusError words an HTTP failure for the person. detail is the service's
// own message, when it gave one.
func statusError(status int, detail string) error {
	var what string
	switch {
	case status == 401 || status == 403:
		what = "密钥无效或没有权限"
	case status == 404:
		what = "找不到这个模型或地址"
	case status == 429:
		what = "请求过于频繁或额度不足"
	case status >= 500:
		what = "服务暂时不可用"
	default:
		what = "请求被拒绝"
	}
	if detail = strings.TrimSpace(detail); detail != "" {
		if r := []rune(detail); len(r) > 200 {
			detail = string(r[:200]) + "…"
		}
		return fmt.Errorf("%s（%d）：%s", what, status, detail)
	}
	return fmt.Errorf("%s（%d）", what, status)
}
