package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Services that speak /chat/completions differ in everything optional, so
// the request carries only the model and the messages.

type openAIClient struct {
	http     *http.Client
	endpoint string
	key      string
	model    string
}

func newOpenAI(cfg Config) (Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("地址要以 http:// 或 https:// 开头")
	}
	return &openAIClient{
		http:     &http.Client{Timeout: requestTimeout},
		endpoint: base + "/chat/completions",
		key:      cfg.key(),
		model:    strings.TrimSpace(cfg.Model),
	}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// retryAfter is the wait before the one retry of a throttled or failed call.
var retryAfter = 2 * time.Second

func (c *openAIClient) Complete(ctx context.Context, req Request) (Reply, error) {
	var messages []chatMessage
	if req.System != "" {
		messages = append(messages, chatMessage{"system", req.System})
	}
	messages = append(messages, chatMessage{"user", req.Prompt})
	body, err := json.Marshal(map[string]any{"model": c.model, "messages": messages})
	if err != nil {
		return Reply{}, err
	}
	for attempt := 0; ; attempt++ {
		reply, status, err := c.send(ctx, body)
		if err == nil || attempt > 0 || (status != http.StatusTooManyRequests && status < 500) {
			return reply, err
		}
		select {
		case <-ctx.Done():
			return Reply{}, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
}

func (c *openAIClient) send(ctx context.Context, body []byte) (Reply, int, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Reply{}, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		r.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return Reply{}, 0, ctx.Err()
		}
		return Reply{}, 0, fmt.Errorf("连不上模型服务：%w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Reply{}, resp.StatusCode, fmt.Errorf("读取回复失败：%w", err)
	}
	var out chatResponse
	parseErr := json.Unmarshal(data, &out)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := ""
		if parseErr == nil && out.Error != nil {
			detail = out.Error.Message
		}
		return Reply{}, resp.StatusCode, statusError(resp.StatusCode, detail)
	}
	if parseErr != nil || len(out.Choices) == 0 {
		return Reply{}, resp.StatusCode, errors.New("这个地址返回的不是 chat/completions 的格式")
	}
	reply := Reply{Text: out.Choices[0].Message.Content, Input: out.Usage.PromptTokens, Output: out.Usage.CompletionTokens}
	switch out.Choices[0].FinishReason {
	case "length":
		return reply, resp.StatusCode, errors.New("模型的回复超出长度上限，没有写完")
	case "content_filter":
		return reply, resp.StatusCode, errors.New("模型拒绝了这次请求")
	}
	return reply, resp.StatusCode, nil
}
