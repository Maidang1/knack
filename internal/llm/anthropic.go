package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// requestTimeout bounds one call. Evaluation replies are short, so a call
// that runs this long has stalled.
const requestTimeout = 5 * time.Minute

// maxOutput leaves room for the model's reasoning ahead of a short answer
// while keeping a non-streaming request inside the timeout.
const maxOutput = 16000

type anthropicClient struct {
	client anthropic.Client
	model  string
}

func newAnthropic(cfg Config) Client {
	opts := []option.RequestOption{option.WithRequestTimeout(requestTimeout)}
	// Without a key here the SDK looks where it always does: the
	// environment, then a profile from `ant auth login`.
	if k := cfg.key(); k != "" {
		opts = append(opts, option.WithAPIKey(k))
	}
	if base := strings.TrimSpace(cfg.BaseURL); base != "" {
		opts = append(opts, option.WithBaseURL(base))
	}
	return &anthropicClient{client: anthropic.NewClient(opts...), model: strings.TrimSpace(cfg.Model)}
}

func (c *anthropicClient) Complete(ctx context.Context, req Request) (Reply, error) {
	// Thinking and effort are left at the model's defaults: the model is the
	// person's choice, possibly behind a gateway, and the accepted values
	// differ between models.
	params := anthropic.MessageNewParams{
		Model:     c.model,
		MaxTokens: maxOutput,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt))},
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
		if req.Cache {
			params.System[0].CacheControl = anthropic.NewCacheControlEphemeralParam()
		}
	}
	resp, err := c.client.Messages.New(ctx, params)
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return Reply{}, statusError(apierr.StatusCode, apierr.Message)
		}
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		return Reply{}, fmt.Errorf("连不上模型服务：%w", err)
	}
	out := Reply{
		Input:  resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens,
		Output: resp.Usage.OutputTokens,
	}
	// A refusal arrives as a successful response, so check before reading.
	switch resp.StopReason {
	case anthropic.StopReasonRefusal:
		return out, errors.New("模型拒绝了这次请求")
	case anthropic.StopReasonMaxTokens:
		return out, errors.New("模型的回复超出长度上限，没有写完")
	}
	var text strings.Builder
	for _, block := range resp.Content {
		if b, ok := block.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(b.Text)
		}
	}
	out.Text = text.String()
	return out, nil
}
