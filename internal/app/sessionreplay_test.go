package app

import (
	"fmt"
	"strings"
	"testing"

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
				// Thinking and tool output stay folded until opened.
				if tt.HasText("PRIVATE_CODEX_REASONING") || tt.HasText("PRIVATE_CODEX_TOOL_OUTPUT") {
					t.Fatal("folded content shown")
				}
				for _, text := range tt.Texts() {
					if strings.HasPrefix(text, "思考") {
						click(t, tt, text)
						break
					}
				}
				if !tt.HasText("PRIVATE_CODEX_REASONING") {
					t.Fatalf("thinking did not open: %q", tt.Texts())
				}
				for _, text := range tt.Texts() {
					if strings.Contains(text, " · PRIVATE_SCRIPT") {
						click(t, tt, text)
						break
					}
				}
				if !tt.HasText("PRIVATE_CODEX_TOOL_OUTPUT") {
					t.Fatalf("tool card did not open: %q", tt.Texts())
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
