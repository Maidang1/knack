package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"knack/internal/evals"
	"knack/internal/insight"
)

func TestSkillEvalDetail(t *testing.T) {
	a := insightsApp(t)
	a.insights.tab = tabEval
	tt := ui.NewTester(a.View, 1440, 1000)
	click(t, tt, "pdf")
	for _, want := range []string{"全部技能", "加载后顺利", "加载记录", "触发评测", "从会话中挑选", "重构解析器", "有失败"} {
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

func TestTriggerSet(t *testing.T) {
	a, m := judgeApp(t)
	tt := ui.NewTester(a.View, 1440, 2400)
	click(t, tt, "从会话中挑选")
	tt.Frame()
	s := a.insights.trigger
	if s.err != "" || len(s.set.Cases) != 1 || !hasTextPart(tt, "把这些 PDF 表单填好") || !hasTextPart(tt, "应触发 1 · 不应触发 0") {
		t.Fatalf("picked cases %q %q", s.err, tt.Texts())
	}
	if saved, _ := s.store.Triggers(); len(saved.Cases) != 1 || saved.Cases[0].Source != evals.FromSession {
		t.Fatal("picked cases not saved")
	}
	// Picking again finds nothing new rather than adding the same prompt twice.
	click(t, tt, "从会话中挑选")
	tt.Frame()
	if len(s.set.Cases) != 1 || !hasTextPart(tt, "没有找到新的提示词") {
		t.Fatalf("picked twice: %d", len(s.set.Cases))
	}

	// The person writes one; the model writes more, told what is there.
	s.adding, s.addNo = "把这个 PDF 转成 Word", 1
	tt.Frame()
	click(t, tt, "添加请求")
	tt.Frame()
	m.replies = []string{`{"cases":[{"query":"invoice.pdf 的表格帮我填一下","should_trigger":true,"why":"填表"},{"query":"合并这两个 PDF","should_trigger":false,"why":"不是填表"}]}`}
	click(t, tt, "让模型编写 16 条")
	tt.Frame()
	if len(s.set.Cases) != 4 || s.err != "" || !hasTextPart(tt, "应触发 2 · 不应触发 2") || !hasTextPart(tt, "模型编写 · 不是填表") {
		t.Fatalf("cases: %+v %q", s.set.Cases, s.err)
	}
	if p := m.asked[0].Prompt; !strings.Contains(p, "[应触发] 把这些 PDF 表单填好") || !strings.Contains(p, "[不应触发] 把这个 PDF 转成 Word") {
		t.Fatalf("synth prompt:\n%s", p)
	}

	// A run states what it sends, then asks once per request with the skill list.
	click(t, tt, "模拟 4 条")
	tt.Frame()
	if s.plan == nil || len(m.asked) != 1 || !hasTextPart(tt, "将把 4 条请求逐条发给 api.anthropic.com") {
		t.Fatalf("trigger plan: %q", tt.Texts())
	}
	m.replies = []string{`{"skill":"pdf","reason":"填表"}`, `{"skill":"pdf","reason":"提到 PDF"}`, `{"skill":"","reason":"不需要"}`, `{"skill":"","reason":"直接做"}`}
	click(t, tt, "开始模拟")
	tt.Frame()
	last, ok := s.set.Last()
	if !ok || s.busy != "" || s.err != "" || len(last.Trials) != 4 || !m.asked[1].Cache || !strings.Contains(m.asked[1].System, "- pdf: ") {
		t.Fatalf("run: %+v busy=%q err=%q", last, s.busy, s.err)
	}
	score := last.Score("pdf", s.set.Cases)
	if score.Hit != 1 || score.Should != 2 || score.False != 1 || !hasTextPart(tt, "现在的描述") || !hasTextPart(tt, "上次模拟误加载了它") {
		t.Fatalf("score %+v %q", score, tt.Texts())
	}

	// The model rewords the description from the misses; trying it is a new run.
	m.replies = []string{`{"description":"填写 PDF 表单。转换和合并 PDF 不用这个技能。","why":"写明了不该用的情况"}`}
	click(t, tt, "让模型改写描述")
	tt.Frame()
	if s.draft != "填写 PDF 表单。转换和合并 PDF 不用这个技能。" || !hasTextPart(tt, "模型的改写：写明了不该用的情况") || !hasTextPart(tt, "改动只用于模拟") {
		t.Fatalf("revised draft %q %q", s.draft, s.err)
	}
	if p := m.asked[len(m.asked)-1].Prompt; !strings.Contains(p, "不应触发，但 Agent 加载了这个技能") {
		t.Fatalf("revise prompt:\n%s", p)
	}
	click(t, tt, "模拟 4 条")
	tt.Frame()
	m.replies = []string{`{"skill":"pdf"}`, `{"skill":"pdf"}`, `{"skill":""}`, `{"skill":""}`}
	click(t, tt, "开始模拟")
	tt.Frame()
	if len(s.set.Runs) != 2 || !strings.Contains(m.asked[len(m.asked)-1].System, "转换和合并 PDF 不用这个技能") || !hasTextPart(tt, "试用的描述") {
		t.Fatalf("second run: %d runs", len(s.set.Runs))
	}

	// Export writes the cases still in use in skill-creator's format.
	s.set.Cases[1].Off = true
	click(t, tt, "导出 eval_set.json")
	tt.Frame()
	b, err := os.ReadFile(a.evalSetPath("pdf"))
	var cases []insight.TriggerCase
	if err != nil || json.Unmarshal(b, &cases) != nil || len(cases) != 3 || cases[0].Query != "把这些 PDF 表单填好" || !cases[0].ShouldTrigger {
		t.Fatalf("eval set %s %v", b, err)
	}
	if !hasTextPart(tt, "已写入") || strings.Contains(strings.Join(tt.Texts(), " "), "run_loop") {
		t.Fatal("export not confirmed")
	}
}

func TestTriggerStopsOnFailure(t *testing.T) {
	a, m := judgeApp(t)
	tt := ui.NewTester(a.View, 1440, 2400)
	tt.Frame()
	s := a.insights.trigger
	s.set.Add(evals.NewCase("a", true, evals.FromPerson, ""), evals.NewCase("b", false, evals.FromPerson, ""))
	tt.Frame()
	click(t, tt, "模拟 2 条")
	tt.Frame()
	m.replies = []string{`{"skill":"pdf"}`}
	click(t, tt, "开始模拟")
	tt.Frame()
	last, _ := s.set.Last()
	if s.busy != "" || len(last.Trials) != 1 || !hasTextPart(tt, "模拟停下了：no reply scripted") {
		t.Fatalf("failed run: busy=%q trials=%d texts=%q", s.busy, len(last.Trials), tt.Texts())
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
