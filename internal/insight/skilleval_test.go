package insight

import (
	"os"
	"strings"
	"testing"
	"time"

	"knack/internal/session"
)

func TestEvaluateSkills(t *testing.T) {
	turn := func(id string, at time.Time, skills []session.SkillCall, tools []session.ToolCall, interrupted, corrects bool) session.Turn {
		return session.Turn{ID: id, Start: at, Skills: skills, Tools: tools, Interrupted: interrupted, Corrects: corrects,
			Requests: []session.Request{{ID: id, End: at, UsageKnown: true, Tokens: session.Tokens{Input: 1000}}}}
	}
	edit := t0.Add(48 * time.Hour)
	pdf := func(at time.Time, src string) []session.SkillCall {
		return []session.SkillCall{{Name: "pdf", ToolID: "sk", At: at, Source: src, Tokens: 800, Uses: 1}}
	}
	ok := []session.ToolCall{{ID: "sk", Name: "Skill", Start: t0}, {ID: "a", Name: "Bash", Start: t0.Add(time.Hour * 100)}}
	bad := []session.ToolCall{{ID: "b", Name: "Bash", Start: t0.Add(time.Hour * 100), Failed: true}}
	s := session.Session{ID: "s", Agent: session.Claude, Title: "表单", Turns: []session.Turn{
		turn("1", t0, pdf(t0, session.SkillByModel), bad, false, false),
		turn("2", t0.Add(time.Hour), nil, nil, false, true), // corrects turn 1
		turn("3", edit.Add(time.Hour), pdf(edit.Add(time.Hour), session.SkillByCommand), ok, false, false),
		turn("4", edit.Add(2*time.Hour), pdf(edit.Add(2*time.Hour), session.SkillByModel), nil, true, false),
	}}
	child := session.Session{ID: "c", Agent: session.Claude, Turns: []session.Turn{turn("c1", t0, []session.SkillCall{{Name: "review", At: t0}}, nil, false, false)}}
	s.Turns[0].Subagents = []session.Subagent{{ID: "c", Session: child}}

	r := EvaluateSkills(&session.Snapshot{Sessions: []session.Session{s}}, session.Filter{})
	e, found := r.Lookup("pdf")
	if !found || len(e.Episodes) != 3 || e.Auto != 2 || e.Manual != 1 || e.Used != 3 || e.Clean != 1 || e.Corrected != 1 || e.Interrupted != 1 || e.LoadTokens != 2400 {
		t.Fatalf("pdf %+v", e)
	}
	if e.Episodes[0].Turn != 3 || e.Episodes[2].Outcome() != "被纠正" || e.MedianLoad() != 800 {
		t.Fatalf("episodes %+v", e.Episodes)
	}
	// The Skill call itself is not work done after loading.
	if e.Episodes[1].Calls != 1 {
		t.Fatalf("calls after load %+v", e.Episodes[1])
	}
	before, after := e.Split(edit)
	if before.Episodes != 1 || before.Clean != 0 || after.Episodes != 2 || after.Clean != 1 {
		t.Fatalf("split %+v %+v", before, after)
	}
	if rv, ok := r.Lookup("review"); !ok || !rv.Episodes[0].Subagent || rv.Episodes[0].SessionID != "s" || rv.Episodes[0].TurnID != "1" {
		t.Fatalf("sub-Agent loads belong to the turn that ran them: %+v", rv)
	}
	if r.Baseline.Episodes != 1 || r.Baseline.Clean != 1 {
		t.Fatalf("baseline %+v", r.Baseline)
	}
	f := session.Filter{From: edit}
	if e, _ := EvaluateSkills(&session.Snapshot{Sessions: []session.Session{s}}, f).Lookup("pdf"); len(e.Episodes) != 2 {
		t.Fatalf("filter %+v", e)
	}
}

func TestTriggerSet(t *testing.T) {
	if q := PromptQuery("<command-message>pdf</command-message>\n<command-name>/pdf</command-name>\n<command-args>fill the tax form</command-args>"); q != "fill the tax form" {
		t.Fatalf("command query %q", q)
	}
	if q := PromptQuery("[Image #1] 把这个 PDF 表单填好"); q != "把这个 PDF 表单填好" {
		t.Fatalf("image placeholder %q", q)
	}
	if PromptQuery("<environment_context>cwd</environment_context>") != "" || PromptQuery("[Request interrupted by user]") != "" {
		t.Fatal("injected text is not a query")
	}
	desc := "Extract text and tables from PDF files, fill PDF forms. Use when working with PDF documents."
	if Relevance(desc, "fill the pdf forms for me") == 0 || Relevance(desc, "deploy the worker") != 0 {
		t.Fatal("relevance")
	}
	loaded := []TriggerCandidate{{Query: "fill the form", Loaded: true, At: t0}, {Query: "Fill  the form", Loaded: true, At: t0.Add(time.Hour)}, {Query: "merge pdfs", Loaded: true, At: t0}}
	others := []TriggerCandidate{{Query: "read pdf tables", Score: 0.5}, {Query: "unrelated", Score: 0}, {Query: "pdf forms help", Score: 0.8},
		{Query: "barely", Score: 0.01}, {Query: "review my diff", Other: "review"}}
	set := TriggerSet(loaded, others, 5)
	if len(set) != 5 || set[0].Query != "Fill  the form" || set[1].Query != "merge pdfs" || set[2].Query != "pdf forms help" || set[3].Query != "read pdf tables" || set[4].Query != "review my diff" {
		t.Fatalf("set %+v", set)
	}
	path := t.TempDir() + "/pdf/eval_set.json"
	if err := WriteTriggerSet(path, []TriggerCase{{Query: "fill the form", ShouldTrigger: true}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"should_trigger": true`) {
		t.Fatalf("file %s", b)
	}
}
