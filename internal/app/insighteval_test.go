package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/insight"
)

func TestSkillEvalDetail(t *testing.T) {
	a := insightsApp(t)
	a.insights.tab = tabEval
	tt := ui.NewTester(a.View, 1440, 1000)
	click(t, tt, "pdf")
	for _, want := range []string{"全部技能", "加载后顺利", "加载记录", "触发评测集", "从会话中挑选", "重构解析器", "有失败"} {
		if !hasTextPart(tt, want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	// pdf has nothing beside SKILL.md, so whether its files were used is not judged.
	if hasTextPart(tt, "从没用到技能目录") {
		t.Fatal("adherence judged for a skill without files")
	}
	click(t, tt, "重构解析器 第 1 轮")
	if a.page != "sessions" || a.sessions.selected != "claude:s1" || a.sessions.selectedTurn != "t1" {
		t.Fatalf("episode did not open its turn: page=%s selected=%s turn=%s", a.page, a.sessions.selected, a.sessions.selectedTurn)
	}
}

func TestTriggerSetExport(t *testing.T) {
	a := insightsApp(t)
	s := &a.sessions.snapshot.Sessions[0]
	at := s.Turns[0].Start.UTC().Format(time.RFC3339Nano)
	log := filepath.Join(t.TempDir(), "s1.jsonl")
	line := `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"` + at + `","message":{"role":"user","content":"把这些 PDF 表单填好"}}`
	if err := os.WriteFile(log, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Path = log
	a.insights.tab, a.insights.evalSkill = tabEval, "pdf"
	tt := ui.NewTester(a.View, 1440, 1000)
	click(t, tt, "从会话中挑选")
	tt.Frame()
	if !hasTextPart(tt, "把这些 PDF 表单填好") || !hasTextPart(tt, "应触发 1 · 不应触发 0") {
		t.Fatalf("picked cases %q", tt.Texts())
	}
	click(t, tt, "导出 eval_set.json")
	b, err := os.ReadFile(a.evalSetPath("pdf"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []insight.TriggerCase
	if json.Unmarshal(b, &cases) != nil || len(cases) != 1 || cases[0].Query != "把这些 PDF 表单填好" || !cases[0].ShouldTrigger {
		t.Fatalf("eval set %s", b)
	}
	tt.Frame()
	if !hasTextPart(tt, "已写入") {
		t.Fatal("export not confirmed")
	}
	// Leaving a case out drops it from the file.
	a.insights.evalSet.cases[0].label = 2
	a.exportTriggers(a.insights.evalSet)
	if b, _ := os.ReadFile(a.evalSetPath("pdf")); strings.TrimSpace(string(b)) != "null" && strings.TrimSpace(string(b)) != "[]" {
		t.Fatalf("left-out case exported: %s", b)
	}
}

func TestSessionDiagnosisFindings(t *testing.T) {
	a := insightsApp(t)
	a.openSession("claude:s1")
	tt := ui.NewTester(a.View, 1440, 1000)
	tt.Frame()
	for _, want := range []string{"每次模型调用的上下文", "疑似循环", "Bash 用同样的输入连续调用 3 次"} {
		if !hasTextPart(tt, want) {
			t.Errorf("missing %q in %q", want, tt.Texts())
		}
	}
	click(t, tt, "第 1 轮 · 疑似循环 · Bash 用同样的输入连续调用 3 次")
	d := a.selectedSessionData(a.sessionRows())
	if d == nil || !a.sessions.stepOpen {
		t.Fatal("finding did not open its step")
	}
	if step := d.turns[0].steps[a.sessions.selectedStep]; step.tool == nil || step.tool.ID != "b1" {
		t.Fatalf("finding opened step %+v", step)
	}
}
