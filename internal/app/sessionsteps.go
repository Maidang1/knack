package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"knack/internal/session"
)

// Steps retain only the metadata already present in the immutable snapshot.
// Parallel calls remain separate rows; their lengths must never be summed.
type sessionStep struct {
	name, kind, skills string
	start, end         time.Time
	elapsed            session.Duration
	phase              sessionPhase
	thinking           time.Duration
	request            *session.Request
	tool               *session.ToolCall
	child              *session.Session
	wait               *session.Wait
	event              *session.Event
}

func turnSteps(t session.Turn) []sessionStep {
	var steps []sessionStep
	for i, r := range t.Requests {
		phase, name := phaseGen, fmt.Sprintf("模型请求 %d", i+1)
		if r.Retry {
			phase, name = phaseRetry, fmt.Sprintf("重试请求 %d（推断）", i+1)
		}
		if r.APIError {
			phase, name = phaseRetry, fmt.Sprintf("模型请求 %d · 失败", i+1)
		}
		start := time.Time{}
		if r.Elapsed.Known && !r.End.IsZero() {
			start = r.End.Add(-r.Elapsed.Value)
		}
		steps = append(steps, sessionStep{name: name, kind: "模型请求", start: start, end: r.End, elapsed: r.Elapsed, phase: phase, thinking: min(r.Thinking.Value, r.Elapsed.Value), request: &t.Requests[i]})
	}
	children := map[string]bool{}
	for _, sub := range t.Subagents {
		if sub.ToolID != "" {
			children[sub.ToolID] = true
		}
	}
	permissionTools := map[string]bool{}
	for i, tool := range t.Tools {
		if children[tool.ID] && !tool.PermissionWait {
			continue
		}
		phase, kind := phaseTool, "工具调用"
		if tool.AgentID != "" {
			phase, kind = phaseSub, "子 Agent"
		}
		name := tool.Name
		if name == "" {
			name = "工具名称暂不支持"
		}
		if tool.PermissionWait {
			phase, kind = phaseWait, "等待你"
			name += " · 等待你确认"
			permissionTools[tool.ID] = true
		}
		var skills []string
		for _, sk := range t.Skills {
			if sk.ToolID == tool.ID {
				skills = append(skills, sk.Name)
			}
		}
		steps = append(steps, sessionStep{name: name, kind: kind, skills: strings.Join(skills, "、"), start: tool.Start, end: tool.End, elapsed: tool.Elapsed, phase: phase, tool: &t.Tools[i]})
	}
	for i, sub := range t.Subagents {
		elapsed := session.Duration{}
		if !sub.Session.Start.IsZero() && !sub.Session.End.Before(sub.Session.Start) {
			elapsed = session.Duration{Value: sub.Session.End.Sub(sub.Session.Start), Known: true, Estimated: true}
		}
		step := sessionStep{name: "子 Agent · " + sub.ID, kind: "子 Agent", start: sub.Session.Start, end: sub.Session.End, elapsed: elapsed, phase: phaseSub, child: &t.Subagents[i].Session}
		if sub.ToolID != "" {
			for j := range t.Tools {
				if t.Tools[j].ID == sub.ToolID {
					step.tool = &t.Tools[j]
					break
				}
			}
			var skills []string
			for _, sk := range t.Skills {
				if sk.ToolID == sub.ToolID {
					skills = append(skills, sk.Name)
				}
			}
			step.skills = strings.Join(skills, "、")
		}
		steps = append(steps, step)
	}
	for i, w := range t.Waits {
		if w.Kind == "permission" && permissionTools[w.ToolID] {
			continue
		}
		name := "等待你回复"
		if w.Kind == "permission" {
			name = "等待你确认"
		}
		if w.Kind == "away" {
			name = "离开相关间隔（估算）"
		}
		steps = append(steps, sessionStep{name: name, kind: "等待你", start: w.Start, end: w.End, elapsed: w.Elapsed, phase: phaseWait, wait: &t.Waits[i]})
	}
	for i, ev := range t.Events {
		name := ""
		switch ev.Kind {
		case "compact":
			name = "上下文压缩"
		case "interrupted":
			name = "中断事件"
		case "timeout":
			name = "超时事件"
		case "api_error":
			linked := false
			for _, r := range t.Requests {
				if r.APIError && r.End.Equal(ev.At) {
					linked = true
				}
			}
			if !linked {
				name = "API 请求失败"
			}
		}
		if name == "" {
			continue
		}
		// Timeout duration is a threshold, not a measured interval. Only compact
		// events have a duration that can be placed backwards from the timestamp.
		start, elapsed := ev.At, session.Duration{}
		if ev.Kind == "compact" && ev.Duration.Known && !ev.At.IsZero() {
			elapsed = ev.Duration
			start = ev.At.Add(-elapsed.Value)
		}
		steps = append(steps, sessionStep{name: name, kind: "重试与事件", start: start, end: ev.At, elapsed: elapsed, phase: phaseRetry, event: &t.Events[i]})
	}
	slices.SortStableFunc(steps, func(a, b sessionStep) int {
		if a.start.IsZero() != b.start.IsZero() {
			if a.start.IsZero() {
				return 1
			}
			return -1
		}
		return a.start.Compare(b.start)
	})
	return steps
}

func stepDuration(s sessionStep) string {
	if !s.elapsed.Known {
		return "暂不支持"
	}
	value := session.FormatDuration(s.elapsed.Value)
	if s.elapsed.Value > 0 && s.elapsed.Value < time.Second {
		value = fmt.Sprintf("%.1fs", s.elapsed.Value.Seconds())
		if s.elapsed.Value < 100*time.Millisecond {
			value = "<0.1s"
		}
	}
	if s.elapsed.Estimated {
		value = "≈ " + value
	}
	return value
}

// All rows use the same piecewise time transform, including rows overlapping
// a long step. Merge cuts first so overlapping long steps are compressed once.
type stepCut struct{ from, to time.Duration }
type stepAxis struct {
	total, display time.Duration
	cuts           []stepCut
}

func newStepAxis(turn sessionTurnView, fold bool) stepAxis {
	axis := stepAxis{total: turn.phases.total, display: turn.phases.total}
	if !fold || axis.total <= 0 || turn.turn.Start.IsZero() {
		return axis
	}
	for _, s := range turn.steps {
		if !s.elapsed.Known || s.start.IsZero() || s.end.IsZero() || !session.FoldLongStep(s.elapsed.Value, axis.total) {
			continue
		}
		from, to := max(0, s.start.Sub(turn.turn.Start)), min(axis.total, s.end.Sub(turn.turn.Start))
		if to > from {
			axis.cuts = append(axis.cuts, stepCut{from, to})
		}
	}
	slices.SortFunc(axis.cuts, func(a, b stepCut) int {
		if a.from < b.from {
			return -1
		}
		if a.from > b.from {
			return 1
		}
		return 0
	})
	var merged []stepCut
	for _, cut := range axis.cuts {
		if len(merged) > 0 && cut.from <= merged[len(merged)-1].to {
			merged[len(merged)-1].to = max(merged[len(merged)-1].to, cut.to)
		} else {
			merged = append(merged, cut)
		}
	}
	axis.cuts = merged
	for _, cut := range merged {
		axis.display -= cut.to - cut.from - min(10*time.Second, cut.to-cut.from)
	}
	return axis
}
func (a stepAxis) at(offset time.Duration) float32 {
	if a.display <= 0 {
		return 0
	}
	offset = max(0, min(a.total, offset))
	mapped := float64(offset)
	for _, cut := range a.cuts {
		length := cut.to - cut.from
		inside := max(0, min(offset-cut.from, length))
		mapped -= float64(inside) * (1 - float64(min(10*time.Second, length))/float64(length))
	}
	return float32(mapped / float64(a.display))
}

func stepClock(at time.Time) string {
	if at.IsZero() {
		return "暂不支持"
	}
	return at.Local().Format("15:04:05")
}
func supportedText(s string) string {
	if s == "" {
		return "暂不支持"
	}
	return s
}

// A tool's usage/model cannot be attributed to a surrounding model request.
func stepFacts(s sessionStep, agent session.Agent) [][2]string {
	model, usage, rate, timeout, interrupted, source := "暂不支持", "暂不支持", "暂不支持", "暂不支持", "暂不支持", "暂不支持"
	tool, skill := "暂不支持", supportedText(s.skills)
	if r := s.request; r != nil {
		model = supportedText(r.Model)
		if r.UsageKnown {
			usage = fmt.Sprintf("%s · 输入 %s / 输出 %s / 缓存读 %s / 写 %s", tokenNumber(r.Tokens.Total()), tokenNumber(r.Tokens.Input), tokenNumber(r.Tokens.Output), tokenNumber(r.Tokens.CacheRead), tokenNumber(r.Tokens.CacheWrite))
		}
		if v, ok := r.OutputRate(); ok {
			rate = fmt.Sprintf("≈ %.1f tok/s", v)
		}
	}
	if t := s.tool; t != nil {
		tool = supportedText(t.Name)
		if session.LookupAgent(agent).ToolOutcome {
			timeout = "未记录"
			interrupted = "未记录"
		}
		if t.TimedOutAfter > 0 {
			timeout = "是 · 阈值 " + session.FormatDuration(t.TimedOutAfter)
		}
		if t.Interrupted {
			interrupted = "是"
		}
		source = supportedText(t.PermissionSource)
		if t.PermissionDecision != "" {
			source += " · " + t.PermissionDecision
		}
	}
	if child := s.child; child != nil {
		models := []string{}
		tokens := session.Tokens{}
		known, missing := false, false
		var output int64
		var generation time.Duration
		for _, t := range child.Turns {
			for _, r := range t.Requests {
				if r.Model != "" && !slices.Contains(models, r.Model) {
					models = append(models, r.Model)
				}
				if r.UsageKnown {
					known = true
					tokens.Input += r.Tokens.Input
					tokens.Output += r.Tokens.Output
					tokens.CacheRead += r.Tokens.CacheRead
					tokens.CacheWrite += r.Tokens.CacheWrite
				} else {
					missing = true
				}
				if _, ok := r.OutputRate(); ok {
					output += r.Tokens.Output
					generation += r.Generation.Value
				}
			}
			if t.Interrupted {
				interrupted = "是"
			}
		}
		model = supportedText(strings.Join(models, "、"))
		if known {
			usage = tokenNumber(tokens.Total())
			if missing {
				usage += "（部分请求）"
			}
		}
		if generation > 0 {
			rate = fmt.Sprintf("≈ %.1f tok/s", float64(output)/generation.Seconds())
		}
	}
	if ev := s.event; ev != nil {
		if ev.Kind == "timeout" {
			timeout = "是"
			if ev.Duration.Known {
				timeout += " · 阈值 " + session.FormatDuration(ev.Duration.Value)
			}
		}
		if ev.Kind == "interrupted" {
			interrupted = "是"
		}
	}
	var facts [][2]string
	if s.request != nil || s.child != nil {
		facts = append(facts, [2]string{"模型", model}, [2]string{"Token", usage}, [2]string{"输出速率", rate})
	}
	if s.tool != nil {
		facts = append(facts, [2]string{"工具", tool})
		if s.tool.Name == "Skill" || s.skills != "" {
			facts = append(facts, [2]string{"Skill", skill})
		}
		facts = append(facts, [2]string{"是否超时", timeout}, [2]string{"是否中断", interrupted})
	}
	if s.child != nil && s.tool == nil {
		facts = append(facts, [2]string{"是否中断", interrupted})
	}
	if s.tool != nil && s.tool.PermissionWait || s.wait != nil && s.wait.Kind == "permission" {
		facts = append(facts, [2]string{"权限等待来源", source})
	} else if s.tool != nil && s.tool.PermissionSource != "" {
		facts = append(facts, [2]string{"权限决定来源", source})
	}
	if s.wait != nil {
		names := map[string]string{"reply": "回复间隔", "permission": "权限确认间隔（包含执行）", "away": "离开相关间隔（非精确离开）"}
		facts = append(facts, [2]string{"等待类型", supportedText(names[s.wait.Kind])})
	}
	if s.request != nil && s.request.Retry {
		facts = append(facts, [2]string{"重试依据", "前一次请求失败；日志推断"})
	}
	if s.event != nil {
		switch s.event.Kind {
		case "timeout":
			facts = append(facts, [2]string{"是否超时", timeout})
		case "interrupted":
			facts = append(facts, [2]string{"是否中断", interrupted})
		case "compact":
			facts = append(facts, [2]string{"压缩耗时", stepDuration(s)})
		}
	}
	return facts
}
