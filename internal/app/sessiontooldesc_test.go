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

// Each Agent's names and argument shapes for the same kind of work read the
// same way.
func TestDescribeTool(t *testing.T) {
	for _, c := range []struct {
		name, input, verb, subject, detail string
	}{
		{"Read", `{"file_path":"/Users/me/code/app/README.md"}`, "读取", "README.md", "…/code/app"},
		{"Read", `{"file_path":"/a/b.go","offset":10,"limit":5}`, "读取", "b.go", "/a · 第 10–14 行"},
		{"LS", `{"directory_path":"/Users/me/code/app"}`, "列出", "app/", "…/me/code"},
		{"Glob", `{"folder":"/x","patterns":["**/AGENTS.md","**/SKILL.md"]}`, "查找文件", "**/AGENTS.md, **/SKILL.md", "/x"},
		{"Grep", `{"pattern":"TODO","path":"/x","glob":"*.go"}`, "搜索", "TODO", "/x · *.go"},
		{"Bash", `{"command":"go test ./...","description":"Run the tests"}`, "运行", "Run the tests", "go test ./..."},
		{"shell", `{"command":["bash","-lc","ls -la"]}`, "运行", "ls -la", ""},
		{"exec_command", `{"cmd":"rg foo","workdir":"/x"}`, "运行", "rg foo", "/x"},
		{"Script", `{"script":"await tools.Read({});\nawait tools.LS({});\nawait tools.Read({})"}`, "运行脚本", "调用 Read、LS", ""},
		{"Edit", `{"file_path":"/a/b.go","old_string":"x","new_string":"y"}`, "编辑", "b.go", "/a"},
		{"Create", `{"file_path":"/a/b.go","content":"1\n2\n"}`, "新建", "b.go", "/a · 2 行"},
		{"apply_patch", "*** Begin Patch\n*** Update File: /a/x.go\n*** Add File: /a/y.go\n*** End Patch", "修改", "2 个文件", "x.go、y.go"},
		{"Skill", `{"skill":"design"}`, "启用技能", "design", ""},
		{"Task", `{"description":"Review docs","prompt":"…","subagent_type":"worker"}`, "派出子 Agent", "Review docs", "worker"},
		{"FetchUrl", `{"url":"https://example.com/a"}`, "打开网页", "example.com/a", ""},
		{"mystery", `{"q":1}`, "mystery", `{"q":1}`, ""},
	} {
		d := describeTool(c.name, c.input)
		if d.verb != c.verb || d.subject != c.subject || d.detail != c.detail {
			t.Errorf("%s %s: got %q %q %q", c.name, c.input, d.verb, d.subject, d.detail)
		}
	}
	for _, input := range []string{
		`{"todos":"1. [completed] A\n2. [in_progress] B\n3. [pending] C"}`,
		`{"todos":[{"content":"A","status":"completed"},{"content":"B","status":"in_progress"},{"content":"C","status":"pending"}]}`,
		`{"plan":[{"step":"A","status":"completed"},{"step":"B","status":"in_progress"},{"step":"C","status":"pending"}]}`,
	} {
		d := describeTool("TodoWrite", input)
		var got []string
		for _, it := range d.todos {
			got = append(got, it.text+":"+it.status)
		}
		if strings.Join(got, ",") != "A:done,B:active,C:pending" || d.subject != "3 项，完成 1" {
			t.Errorf("todos %s: %q %q", input, got, d.subject)
		}
	}
	for _, c := range []struct{ name, output, want string }{
		{"Read", "Read /a/README.md: lines 1-240 (240 lines, 12.6 KiB)", "240 行"},
		{"Read", "1\tpackage a\n2\t\n3\tfunc x() {}", "3 行"},
		{"LS", `[{"name":"a"},{"name":"b"}]`, "2 项"},
		{"Glob", `[]`, "没有结果"},
		{"Grep", `[{"line":1},{"line":2},{"line":3}]`, "3 处匹配"},
		{"Script", "{\"i\":0,\"result\":1}\n{\"i\":1,\"result\":2}", "返回 2 个结果"},
		{"AskUser", "1. [question] Who?\n[answer] Developers", "回答：Developers"},
		{"Bash", "ok\nmore", ""},
	} {
		if got := toolOutcome(c.name, c.output); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.name, c.output, got, c.want)
		}
	}
}

// Reads and searches in a row fold into one line that opens to each call;
// other calls show their argument and the first lines of their output.
func TestReplayTranscriptFoldsLooks(t *testing.T) {
	a := New(t.TempDir())
	start := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	at := func(s int) string { return start.Add(time.Duration(s) * time.Second).Format(time.RFC3339) }
	use := func(uuid string, s int, req, tool, name, input string) string {
		return `{"type":"assistant","uuid":"` + uuid + `","timestamp":"` + at(s) + `","message":{"id":"` + req + `","model":"claude-test","content":[{"type":"tool_use","id":"` + tool + `","name":"` + name + `","input":` + input + `}]}}`
	}
	result := func(uuid string, s int, tool, out string) string {
		return `{"type":"user","uuid":"` + uuid + `","timestamp":"` + at(s) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + tool + `","content":"` + out + `"}]}}`
	}
	lines := []string{
		`{"type":"user","uuid":"u0","timestamp":"` + at(0) + `","message":{"role":"user","content":"GROUP_PROMPT"}}`,
		use("a1", 1, "req1", "t1", "Read", `{"file_path":"/repo/README.md"}`),
		result("u1", 2, "t1", "1\\tline"),
		use("a1b", 2, "req1b", "t1b", "Grep", `{"pattern":"GROUP_PATTERN","path":"/repo"}`),
		result("u1b", 2, "t1b", "README.md"),
		use("a2", 3, "req2", "t2", "TodoWrite", `{"todos":[{"content":"GROUP_TODO","status":"in_progress"}]}`),
		result("u2", 4, "t2", "ok"),
		use("a3", 5, "req3", "t3", "Bash", `{"command":"GROUP_CMD"}`),
		result("u3", 6, "t3", "GROUP_OUT\\n2\\n3\\n4\\nGROUP_LAST"),
		`{"type":"assistant","uuid":"a4","timestamp":"` + at(7) + `","message":{"id":"req4","model":"claude-test","content":[{"type":"text","text":"GROUP_REPLY"}]}}`,
	}
	path := filepath.Join(t.TempDir(), "group.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	turn := session.Turn{ID: "turn", Start: when(0), End: when(7),
		Requests: []session.Request{{ID: "req1"}, {ID: "req1b"}, {ID: "req2"}, {ID: "req3"}, {ID: "req4"}},
		Tools:    []session.ToolCall{{ID: "t1", Name: "Read"}, {ID: "t1b", Name: "Grep"}, {ID: "t2", Name: "TodoWrite"}, {ID: "t3", Name: "Bash"}}}
	root := session.Session{ID: "group", Agent: session.Claude, Path: path, CWD: "/repo", Start: when(0), End: when(7), Turns: []session.Turn{turn}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root))
	a.sessions.replayOpen = true
	tt := ui.NewTester(a.View, 1180, 900)
	for range 200 {
		if tt.HasText("GROUP_REPLY") {
			break
		}
		time.Sleep(5 * time.Millisecond)
		tt.Frame()
	}
	for _, want := range []string{"读取了 README.md，搜索了 1 个模式", "TodoWrite", "GROUP_TODO", "Bash", "(GROUP_CMD)", "GROUP_OUT\n2\n3", "… +2 行", "GROUP_REPLY"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if tt.HasText("GROUP_PATTERN") || tt.HasText("GROUP_LAST") || tt.HasText("模型调用") {
		t.Fatalf("folded calls or clipped output shown: %q", tt.Texts())
	}
	click(t, tt, "读取了 README.md，搜索了 1 个模式")
	if !tt.HasText("(README.md)") || !tt.HasText("(GROUP_PATTERN)") || !tt.HasText("1 行") {
		t.Fatalf("folded calls did not open: %q", tt.Texts())
	}
	click(t, tt, "… +2 行")
	if !tt.HasText("GROUP_OUT\n2\n3\n4\nGROUP_LAST") {
		t.Fatalf("output did not open in full: %q", tt.Texts())
	}
	click(t, tt, "Bash GROUP_CMD")
	if !tt.HasText("输入") {
		t.Fatalf("tool call did not open to its input: %q", tt.Texts())
	}
	exportSessionPreview(t, tt, "replay-loop-grouped.png")
}
