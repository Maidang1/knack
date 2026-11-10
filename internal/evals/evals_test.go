package evals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"knack/internal/insight"
	"knack/internal/llm"
	"knack/internal/session"
)

// scripted replies in order and keeps what it was asked.
type scripted struct {
	replies []string
	asked   []llm.Request
}

func (s *scripted) Complete(_ context.Context, req llm.Request) (llm.Reply, error) {
	s.asked = append(s.asked, req)
	if len(s.replies) == 0 {
		return llm.Reply{}, errors.New("no reply scripted")
	}
	text := s.replies[0]
	s.replies = s.replies[1:]
	return llm.Reply{Text: text, Input: 100, Output: 10}, nil
}

func turn() []session.Entry {
	return []session.Entry{
		{Kind: session.EntryUser, Text: "把这份 PDF 表单填好"},
		{Kind: session.EntryContext, Text: "Base directory for this skill: /skills/pdf\n\n# pdf\nlong instructions"},
		{Kind: session.EntryContext, Text: "<system-reminder>unrelated</system-reminder>"},
		{Kind: session.EntryThinking, Text: "private reasoning"},
		{Kind: session.EntryTool, Name: "Bash", Input: `{"command":"python scripts/fill.py form.pdf --token=abcdef1234567890"}`, Text: "filled 12   fields\nwrote out.pdf"},
		{Kind: session.EntryTool, Name: "Read", Input: `{"file_path":"out.pdf"}`, Text: "no such file", Error: true},
		{Kind: session.EntryAssistant, Text: "表单已填好，保存为 out.pdf。"},
	}
}

func TestExcerpt(t *testing.T) {
	x := NewExcerpt(turn(), "不对，第二页没填")
	text := x.Text()
	for _, want := range []string{"[1] 用户：把这份 PDF 表单填好", "[2] （技能说明在这里注入上下文，内容略）", "[3] 工具 Bash", "输出（失败）：no such file", "[6] 下一轮用户说：不对，第二页没填"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	for _, not := range []string{"long instructions", "unrelated", "private reasoning", "abcdef1234567890"} {
		if strings.Contains(text, not) {
			t.Errorf("excerpt leaks %q", not)
		}
	}
	if x.Truncated {
		t.Fatal("short turn marked truncated")
	}
	// A quote is found despite reflowed whitespace, and its step corrected.
	if step, ok := x.Locate(5, "filled 12 fields wrote out.pdf"); !ok || step != 3 {
		t.Fatalf("locate: %d %v", step, ok)
	}
	if _, ok := x.Locate(3, "filled 13 fields"); ok {
		t.Fatal("located words that are not there")
	}

	long := NewExcerpt([]session.Entry{{Kind: session.EntryTool, Name: "Bash", Input: "{}", Text: strings.Repeat("字", 5000)}}, "")
	if !long.Truncated || !strings.Contains(long.Text(), "（省略 3500 字）") {
		t.Fatalf("long output not cut: truncated=%v", long.Truncated)
	}
	var many []session.Entry
	for range 60 {
		many = append(many, session.Entry{Kind: session.EntryAssistant, Text: strings.Repeat("a", maxMessage)})
	}
	huge := NewExcerpt(many, "")
	if n := len([]rune(huge.Text())); !huge.Truncated || n > maxExcerpt+5000 || !strings.Contains(huge.Steps[30], "省略") || strings.Contains(huge.Steps[0], "省略") || strings.Contains(huge.Steps[59], "省略") {
		t.Fatalf("turn over budget not trimmed from the middle: %d runes", n)
	}
}

func TestRedact(t *testing.T) {
	for in, gone := range map[string]string{
		"the key sk-fake-0000000000000000 was pasted":      "sk-fake",
		"export SERVICE_API_KEY=notarealvalue123":          "notarealvalue",
		`{"password": "hunter2hunter2"}`:                   "hunter2",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz": "abcdefghijkl",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123":         "ghp_",
		"AKIAIOSFODNN7EXAMPLE":                             "AKIA",
	} {
		if out := Redact(in); strings.Contains(out, gone) || !strings.Contains(out, redacted) {
			t.Errorf("Redact(%q) = %q", in, out)
		}
	}
	if in := "the token count was 1234 and the key idea is simple"; Redact(in) != in {
		t.Errorf("ordinary prose changed: %q", Redact(in))
	}
}

func TestDraft(t *testing.T) {
	c := &scripted{replies: []string{"好的：\n```json\n" + `{"criteria":[
		{"id":"Read First","text":"填写之前先读取了表单的字段"},
		{"id":"read-first","text":"用脚本而不是手写代码填写"},
		{"id":"","text":"  "},
		{"id":"！","text":"最后说明了输出文件的位置"}]}` + "\n```"}}
	doc := SkillDoc{Name: "pdf", Description: "Fill PDF forms.", Body: "# pdf\nRun scripts/fill.py", Files: []string{"scripts/fill.py"}}
	got, reply, err := Draft(context.Background(), c, doc, []Sample{{Outcome: "被纠正", Excerpt: NewExcerpt(turn(), "不对")}})
	if err != nil || reply.Input != 100 {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "read-first" || got[1].ID != "read-first-2" || got[2].ID != "c" || got[0].Kind != KindLLM {
		t.Fatalf("criteria: %+v", got)
	}
	prompt := c.asked[0].Prompt
	for _, want := range []string{"Run scripts/fill.py", "- scripts/fill.py", `outcome="被纠正"`, "下一轮用户说：不对"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("draft prompt lacks %q", want)
		}
	}
	if _, _, err := Draft(context.Background(), &scripted{replies: []string{"抱歉，我做不到"}}, doc, nil); err == nil {
		t.Fatal("accepted a reply without criteria")
	}
	if id := NewID(append(got, Criterion{ID: "custom-1"})); id != "custom-2" {
		t.Fatalf("new id %s", id)
	}
}

func TestJudge(t *testing.T) {
	criteria := []Criterion{
		{ID: "script", Text: "用脚本填写", Kind: KindLLM},
		{ID: "verify", Text: "填完后核对了结果", Kind: KindLLM},
		{ID: "report", Text: "说明了输出位置", Kind: KindLLM},
	}
	c := &scripted{replies: []string{`{"results":[
		{"id":"script","verdict":"PASS","step":3,"quote":"python scripts/fill.py form.pdf","reason":"调用了自带脚本"},
		{"id":"verify","verdict":"fail","step":4,"quote":"核对无误，全部字段正确","reason":"没有核对"},
		{"id":"extra","verdict":"pass"}]}`}}
	x := NewExcerpt(turn(), "")
	wrong := []Result{{ID: "verify", Verdict: Pass, Quote: "看了一眼", Review: ReviewWrong}, {ID: "gone", Verdict: Pass, Review: ReviewWrong}}
	got, _, err := Judge(context.Background(), c, SkillDoc{Name: "pdf"}, criteria, wrong, x)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Verdict != Pass || !got[0].Verified || got[0].Step != 3 {
		t.Fatalf("quoted verdict: %+v", got[0])
	}
	// The model invented this quote; the verdict is kept but not vouched for.
	if got[1].Verdict != Fail || got[1].Verified || got[1].Step != 0 {
		t.Fatalf("unverified verdict: %+v", got[1])
	}
	if got[2].Verdict != NA || got[2].Reason == "" {
		t.Fatalf("criterion the model skipped: %+v", got[2])
	}
	prompt := c.asked[0].Prompt
	if !strings.Contains(prompt, "你曾判为通过，评测者认为应为未通过") || !strings.Contains(prompt, "看了一眼") || strings.Contains(prompt, "gone") {
		t.Fatalf("corrections in prompt:\n%s", prompt)
	}
	if _, _, err := Judge(context.Background(), &scripted{replies: []string{"not json"}}, SkillDoc{}, criteria, nil, x); err == nil {
		t.Fatal("accepted a reply that is not JSON")
	}
}

func TestEvaluatorAndStore(t *testing.T) {
	e := Evaluator{Skill: "pdf", Created: time.Now().UTC().Truncate(time.Second), Criteria: append(Rules(true, true), Criterion{ID: "a", Text: "先读后写", Kind: KindLLM})}
	if len(Rules(false, false)) != 2 || len(Rules(true, true)) != 4 || len(e.Judged()) != 1 {
		t.Fatal("rules")
	}
	hash := e.Hash()
	e.Criteria = e.Criteria[1:] // dropping a rule leaves the judged criteria alone
	if e.Hash() != hash {
		t.Fatal("hash depends on rules")
	}
	edited := e
	edited.Criteria = append([]Criterion{}, e.Criteria...)
	edited.Criteria[len(edited.Criteria)-1].Text = "先读后写，再核对"
	if edited.Hash() == hash {
		t.Fatal("hash ignores a reworded criterion")
	}

	ep := insight.Episode{Agent: session.Claude, SessionID: "s1", TurnID: "t1", Failed: 1, Uses: 2}
	if Key(ep) != "claude:s1:t1" {
		t.Fatal(Key(ep))
	}
	for rule, want := range map[string]string{RuleNoFailures: Fail, RuleNotInterrupted: Pass, RuleNotCorrected: Pass, RuleUsedFiles: Pass, "unknown": NA} {
		if got := RuleVerdict(Criterion{Rule: rule}, ep); got != want {
			t.Errorf("%s = %s, want %s", rule, got, want)
		}
	}

	s := Store{Dir: SkillDir(filepath.Join(t.TempDir(), "evals"), "plugin:pdf")}
	if filepath.Base(s.Dir) != "plugin-pdf" {
		t.Fatal(s.Dir)
	}
	if got, err := s.Evaluator(); got != nil || err != nil {
		t.Fatalf("missing evaluator: %v %v", got, err)
	}
	if err := s.SaveEvaluator(e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Evaluator()
	if err != nil || got.Hash() != hash || !got.Created.Equal(e.Created) {
		t.Fatalf("evaluator round trip: %+v %v", got, err)
	}
	v := Verdict{Evaluator: hash, Model: "m", At: time.Now(), Results: []Result{
		{ID: "a", Verdict: Pass, Review: ReviewWrong},
		{ID: "b", Verdict: Fail, Review: ReviewOK},
		{ID: "c", Verdict: NA},
	}}
	if tally := v.Count(); tally.Pass != 0 || tally.Fail != 2 || tally.NA != 1 || tally.Reviewed != 2 || tally.Agreed != 1 {
		t.Fatalf("tally %+v", tally)
	}
	if err := s.SaveVerdicts(map[string]Verdict{Key(ep): v}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(s.Dir, "verdicts.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("verdicts readable by others")
	}
	all, err := s.Verdicts()
	if err != nil || len(all) != 1 || len(Corrections(all, 8)) != 1 || Corrections(all, 8)[0].ID != "a" {
		t.Fatalf("verdicts round trip: %+v %v", all, err)
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.Dir); !os.IsNotExist(err) {
		t.Fatal("cleared directory remains")
	}
}
