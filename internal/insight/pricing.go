// Package insight turns local session statistics and skill scans into
// diagnoses: what the sessions cost, how much context each starts with,
// and which skills earn their keep. It reads only the
// session and skill snapshots, plus the user's own configuration files.
package insight

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"knack/internal/session"
)

// Price is US dollars per million tokens of each kind.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// claude prices a model at Anthropic's first-party rates: cache writes at
// the 5-minute rate, 1.25 times input.
func claude(input, output, read float64) Price {
	return Price{Input: input, Output: output, CacheRead: read, CacheWrite: input * 1.25}
}

// DefaultPrices are Anthropic's published first-party rates, by model ID
// prefix. Partner platforms and subscriptions bill differently, so every
// figure is an estimate. Other providers' models are left to prices.json.
var DefaultPrices = map[string]Price{
	"claude-fable-5-1":  claude(10, 50, 0.25),
	"claude-mythos-5-1": claude(10, 50, 0.25),
	"claude-fable-5":    claude(10, 50, 1),
	"claude-mythos-5":   claude(10, 50, 1),
	"claude-opus-5-5":   claude(4, 20, 0.20),
	"claude-opus-5":     claude(5, 25, 0.5),
	"claude-opus-4-8":   claude(5, 25, 0.5),
	"claude-opus-4-7":   claude(5, 25, 0.5),
	"claude-opus-4-6":   claude(5, 25, 0.5),
	"claude-opus-4-5":   claude(5, 25, 0.5),
	"claude-opus-4-1":   claude(15, 75, 1.5),
	"claude-opus-4":     claude(15, 75, 1.5),
	"claude-sonnet-5-5": claude(2, 10, 0.20),
	"claude-sonnet-5":   claude(2, 10, 0.2),
	"claude-sonnet-4":   claude(3, 15, 0.3),
	"claude-haiku-5-5":  claude(0.1, 0.5, 0.01),
	"claude-haiku-4-5":  claude(1, 5, 0.1),
	// Claude Code's own placeholder replies; no request was made.
	"<synthetic>": {},
}

// Prices maps model names to rates. Lookup tries the exact name, then the
// longest prefix, on the full name and on its last path segment, so
// "custom:proxy/claude-opus-5-5" and dated IDs find their model.
type Prices map[string]Price

func (p Prices) Lookup(model string) (Price, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return Price{}, false
	}
	candidates := []string{name}
	if i := strings.LastIndexAny(name, "/:"); i >= 0 && i+1 < len(name) {
		candidates = append(candidates, name[i+1:])
	}
	for _, c := range candidates {
		if price, ok := p[c]; ok {
			return price, true
		}
	}
	best, found := "", false
	for key := range p {
		for _, c := range candidates {
			if strings.HasPrefix(c, key) && len(key) > len(best) {
				best, found = key, true
			}
		}
	}
	if !found {
		return Price{}, false
	}
	return p[best], true
}

// Cost is the dollar estimate of tokens at price.
func (p Price) Cost(t session.Tokens) float64 {
	return (float64(t.Input)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6
}

// LoadPrices returns the defaults overlaid with the user's prices.json, a
// map from model name or prefix to rates. A missing file is no override.
func LoadPrices(path string) (Prices, error) {
	out := Prices{}
	for k, v := range DefaultPrices {
		out[k] = v
	}
	if path == "" {
		return out, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("读取价格表：%w", err)
	}
	var file struct {
		Models map[string]Price `json:"models"`
	}
	if err := json.Unmarshal(b, &file); err != nil {
		return out, fmt.Errorf("价格表格式无效：%w", err)
	}
	for k, v := range file.Models {
		if v.Input < 0 || v.Output < 0 || v.CacheRead < 0 || v.CacheWrite < 0 {
			return out, fmt.Errorf("价格表：%s 的价格不能为负数", k)
		}
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out, nil
}

// Names lists the priced model names, sorted.
func (p Prices) Names() []string {
	out := make([]string, 0, len(p))
	for k := range p {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
