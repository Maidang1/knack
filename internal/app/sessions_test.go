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

	"knack/internal/session"

	"github.com/egoist/mygo/ui"
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
			dark := &ui.Theme{Dark: true}
			surface := token(dark, "surface")
			for _, background := range []ui.Color{token(dark, "bg"), surface, flatten(token(dark, "hover"), surface), flatten(token(dark, "selected"), surface)} {
				x, y := luminance(sessionColor(name, true)), luminance(background)
				if x < y {
					x, y = y, x
				}
				if ratio := (x + .05) / (y + .05); ratio < 3 {
					t.Fatalf("%s vs %v: %.2f:1 < 3:1", pair[1], background, ratio)
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
		if strings.HasPrefix(text, "0 个") {
			t.Fatalf("zero actionable row: %q", text)
		}
	}
	writeSkill(t, filepath.Join(a.home, ".agents/skills/orphan"), "orphan")
	a.startScan()
	tt.Frame()
	if tt.HasText("目前没有需要处理的事") || !tt.HasText("1 个共享技能未挂出") {
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
				a.openSession("claude:parent")
				tt := ui.NewTester(a.View, width, height)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				if !tt.HasText("Agent 调用") || !tt.HasText("模型输出速率") || !tt.HasText("P10 0.5 · 中位数 7.8 · P90 10.0 tok/s · 6 个样本（估算）") {
					t.Fatalf("missing session measures: %q", tt.Texts())
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
				// Where the parts fall depends on the platform's font metrics, and the
				// short window puts the waterfall below the fold: scroll each part to
				// the top before measuring it.
				content, _ := tt.Find("会话详情内容")
				for _, label := range []string{"会话详情内容", "阶段占比条", "轮次列表", "单轮步骤瀑布图"} {
					if r, _ := tt.Find(label); r.Y > content.Y {
						tt.Scroll(content.X+content.W/2, content.Y+content.H/2, 0, r.Y-content.Y)
					}
					r, ok := tt.Find(label)
					if !ok || r.W < 80 || r.X < 0 || r.X+r.W > float32(width)+1 {
						t.Fatalf("clipped %s: %+v", label, r)
					}
				}
				tt.Scroll(content.X+content.W/2, content.Y+content.H/2, 0, -2000)
				exportSessionPreview(t, tt, fmt.Sprintf("session-page-%d-dark-%t.png", width, dark))
				var revealed string
				a.revealFn = func(path string) { revealed = path }
				click(t, tt, "定位原始日志")
				if revealed != d.session.Path {
					t.Fatal("source log action selected wrong file")
				}
				// The page opens on the turn of the longest concern, marked in the chart.
				concern := d.concerns[0]
				if a.sessions.selectedTurn != d.turns[concern.turn].turn.ID {
					t.Fatal("page did not open on the concern's turn")
				}
				if _, ok := tt.Find("本轮有耗时异常"); !ok {
					t.Fatal("turn list hides the concern")
				}
				// The list's rows take the arrows, and the choice follows the focus.
				focusSessionControl(t, tt, turnLabel(d.turns[0]))
				if a.sessions.selectedTurn != d.turns[0].turn.ID {
					t.Fatal("focusing a bar did not choose its turn")
				}
				tt.Key(0, ui.KeyDown)
				if !tt.Focused(turnLabel(d.turns[1])) || a.sessions.selectedTurn != d.turns[1].turn.ID {
					t.Fatal("turn list arrow navigation")
				}
				tt.Key(0, ui.KeyEscape)
				if a.sessions.open {
					t.Fatal("Escape did not return to the list")
				}
				if _, ok := tt.Find("会话列表"); !ok {
					t.Fatal("list missing after closing the session")
				}
				exportSessionPreview(t, tt, fmt.Sprintf("session-list-%d-dark-%t.png", width, dark))
				// Filtering uses the full parsed session, not the unsupported zero usage.
				click(t, tt, "搜索会话")
				tt.Type("Factory")
				rows := a.sessionRows()
				if len(rows) != 1 || rows[0].Agent != session.Factory {
					t.Fatal("session search")
				}
				click(t, tt, recentLabel(rows[0]))
				if !a.sessions.open || !tt.HasText("暂不支持") || !tt.HasText("速率暂不支持") {
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
	if a.page != "sessions" || a.sessions.selected != "claude:parent" || !a.sessions.open {
		t.Fatal("recent session link")
	}
	click(t, tt, "首页")
	click(t, tt, "查看耗时异常会话")
	d := a.sessions.detail
	if a.page != "sessions" || !a.sessions.open || !a.sessions.anomaliesOnly || a.sessions.selectedTurn != d.turns[d.concerns[0].turn].turn.ID {
		t.Fatal("anomaly metric link must open the turn of the longest concern")
	}
	for _, row := range a.sessionRows() {
		if !a.overviewData().anomalies[sessionID(row)] {
			t.Fatal("anomaly filter includes healthy session")
		}
	}
	// Native list navigation must update the stable selection key and detail.
	tt.Key(0, ui.KeyEscape)
	if a.sessions.open {
		t.Fatal("Escape did not return to the list")
	}
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
	focusSessionControl(t, tt, "会话列表")
	before := a.sessions.index
	tt.Key(0, ui.KeyDown)
	if a.sessions.index == before || a.sessions.open {
		t.Fatal("list Down must choose the next session without opening it")
	}
	tt.Key(0, ui.KeyEnter)
	if !a.sessions.open || a.sessions.selected != sessionID(rows[a.sessions.index]) {
		t.Fatal("Enter did not open the chosen session")
	}
	click(t, tt, "下一个会话")
	if a.sessions.selected != sessionID(rows[before+2]) {
		t.Fatal("next session")
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
	a.openSession(sessionID(root))
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
