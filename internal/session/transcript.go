package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Transcript content is read on demand for replay and kept only in memory. The
// statistics cache never stores it; see the package comment.
const (
	maxTranscriptRead = 512 << 20
	maxEntryText      = 64 << 10
)

type EntryKind string

const (
	EntryUser      EntryKind = "user"      // what the person typed
	EntryContext   EntryKind = "context"   // injected instructions or environment, not typed
	EntryAssistant EntryKind = "assistant" // reply text
	EntryThinking  EntryKind = "thinking"  // reasoning text or summary
	EntryTool      EntryKind = "tool"      // a tool call with its input and output
	EntryEvent     EntryKind = "event"     // compaction, API error, interruption
)

// Entry is one replayable record. RequestID and ToolID match Request.ID and
// ToolCall.ID of the parsed statistics, so replay content and timing join
// without guessing. Start/End are log timestamps.
type Entry struct {
	Kind              EntryKind
	Start, End        time.Time
	RequestID, ToolID string
	Name              string // tool name, model, or event label
	Text              string // message, thinking, tool output, or event detail
	Input             string // tool input
	Error             bool
	Truncated         bool
}

type Transcript struct {
	Entries   []Entry
	Truncated bool // the file exceeded the replay read limit
	Skipped   int  // records that were invalid or too large to show
}

// ReadTranscript reads a log's conversation content for replay. It opens the
// file read-only, bounds every text field, and returns nothing to persist.
func ReadTranscript(ctx context.Context, path string, agent Agent) (*Transcript, error) {
	if formatOf(agent) == nil {
		return nil, fmt.Errorf("该 Agent 暂不支持回放")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开日志")
	}
	defer f.Close()
	var src io.Reader = f
	if strings.HasSuffix(path, ".zst") {
		dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(64<<20))
		if err != nil {
			return nil, fmt.Errorf("无法解压日志")
		}
		defer dec.Close()
		src = dec
	}
	out := &Transcript{}
	tr := transcriptReader{out: out, f: formatOf(agent), tools: map[string]int{}, seen: map[string]bool{}}
	reader := bufio.NewReaderSize(io.LimitReader(src, maxTranscriptRead), 64<<10)
	var buf []byte
	large := false
	var read int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		read += int64(len(fragment))
		if !large && len(buf)+len(fragment) <= maxLineBytes {
			buf = append(buf, fragment...)
		} else {
			if !large {
				if record := largeRecordHeader(buf[:min(len(buf), largeHeadBytes)]); record != nil {
					tr.line(record)
				} else {
					out.Skipped++
				}
			}
			large, buf = true, buf[:0]
		}
		end := len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
		if end || readErr == io.EOF {
			if !large && len(bytes.TrimSpace(buf)) > 0 && !tr.line(buf) {
				out.Skipped++
			}
			buf, large = buf[:0], false
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, fmt.Errorf("读取日志失败")
			}
			break
		}
	}
	out.Truncated = read >= maxTranscriptRead
	return out, nil
}

type transcriptReader struct {
	out   *Transcript
	f     format
	tools map[string]int
	seen  map[string]bool
	model string
	// Codex: entries since the last usage record belong to the next response.
	pending []int
	timing  map[string][2]time.Time
}

func (tr *transcriptReader) add(e Entry) int {
	e.Text, e.Truncated = clip(e.Text)
	var cut bool
	e.Input, cut = clip(e.Input)
	e.Truncated = e.Truncated || cut
	tr.out.Entries = append(tr.out.Entries, e)
	return len(tr.out.Entries) - 1
}

func (tr *transcriptReader) result(id string, at time.Time, text string, failed bool) {
	i, ok := tr.tools[id]
	if !ok {
		return
	}
	e := &tr.out.Entries[i]
	if !e.End.IsZero() {
		return
	}
	e.End = at
	e.Error = e.Error || failed
	var cut bool
	e.Text, cut = clip(text)
	e.Truncated = e.Truncated || cut
}

func (tr *transcriptReader) line(b []byte) bool {
	var o object
	if json.Unmarshal(b, &o) != nil || o == nil {
		return false
	}
	if id := tr.f.recordID(o); id != "" {
		if tr.seen[id] {
			return true
		}
		tr.seen[id] = true
	}
	at, _ := tr.f.record(o)
	tr.f.replay(tr, o, at)
	return true
}

func (claudeFormat) replay(tr *transcriptReader, o object, at time.Time) {
	switch str(o, "type") {
	case "user":
		m := obj(o, "message")
		bs := blocks(m)
		if isToolResult(bs) {
			for _, b := range bs {
				if str(b, "type") == "tool_result" {
					tr.result(str(b, "tool_use_id"), at, contentText(b["content"]), flag(b, "is_error"))
				}
			}
			return
		}
		if !hasUserContent(m, bs) {
			return
		}
		text := contentText(m["content"])
		kind := userKind(text)
		if flag(o, "isMeta") || flag(o, "isCompactSummary") {
			kind = EntryContext
		}
		tr.userEntry(kind, at, text)
	case "assistant":
		m := obj(o, "message")
		rid := first(str(m, "id"), str(o, "requestRef"), str(o, "requestId"), str(o, "uuid"))
		tr.assistant(blocks(m), rid, str(m, "model"), at)
		if flag(o, "isApiErrorMessage") {
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, RequestID: rid, Name: "API 请求失败", Text: contentText(m["content"]), Error: true})
		}
	case "system":
		switch str(o, "subtype") {
		case "compact_boundary":
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "上下文压缩"})
		case "api_error":
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "API 请求失败", Text: str(o, "content"), Error: true})
		case "away_summary":
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "离开摘要", Text: str(o, "content")})
		}
	}
}

func (tr *transcriptReader) assistant(bs []object, rid, model string, at time.Time) {
	for _, b := range bs {
		switch str(b, "type") {
		case "thinking", "redacted_thinking":
			text := str(b, "thinking")
			tr.add(Entry{Kind: EntryThinking, Start: at, End: at, RequestID: rid, Name: model, Text: text})
		case "text":
			if text := str(b, "text"); strings.TrimSpace(text) != "" {
				tr.add(Entry{Kind: EntryAssistant, Start: at, End: at, RequestID: rid, Name: model, Text: text})
			}
		case "tool_use":
			id := str(b, "id")
			if _, ok := tr.tools[id]; ok || id == "" {
				continue
			}
			tr.tools[id] = tr.add(Entry{Kind: EntryTool, Start: at, RequestID: rid, ToolID: id, Name: str(b, "name"), Input: jsonText(b["input"])})
		}
	}
}

func (factoryFormat) replay(tr *transcriptReader, o object, at time.Time) {
	switch str(o, "type") {
	case "message":
		m := obj(o, "message")
		bs := blocks(m)
		if str(m, "hookEventName") != "" {
			return
		}
		switch str(m, "role") {
		case "user":
			if isToolResult(bs) {
				for _, b := range bs {
					if str(b, "type") == "tool_result" {
						tr.result(str(b, "tool_use_id"), at, contentText(b["content"]), flag(b, "is_error"))
					}
				}
			} else if hasUserContent(m, bs) {
				text := contentText(m["content"])
				tr.userEntry(userKind(text), at, text)
			}
		case "assistant":
			tr.assistant(bs, str(o, "id"), str(m, "modelId"), at)
		}
	case "compaction_state":
		tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "上下文压缩"})
	case "agent_turn_outcome":
		if str(o, "reason") == "interrupted" {
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "已中断", Error: true})
		}
	}
}

func (codexFormat) replay(tr *transcriptReader, o object, at time.Time) {
	pl := obj(o, "payload")
	switch str(o, "type") {
	case "turn_context":
		tr.model = str(pl, "model")
	case "token_usage_record":
		rid := str(pl, "response_id")
		for _, i := range tr.pending {
			tr.out.Entries[i].RequestID = rid
		}
		tr.pending = tr.pending[:0]
	case "compacted":
		tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "上下文压缩"})
	case "event_msg":
		switch str(pl, "type") {
		case "item_completed":
			// Completion events precede the matching response item and carry
			// its measured start and end.
			item := obj(pl, "item")
			from, to := num(pl, "started_at_ms"), num(pl, "completed_at_ms")
			if from > 0 && to >= from {
				if tr.timing == nil {
					tr.timing = map[string][2]time.Time{}
				}
				tr.timing[str(item, "type")] = [2]time.Time{time.UnixMilli(from), time.UnixMilli(to)}
			}
		case "turn_aborted":
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "已中断", Error: true})
		case "error":
			tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "错误", Text: str(pl, "message"), Error: true})
		}
	case "response_item":
		measured := func(kind string) (time.Time, time.Time) {
			if span, ok := tr.timing[kind]; ok {
				delete(tr.timing, kind)
				return span[0], span[1]
			}
			return at, at
		}
		switch str(pl, "type") {
		case "message":
			text := contentText(pl["content"])
			switch str(pl, "role") {
			case "user":
				kind := EntryUser
				if codexInjected(text) {
					kind = EntryContext
				}
				tr.add(Entry{Kind: kind, Start: at, End: at, Text: text})
			case "developer", "system":
				tr.add(Entry{Kind: EntryContext, Start: at, End: at, Text: text})
			case "assistant":
				start, end := measured("AgentMessage")
				tr.pending = append(tr.pending, tr.add(Entry{Kind: EntryAssistant, Start: start, End: end, Name: tr.model, Text: text}))
			}
		case "reasoning":
			var parts []string
			for _, s := range objects(pl["summary"]) {
				parts = append(parts, str(s, "text"))
			}
			start, end := measured("Reasoning")
			tr.pending = append(tr.pending, tr.add(Entry{Kind: EntryThinking, Start: start, End: end, Name: tr.model, Text: strings.Join(parts, "\n\n")}))
		case "function_call", "custom_tool_call", "local_shell_call", "web_search_call", "tool_search_call":
			id := first(str(pl, "call_id"), str(pl, "id"))
			if _, ok := tr.tools[id]; ok || id == "" {
				return
			}
			input := str(pl, "arguments")
			if input == "" {
				input = str(pl, "input")
			}
			if input == "" {
				input = jsonText(first2(pl["action"], pl["arguments"], pl["input"]))
			}
			name := first(str(pl, "name"), strings.TrimSuffix(str(pl, "type"), "_call"))
			i := tr.add(Entry{Kind: EntryTool, Start: at, ToolID: id, Name: name, Input: prettyJSON(input)})
			tr.tools[id] = i
			tr.pending = append(tr.pending, i)
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "tool_search_output":
			output := str(pl, "output")
			if output == "" {
				output = contentText(pl["output"])
			}
			tr.result(str(pl, "call_id"), at, output, false)
		}
	}
}

func (tr *transcriptReader) userEntry(kind EntryKind, at time.Time, text string) {
	if strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted by user") {
		tr.add(Entry{Kind: EntryEvent, Start: at, End: at, Name: "已中断", Text: "用户中断了请求", Error: true})
		return
	}
	tr.add(Entry{Kind: kind, Start: at, End: at, Text: text})
}

// Claude Code and Factory put reminders and command output in user records.
func userKind(text string) EntryKind {
	t := strings.TrimSpace(text)
	for _, prefix := range []string{"<system-reminder>", "<local-command-caveat>", "<local-command-stdout>", "<local-command-stderr>", "Caveat: "} {
		if strings.HasPrefix(t, prefix) {
			return EntryContext
		}
	}
	return EntryUser
}

// Codex sends environment and AGENTS.md instructions as user-role messages.
func codexInjected(text string) bool {
	t := strings.TrimSpace(text)
	for _, prefix := range []string{"<environment_context>", "<user_instructions>", "# AGENTS.md", "<permissions", "<turn_aborted>", "<skill>", "<subagent_notification>"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func objects(raw json.RawMessage) []object {
	var out []object
	_ = json.Unmarshal(raw, &out)
	return out
}

func first2(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

// contentText flattens a string or a list of content blocks to display text.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range objects(raw) {
		switch str(b, "type") {
		case "text", "input_text", "output_text":
			parts = append(parts, str(b, "text"))
		case "image", "input_image":
			parts = append(parts, "[图片]")
		case "tool_result":
			parts = append(parts, contentText(b["content"]))
		default:
			if text := str(b, "text"); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return prettyJSON(s)
	}
	var out bytes.Buffer
	if json.Indent(&out, raw, "", "  ") == nil {
		return out.String()
	}
	return string(raw)
}

// prettyJSON indents a string that holds a JSON object; other text is kept.
func prettyJSON(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return s
	}
	var out bytes.Buffer
	if json.Indent(&out, []byte(t), "", "  ") == nil {
		return out.String()
	}
	return s
}

func clip(s string) (string, bool) {
	if len(s) <= maxEntryText {
		return s, false
	}
	cut := maxEntryText
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut], true
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
