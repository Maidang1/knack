package evals

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestTriggerSet(t *testing.T) {
	a := NewCase("  Fill   the PDF form ", true, FromSession, "")
	if b := NewCase("fill the pdf FORM", false, FromPerson, ""); a.ID != b.ID || a.Query != "Fill   the PDF form" {
		t.Fatalf("ids differ for the same query: %s %s", a.ID, b.ID)
	}
	var set Triggers
	near := NewCase("merge two PDFs", false, FromModel, "")
	if n := set.Add(a, NewCase("fill the pdf form", true, FromModel, ""), near, NewCase(" ", true, FromPerson, "")); n != 2 {
		t.Fatalf("added %d", n)
	}
	off := NewCase("rotate a page", true, FromPerson, "")
	off.Off = true
	set.Add(off)
	if len(set.Active()) != 2 {
		t.Fatal("left-out case is active")
	}
	run := Run{Trials: map[string]Trial{a.ID: {Picked: "pdf"}, near.ID: {Picked: "pdf", Reason: "mentions PDF"}, off.ID: {}}}
	if s := run.Score("pdf", set.Cases); s.Should != 1 || s.Hit != 1 || s.ShouldNot != 1 || s.False != 1 || s.Correct() != 1 || s.Total() != 2 {
		t.Fatalf("score %+v", s)
	}
	if m := run.Misses("pdf", set.Cases); len(m) != 1 || m[0].Query != "merge two PDFs" || m[0].Reason != "mentions PDF" {
		t.Fatalf("misses %+v", m)
	}
	for i := range 8 {
		set.AddRun(Run{Peers: i})
	}
	if last, _ := set.Last(); len(set.Runs) != keepRuns || last.Peers != 7 {
		t.Fatalf("runs kept: %d", len(set.Runs))
	}

	s := Store{Dir: t.TempDir()}
	if got, err := s.Triggers(); err != nil || len(got.Cases) != 0 {
		t.Fatal("missing set", err)
	}
	if err := s.SaveTriggers(set); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Triggers(); err != nil || len(got.Cases) != 3 || got.Cases[2].Off != true {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

func TestRoute(t *testing.T) {
	skills := []Peer{{"xlsx", "Spreadsheets."}, {"pdf", "Fill PDF\n  forms."}}
	c := &scripted{replies: []string{"```json\n{\"skill\":\"/PDF\",\"reason\":\"a form\"}\n```", `{"skill":"","reason":"no skill"}`}}
	got, _, err := Route(context.Background(), c, skills, "fill this form")
	if err != nil || got.Picked != "pdf" || got.Reason != "a form" {
		t.Fatalf("%+v %v", got, err)
	}
	req := c.asked[0]
	if !req.Cache || !strings.Contains(req.System, "- pdf: Fill PDF forms.\n- xlsx: Spreadsheets.") || !strings.Contains(req.Prompt, "fill this form") {
		t.Fatalf("route request:\n%s\n%s", req.System, req.Prompt)
	}
	if got, _, _ := Route(context.Background(), c, skills, "hi"); got.Picked != "" {
		t.Fatalf("no skill: %+v", got)
	}
}

func TestSynthesize(t *testing.T) {
	have := []Case{NewCase("fill the pdf form", true, FromSession, "")}
	c := &scripted{replies: []string{`{"cases":[
		{"query":"Fill the PDF  form","should_trigger":true,"why":"dup"},
		{"query":"把 invoice.pdf 里的表格填一下","should_trigger":true,"why":"填表"},
		{"query":"把这个 PDF 转成 Word","should_trigger":false,"why":"转换不是填表"},
		{"query":"","should_trigger":true}]}`}}
	got, _, err := Synthesize(context.Background(), c, SkillDoc{Name: "pdf", Description: "Fill forms", Body: "# pdf"}, []Peer{{"docx", "Word files"}}, have, 4)
	if err != nil || len(got) != 2 || got[0].Source != FromModel || got[1].Should || got[1].Note != "转换不是填表" {
		t.Fatalf("%+v %v", got, err)
	}
	p := c.asked[0].Prompt
	for _, want := range []string{"- docx: Word files", "[应触发] fill the pdf form", "4 条应触发"} {
		if !strings.Contains(p, want) {
			t.Errorf("synth prompt lacks %q", want)
		}
	}
	if _, _, err := Synthesize(context.Background(), &scripted{replies: []string{`{"cases":[{"query":"fill the pdf form","should_trigger":true}]}`}}, SkillDoc{}, nil, have, 4); err == nil {
		t.Fatal("accepted only duplicates")
	}
}

func TestRevise(t *testing.T) {
	c := &scripted{replies: []string{`{"description":" Fill PDF forms. Not for converting files. ","why":"划清边界"}`}}
	misses := []Miss{{Query: "convert to word", Picked: "pdf"}, {Query: "fill invoice", Should: true, Picked: "docx", Reason: "word"}}
	desc, why, _, err := Revise(context.Background(), c, SkillDoc{Name: "pdf", Body: "# pdf"}, nil, "Fill forms", misses, 7)
	if err != nil || desc != "Fill PDF forms. Not for converting files." || why != "划清边界" {
		t.Fatalf("%q %q %v", desc, why, err)
	}
	p := c.asked[0].Prompt
	for _, want := range []string{"技能描述：Fill forms", "7 条用例判断正确", "不应触发，但 Agent 加载了这个技能：convert to word", "应触发，但 Agent 加载了 docx：fill invoice", "Agent 的理由：word"} {
		if !strings.Contains(p, want) {
			t.Errorf("revise prompt lacks %q", want)
		}
	}
}

func TestAnalyze(t *testing.T) {
	ev := Evaluator{Criteria: []Criterion{{ID: "read", Text: "先读取表单字段", Kind: KindLLM}, {ID: "rule", Kind: KindRule, Rule: RuleNoFailures, Text: "x"}}}
	hash := ev.Hash()
	verdicts := map[string]Verdict{
		"c:s1:t1": {Evaluator: hash, Results: []Result{{ID: "read", Verdict: Fail, Reason: "直接写了", Quote: "fill.py", Verified: true}}},
		"c:s2:t1": {Evaluator: hash, Results: []Result{{ID: "read", Verdict: Pass, Review: ReviewWrong, Quote: "made up"}}},
		"c:s3:t1": {Evaluator: hash, Results: []Result{{ID: "read", Verdict: Fail, Review: ReviewWrong}}},
		"c:s4:t1": {Evaluator: "old", Results: []Result{{ID: "read", Verdict: Fail}}},
	}
	fails := Failures(ev, verdicts)
	if len(fails) != 2 || fails[0].Load != "c:s1:t1" || fails[0].Quote != "fill.py" || fails[1].Quote != "" || !strings.Contains(fails[1].Reason, "评测者认为没做到") {
		t.Fatalf("failures %+v", fails)
	}
	c := &scripted{replies: []string{`{"causes":[
		{"title":"偶发","detail":"d","fix":"f","failures":[2]},
		{"title":"没读字段","detail":"d","fix":"在第 2 步前加一句","failures":[1,2,2,9]},
		{"title":"","failures":[1]}]}`}}
	causes, _, err := Analyze(context.Background(), c, SkillDoc{Name: "pdf", Body: "# pdf"}, fails)
	if err != nil || len(causes) != 2 || causes[0].Title != "没读字段" || causes[0].Count != 2 || len(causes[0].Loads) != 2 {
		t.Fatalf("%+v %v", causes, err)
	}
	if p := c.asked[0].Prompt; !strings.Contains(p, "[1] 第 1 次加载 · 标准：先读取表单字段") || !strings.Contains(p, "原文：fill.py") || strings.Contains(p, "made up") {
		t.Fatalf("causes prompt:\n%s", p)
	}

	s := Store{Dir: t.TempDir()}
	if a, err := s.Analysis(); a != nil || err != nil {
		t.Fatal("missing analysis")
	}
	if err := s.SaveAnalysis(Analysis{Evaluator: hash, Failures: 2, At: time.Now(), Causes: causes}); err != nil {
		t.Fatal(err)
	}
	if a, err := s.Analysis(); err != nil || len(a.Causes) != 2 {
		t.Fatalf("round trip %+v %v", a, err)
	}
}
