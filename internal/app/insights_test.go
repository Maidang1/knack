package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func insightsApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "pdf"), "Extract text and tables from PDF files. Use when working with PDF files and forms.")
	writeSkill(t, filepath.Join(home, ".claude", "skills", "idle"), "Formats spreadsheets nicely for reports. Use when the user asks to tidy a spreadsheet.")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Read"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify --secret X"}]}]}}`), 0o600)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"github":{"command":"npx","args":["SECRET_ARG"]},"unused":{"command":"idle-mcp"}}}`), 0o600)
	os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("Be brief."), 0o600)
	a := New(home)
	a.startScan()
	now := time.Now()
	ms := session.Duration{Value: time.Minute, Known: true}
	tools := []session.ToolCall{
		{ID: "b1", Name: "Bash", Start: now, InputHash: "same", Failed: true},
		{ID: "b2", Name: "Bash", Start: now, InputHash: "same"},
		{ID: "b3", Name: "Bash", Start: now, InputHash: "same"},
		{ID: "g1", Name: "mcp__github__get_issue", Start: now},
	}
	for i := range 3 {
		tools = append(tools, session.ToolCall{ID: "w" + string(rune('0'+i)), Name: "WebFetch", Start: now, PermissionWait: true, Elapsed: ms})
	}
	s := session.Session{ID: "s1", Agent: session.Claude, Title: "重构解析器", CWD: filepath.Join(home, "code", "app"), Start: now.Add(-time.Hour), End: now,
		Turns: []session.Turn{{ID: "t1", Start: now.Add(-time.Hour), End: now, Tools: tools,
			Requests: []session.Request{
				{ID: "r1", Model: "claude-opus-5-5", End: now, UsageKnown: true, Tokens: session.Tokens{Input: 2000, CacheWrite: 30000, Output: 1000}},
				{ID: "r2", Model: "claude-opus-5-5", End: now, UsageKnown: true, Tokens: session.Tokens{Input: 500, CacheRead: 32000, Output: 800}},
				{ID: "r3", Model: "gpt-6.1-sol", End: now, UsageKnown: true, Tokens: session.Tokens{Input: 9000}},
			},
			Skills: []session.SkillCall{{Name: "pdf", At: now}},
		}}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{s}}
	a.sessions.period = 3
	a.page = insightsPage
	return a
}

func TestInsightsTabs(t *testing.T) {
	a := insightsApp(t)
	tt := ui.NewTester(a.View, 1440, 1000)
	want := [][]string{
		{"估算费用 · 全部", "重构解析器", "claude-opus-5-5", "未定价", "打开价格表"},
		{"首个请求输入 · 中位", "32.0K", "按项目", "app"},
		{"已加载的技能", "90 天未调用 · 按常驻开销排序", "idle"},
		{"工具调用", "疑似循环", "Bash", "×3", "WebFetch", "没有 allow 规则"},
		{"项目", "app", "Claude"},
		{"指令文件 · 按加载顺序", "~/.claude/CLAUDE.md", "github", "unused", "notify"},
	}
	for i, texts := range want {
		a.insights.tab = i
		tt.Frame()
		for _, s := range texts {
			if s != "" && !hasTextPart(tt, s) {
				t.Errorf("tab %s: missing %q in %q", insightTabs[i], s, tt.Texts())
			}
		}
		for _, s := range tt.Texts() {
			if strings.Contains(s, "SECRET") || strings.Contains(s, "--secret") {
				t.Fatalf("tab %s shows a configuration secret: %q", insightTabs[i], s)
			}
		}
	}
}

func hasTextPart(tt *ui.Tester, part string) bool {
	for _, s := range tt.Texts() {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestInsightsNavigation(t *testing.T) {
	a := insightsApp(t)
	tt := ui.NewTester(a.View, 1440, 1000)
	click(t, tt, "重构解析器 "+dollars(a.insightData().Cost.Sessions[0].Cost))
	if a.page != "sessions" || a.sessions.selected != "claude:s1" || !a.sessions.open {
		t.Fatalf("expensive session did not open: page=%s selected=%s", a.page, a.sessions.selected)
	}
	a.page = insightsPage
	a.insights.tab = 4
	tt.Frame()
	click(t, tt, "app · ~/code/app")
	if a.page != "sessions" || !strings.HasSuffix(a.sessions.query, filepath.Join("code", "app")) {
		t.Fatalf("project row did not list its sessions: page=%s query=%s", a.page, a.sessions.query)
	}
}

func TestInsightsPriceTable(t *testing.T) {
	a := insightsApp(t)
	opened := ""
	a.openFn = func(p string) { opened = p }
	tt := ui.NewTester(a.View, 1440, 1000)
	click(t, tt, "打开价格表")
	b, err := os.ReadFile(a.pricesPath())
	if err != nil || !strings.Contains(string(b), `"gpt-6.1-sol"`) || opened != a.pricesPath() {
		t.Fatalf("template %q %v opened=%q", b, err, opened)
	}
	os.WriteFile(a.pricesPath(), []byte(`{"models":{"gpt-6.1-sol":{"input":1000000}}}`), 0o600)
	before := a.insightData().Cost.Total
	click(t, tt, "重新读取")
	if got := a.insightData().Cost.Total; got <= before || len(a.insightData().Cost.UnpricedModels) != 0 {
		t.Fatalf("reloaded prices not applied: %v -> %v", before, got)
	}
}
