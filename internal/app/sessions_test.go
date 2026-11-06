package app

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func TestSessionPalette(t *testing.T) {
	// Check the actual chart palette on every background it can be drawn on.
	linear := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= .04045 {
			return x / 12.92
		}
		return math.Pow((x+.055)/1.055, 2.4)
	}
	luminance := func(c ui.Color) float64 { return .2126*linear(c.R) + .7152*linear(c.G) + .0722*linear(c.B) }
	for name, pair := range sessionPalette {
		t.Run(name, func(t *testing.T) {
			if sessionColor(name, false) != ui.Hex(pair[0]) || pair[0] == pair[1] {
				t.Fatal("missing theme-specific palette")
			}
			for _, background := range []string{"#1e1e20", "#2a2a2d", "#343438", "#3e3e43"} {
				x, y := luminance(sessionColor(name, true)), luminance(ui.Hex(background))
				if x < y {
					x, y = y, x
				}
				if ratio := (x + .05) / (y + .05); ratio < 3 {
					t.Fatalf("%s vs %s: %.2f:1 < 3:1", pair[1], background, ratio)
				}
			}
		})
	}
}

func TestOverviewActionableEmptyState(t *testing.T) {
	a := New(t.TempDir())
	a.startScan()
	tt := ui.NewTester(a.View, 1180, 760)
	if !tt.HasText("目前没有需要处理的事") {
		t.Fatal("missing positive empty state")
	}
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "0 个 skill") || strings.HasPrefix(text, "0 个共享 skill") || text == "0 个会话有耗时异常" {
			t.Fatalf("zero actionable row: %q", text)
		}
	}
	writeSkill(t, filepath.Join(a.home, ".agents/skills/orphan"), "orphan")
	a.startScan()
	tt.Frame()
	if tt.HasText("目前没有需要处理的事") || !tt.HasText("1 个共享 skill 未挂到 Agent") {
		t.Fatal("nonzero actionable item missing")
	}
}

func TestSessionBrowser(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := overviewFixture(t)
				height := 900
				if width == 880 {
					height = 560
				}
				a.openSession("claude:parent", 0)
				tt := ui.NewTester(a.View, width, height)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				if !tt.HasText("Agent 调用") || !tt.HasText("模型速率") || !tt.HasText("P10 0.5 · 中位数 7.8 · P90 10.0 tok/s · 6 个样本（估算）") {
					t.Fatalf("missing session overview: %q", tt.Texts())
				}
				d := a.sessions.detail
				if d.summary.Tokens.Total() != 227 || len(d.tracks) != 2 || len(d.turns) != 2 || d.phases.total != 96*time.Second {
					t.Fatalf("selected session projection %+v", d)
				}
				if d.tracks[0].note != "Claude · claude-test · 202 Token · ≈ 8 tok/s" {
					t.Fatal("main Agent stats include child usage twice")
				}
				if d.phases.away != time.Minute || d.phases.unknown != time.Second {
					t.Fatalf("away/unknown attribution %+v", d.phases)
				}
				for _, label := range []string{"会话列表", "会话详情标签", "会话详情内容"} {
					r, ok := tt.Find(label)
					if !ok || r.W < 80 || r.X < 0 || r.X+r.W > float32(width)+1 {
						t.Fatalf("clipped %s: %+v", label, r)
					}
				}
				exportSessionPreview(t, tt, fmt.Sprintf("session-overview-%d-dark-%t.png", width, dark))
				var revealed string
				a.revealFn = func(path string) { revealed = path }
				click(t, tt, "打开日志")
				if revealed != d.session.Path {
					t.Fatal("source log action selected wrong file")
				}
				click(t, tt, "概览")
				tt.Key(0, ui.KeyRight)
				if _, ok := tt.Find("轮次列表"); a.sessions.tab != 1 || !tt.Focused("轮次") || !ok {
					t.Fatal("tab keyboard navigation")
				}
				exportSessionPreview(t, tt, fmt.Sprintf("session-timing-%d-dark-%t.png", width, dark))
				// The longest concern is shown on its turn, which opens from the keyboard.
				concern := d.concerns[0]
				if !tt.HasText(concern.title) {
					t.Fatalf("turn row hides its concern %q", concern.title)
				}
				focusSessionControl(t, tt, turnLabel(d.turns[concern.turn]))
				tt.Key(0, ui.KeyEnter)
				if !a.sessions.roundOpen || a.sessions.selectedTurn != d.turns[concern.turn].turn.ID {
					t.Fatal("concern turn selected wrong round")
				}
				click(t, tt, "返回全部轮次")
				// Enter the waterfall from the keyboard and retain its selection on return.
				label := turnLabel(d.turns[0])
				focusSessionControl(t, tt, label)
				tt.Key(0, ui.KeyEnter)
				if !a.sessions.roundOpen || a.sessions.selectedTurn != d.turns[0].turn.ID || !tt.HasText("调用链") {
					t.Fatal("turn entry did not keep selection")
				}
				click(t, tt, "返回全部轮次")
				if a.sessions.roundOpen || a.sessions.selectedTurn != d.turns[0].turn.ID {
					t.Fatal("round return lost selection")
				}
				focusSessionControl(t, tt, label)
				tt.Key(0, ui.KeyDown)
				if !tt.Focused(turnLabel(d.turns[1])) {
					t.Fatal("turn list arrow navigation")
				}
				tt.Key(0, ui.KeyEnter)
				if a.sessions.selectedTurn != d.turns[1].turn.ID {
					t.Fatal("second round selection")
				}
				click(t, tt, "返回全部轮次")
				// Filtering uses the full parsed session, not the unsupported zero usage.
				click(t, tt, "搜索会话")
				tt.Type("Factory")
				if rows := a.sessionRows(); len(rows) != 1 || rows[0].Agent != session.Factory {
					t.Fatal("session search")
				}
				click(t, tt, "概览")
				if !tt.HasText("暂不支持") || !tt.HasText("速率暂不支持") {
					t.Fatal("Factory usage must remain unsupported")
				}
			})
		}
	}
}

func TestSessionHomeLinks(t *testing.T) {
	a := overviewFixture(t)
	tt := ui.NewTester(a.View, 1180, 900)
	for _, s := range a.overviewData().recent {
		if s.Title == "测试会话" {
			click(t, tt, recentRowLabel(s))
		}
	}
	if a.page != "sessions" || a.sessions.selected != "claude:parent" || a.sessions.tab != 0 {
		t.Fatal("recent session link")
	}
	click(t, tt, "总览")
	click(t, tt, "查看耗时异常会话")
	if a.page != "sessions" || a.sessions.tab != 1 || !a.sessions.anomaliesOnly || !tt.HasText("活跃 ≈") {
		t.Fatal("anomaly metric link")
	}
	for _, row := range a.sessionRows() {
		if !a.overviewData().anomalies[sessionID(row)] {
			t.Fatal("anomaly filter includes healthy session")
		}
	}
	// Native list navigation must update the stable selection key and detail.
	click(t, tt, "搜索会话")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("")
	a.sessions.anomaliesOnly = false
	a.sessions.browser = nil
	tt.Frame()
	rows := a.sessionRows()
	if len(rows) < 2 {
		t.Fatal("need multiple list rows")
	}
	click(t, tt, recentLabel(rows[0]))
	before := a.sessions.selected
	tt.Key(0, ui.KeyDown)
	if a.sessions.selected == before {
		t.Fatal("list Down did not change session")
	}
}

func TestSessionConcurrentPhaseDisplay(t *testing.T) {
	a := New(t.TempDir())
	start := time.Now().Add(-2 * time.Minute)
	known := func(d time.Duration) session.Duration {
		return session.Duration{Value: d, Known: true, Estimated: true}
	}
	tool := func(id string, from, to int) session.ToolCall {
		return session.ToolCall{ID: id, Name: "Bash", Start: start.Add(time.Duration(from) * time.Second), End: start.Add(time.Duration(to) * time.Second), Elapsed: known(time.Duration(to-from) * time.Second)}
	}
	permission := tool("permission", 80, 90)
	permission.PermissionWait = true
	agent := tool("agent", 50, 80)
	agent.AgentID = "child"
	turn := session.Turn{ID: "turn", Start: start, End: start.Add(100 * time.Second), Requests: []session.Request{{ID: "model", Start: start, End: start.Add(10 * time.Second), Elapsed: known(10 * time.Second), Thinking: known(2 * time.Second), Generation: known(8 * time.Second)}}, Tools: []session.ToolCall{tool("one", 10, 50), tool("parallel", 20, 40), agent, permission}, Waits: []session.Wait{{Start: start.Add(90 * time.Second), End: start.Add(100 * time.Second), Kind: "reply"}}}
	root := session.Session{ID: "parallel", Agent: session.Claude, Start: start, End: turn.End, Turns: []session.Turn{turn}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root), 1)
	tt := ui.NewTester(a.View, 1180, 900)
	want := [phaseCount]time.Duration{2 * time.Second, 8 * time.Second, 40 * time.Second, 30 * time.Second, 20 * time.Second, 0}
	if got := a.sessions.detail.phases; got.values != want || got.unknown != 0 || got.total != 100*time.Second {
		t.Fatalf("parallel work/permission double-counted: %+v", got)
	}
	if !tt.HasText("1 轮") || !tt.HasText("子 Agent") {
		t.Fatal("missing phase labels")
	}
}

func exportSessionPreview(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("KNACK_RENDER_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, tt.Image())
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func focusSessionControl(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for range 100 {
		if tt.Focused(label) {
			return
		}
		tt.Key(0, ui.KeyTab)
	}
	t.Fatalf("keyboard cannot reach %q", label)
}
