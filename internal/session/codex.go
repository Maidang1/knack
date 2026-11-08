package session

import (
	"strings"
	"time"
)

type codexFormat struct {
	flatLayout
	typedRecords
}

func (codexFormat) Info() AgentInfo {
	return AgentInfo{ID: Codex, Name: "Codex", Skills: true,
		RateNote:   "无法可靠关联请求起止时间，tok/s 暂不支持",
		TimingNote: "Codex 请求耗时按输入完成到用量记录的间隔估算；旧日志缺少逐请求用量时不计为零。"}
}
func (codexFormat) version() string { return "codex/3" }

// Codex compresses older and archived rollouts in place.
func (codexFormat) layout() layout {
	return layout{roots: []string{".codex/sessions", ".codex/archived_sessions"}, suffixes: []string{".jsonl", ".jsonl.zst"}}
}
func (codexFormat) recordID(o object) string { return str(o, "uuid") }

func (codexFormat) parse(p *parser, o object, at time.Time) {
	s := p.s
	pl := obj(o, "payload")
	switch str(o, "type") {
	case "session_meta":
		s.FormatKnown = true
		s.Session.ID = first(str(pl, "id"), str(pl, "session_id"), s.Session.ID)
		s.Session.CWD = str(pl, "cwd")
		s.Session.GitBranch = str(obj(pl, "git"), "branch")
		spawn := obj(obj(obj(pl, "source"), "subagent"), "thread_spawn")
		if parent := str(spawn, "parent_thread_id"); parent != "" {
			s.Session.ParentID = parent
			s.Session.AgentID = s.Session.ID
		}
	case "turn_context":
		s.Model = str(pl, "model")
	case "token_usage_record":
		id := str(pl, "response_id")
		if id == "" {
			return
		}
		if _, exists := p.requests[id]; exists {
			return
		}
		p.request(id, s.Model, at, codexTokens(obj(pl, "usage")), knownUsage(obj(pl, "usage")), 0, false)
		// A response starts once its input is complete: the turn start, a user
		// message or the last tool output. Its usage record marks the end. The
		// interval is an estimate like Claude's; reasoning items carry their own
		// measured start and end, which gives the thinking part.
		pos := p.requests[id]
		r := &s.Session.Turns[pos.turn].Requests[pos.item]
		r.Start, r.Elapsed, r.Thinking, r.Generation = time.Time{}, Duration{}, Duration{}, Duration{}
		if !s.InputAt.IsZero() && !at.Before(s.InputAt) {
			r.Start = s.InputAt
			r.Elapsed = estimated(at.Sub(s.InputAt))
			if s.Think > 0 {
				r.Thinking = Duration{Value: min(s.Think, r.Elapsed.Value), Known: true}
			}
			if r.Elapsed.Value > r.Thinking.Value {
				r.Generation = estimated(r.Elapsed.Value - r.Thinking.Value)
			}
		}
		s.InputAt, s.Think = at, 0
	case "response_item":
		switch str(pl, "type") {
		case "message":
			if str(pl, "role") == "assistant" {
				s.LastReply = at
			}
			if str(pl, "role") == "user" {
				s.InputAt, s.Think = at, 0
				// The first typed prompt of a turn; injected context such as
				// <environment_context> or AGENTS.md comes as user text too.
				text := strings.TrimSpace(contentText(pl["content"]))
				if !s.Prompted && text != "" && !strings.HasPrefix(text, "<") && !strings.HasPrefix(text, "# AGENTS.md") && len(s.Session.Turns) > 0 {
					s.Prompted = true
					if len(s.Session.Turns) > 1 {
						s.Session.Turns[len(s.Session.Turns)-1].Corrects = correction(text)
					}
				}
			}
			// task_started is the observed authoritative turn boundary. User
			// messages can also be replayed after compaction, so do not count them.
		case "function_call", "custom_tool_call":
			raw := first2(pl["arguments"], pl["input"])
			p.use(str(pl, "call_id"), codexToolName(pl), at, raw)
			p.codexSkills(str(pl, "call_id"), at, raw)
		case "function_call_output", "custom_tool_call_output":
			p.result(str(pl, "call_id"), at, nil, nil)
			p.output(str(pl, "call_id"), contentText(pl["output"]))
			s.InputAt, s.Think = at, 0
		}
	case "compacted":
		p.event("compact", at, Duration{})
	case "event_msg":
		switch str(pl, "type") {
		case "item_completed":
			item := obj(pl, "item")
			if str(item, "type") == "Reasoning" {
				from, to := num(pl, "started_at_ms"), num(pl, "completed_at_ms")
				if from > 0 && to > from {
					s.Think += millis(to - from)
				}
			}
		case "task_started":
			s.InputAt, s.Think, s.Prompted = at, 0, false
			id := str(pl, "turn_id")
			for _, t := range s.Session.Turns {
				if t.ID == id && id != "" {
					return
				}
			}
			p.begin(id, at)
		case "task_complete", "turn_aborted":
			// Completion can arrive after another turn started: use its ID.
			id := str(pl, "turn_id")
			for i := len(s.Session.Turns) - 1; i >= 0; i-- {
				t := &s.Session.Turns[i]
				if id != "" && t.ID != id {
					continue
				}
				t.End = at
				t.Elapsed = Duration{Value: millis(num(pl, "duration_ms")), Known: pl["duration_ms"] != nil}
				if str(pl, "type") == "turn_aborted" {
					t.Interrupted = true
					t.Events = append(t.Events, Event{Kind: "interrupted", At: at})
				} else if i == len(s.Session.Turns)-1 && s.LastReply.IsZero() {
					s.LastReply = at
				}
				break
			}
		}
	}
}

// codexToolName qualifies an MCP tool with its namespace, mcp__<server>,
// as Claude Code names them: mcp__<server>__<tool>.
func codexToolName(pl object) string {
	name, ns := str(pl, "name"), str(pl, "namespace")
	if strings.HasPrefix(ns, "mcp__") && name != "" && !strings.HasPrefix(name, "mcp__") {
		return ns + "__" + name
	}
	return name
}
