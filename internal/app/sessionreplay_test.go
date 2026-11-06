package app

import (
	"fmt"
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
				a.openSession(sessionID(claude), 1)
				tt.Frame()
				if !tt.HasText("PRIVATE_USER_PROMPT") || !tt.HasText("PRIVATE_SECOND_PROMPT") {
					t.Fatalf("turn list lacks prompts: %q", tt.Texts())
				}
				if !tt.HasText("离开相关间隔 ≈") || tt.HasText("PRIVATE_COMPACTION_SUMMARY") {
					t.Fatalf("gap separator or injected context: %q", tt.Texts())
				}
				exportSessionPreview(t, tt, fmt.Sprintf("replay-turns-%d-dark-%t.png", width, dark))

				a.openSession(sessionID(codex), 1)
				tt.Frame()
				d := a.sessions.detail
				if !tt.HasText("PRIVATE_CODEX_PROMPT") {
					t.Fatalf("Codex prompt missing: %q", tt.Texts())
				}
				focusSessionControl(t, tt, turnLabel(d.turns[0]))
				tt.Key(0, ui.KeyEnter)
				if !a.sessions.roundOpen || !tt.HasText("调用链") {
					t.Fatal("turn trace did not open")
				}
				// Steps preview their content; the selected one expands in place.
				if !tt.HasText("PRIVATE_CODEX_REPLY") || !tt.HasText("PRIVATE_SCRIPT") {
					t.Fatalf("step previews missing: %q", tt.Texts())
				}
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
				// Codex request timing is estimated from the completed input.
				r := d.turns[0].turn.Requests[0]
				if !r.Elapsed.Known || !r.Elapsed.Estimated || r.Thinking.Value == 0 {
					t.Fatalf("Codex request timing %+v", r)
				}
				exportSessionPreview(t, tt, fmt.Sprintf("replay-trace-%d-dark-%t.png", width, dark))
				if len(a.sessions.replay) == 0 || a.sessions.replay[0].tr == nil {
					t.Fatal("replay content not held for the open session")
				}
			})
		}
	}
}
