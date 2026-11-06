package session

import (
	"path/filepath"
	"strings"
	"time"
)

type claudeFormat struct{ typedRecords }

func (claudeFormat) Info() AgentInfo {
	return AgentInfo{ID: Claude, Name: "Claude", Skills: true, OutputRate: true, PermissionWait: true, ToolOutcome: true}
}
func (claudeFormat) version() string { return "claude/3" }
func (claudeFormat) layout() layout {
	return layout{roots: []string{".claude/projects"}, suffixes: []string{".jsonl"}}
}

// Sessions are projects/<project>/<session>.jsonl; sub-Agents are
// projects/<project>/<session>/subagents/agent-<id>.jsonl.
func (claudeFormat) visit(parts []string, dir bool) (string, string, bool) {
	if dir {
		return "", "", len(parts) != 3 || parts[2] == "subagents"
	}
	switch {
	case len(parts) == 2:
		return "", "", true
	case len(parts) == 4 && parts[2] == "subagents":
		return parts[1], strings.TrimSuffix(strings.TrimPrefix(parts[3], "agent-"), ".jsonl"), true
	}
	return "", "", false
}
func (claudeFormat) parentLog(f logFile) string {
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(f.path))), f.parent+".jsonl")
}
func (claudeFormat) recordID(o object) string { return str(o, "uuid") }

func (claudeFormat) parse(p *parser, o object, at time.Time) {
	s := p.s
	if s.Session.ParentID == "" {
		s.Session.ID = first(str(o, "sessionId"), s.Session.ID)
	}
	s.Session.CWD = first(str(o, "cwd"), s.Session.CWD)
	s.Session.GitBranch = first(str(o, "gitBranch"), s.Session.GitBranch)
	s.Session.Title = first(str(o, "aiTitle"), s.Session.Title)
	switch str(o, "type") {
	case "user":
		s.FormatKnown = true
		m := obj(o, "message")
		bs := blocks(m)
		if isToolResult(bs) {
			for _, b := range bs {
				if str(b, "type") == "tool_result" {
					p.result(str(b, "tool_use_id"), at, o, b)
					p.output(str(b, "tool_use_id"), contentText(b["content"]))
				}
			}
		} else if flag(o, "isMeta") {
			// A loaded skill's instructions follow its Skill call or the
			// /command that invoked it.
			if id := str(o, "sourceToolUseID"); id != "" || flag(o, "turnCompanion") {
				p.skillContent(id, flag(o, "turnCompanion"), at, contentText(m["content"]))
			}
		} else if !flag(o, "isMeta") && !flag(o, "isCompactSummary") && hasUserContent(m, bs) {
			// Claude Code records an interruption as a user message. It ends
			// the current turn; it is not a new prompt.
			if strings.HasPrefix(strings.TrimSpace(contentText(m["content"])), "[Request interrupted by user") && len(s.Session.Turns) > 0 {
				t := p.turn(at)
				if !t.Interrupted {
					t.Interrupted = true
					t.Events = append(t.Events, Event{Kind: "interrupted", At: at})
				}
				s.LastReply = at
				return
			}
			p.begin(str(o, "uuid"), at)
			text := contentText(m["content"])
			s.Command = slashCommand(text)
			if s.Command == "" && len(s.Session.Turns) > 1 {
				s.Session.Turns[len(s.Session.Turns)-1].Corrects = correction(text)
			}
		}
	case "assistant":
		s.FormatKnown = true
		m := obj(o, "message")
		id := first(str(m, "id"), str(o, "requestRef"), str(o, "requestId"), str(o, "uuid"))
		p.request(id, str(m, "model"), at, claudeTokens(obj(m, "usage")), knownUsage(obj(m, "usage")), millis(num(o, "thinkingDurationMs")), flag(o, "isApiErrorMessage"))
		hasTool := false
		for _, b := range blocks(m) {
			if str(b, "type") == "tool_use" {
				hasTool = true
				p.use(str(b, "id"), str(b, "name"), at, b["input"])
			}
		}
		if flag(o, "isApiErrorMessage") {
			p.event("api_error", at, Duration{})
			s.LastReply = time.Time{}
		} else if !hasTool && (str(m, "stop_reason") == "end_turn" || (hasUserContent(m, blocks(m)) && !p.pendingTools())) {
			s.LastReply = at
		}
	case "system":
		switch str(o, "subtype") {
		case "turn_duration":
			t := p.turn(at)
			t.Elapsed = Duration{Value: millis(num(o, "durationMs")), Known: o["durationMs"] != nil}
		case "compact_boundary":
			meta := obj(o, "compactMetadata")
			p.event("compact", at, Duration{Value: millis(num(meta, "durationMs")), Known: meta["durationMs"] != nil})
		case "away_summary":
			s.Away = true
			p.event("away_summary", at, Duration{})
		}
	}
}

func (p *parser) pendingTools() bool {
	for _, t := range p.s.Session.Turns {
		for _, tool := range t.Tools {
			if tool.End.IsZero() {
				return true
			}
		}
	}
	return false
}
