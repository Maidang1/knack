package session

import "time"

type factoryFormat struct {
	flatLayout
	typedRecords
}

func (factoryFormat) Info() AgentInfo {
	return AgentInfo{ID: Factory, Name: "Factory", Skills: true, ToolOutcome: true,
		UsageNote: "Factory 本地日志没有 Token 用量字段，Token、缓存与输出速率暂不支持",
		RateNote:  "没有输出 Token 字段，tok/s 暂不支持"}
}
func (factoryFormat) version() string { return "factory/1" }
func (factoryFormat) layout() layout {
	return layout{roots: []string{".factory/sessions"}, suffixes: []string{".jsonl"}}
}
func (factoryFormat) recordID(o object) string { return str(o, "id") }

// Factory v2 on this machine has modelId and Claude-shaped tool blocks, but no
// usage on messages. Hook messages have role=user too; they are not new turns.
func (factoryFormat) parse(p *parser, o object, at time.Time) {
	s := p.s
	switch str(o, "type") {
	case "session_start":
		s.FormatKnown = true
		s.Session.ID = first(str(o, "id"), s.Session.ID)
		s.Session.Title = str(o, "title")
		s.Session.CWD = str(o, "cwd")
	case "message":
		m := obj(o, "message")
		bs := blocks(m)
		if str(m, "hookEventName") != "" {
			return
		}
		if str(m, "role") == "user" {
			if isToolResult(bs) {
				for _, b := range bs {
					if str(b, "type") == "tool_result" {
						p.result(str(b, "tool_use_id"), at, o, b)
					}
				}
			} else if hasUserContent(m, bs) {
				p.begin(str(o, "id"), at)
			}
		} else if str(m, "role") == "assistant" {
			var think time.Duration
			hasTool := false
			for _, b := range bs {
				if str(b, "type") == "thinking" {
					think += millis(num(b, "durationMs"))
				}
			}
			p.request(str(o, "id"), str(m, "modelId"), at, Tokens{}, false, think, false)
			for _, b := range bs {
				if str(b, "type") == "tool_use" {
					hasTool = true
					p.use(str(b, "id"), str(b, "name"), at, obj(b, "input"))
				}
			}
			if !hasTool && hasUserContent(m, bs) && !p.pendingTools() {
				s.LastReply = at
			}
		}
	case "compaction_state":
		p.event("compact", at, Duration{})
	case "agent_turn_outcome":
		if str(o, "reason") == "interrupted" {
			p.turn(at).Interrupted = true
			p.event("interrupted", at, Duration{})
		}
	}
}
