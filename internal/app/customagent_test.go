package app

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// A plugin's worker is a copy of the running program, here the test binary.
func TestMain(m *testing.M) {
	session.ServePluginWorker()
	os.Exit(m.Run())
}

func TestPluginAgentLoads(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() { session.LoadPlugins(filepath.Join(home, "missing"), home) })
	dir := filepath.Join(home, ".config/skill-manager/plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UnixMilli()
	src := map[string]string{
		"good.js": `knack.registerAgent({id: "js-agent", name: "JS Agent",
			list() { return [{path: "~/.js-agent/state.db", id: "chat-1", version: 1}] },
			read() { return [{time: ` + strconv.FormatInt(at, 10) + `, role: "user", text: "来自插件"}] }})`,
		"broken.js": "function (",
	}
	for name, s := range src {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(home, ".js-agent/state.db")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.startScan()
	tt := ui.NewTester(a.View, 1180, 760)
	if !slices.Contains(agentOptions(), "JS Agent") {
		t.Fatalf("filter options %q", agentOptions())
	}
	if !tt.HasText("自定义 Agent 配置有 1 个问题，相关 Agent 未加载，悬停查看") {
		t.Fatalf("plugin problem not shown %q", tt.Texts())
	}
	s := a.sessions.snapshot.Sessions
	if len(s) != 1 || s[0].ID != "chat-1" || session.LogPath(s[0].Path) != file {
		t.Fatalf("sessions %+v", s)
	}
	// A session sharing its file with others still opens in the replay.
	a.openSession(sessionID(s[0]))
	a.sessions.replayOpen = true
	a.sideCollapsed = true
	tt.Frame()
	if !tt.HasText("来自插件") || tt.HasText("无法读取日志，回放内容暂不可用") {
		t.Fatalf("replay %q", tt.Texts())
	}
}

func TestCustomAgentOverview(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() { session.LoadAgentConfig(filepath.Join(home, "missing.json")) })
	config := `{"agents": [
		{"id": "my-agent", "name": "My Agent", "roots": ["~/.my-agent/logs"],
		 "fields": {"time": "ts", "role": "role", "text": "content", "id": "id", "model": "model",
		  "usage": {"input": "usage.prompt_tokens", "output": "usage.completion_tokens", "cacheRead": "usage.cached_tokens", "inputIncludesCache": true},
		  "toolCalls": {"path": "tool_calls", "id": "id", "name": "function.name", "input": "function.arguments"},
		  "toolResults": {"id": "tool_call_id", "output": "content"}}},
		{"id": "broken", "name": "Broken", "roots": ["~/.claude/projects"], "fields": {"time": "t", "role": "r", "text": "c"}}
	]}`
	b, err := os.ReadFile("../session/testdata/custom.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	// Move the fixture to today so it falls in the default 30-day range.
	now := time.Now()
	shift := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location()).UnixMilli() - 1793491200000
	b = regexp.MustCompile(`"ts":(\d+)`).ReplaceAllFunc(b, func(m []byte) []byte {
		n, _ := strconv.ParseInt(string(m[5:]), 10, 64)
		return []byte(`"ts":` + strconv.FormatInt(n+shift, 10))
	})
	for path, data := range map[string][]byte{".config/skill-manager/agents.json": []byte(config), ".my-agent/logs/chat-1.jsonl": b} {
		path = filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := New(home)
	a.startScan()
	tt := ui.NewTester(a.View, 1180, 760)
	if !slices.Contains(agentOptions(), "My Agent") || slices.Contains(agentOptions(), "Broken") {
		t.Fatalf("filter options %q", agentOptions())
	}
	d := a.overviewData()
	if !slices.Contains(d.series, "my-agent") || d.summary.Tokens.Total() != 365 {
		t.Fatalf("overview series %q summary %+v", d.series, d.summary.Tokens)
	}
	if !tt.HasText("My Agent") || !tt.HasText("自定义 Agent 配置有 1 个问题，相关 Agent 未加载，悬停查看") {
		t.Fatalf("custom Agent not shown %q", tt.Texts())
	}
	exportSessionPreview(t, tt, "custom-agent-overview.png")
	if agentColor("my-agent", false) != sessionColor("agent-extra-0", false) {
		t.Fatal("custom Agent needs its own chart color")
	}
	// The replay's turn list and conversation show the mapped records.
	a.openSession(sessionID(a.sessions.snapshot.Sessions[0]))
	a.sessions.replayOpen = true
	a.sideCollapsed = true // the status strip shows in place of the sidebar
	tt.Frame()
	if !tt.HasText("列出文件") || !tt.HasText("谢谢") || !tt.HasText("本地只读 · Claude / Codex / Factory / My Agent") || tt.HasText("My Agent · .") {
		t.Fatalf("custom turns %q", tt.Texts())
	}
	click(t, tt, "第 1 轮 · 活跃 ≈ 8s")
	a.sessions.replayFilter = replayToolsOnly
	tt.Frame()
	if !tt.HasText("shell") || tt.HasText("有 README.md 和 main.go。") {
		t.Fatalf("tools-only filter %q", tt.Texts())
	}
	a.sessions.replayFilter = replayAll
	tt.Frame()
	if !tt.HasText("shell") || !tt.HasText("有 README.md 和 main.go。") {
		t.Fatalf("custom trace %q", tt.Texts())
	}
	a.sessions.agent = "My Agent"
	if d := a.overviewData(); d.summary.Sessions != 1 || len(d.agents) != 1 || d.agents[0].id != "my-agent" {
		t.Fatalf("filtered overview %+v", d.summary)
	}
}
