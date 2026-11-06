package insight

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"knack/internal/session"
)

// A single session, read for what it cost and where it went wrong: the
// context each model call carried, the calls that rebuilt the prompt cache,
// tool results that stayed in context for many calls, loops, failure runs
// and turns that were interrupted or corrected.

// ContextPoint is one model call in session order.
type ContextPoint struct {
	Turn       int // index into Session.Turns
	RequestID  string
	At         time.Time
	Model      string
	Tokens     session.Tokens
	UsageKnown bool
	// Compacted: the context was compacted since the previous call.
	Compacted bool
	// Rebuild says why this call wrote most of its prompt to the cache
	// again instead of reading it; empty when it did not.
	Rebuild string
}

// SessionFinding is one thing worth a look, with the turn and step it is in.
type SessionFinding struct {
	Kind      string
	Turn      int
	ToolID    string
	RequestID string
	Title     string
	Detail    string
	// Tokens is what it cost, when that can be estimated, for ranking.
	Tokens int64
}

const (
	FindingRebuild    = "rebuild"
	FindingLargeOut   = "output"
	FindingLoop       = "loop"
	FindingFailures   = "failures"
	FindingInterrupt  = "interrupted"
	FindingCorrected  = "corrected"
	FindingAPIError   = "api_error"
	FindingPermission = "permission"
	FindingSkill      = "skill"
)

// SessionDiagnosis is a session's context curve and findings, most costly
// first.
type SessionDiagnosis struct {
	Points   []ContextPoint
	Findings []SessionFinding
	Peak     int64 // largest input of one call
	// RebuildTokens is the cache writes of calls that rebuilt the cache.
	RebuildTokens int64
}

const (
	// A call rebuilds the cache when it writes at least this much and more
	// than it reads.
	rebuildMin = 10_000
	// A tool result this large is worth following through later calls.
	largeOutputBytes = 20_000
	failureRun       = 3
	longPermission   = 2 * time.Minute
)

// OutputTokens estimates the tokens of a tool result from its size.
func OutputTokens(bytes int64) int64 { return (bytes + 3) / 4 }

// DiagnoseSession reads one session. Sub-Agents run in their own context,
// so only the session's own calls form its curve.
func DiagnoseSession(s session.Session) SessionDiagnosis {
	var d SessionDiagnosis
	var compactions []time.Time
	for _, t := range s.Turns {
		for _, e := range t.Events {
			if e.Kind == "compact" {
				compactions = append(compactions, e.At)
			}
		}
	}
	slices.SortFunc(compactions, func(a, b time.Time) int { return a.Compare(b) })
	compactedBetween := func(from, to time.Time) bool {
		for _, c := range compactions {
			if c.After(from) && !c.After(to) {
				return true
			}
		}
		return false
	}

	var prev *ContextPoint
	for ti, t := range s.Turns {
		reqs := slices.Clone(t.Requests)
		slices.SortStableFunc(reqs, func(a, b session.Request) int { return a.End.Compare(b.End) })
		for _, r := range reqs {
			if r.APIError {
				continue
			}
			p := ContextPoint{Turn: ti, RequestID: r.ID, At: r.End, Model: r.Model, Tokens: r.Tokens, UsageKnown: r.UsageKnown}
			if prev != nil {
				p.Compacted = compactedBetween(prev.At, cmp.Or(r.Start, r.End))
			}
			if r.UsageKnown {
				d.Peak = max(d.Peak, r.Tokens.InputTotal())
				if prev != nil && r.Tokens.CacheWrite >= rebuildMin && r.Tokens.CacheWrite > r.Tokens.CacheRead {
					p.Rebuild = rebuildReason(*prev, p, r)
					d.RebuildTokens += r.Tokens.CacheWrite
					d.Findings = append(d.Findings, SessionFinding{Kind: FindingRebuild, Turn: ti, RequestID: r.ID,
						Title: "缓存重建", Detail: fmt.Sprintf("%s，写入缓存 %s Token", p.Rebuild, compact(r.Tokens.CacheWrite)), Tokens: r.Tokens.CacheWrite})
				}
			}
			d.Points = append(d.Points, p)
			if r.UsageKnown {
				prev = &d.Points[len(d.Points)-1]
			}
		}
	}

	// A large tool result is read again by every later call until the
	// context is compacted.
	for ti, t := range s.Turns {
		for _, call := range t.Tools {
			if call.OutputBytes < largeOutputBytes {
				continue
			}
			end := cmp.Or(call.End, call.Start)
			carried := 0
			for _, p := range d.Points {
				if !p.At.After(end) || !p.UsageKnown {
					continue
				}
				if p.Compacted && carried > 0 {
					break
				}
				carried++
			}
			size := OutputTokens(call.OutputBytes)
			d.Findings = append(d.Findings, SessionFinding{Kind: FindingLargeOut, Turn: ti, ToolID: call.ID,
				Title: "大段工具结果", Detail: fmt.Sprintf("%s 返回约 %s Token，之后 %d 次模型调用都带着它，约 %s Token", cmp.Or(call.Name, "工具"), compact(size), carried, compact(size*int64(carried))),
				Tokens: size * int64(max(carried, 1))})
		}
	}

	for ti, t := range s.Turns {
		for _, l := range TurnLoops(t) {
			d.Findings = append(d.Findings, SessionFinding{Kind: FindingLoop, Turn: ti, ToolID: l.ToolID,
				Title: "疑似循环", Detail: fmt.Sprintf("%s 用同样的输入连续调用 %d 次", l.Tool, l.Repeats)})
		}
		run, first := 0, ""
		flush := func() {
			if run >= failureRun {
				d.Findings = append(d.Findings, SessionFinding{Kind: FindingFailures, Turn: ti, ToolID: first,
					Title: "连续失败", Detail: fmt.Sprintf("连续 %d 次工具调用失败", run)})
			}
			run, first = 0, ""
		}
		for _, call := range t.Tools {
			if call.Failed || call.TimedOutAfter > 0 {
				if run == 0 {
					first = call.ID
				}
				run++
				continue
			}
			flush()
		}
		flush()
		errors := 0
		for _, e := range t.Events {
			if e.Kind == "api_error" {
				errors++
			}
		}
		if errors > 0 {
			d.Findings = append(d.Findings, SessionFinding{Kind: FindingAPIError, Turn: ti, Title: "API 请求失败", Detail: fmt.Sprintf("%d 次请求失败", errors)})
		}
		for _, w := range t.Waits {
			if w.Kind == "permission" && w.Elapsed.Known && w.Elapsed.Value >= longPermission && !InteractiveTools[toolName(t, w.ToolID)] {
				d.Findings = append(d.Findings, SessionFinding{Kind: FindingPermission, Turn: ti, ToolID: w.ToolID,
					Title: "等待确认", Detail: fmt.Sprintf("%s 等了 %s", cmp.Or(toolName(t, w.ToolID), "工具"), session.FormatDuration(w.Elapsed.Value))})
			}
		}
		if t.Interrupted {
			d.Findings = append(d.Findings, SessionFinding{Kind: FindingInterrupt, Turn: ti, Title: "被中断", Detail: "这一轮被中断"})
		}
		if ti+1 < len(s.Turns) && s.Turns[ti+1].Corrects {
			d.Findings = append(d.Findings, SessionFinding{Kind: FindingCorrected, Turn: ti, Title: "疑似被纠正", Detail: "下一轮开头像是在纠正这一轮"})
		}
		for _, sk := range t.Skills {
			ep := episode(t, sk)
			if ep.Failed > 0 || t.Interrupted || (ti+1 < len(s.Turns) && s.Turns[ti+1].Corrects) {
				d.Findings = append(d.Findings, SessionFinding{Kind: FindingSkill, Turn: ti, ToolID: sk.ToolID,
					Title: "技能 " + sk.Name, Detail: fmt.Sprintf("加载后 %d 次工具调用，%d 次失败%s", ep.Calls, ep.Failed, map[bool]string{true: "，这一轮被中断"}[t.Interrupted]), Tokens: sk.Tokens})
			}
		}
	}

	rank := map[string]int{FindingLoop: 0, FindingFailures: 1, FindingSkill: 2, FindingCorrected: 3, FindingInterrupt: 4, FindingLargeOut: 5, FindingRebuild: 6, FindingAPIError: 7, FindingPermission: 8}
	slices.SortStableFunc(d.Findings, func(a, b SessionFinding) int {
		if a.Tokens > 0 && b.Tokens > 0 && a.Kind == b.Kind {
			return cmp.Compare(b.Tokens, a.Tokens)
		}
		return cmp.Or(cmp.Compare(rank[a.Kind], rank[b.Kind]), cmp.Compare(a.Turn, b.Turn))
	})
	return d
}

func rebuildReason(prev, p ContextPoint, r session.Request) string {
	switch {
	case p.Compacted:
		return "上下文压缩后"
	case prev.Model != "" && r.Model != "" && prev.Model != r.Model:
		return "模型从 " + prev.Model + " 换成 " + r.Model
	}
	start := cmp.Or(r.Start, r.End)
	if gap := start.Sub(prev.At); gap >= 5*time.Minute {
		return "距上次调用 " + session.FormatDuration(gap)
	}
	return "缓存未命中"
}

// TurnLoop is a run of identical calls to one tool in a turn.
type TurnLoop struct {
	Tool    string
	ToolID  string // the first call of the run
	Repeats int
}

// TurnLoops finds runs of LoopRepeats or more identical consecutive calls,
// leaving out tools that poll something still running.
func TurnLoops(t session.Turn) []TurnLoop {
	var out []TurnLoop
	run, last, first := 0, "", ""
	flush := func() {
		if run >= LoopRepeats {
			out = append(out, TurnLoop{Tool: last[:strings.IndexByte(last, 0)], ToolID: first, Repeats: run})
		}
	}
	for _, call := range t.Tools {
		name := cmp.Or(call.Name, "未知工具")
		if call.InputHash == "" || pollingTools[name] {
			flush()
			run, last = 0, ""
			continue
		}
		key := name + "\x00" + call.InputHash
		if key == last {
			run++
			continue
		}
		flush()
		run, last, first = 1, key, call.ID
	}
	flush()
	return out
}

// compact writes a token count as 12.3K or 1.2M.
func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}
