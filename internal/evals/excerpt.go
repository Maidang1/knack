package evals

import (
	"fmt"
	"regexp"
	"strings"

	"knack/internal/session"
)

// Excerpt is the part of a turn a model reads to judge it: numbered steps
// a verdict can cite. Long fields are cut in the middle and say so, and
// whatever looks like a credential is taken out before anything is sent.
type Excerpt struct {
	Steps []string // step N is Steps[N-1]
	// Truncated says some text was left out for length.
	Truncated bool
}

const (
	maxMessage = 4000   // a prompt or reply
	maxToolIO  = 1500   // a tool's input, and its output
	maxExcerpt = 120000 // the whole excerpt
)

// NewExcerpt builds the excerpt of one turn from its replay entries. next
// is the prompt that followed the turn, which shows whether the person
// accepted the result.
func NewExcerpt(entries []session.Entry, next string) Excerpt {
	var x Excerpt
	add := func(s string) { x.Steps = append(x.Steps, s) }
	for _, e := range entries {
		switch e.Kind {
		case session.EntryUser:
			add("用户：" + x.clip(e.Text, maxMessage))
		case session.EntryContext:
			// Injected text is not what anyone did. A loaded skill is worth
			// a line so the judge knows when the instructions arrived.
			if strings.HasPrefix(strings.TrimSpace(e.Text), "Base directory for this skill:") {
				add("（技能说明在这里注入上下文，内容略）")
			}
		case session.EntryAssistant:
			add("助手：" + x.clip(e.Text, maxMessage))
		case session.EntryTool:
			out := "输出"
			if e.Error {
				out = "输出（失败）"
			}
			add(fmt.Sprintf("工具 %s\n输入：%s\n%s：%s", e.Name, x.clip(e.Input, maxToolIO), out, x.clip(e.Text, maxToolIO)))
		case session.EntryEvent:
			add(strings.TrimSpace("事件：" + e.Name + " " + x.clip(e.Text, maxToolIO)))
		}
	}
	if next = strings.TrimSpace(next); next != "" {
		add("下一轮用户说：" + x.clip(next, maxToolIO))
	}
	x.fit()
	return x
}

// clip redacts s and, when it is longer than limit, keeps its start and end.
func (x *Excerpt) clip(s string, limit int) string {
	r := []rune(Redact(strings.TrimSpace(s)))
	if len(r) <= limit {
		return string(r)
	}
	x.Truncated = true
	head, tail := limit*2/3, limit/3
	return fmt.Sprintf("%s\n…（省略 %d 字）…\n%s", string(r[:head]), len(r)-head-tail, string(r[len(r)-tail:]))
}

// fit drops steps from the middle until the excerpt is within its budget:
// how a turn began and how it ended say the most about it.
func (x *Excerpt) fit() {
	total := 0
	for _, s := range x.Steps {
		total += len([]rune(s))
	}
	if total <= maxExcerpt {
		return
	}
	x.Truncated = true
	kept, used := make([]bool, len(x.Steps)), 0
	take := func(i int) bool {
		n := len([]rune(x.Steps[i]))
		if used+n > maxExcerpt {
			return false
		}
		kept[i], used = true, used+n
		return true
	}
	// Take from both ends in turn while there is room.
	for lo, hi := 0, len(x.Steps)-1; lo <= hi; lo, hi = lo+1, hi-1 {
		if !take(lo) || lo == hi || !take(hi) {
			break
		}
	}
	for i := range x.Steps {
		if !kept[i] {
			x.Steps[i] = "（这一步因整轮过长省略）"
		}
	}
}

// Text renders the steps with their numbers.
func (x Excerpt) Text() string {
	var b strings.Builder
	for i, s := range x.Steps {
		fmt.Fprintf(&b, "[%d] %s\n\n", i+1, s)
	}
	return strings.TrimSpace(b.String())
}

// Locate finds a quote in the excerpt: in the step it was said to be from,
// else in any step. It returns the step that holds it.
func (x Excerpt) Locate(step int, quote string) (int, bool) {
	q := squash(quote)
	if q == "" {
		return 0, false
	}
	if step >= 1 && step <= len(x.Steps) && strings.Contains(squash(x.Steps[step-1]), q) {
		return step, true
	}
	for i, s := range x.Steps {
		if strings.Contains(squash(s), q) {
			return i + 1, true
		}
	}
	return 0, false
}

// squash removes the whitespace differences a model introduces when copying.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

var secrets = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(?:sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
}

// named catches a value by the name in front of it: api_key=…, "token": "…",
// Authorization: Bearer ….
var named = regexp.MustCompile(`(?i)((?:api[_-]?key|secret|token|passwd|password|authorization)["']?\s*[:=]\s*["']?(?:bearer\s+)?)[^\s"',;]{8,}`)

const redacted = "[已隐去]"

// Redact takes out what looks like a credential. It is a net for the common
// shapes, not a guarantee.
func Redact(s string) string {
	for _, re := range secrets {
		s = re.ReplaceAllString(s, redacted)
	}
	return named.ReplaceAllString(s, "${1}"+redacted)
}
