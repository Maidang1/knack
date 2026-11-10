package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/evals"
	"knack/internal/llm"
)

// scriptedModel replies in order and keeps what it was sent.
type scriptedModel struct {
	replies []string
	asked   []llm.Request
	err     error
}

func (m *scriptedModel) Complete(_ context.Context, req llm.Request) (llm.Reply, error) {
	m.asked = append(m.asked, req)
	if m.err != nil {
		return llm.Reply{}, m.err
	}
	if len(m.replies) == 0 {
		return llm.Reply{}, errors.New("no reply scripted")
	}
	text := m.replies[0]
	m.replies = m.replies[1:]
	return llm.Reply{Text: text, Input: 1200, Output: 80}, nil
}

// judgeApp is insightsApp with a connected model and a log for the turn
// that loaded pdf.
func judgeApp(t *testing.T) (*App, *scriptedModel) {
	t.Helper()
	a := insightsApp(t)
	s := &a.sessions.snapshot.Sessions[0]
	at := func(d time.Duration) string { return s.Turns[0].Start.Add(d).UTC().Format(time.RFC3339Nano) }
	lines := []string{
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"` + at(0) + `","message":{"role":"user","content":"把这些 PDF 表单填好"}}`,
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"` + at(time.Second) + `","message":{"id":"r1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"python scripts/fill.py form.pdf --token=abcdef1234567890"}}]}}`,
		`{"type":"user","uuid":"u2","sessionId":"s1","timestamp":"` + at(2*time.Second) + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"filled 12 fields"}]}}`,
		`{"type":"assistant","uuid":"a2","sessionId":"s1","timestamp":"` + at(3*time.Second) + `","message":{"id":"r2","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"表单已填好，保存为 out.pdf。"}]}}`,
	}
	log := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(log, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Path = log
	m := &scriptedModel{}
	a.model.config = llm.Config{Enabled: true, Protocol: llm.Anthropic, Model: "claude-opus-5-5"}
	a.model.draft = a.model.config
	a.model.connect = func(llm.Config) (llm.Client, error) { return m, nil }
	a.insights.tab, a.insights.evalSkill = tabEval, "pdf"
	return a, m
}

const (
	draftReply  = `{"criteria":[{"id":"use-script","text":"用自带脚本填写表单"},{"id":"verify","text":"填完后核对了字段"}]}`
	judgeReply1 = `{"results":[
		{"id":"use-script","verdict":"pass","step":2,"quote":"python scripts/fill.py form.pdf","reason":"调用了自带的填写脚本"},
		{"id":"verify","verdict":"fail","step":3,"quote":"逐个核对了全部字段","reason":"没有看到核对的步骤"}]}`
)

func TestJudgeNeedsConnection(t *testing.T) {
	a, m := judgeApp(t)
	a.model.config.Enabled = false
	tt := ui.NewTester(a.View, 1440, 1000)
	tt.Frame()
	if !hasTextPart(tt, "LLM 评测") || !hasTextPart(tt, "还没有连接模型") {
		t.Fatalf("section missing: %q", tt.Texts())
	}
	// The draft button is there but does nothing until the connection is on.
	_ = tt.Click("生成评估器")
	tt.Frame()
	if len(m.asked) != 0 || a.insights.judge.ev != nil {
		t.Fatal("sent content with the connection off")
	}
	click(t, tt, "连接模型")
	tt.Frame()
	if !a.settingsOpen || a.settingsTab != settingsModelTab || !hasTextPart(tt, "允许把会话内容发给这个服务") {
		t.Fatalf("settings did not open on the model: open=%v tab=%d", a.settingsOpen, a.settingsTab)
	}
}

func TestJudgeFlow(t *testing.T) {
	a, m := judgeApp(t)
	m.replies = []string{draftReply, judgeReply1}
	tt := ui.NewTester(a.View, 1440, 2000)
	click(t, tt, "生成评估器")
	tt.Frame()
	j := a.insights.judge
	if j == nil || j.ev == nil || j.err != "" {
		t.Fatalf("no evaluator: %+v", j)
	}
	// Rules Knack checks itself come first, then what the model drafted.
	if got := j.ev.Judged(); len(got) != 2 || got[0].ID != "use-script" || len(j.ev.Criteria) != 5 {
		t.Fatalf("criteria: %+v", j.ev.Criteria)
	}
	draft := m.asked[0].Prompt
	for _, want := range []string{"# pdf", "把这些 PDF 表单填好", "filled 12 fields"} {
		if !strings.Contains(draft, want) {
			t.Errorf("draft prompt lacks %q", want)
		}
	}
	if strings.Contains(draft, "abcdef1234567890") {
		t.Fatal("draft prompt carries a credential")
	}
	if saved, err := j.store.Evaluator(); err != nil || saved == nil || saved.Hash() != j.ev.Hash() {
		t.Fatalf("evaluator not saved: %v", err)
	}
	if !hasTextPart(tt, "评估标准") || !hasTextPart(tt, "0/1") {
		t.Fatalf("evaluator not shown: %q", tt.Texts())
	}

	// Planning reads the log and states what would be sent; nothing goes yet.
	click(t, tt, "评审 1 次加载")
	tt.Frame()
	if len(m.asked) != 1 || j.plan == nil || !hasTextPart(tt, "将把 1 次加载的会话记录发给 api.anthropic.com") || !hasTextPart(tt, "Token（估算）") {
		t.Fatalf("plan: asked=%d texts=%q", len(m.asked), tt.Texts())
	}
	click(t, tt, "开始评审")
	tt.Frame()
	if j.err != "" || j.busy != "" || len(m.asked) != 2 {
		t.Fatalf("run: err=%q busy=%q asked=%d", j.err, j.busy, len(m.asked))
	}
	prompt := m.asked[1].Prompt
	if !strings.Contains(prompt, "use-script：用自带脚本填写表单") || !strings.Contains(prompt, "filled 12 fields") || strings.Contains(prompt, "abcdef1234567890") {
		t.Fatalf("judge prompt:\n%s", prompt)
	}
	v, ok := j.current("claude:s1:t1")
	if !ok || len(v.Results) != 2 || !v.Results[0].Verified || v.Results[1].Verified || v.Input != 1200 {
		t.Fatalf("verdict: %+v", v)
	}
	if !hasTextPart(tt, "评审结论") || !hasTextPart(tt, "1/1") {
		t.Fatalf("results not listed: %q", tt.Texts())
	}

	click(t, tt, "重构解析器 第 1 轮的评审结论")
	tt.Frame()
	for _, want := range []string{"通过 · 用自带脚本填写表单", "调用了自带的填写脚本", "第 2 步：“python scripts/fill.py form.pdf”", "未通过 · 填完后核对了字段", "引文在这一轮的记录里没有找到", "未通过 · 加载之后的工具调用没有失败"} {
		if !hasTextPart(tt, want) {
			t.Errorf("verdict lacks %q in %q", want, tt.Texts())
		}
	}

	// The person's word on each verdict is kept and counted.
	click(t, tt, "判错了")
	tt.Frame()
	click(t, tt, "其余都判对了")
	tt.Frame()
	v, _ = j.current("claude:s1:t1")
	if v.Results[0].Review != evals.ReviewWrong || v.Results[1].Review != evals.ReviewOK || v.Results[0].Final() != evals.Fail {
		t.Fatalf("reviews: %+v", v.Results)
	}
	if saved, _ := j.store.Verdicts(); saved["claude:s1:t1"].Results[0].Review != evals.ReviewWrong {
		t.Fatal("review not saved")
	}
	e, _ := a.skillEvals().Lookup("pdf")
	if sum := j.summary(e); !hasTextPart(tt, "模型判为通过，你标为判错。") || sum.tally.Agreed != 1 || sum.tally.Reviewed != 2 {
		t.Fatalf("correction not shown: %q", tt.Texts())
	}

	// A new criterion makes the verdict stale; the next run tells the model
	// where it was wrong before.
	j.adding = "最后说明了输出文件的位置"
	tt.Frame()
	click(t, tt, "添加")
	tt.Frame()
	if _, ok := j.current("claude:s1:t1"); ok || !hasTextPart(tt, "标准改动过，1 次加载需要重新评审") {
		t.Fatalf("stale verdict still current: %q", tt.Texts())
	}
	m.replies = []string{`{"results":[{"id":"use-script","verdict":"fail","step":0,"quote":"","reason":"x"}]}`}
	click(t, tt, "评审 1 次加载")
	tt.Frame()
	click(t, tt, "开始评审")
	tt.Frame()
	last := m.asked[len(m.asked)-1].Prompt
	if !strings.Contains(last, "你曾判为通过，评测者认为应为未通过") || !strings.Contains(last, "custom-1：最后说明了输出文件的位置") {
		t.Fatalf("correction not sent:\n%s", last)
	}
}

func TestJudgeCauses(t *testing.T) {
	a, m := judgeApp(t)
	m.replies = []string{draftReply, judgeReply1,
		`{"causes":[{"title":"没有核对填写结果","detail":"脚本跑完就结束了","fix":"在 SKILL.md 第 3 步后加：用 scripts/check.py 核对","failures":[1]}]}`}
	tt := ui.NewTester(a.View, 1440, 2400)
	click(t, tt, "生成评估器")
	tt.Frame()
	click(t, tt, "评审 1 次加载")
	tt.Frame()
	click(t, tt, "开始评审")
	tt.Frame()
	// Asking for causes states what it sends first.
	click(t, tt, "归纳 1 条未通过的原因")
	tt.Frame()
	j := a.insights.judge
	if len(m.asked) != 2 || j.causes == nil || !hasTextPart(tt, "将把 SKILL.md 和 1 条未通过结论的理由、引文发给 api.anthropic.com") {
		t.Fatalf("cause plan: asked=%d texts=%q", len(m.asked), tt.Texts())
	}
	click(t, tt, "开始归纳")
	tt.Frame()
	p := m.asked[2].Prompt
	if !strings.Contains(p, "# pdf") || !strings.Contains(p, "标准：填完后核对了字段") || strings.Contains(p, "逐个核对了全部字段") {
		t.Fatalf("causes prompt (an unverified quote is not evidence):\n%s", p)
	}
	for _, want := range []string{"没有核对填写结果", "1 次加载 · 1 条结论 · 重构解析器 第 1 轮", "建议：在 SKILL.md 第 3 步后加"} {
		if !hasTextPart(tt, want) {
			t.Errorf("cause lacks %q in %q", want, tt.Texts())
		}
	}
	if saved, err := j.store.Analysis(); err != nil || saved == nil || len(saved.Causes) != 1 {
		t.Fatalf("analysis not saved: %v", err)
	}
	// Marking the failure wrong leaves nothing to explain; the analysis says it is out of date.
	v, _ := j.current("claude:s1:t1")
	v.Results[1].Review = evals.ReviewWrong
	j.verdicts["claude:s1:t1"] = v
	tt.Frame()
	if hasTextPart(tt, "重新归纳") || !hasTextPart(tt, "没有核对填写结果") {
		t.Fatalf("analysis with no failures left: %q", tt.Texts())
	}
}

func TestJudgeStopsOnFailure(t *testing.T) {
	a, m := judgeApp(t)
	m.replies = []string{draftReply}
	tt := ui.NewTester(a.View, 1440, 2000)
	click(t, tt, "生成评估器")
	tt.Frame()
	click(t, tt, "评审 1 次加载")
	tt.Frame()
	m.err = errors.New("密钥无效或没有权限（401）")
	click(t, tt, "开始评审")
	tt.Frame()
	j := a.insights.judge
	if j.busy != "" || len(j.verdicts) != 0 || !hasTextPart(tt, "评审停下了：密钥无效或没有权限（401）") {
		t.Fatalf("failed run: busy=%q verdicts=%d texts=%q", j.busy, len(j.verdicts), tt.Texts())
	}
}

func TestModelSettings(t *testing.T) {
	a, m := judgeApp(t)
	m.replies = []string{"ok", draftReply}
	a.settingsTab = settingsModelTab
	a.openSettings()
	tt := ui.NewTester(a.View, 1440, 1000)
	tt.Frame()
	for _, want := range []string{"技能评测用它起草评估标准", "OpenAI 兼容", "允许把会话内容发给这个服务", "关闭时不会向任何服务发送内容。"} {
		if !hasTextPart(tt, want) {
			t.Errorf("settings lack %q in %q", want, tt.Texts())
		}
	}
	a.model.draft.APIKey = "$KNACK_KEY"
	click(t, tt, "保存并测试")
	tt.Frame()
	if !hasTextPart(tt, "连接正常，claude-opus-5-5 有回复。") {
		t.Fatalf("test result: %q %q", a.model.status, tt.Texts())
	}
	st, err := os.Stat(a.modelPath())
	saved, lerr := llm.Load(a.modelPath())
	if err != nil || lerr != nil || st.Mode().Perm() != 0o600 || saved.APIKey != "$KNACK_KEY" || !saved.Enabled {
		t.Fatalf("saved connection: %+v %v %v", saved, err, lerr)
	}
	click(t, tt, "允许发送会话内容")
	tt.Frame()
	if saved, _ := llm.Load(a.modelPath()); saved.Enabled || a.model.config.Ready() {
		t.Fatal("switching off was not saved")
	}

	// Clearing takes a second click and removes what evaluation wrote.
	a.model.config.Enabled = true
	a.settingsOpen = false
	tt.Frame()
	click(t, tt, "生成评估器")
	tt.Frame()
	dir := a.insights.judge.store.Dir
	if _, err := os.Stat(filepath.Join(dir, "evaluator.json")); err != nil {
		t.Fatal(err)
	}
	a.openSettings()
	tt.Frame()
	click(t, tt, "清除评测数据")
	tt.Frame()
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("cleared on the first click")
	}
	click(t, tt, "确认清除")
	tt.Frame()
	// The page behind the dialog reloads the skill's evaluation, now empty.
	if _, err := os.Stat(dir); !os.IsNotExist(err) || a.insights.judge == nil || a.insights.judge.ev != nil {
		t.Fatal("evaluation data remains")
	}
}
