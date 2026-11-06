package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/skill"
)

func overviewFixture(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".agents/skills/build"), "build")
	writeSkill(t, filepath.Join(home, ".claude/skills/build"), "local")
	writeSkill(t, filepath.Join(home, ".cursor/skills/ordinary"), "ordinary")
	writeSkill(t, filepath.Join(home, ".cursor/skills-cursor/shipped"), "read-only")
	for _, file := range []struct{ fixture, path string }{
		{"claude.jsonl", ".claude/projects/p/parent.jsonl"},
		{"claude-child.jsonl", ".claude/projects/p/parent/subagents/agent-child-a.jsonl"},
		{"codex.jsonl", ".codex/sessions/2026/11/01/codex.jsonl"},
		{"factory.jsonl", ".factory/sessions/p/factory.jsonl"},
	} {
		b, err := os.ReadFile("../session/testdata/" + file.fixture)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		base := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
		original := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		lines := bytes.Split(bytes.TrimSpace(b), []byte{'\n'})
		b = nil
		for _, line := range lines {
			var record map[string]json.RawMessage
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal(err)
			}
			if raw, ok := record["timestamp"]; ok {
				var ts string
				if err := json.Unmarshal(raw, &ts); err != nil {
					t.Fatal(err)
				}
				at, err := time.Parse(time.RFC3339Nano, ts)
				if err != nil {
					t.Fatal(err)
				}
				record["timestamp"], err = json.Marshal(base.Add(at.Sub(original)).Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			b = append(b, encoded...)
			b = append(b, '\n')
		}
		path := filepath.Join(home, filepath.FromSlash(file.path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := New(home)
	a.startScan()
	return a
}

func TestSessionOverview(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := overviewFixture(t)
				height := 760
				if width == 880 {
					height = 560
				}
				tt := ui.NewTester(a.View, width, height)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				if a.page != "overview" || !tt.HasText("每日 Token") || !tt.HasText("需要处理") || !tt.HasText("最近会话") {
					t.Fatalf("missing overview sections %q", tt.Texts())
				}
				d := a.overviewData()
				if d.summary.Sessions != 3 || d.summary.Tokens.Total() != 407 || d.summary.UnmeteredRequests != 3 {
					t.Fatalf("overview data %+v", d.summary)
				}
				if !tt.HasText("不在本地保存会话日志，暂时无法统计") || !tt.HasText("暂不支持") {
					t.Fatal("unsupported sources look like measured values")
				}
				chart, ok := tt.Find("每日 Token 图表")
				if !ok || chart.W < 100 || chart.H < 100 || chart.X < a.sidebarWidth() || chart.X+chart.W > float32(width) {
					t.Fatalf("chart collapsed/overflow: %+v", chart)
				}
				im := tt.Image()
				bars := 0
				for y := int(chart.Y); y < int(chart.Y+chart.H); y++ {
					for x := int(chart.X); x < int(chart.X+chart.W); x++ {
						pixel := im.RGBAAt(x, y)
						want := agentColor(session.Claude, dark)
						if pixel.R == want.R && pixel.G == want.G && pixel.B == want.B {
							bars++
						}
					}
				}
				if bars < 100 {
					t.Fatalf("daily chart has no visible Agent bars (%d pixels)", bars)
				}
				click(t, tt, "统计时间范围")
				tt.Key(0, ui.KeyHome)
				if a.sessions.period != 0 {
					t.Fatal("period keyboard navigation")
				}
				click(t, tt, "统计 Agent")
				tt.Key(0, ui.KeyEnd)
				tt.Key(0, ui.KeyEnter)
				if a.sessions.agent != "Factory" || a.overviewData().summary.Sessions != 1 {
					t.Fatalf("agent filter keyboard navigation: %s", a.sessions.agent)
				}
				a.sessions.agent = "全部 Agent"
				a.sessions.period = 2
				tt.Frame()
				click(t, tt, "查看全部问题")
				if a.page != "issues" {
					t.Fatal("issues navigation")
				}
				click(t, tt, "总览")
				click(t, tt, "会话")
				if a.page != "sessions" || !tt.HasText("会话列表") {
					t.Fatal("session entry")
				}
				click(t, tt, "总览")
				if dir := os.Getenv("KNACK_RENDER_DIR"); dir != "" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("overview-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					err = png.Encode(f, tt.Image())
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAgentBuiltinGrouping(t *testing.T) {
	a := overviewFixture(t)
	tt := ui.NewTester(a.View, 1180, 760)
	if tt.HasText("Cursor 自带") || tt.HasText("自带") {
		t.Fatal("standalone builtin navigation remains")
	}
	click(t, tt, "Cursor")
	if !tt.HasText("ordinary") || !tt.HasText("shipped") || !tt.HasText("自带 · 只读") {
		t.Fatalf("missing grouped skills %q", tt.Texts())
	}
	click(t, tt, "shipped")
	if !tt.HasText("只展示") || tt.HasText("改为软链") {
		t.Fatal("bundled skill became writable")
	}
	for _, r := range a.rows() {
		if r.name == "shipped" && (r.entry.RootRole != skill.RoleBuiltin || !r.entry.System) {
			t.Fatal("bundled source identity changed")
		}
	}
	// Startup shows the last statistics without reading logs in New.
	reopened := New(a.home)
	if !reopened.sessions.cached || reopened.sessions.snapshot == nil || reopened.overviewData().summary.Tokens.Total() != 407 {
		t.Fatal("session startup cache")
	}
}
