package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// The turn list and trace replay what the log recorded, joined to the parsed
// timing by request and tool IDs. Replay text is read on demand, not cached.
func TestSessionReplay(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := overviewFixture(t)
				tt := ui.NewTester(a.View, width, 900)
				tt.SetDark(dark)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				var codex, claude session.Session
				for _, s := range a.sessions.snapshot.Sessions {
					switch s.Agent {
					case session.Codex:
						codex = s
					case session.Claude:
						claude = s
					}
				}
				a.openSession(sessionID(claude))
				a.sessions.replayOpen = true
				tt.Frame()
				if !tt.HasText("PRIVATE_USER_PROMPT") || !tt.HasText("PRIVATE_SECOND_PROMPT") {
					t.Fatalf("turn list lacks prompts: %q", tt.Texts())
				}
				if tt.HasText("PRIVATE_COMPACTION_SUMMARY") {
					t.Fatalf("injected context shown: %q", tt.Texts())
				}
				exportSessionPreview(t, tt, fmt.Sprintf("replay-turns-%d-dark-%t.png", width, dark))
				a.sessions.replayOpen = false
				tt.Frame()
				if !tt.HasText("离开 ≈") {
					t.Fatalf("waits between turns missing: %q", tt.Texts())
				}

				a.openSession(sessionID(codex))
				a.sessions.replayOpen = true
				tt.Frame()
				d := a.sessions.detail
				a.chooseSessionTurn(d, 0)
				tt.Frame()
				if !tt.HasText("PRIVATE_CODEX_PROMPT") || !tt.HasText("PRIVATE_CODEX_REPLY") || !tt.HasText("PRIVATE_SCRIPT") {
					t.Fatalf("conversation lacks prompt, reply or tool call: %q", tt.Texts())
				}
				// The turn reads as the agent loop, each step in the order it ran.
				order := []string{"用户输入", "第 1 次模型调用", "模型思考", "模型回复", "调用工具", "工具返回"}
				last := float32(-1)
				for _, step := range order {
					r, ok := tt.Find(step)
					if !ok || r.Y <= last {
						t.Fatalf("loop step %s missing or out of order: %+v %q", step, r, tt.Texts())
					}
					last = r.Y
				}
				// Thinking shows its first line and opens in full; a tool's
				// return shows its first line, its input opens from the call.
				if !tt.HasText("PRIVATE_CODEX_REASONING") || !tt.HasText("PRIVATE_CODEX_TOOL_OUTPUT") {
					t.Fatalf("loop hides thinking or tool output: %q", tt.Texts())
				}
				if _, ok := tt.Find("输入"); ok {
					t.Fatal("tool input shown before the call was opened")
				}
				for _, text := range tt.Texts() {
					if strings.HasPrefix(text, "模型思考 · ") {
						click(t, tt, text)
						break
					}
				}
				if !anyExpanded(a, "think:") {
					t.Fatalf("thinking did not open: %v", a.sessions.expanded)
				}
				for _, text := range tt.Texts() {
					if strings.Contains(text, " · PRIVATE_SCRIPT") {
						click(t, tt, text)
						break
					}
				}
				if !tt.HasText("输入") {
					t.Fatalf("tool call did not open to its input: %q", tt.Texts())
				}
				exportSessionPreview(t, tt, fmt.Sprintf("replay-trace-%d-dark-%t.png", width, dark))
				a.sessions.replayFilter = replayRepliesOnly
				tt.Frame()
				if tt.HasText("PRIVATE_SCRIPT") || !tt.HasText("PRIVATE_CODEX_REPLY") {
					t.Fatal("replies-only filter")
				}
				a.sessions.replayFilter = replayAll
				a.sessions.replayQuery = "private_codex_tool_output"
				tt.Frame()
				if !tt.HasText("1 轮匹配") || tt.HasText("PRIVATE_CODEX_REPLY") {
					t.Fatalf("search %q", tt.Texts())
				}
				a.sessions.replayQuery = ""
				// The chosen step, opened, shows the same content under its row.
				a.sessions.replayOpen, a.sessions.stepOpen = false, true
				for i, s := range d.turns[0].steps {
					if s.request != nil {
						a.sessions.selectedStep = i
						break
					}
				}
				tt.Frame()
				if !tt.HasText("PRIVATE_CODEX_REASONING") || !tt.HasText("回复") {
					t.Fatalf("model request detail lacks thinking/reply: %q", tt.Texts())
				}
				for i, s := range d.turns[0].steps {
					if s.tool != nil {
						a.sessions.selectedStep = i
					}
				}
				tt.Frame()
				if !tt.HasText("输入") || !tt.HasText("PRIVATE_CODEX_TOOL_OUTPUT") {
					t.Fatalf("tool detail lacks input/output: %q", tt.Texts())
				}
				exportSessionPreview(t, tt, fmt.Sprintf("replay-step-open-%d-dark-%t.png", width, dark))
				// Codex request timing is estimated from the completed input.
				r := d.turns[0].turn.Requests[0]
				if !r.Elapsed.Known || !r.Elapsed.Estimated || r.Thinking.Value == 0 {
					t.Fatalf("Codex request timing %+v", r)
				}
				if len(a.sessions.replay) == 0 || a.sessions.replay[0].tr == nil {
					t.Fatal("replay content not held for the open session")
				}
			})
		}
	}
}

func anyExpanded(a *App, prefix string) bool {
	for k, v := range a.sessions.expanded {
		if v && strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// A Claude request logs its thinking as a signature-only block and, when
// shown, a second block with the text: the loop shows one thinking step.
func TestReplayLoopClaudeThinking(t *testing.T) {
	a := New(t.TempDir())
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	at := func(s int) string { return start.Add(time.Duration(s) * time.Second).Format(time.RFC3339) }
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"` + at(0) + `","message":{"role":"user","content":"LOOP_PROMPT"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"` + at(3) + `","message":{"id":"req1","model":"claude-test","content":[{"type":"thinking","thinking":"","signature":"SIG"}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"` + at(3) + `","message":{"id":"req1","model":"claude-test","content":[{"type":"thinking","thinking":"LOOP_THOUGHT","signature":"SIG"}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"` + at(4) + `","message":{"id":"req1","model":"claude-test","content":[{"type":"tool_use","id":"tool1","name":"Bash","input":{"command":"LOOP_COMMAND"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"` + at(6) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool1","content":"LOOP_OUTPUT"}]}}`,
		`{"type":"assistant","uuid":"a4","timestamp":"` + at(8) + `","message":{"id":"req2","model":"claude-test","content":[{"type":"thinking","thinking":"","signature":"SIG"}]}}`,
		`{"type":"assistant","uuid":"a5","timestamp":"` + at(9) + `","message":{"id":"req2","model":"claude-test","content":[{"type":"text","text":"LOOP_REPLY"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "loop.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	dur := func(s int) session.Duration {
		return session.Duration{Value: time.Duration(s) * time.Second, Known: true}
	}
	turn := session.Turn{ID: "turn", Start: when(0), End: when(9),
		Requests: []session.Request{{ID: "req1", Model: "claude-test", End: when(4), Elapsed: dur(4)}, {ID: "req2", Model: "claude-test", End: when(9), Elapsed: dur(3)}},
		Tools:    []session.ToolCall{{ID: "tool1", Name: "Bash", Start: when(4), End: when(6), Elapsed: dur(2)}}}
	root := session.Session{ID: "loop", Agent: session.Claude, Path: path, Start: when(0), End: when(9), Turns: []session.Turn{turn}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root))
	a.sessions.replayOpen = true
	tt := ui.NewTester(a.View, 1180, 900)
	for range 200 {
		if tt.HasText("LOOP_REPLY") {
			break
		}
		time.Sleep(5 * time.Millisecond)
		tt.Frame()
	}
	order := []string{"用户输入", "第 1 次模型调用", "模型思考", "调用工具", "工具返回", "第 2 次模型调用", "模型思考", "模型回复"}
	var steps []string
	for _, text := range tt.Texts() {
		if slices.Contains(order, text) {
			steps = append(steps, text)
		}
	}
	if strings.Join(steps, ",") != strings.Join(order, ",") {
		t.Fatalf("loop steps %q, want %q", steps, order)
	}
	// The first request's text wins over its signature; the second has only a signature.
	if !tt.HasText("LOOP_THOUGHT") || !tt.HasText("LOOP_OUTPUT") || !tt.HasText("LOOP_COMMAND") {
		t.Fatalf("loop content missing: %q", tt.Texts())
	}
	n := 0
	for _, text := range tt.Texts() {
		if text == "日志只保存了签名，没有思考正文" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("signature-only thinking shown %d times", n)
	}
	exportSessionPreview(t, tt, "replay-loop-claude.png")
}

// Injected context stays folded under its turn until asked for; a Codex
// session's base system prompt is one of those entries.
func TestReplayInjectedContext(t *testing.T) {
	a := New(t.TempDir())
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	at := func(s int) string { return start.Add(time.Duration(s) * time.Second).Format(time.RFC3339) }
	lines := []string{
		`{"type":"session_meta","timestamp":"` + at(0) + `","payload":{"id":"cx","base_instructions":{"text":"CTX_SYSTEM_PROMPT"}}}`,
		`{"type":"event_msg","timestamp":"` + at(0) + `","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"type":"response_item","timestamp":"` + at(1) + `","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"CTX_PROMPT"}]}}`,
		`{"type":"response_item","timestamp":"` + at(2) + `","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"CTX_REPLY"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "ctx.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn := session.Turn{ID: "t1", Start: start, End: start.Add(2 * time.Second)}
	root := session.Session{ID: "cx", Agent: session.Codex, Path: path, Start: start, End: start.Add(2 * time.Second), Turns: []session.Turn{turn}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root))
	a.sessions.replayOpen = true
	tt := ui.NewTester(a.View, 1180, 900)
	for range 200 {
		if tt.HasText("CTX_REPLY") {
			break
		}
		time.Sleep(5 * time.Millisecond)
		tt.Frame()
	}
	if tt.HasText("CTX_SYSTEM_PROMPT") {
		t.Fatal("injected context shown before it was asked for")
	}
	click(t, tt, "查看 1 条自动注入的上下文")
	if !tt.HasText("系统提示词") || !tt.HasText("CTX_SYSTEM_PROMPT") {
		t.Fatalf("system prompt not shown: %q", tt.Texts())
	}
	click(t, tt, "收起自动注入的上下文")
	if tt.HasText("CTX_SYSTEM_PROMPT") {
		t.Fatal("context did not fold again")
	}
}
