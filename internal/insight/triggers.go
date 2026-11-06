package insight

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"knack/internal/session"
)

// A trigger eval set asks whether a skill's description makes the model
// load it for the right requests. Anthropic's skill-creator runs one as a
// JSON list of {"query", "should_trigger"} with real-looking requests:
// some that should load the skill and near misses that should not. The
// logs hold both: prompts whose turn loaded the skill, and prompts that
// talk about the same things and did not.

// TriggerCase is one query in skill-creator's eval set format.
type TriggerCase struct {
	Query         string `json:"query"`
	ShouldTrigger bool   `json:"should_trigger"`
}

// TriggerCandidate is a prompt from the logs and what happened to it.
type TriggerCandidate struct {
	Query     string
	Loaded    bool    // the turn loaded the skill
	Manual    bool    // because the person typed /name
	Score     float64 // how much of the prompt the description covers
	Other     string  // a different skill the turn loaded instead
	Agent     session.Agent
	SessionID string
	TurnID    string
	At        time.Time
}

// ShouldTrigger is the label a candidate starts with: prompts that loaded
// the skill should, near misses should not until the person says so.
func (c TriggerCandidate) ShouldTrigger() bool { return c.Loaded }

var (
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	markup      = regexp.MustCompile(`(?s)<command-(?:message|name)>.*?</command-(?:message|name)>|\[Image #\d+\]|\[图片\]`)
)

// maxQuery keeps one long paste from becoming a whole eval case.
const maxQuery = 1500

// PromptQuery is what the person asked in a prompt: a /command's
// arguments, without command markup or image placeholders. Prompts that
// are only injected context come back empty.
func PromptQuery(text string) string {
	if m := commandArgs.FindStringSubmatch(text); m != nil {
		text = m[1]
	}
	text = strings.TrimSpace(markup.ReplaceAllString(text, ""))
	if strings.HasPrefix(text, "<") || strings.HasPrefix(text, "# AGENTS.md") || strings.HasPrefix(text, "[Request interrupted") {
		return ""
	}
	if r := []rune(text); len(r) > maxQuery {
		text = string(r[:maxQuery]) + "…"
	}
	return text
}

// Relevance is the share of a prompt's terms that the description also
// uses, zero unless at least two are shared.
func Relevance(description, query string) float64 {
	d, q := terms(description), terms(query)
	shared := 0
	for w := range q {
		if d[w] {
			shared++
		}
	}
	if shared < 2 || len(q) == 0 {
		return 0
	}
	return float64(shared) / float64(len(q))
}

// minRelevance is the overlap that makes a prompt a near miss.
const minRelevance = 0.05

// TriggerSet picks up to limit candidates, about half that loaded the
// skill and half that did not: the closest near misses first, then prompts
// whose turn loaded a different skill. Queries are distinct.
func TriggerSet(loaded, others []TriggerCandidate, limit int) []TriggerCandidate {
	seen := map[string]bool{}
	pick := func(list []TriggerCandidate, n int) []TriggerCandidate {
		var out []TriggerCandidate
		for _, c := range list {
			key := strings.ToLower(strings.Join(strings.Fields(c.Query), " "))
			if key == "" || seen[key] || len(out) >= n {
				continue
			}
			seen[key] = true
			out = append(out, c)
		}
		return out
	}
	loaded = slices.Clone(loaded)
	slices.SortStableFunc(loaded, func(a, b TriggerCandidate) int { return b.At.Compare(a.At) })
	var near, elsewhere []TriggerCandidate
	for _, c := range others {
		switch {
		case c.Score >= minRelevance:
			near = append(near, c)
		case c.Other != "":
			elsewhere = append(elsewhere, c)
		}
	}
	slices.SortStableFunc(near, func(a, b TriggerCandidate) int { return cmp.Or(cmp.Compare(b.Score, a.Score), b.At.Compare(a.At)) })
	slices.SortStableFunc(elsewhere, func(a, b TriggerCandidate) int { return b.At.Compare(a.At) })
	half := (limit + 1) / 2
	out := pick(loaded, half)
	out = append(out, pick(near, limit-len(out))...)
	out = append(out, pick(elsewhere, limit-len(out))...)
	return out
}

// WriteTriggerSet writes cases as skill-creator's eval_set.json.
func WriteTriggerSet(path string, cases []TriggerCase) error {
	b, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
