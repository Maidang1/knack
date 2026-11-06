package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func sessionRoundFixture(t *testing.T) *App {
	t.Helper()
	a := New(t.TempDir())
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	dur := func(s int) session.Duration {
		return session.Duration{Value: time.Duration(s) * time.Second, Known: true, Estimated: true}
	}
	turn := session.Turn{ID: "round1", Start: start, End: at(418), Elapsed: dur(406), Requests: []session.Request{
		{ID: "model1", Model: "claude-sonnet", End: at(8), Elapsed: dur(8), Thinking: dur(2), Generation: dur(6), Tokens: session.Tokens{Input: 1200, Output: 240, CacheRead: 800}, UsageKnown: true},
		{ID: "model2", Model: "claude-sonnet", End: at(390), Elapsed: dur(8), Generation: dur(8), Retry: true, Tokens: session.Tokens{Input: 200, Output: 160}, UsageKnown: true},
	}, Tools: []session.ToolCall{
		{ID: "read", Name: "Read", Start: at(8), End: at(10), Elapsed: dur(2)},
		{ID: "long", Name: "Bash", Start: at(10), End: at(382), Elapsed: dur(372)},
		{ID: "permission", Name: "Skill", Start: at(390), End: at(396), Elapsed: dur(6), PermissionWait: true, PermissionSource: "user_temporary", PermissionDecision: "allow", TimedOutAfter: 5 * time.Second, Interrupted: true},
	}, Skills: []session.SkillCall{{Name: "review", ToolID: "permission", At: at(390)}}, Waits: []session.Wait{
		{Start: at(390), End: at(396), Elapsed: dur(6), Kind: "permission", ToolID: "permission"},
		{Start: at(406), End: at(418), Elapsed: dur(12), Kind: "reply"},
	}, Subagents: []session.Subagent{{ID: "child", Session: session.Session{ID: "child", Agent: session.Claude, Start: at(100), End: at(120)}}}, Events: []session.Event{{Kind: "compact", At: at(398), Duration: dur(2)}}}
	root := session.Session{ID: "waterfall", Agent: session.Claude, Title: "单轮步骤示例", CWD: "/example/knack", Start: start, End: at(430), Turns: []session.Turn{turn, {ID: "round2", Start: at(418), End: at(430)}}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root), 1)
	a.chooseSessionTurn(sessionDetail(a.sessions.snapshot, root), 0)
	return a
}

// Exercise the complete UI against synthetic metadata, including real pixels,
// keyboard focus, persistence across App.New and both sides of a failed write.
func TestSessionRound(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := sessionRoundFixture(t)
				tt := ui.NewTester(a.View, width, 1100)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				d := a.sessions.detail
				turn := d.turns[0]
				if len(turn.steps) != 8 {
					t.Fatalf("permission duplicated or steps lost: %+v", turn.steps)
				}
				for _, label := range []string{"单轮摘要", "步骤时间比例", "单轮步骤瀑布图", "步骤详情"} {
					r, ok := tt.Find(label)
					if !ok || r.W < 100 || r.X+r.W > float32(width)+1 {
						t.Fatalf("clipped %s: %+v", label, r)
					}
				}
				for i := range turn.steps {
					r, ok := tt.Find(stepDuration(turn.steps[i]))
					if !ok || r.W < 45 || r.X+r.W > float32(width)+1 {
						t.Fatalf("duration clipped: %+v", r)
					}
				}
				if !tt.HasText("模型 · claude-sonnet") || !tt.HasText("≈ 6m12s") {
					t.Fatalf("missing actual model/duration: %q", tt.Texts())
				}
				// The long tool's span must be visible in the screenshot, not merely in the tree.
				toolColor := sessionColor("phase-tool", dark)
				pixels := 0
				im := tt.Image()
				for y := 0; y < im.Bounds().Dy(); y++ {
					for x := int(a.sidebarWidth() + a.sessions.listW); x < width; x++ {
						p := im.RGBAAt(x, y)
						if p.R == toolColor.R && p.G == toolColor.G && p.B == toolColor.B {
							pixels++
						}
					}
				}
				if pixels < 100 {
					t.Fatalf("waterfall not painted: %d", pixels)
				}
				exportSessionPreview(t, tt, fmt.Sprintf("round-real-%d-dark-%t.png", width, dark))
				click(t, tt, "折叠长步骤")
				if !a.foldLongSteps || !tt.HasText("折叠 6m12s（+10s 至 +6m22s）") || !tt.HasText("≈ 6m12s ∕∕") {
					t.Fatal("fold lost actual duration")
				}
				axis := newStepAxis(turn, true)
				if len(axis.cuts) != 1 || axis.display != 56*time.Second || math.Abs(float64(axis.at(382*time.Second))-20.0/56) > 1e-6 {
					t.Fatalf("compressed axis: %+v", axis)
				}
				exportSessionPreview(t, tt, fmt.Sprintf("round-folded-%d-dark-%t.png", width, dark))
				// This setting must survive both saving an unrelated setting and restart.
				a.setAgentShown("pool", false)
				reloaded := New(a.home)
				if !reloaded.foldLongSteps || !reloaded.hidden["pool"] {
					t.Fatal("preferences clobbered across restart")
				}
				a.setFoldLongSteps(false)
				if reloaded = New(a.home); reloaded.foldLongSteps || !reloaded.hidden["pool"] {
					t.Fatal("real scale did not persist")
				}
				stepLabel := func(i int) string {
					s := turn.steps[i]
					return fmt.Sprintf("步骤 %d · %s · %s", i+1, s.name, stepDuration(s))
				}
				focusSessionControl(t, tt, stepLabel(0))
				tt.Key(0, ui.KeyDown)
				if a.sessions.selectedStep != 1 {
					t.Fatal("step arrows do not update details")
				}
				permissionIndex := -1
				for i, s := range turn.steps {
					if s.tool != nil && s.tool.ID == "permission" {
						permissionIndex = i
					}
				}
				for range permissionIndex - 1 {
					tt.Key(0, ui.KeyDown)
				}
				if !tt.HasText("review") || !tt.HasText("user_temporary · allow") || !tt.HasText("是 · 阈值 5s") {
					t.Fatal("missing permission/skill/status metadata")
				}
				scroll, _ := tt.Find("会话详情内容")
				tt.Scroll(scroll.X+scroll.W/2, scroll.Y+scroll.H/2, 0, 2000)
				exportSessionPreview(t, tt, fmt.Sprintf("round-detail-%d-dark-%t.png", width, dark))
				tt.Key(0, ui.KeyEscape)
				if a.sessions.roundOpen || a.sessions.selectedTurn != turn.turn.ID || !tt.Focused(turnLabel(turn)) {
					t.Fatalf("Escape lost turn/focus: open=%t selected=%s focusPending=%t focused=%t", a.sessions.roundOpen, a.sessions.selectedTurn, a.sessions.focusTurn, tt.Focused(turnLabel(turn)))
				}
				tt.Key(0, ui.KeyEnter)
				if !a.sessions.roundOpen {
					t.Fatal("cannot reenter from restored turn focus")
				}
				focusSessionControl(t, tt, "下一轮")
				tt.Key(0, ui.KeyEnter)
				if a.sessions.selectedTurn != "round2" || !tt.HasText("本轮没有已记录的步骤") {
					t.Fatal("next round/empty state")
				}
				focusSessionControl(t, tt, "上一轮")
				tt.Key(0, ui.KeyEnter)
				if a.sessions.selectedTurn != "round1" {
					t.Fatal("previous round")
				}
				a.openSettings()
				tt.Frame()
				tt.Key(0, ui.KeyEscape)
				if a.settingsOpen || !a.sessions.roundOpen {
					t.Fatal("Escape stole dialog dismissal")
				}
				// At the short 880 window, keyboard navigation must scroll rows into view.
				if width == 880 {
					tt = ui.NewTester(a.View, 880, 560)
					tt.SetDark(dark)
					focusSessionControl(t, tt, stepLabel(0))
					for range permissionIndex {
						tt.Key(0, ui.KeyDown)
					}
					r, _ := tt.Find(stepLabel(permissionIndex))
					if r.Y < 0 || r.Y+r.H > 560 {
						t.Fatalf("focused row not visible: %+v", r)
					}
					exportSessionPreview(t, tt, fmt.Sprintf("round-compact-880-dark-%t.png", dark))
				}
				// Fail a settings write without changing either in-memory preference.
				blocker := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(blocker, []byte("block"), 0o600); err != nil {
					t.Fatal(err)
				}
				a.settingsPath = filepath.Join(blocker, "settings.json")
				before := a.foldLongSteps
				a.setFoldLongSteps(!before)
				if a.foldLongSteps != before || a.notice == "" {
					t.Fatal("failed write changed preference")
				}
			})
		}
	}
}

func TestSessionRoundUnavailable(t *testing.T) {
	a := New(t.TempDir())
	start := time.Now()
	root := session.Session{ID: "codex", Agent: session.Codex, Start: start, End: start.Add(time.Minute), Turns: []session.Turn{{ID: "one", Start: start, End: start.Add(time.Minute), Requests: []session.Request{{ID: "request", Model: "gpt", End: start.Add(time.Minute)}}}}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root), 1)
	a.chooseSessionTurn(sessionDetail(a.sessions.snapshot, root), 0)
	tt := ui.NewTester(a.View, 1180, 1100)
	if !tt.HasText("暂不支持") || !tt.HasText("模型 · gpt") {
		t.Fatalf("missing data appears measured: %q", tt.Texts())
	}
	for _, s := range a.sessions.detail.turns[0].steps {
		if !s.start.IsZero() || s.elapsed.Known {
			t.Fatal("fabricated request timing")
		}
	}
	// A missing timestamp must not produce a measured zero-length round.
	root.Turns[0].Start = time.Time{}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	tt.Frame()
	if !tt.HasText("活跃 暂不支持") || !tt.HasText("时间轴暂不支持") {
		t.Fatal("missing turn timestamps appear as zero duration")
	}
	for _, text := range tt.Texts() {
		if strings.Contains(text, "prompt") || strings.Contains(text, "复制命令") {
			t.Fatal("private content action exposed")
		}
	}
}
