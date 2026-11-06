package session

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type object map[string]json.RawMessage

func decode(b []byte) object {
	var o object
	_ = json.Unmarshal(b, &o)
	return o
}
func str(o object, k string) string { var v string; _ = json.Unmarshal(o[k], &v); return v }
func num(o object, k string) int64  { var v int64; _ = json.Unmarshal(o[k], &v); return max(v, 0) }
func flag(o object, k string) bool  { var v bool; _ = json.Unmarshal(o[k], &v); return v }
func obj(o object, k string) object { return decode(o[k]) }
func stamp(o object) time.Time      { t, _ := time.Parse(time.RFC3339Nano, str(o, "timestamp")); return t }
func blocks(m object) []object      { var b []object; _ = json.Unmarshal(m["content"], &b); return b }
func millis(n int64) time.Duration {
	// Bound corrupt or hostile values before multiplying to avoid overflow.
	return time.Duration(min(n, int64((1<<63-1)/time.Millisecond))) * time.Millisecond
}

// parseState contains only the checkpoint and structured display data. All
// indices are rebuilt per changed file, keeping the on-disk cache portable.
type parseState struct {
	Session     Session
	Previous    time.Time
	LastReply   time.Time
	Away        bool
	Model       string
	Sequence    int64
	Seen        map[string]bool
	FormatKnown bool
	// Codex: when the next response's input was complete (turn start, user
	// message or tool output) and the reasoning time observed since then.
	InputAt time.Time
	Think   time.Duration
}

type position struct{ turn, item int }
type parser struct {
	s        *parseState
	f        format
	requests map[string]position
	tools    map[string]position
}

func newParser(s *parseState) *parser {
	p := &parser{s: s, f: formatOf(s.Session.Agent), requests: map[string]position{}, tools: map[string]position{}}
	if s.Seen == nil {
		s.Seen = map[string]bool{}
	}
	for ti, t := range s.Session.Turns {
		for i, r := range t.Requests {
			p.requests[r.ID] = position{ti, i}
		}
		for i, r := range t.Tools {
			p.tools[r.ID] = position{ti, i}
		}
	}
	return p
}

func (p *parser) line(b []byte) error {
	var o object
	if err := json.Unmarshal(b, &o); err != nil {
		return fmt.Errorf("无效 JSON 记录")
	}
	if p.f == nil {
		return fmt.Errorf("不支持的 Agent")
	}
	at, ok := p.f.record(o)
	if o == nil || !ok {
		return fmt.Errorf("缺少记录类型")
	}
	s := p.s
	s.Sequence++
	// Checkpoints prevent replay on append; IDs also suppress duplicated records
	// in copied/compacted histories. Only message IDs, never raw lines, persist.
	if id := p.f.recordID(o); id != "" {
		if s.Seen[id] {
			return nil
		}
		s.Seen[id] = true
	}
	if !at.IsZero() {
		if s.Session.Start.IsZero() || at.Before(s.Session.Start) {
			s.Session.Start = at
		}
		if at.After(s.Session.End) {
			s.Session.End = at
		}
	}
	p.f.parse(p, o, at)
	// The previous *timestamped* record is used, including metadata records.
	// Untimestamped api-request-blob records must not erase the timing anchor.
	if !at.IsZero() {
		s.Previous = at
	}
	return nil
}

func (p *parser) turn(at time.Time) *Turn {
	if len(p.s.Session.Turns) == 0 {
		p.begin("", at)
	}
	t := &p.s.Session.Turns[len(p.s.Session.Turns)-1]
	if t.End.IsZero() || at.After(t.End) {
		t.End = at
	}
	return t
}

func (p *parser) begin(id string, at time.Time) {
	s := p.s
	if !s.LastReply.IsZero() && !at.IsZero() && !at.Before(s.LastReply) && len(s.Session.Turns) > 0 {
		kind := "reply"
		if s.Away {
			kind = "away"
		}
		t := &s.Session.Turns[len(s.Session.Turns)-1]
		t.Waits = append(t.Waits, Wait{Start: s.LastReply, End: at, Elapsed: estimated(at.Sub(s.LastReply)), Kind: kind})
	}
	s.LastReply = time.Time{}
	s.Away = false
	if id == "" {
		id = fmt.Sprintf("turn-%d", len(s.Session.Turns)+1)
	}
	s.Session.Turns = append(s.Session.Turns, Turn{ID: id, Start: at, End: at})
}

func (p *parser) event(kind string, at time.Time, d Duration) {
	t := p.turn(at)
	t.Events = append(t.Events, Event{Kind: kind, At: at, Duration: d})
}

func (p *parser) request(id, model string, at time.Time, usage Tokens, known bool, think time.Duration, apiError bool) {
	if id == "" {
		id = fmt.Sprintf("record-%d", p.s.Sequence)
	}
	pos, exists := p.requests[id]
	if !exists {
		t := p.turn(at)
		retry := len(t.Requests) > 0 && t.Requests[len(t.Requests)-1].APIError
		t.Requests = append(t.Requests, Request{ID: id, Model: model, End: at, Retry: retry})
		pos = position{len(p.s.Session.Turns) - 1, len(t.Requests) - 1}
		p.requests[id] = pos
	}
	r := &p.s.Session.Turns[pos.turn].Requests[pos.item]
	if model != "" {
		r.Model = model
	}
	if !at.IsZero() && !p.s.Previous.IsZero() && !at.Before(p.s.Previous) {
		if r.Start.IsZero() {
			r.Start = p.s.Previous
		}
		if !exists || at.After(r.End) {
			r.Elapsed.Value += at.Sub(p.s.Previous)
			r.Elapsed.Known = true
			r.Elapsed.Estimated = true
		}
	}
	if at.After(r.End) {
		r.End = at
	}
	r.UsageKnown = r.UsageKnown || known
	// Claude emits the same usage on several content blocks; output usage can
	// also grow as streaming completes. Componentwise maxima avoid double count.
	r.Tokens = Tokens{max(r.Tokens.Input, usage.Input), max(r.Tokens.Output, usage.Output), max(r.Tokens.CacheRead, usage.CacheRead), max(r.Tokens.CacheWrite, usage.CacheWrite)}
	if think > 0 {
		r.Thinking.Value += think
		r.Thinking.Known = true
	}
	r.APIError = r.APIError || apiError
	r.Generation = Duration{}
	if r.Elapsed.Known && (!r.Thinking.Known || r.Elapsed.Value > r.Thinking.Value) {
		r.Generation = estimated(r.Elapsed.Value - r.Thinking.Value)
	}
}

func (p *parser) use(id, name string, at time.Time, raw json.RawMessage) {
	if id == "" {
		return
	}
	if _, ok := p.tools[id]; ok {
		return
	}
	t := p.turn(at)
	t.Tools = append(t.Tools, ToolCall{ID: id, Name: name, Start: at, InputHash: inputHash(raw)})
	p.tools[id] = position{len(p.s.Session.Turns) - 1, len(t.Tools) - 1}
	if name == "Skill" {
		if skill := str(decode(raw), "skill"); skill != "" {
			t.Skills = append(t.Skills, SkillCall{Name: skill, ToolID: id, At: at})
		}
	}
	p.s.LastReply = time.Time{}
}

func (p *parser) result(id string, at time.Time, o, block object) {
	pos, ok := p.tools[id]
	if !ok {
		return
	}
	t := &p.s.Session.Turns[pos.turn]
	tool := &t.Tools[pos.item]
	if !tool.End.IsZero() {
		return
	}
	tool.End = at
	if !at.IsZero() && !tool.Start.IsZero() {
		tool.Elapsed = estimated(at.Sub(tool.Start))
	}
	res := obj(o, "toolUseResult")
	tool.Interrupted = flag(res, "interrupted")
	tool.Failed = flag(block, "is_error")
	tool.TimedOutAfter = millis(num(res, "timedOutAfterMs"))
	tool.AgentID = str(res, "agentId")
	decision := obj(o, "permissionDecision")
	tool.PermissionDecision = str(decision, "decision")
	tool.PermissionSource = str(decision, "source")
	// Only the observed explicit user-confirmation source counts. Automatic
	// config/classifier decisions do not imply human waiting.
	tool.PermissionWait = tool.PermissionSource == "user_temporary"
	if tool.PermissionWait && tool.Elapsed.Known {
		t.Waits = append(t.Waits, Wait{Start: tool.Start, End: at, Elapsed: tool.Elapsed, Kind: "permission", ToolID: id})
	}
	if tool.Interrupted {
		t.Interrupted = true
		t.Events = append(t.Events, Event{Kind: "interrupted", At: at})
	}
	if tool.TimedOutAfter > 0 {
		t.Events = append(t.Events, Event{Kind: "timeout", At: at, Duration: Duration{Value: tool.TimedOutAfter, Known: true}})
	}
	if at.After(t.End) {
		t.End = at
	}
}

func claudeTokens(o object) Tokens {
	return Tokens{num(o, "input_tokens"), num(o, "output_tokens"), num(o, "cache_read_input_tokens"), num(o, "cache_creation_input_tokens")}
}
func codexTokens(o object) Tokens {
	read, write := num(o, "cached_input_tokens"), num(o, "cache_write_input_tokens")
	return Tokens{max(0, num(o, "input_tokens")-read-write), num(o, "output_tokens"), read, write}
}

// inputHash fingerprints a tool input. Inputs are compacted first, so
// formatting differences between log writers do not split identical calls.
func inputHash(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) == nil {
		raw = buf.Bytes()
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func first(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func knownUsage(o object) bool {
	_, in := o["input_tokens"]
	_, out := o["output_tokens"]
	return in && out
}
func isToolResult(bs []object) bool {
	for _, b := range bs {
		if str(b, "type") == "tool_result" {
			return true
		}
	}
	return false
}
func hasUserContent(m object, bs []object) bool {
	if strings.TrimSpace(str(m, "content")) != "" {
		return true
	}
	for _, b := range bs {
		if str(b, "type") == "text" || str(b, "type") == "image" || str(b, "type") == "input_text" {
			return true
		}
	}
	return false
}
