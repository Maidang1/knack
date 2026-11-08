package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseLines(t *testing.T, agent Agent, lines ...string) Session {
	t.Helper()
	s := &parseState{Session: Session{Agent: agent}}
	p := newParser(s)
	for _, l := range lines {
		if err := p.line([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	return s.Session
}

func TestClaudeSkillEpisodes(t *testing.T) {
	body := "Base directory for this skill: /home/u/.claude/skills/pdf\n\n# PDF\n" + strings.Repeat("Use the scripts. ", 100)
	bodyJSON, _ := json.Marshal(body)
	s := parseLines(t, Claude,
		`{"type":"user","uuid":"u1","timestamp":"2026-11-01T00:00:00Z","message":{"role":"user","content":"fill the form"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-01T00:00:01Z","message":{"id":"m1","content":[{"type":"tool_use","id":"s1","name":"Skill","input":{"skill":"pdf"}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-01T00:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","content":"Launching skill: pdf"}]}}`,
		`{"type":"user","uuid":"m1","isMeta":true,"sourceToolUseID":"s1","timestamp":"2026-11-01T00:00:02Z","message":{"role":"user","content":[{"type":"text","text":`+string(bodyJSON)+`}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-01T00:00:03Z","message":{"id":"m2","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"python /home/u/.claude/skills/pdf/scripts/fill.py"}}]}}`,
		`{"type":"user","uuid":"r2","timestamp":"2026-11-01T00:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"filled 12 fields"}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-11-01T00:00:05Z","message":{"id":"m3","content":[{"type":"tool_use","id":"b2","name":"Read","input":{"file_path":"/home/u/.claude/skills/pdf/SKILL.md"}}]}}`,
		// The person invokes a skill with /loop; its text follows as a companion.
		`{"type":"user","uuid":"u2","timestamp":"2026-11-01T00:01:00Z","message":{"role":"user","content":"<command-message>review</command-message>\n<command-name>/review</command-name>"}}`,
		`{"type":"user","uuid":"m2","isMeta":true,"turnCompanion":true,"timestamp":"2026-11-01T00:01:00Z","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /x/review\n\nReview the diff."}]}}`,
		// A built-in command has no companion and is not a skill.
		`{"type":"user","uuid":"u3","timestamp":"2026-11-01T00:02:00Z","message":{"role":"user","content":"<command-name>/model</command-name>"}}`,
		`{"type":"user","uuid":"u4","timestamp":"2026-11-01T00:03:00Z","message":{"role":"user","content":"不对，表单还是空的"}}`,
	)
	if len(s.Turns) != 4 {
		t.Fatalf("turns %d", len(s.Turns))
	}
	sk := s.Turns[0].Skills
	if len(sk) != 1 || sk[0].Source != SkillByModel || sk[0].Dir != "/home/u/.claude/skills/pdf" || sk[0].Tokens < 300 || sk[0].Uses != 1 {
		t.Fatalf("model-loaded skill %+v", sk)
	}
	if tools := s.Turns[0].Tools; tools[1].OutputBytes != int64(len("filled 12 fields")) {
		t.Fatalf("output size %+v", tools[1])
	}
	if sk := s.Turns[1].Skills; len(sk) != 1 || sk[0].Name != "review" || sk[0].Source != SkillByCommand || sk[0].Dir != "/x/review" || sk[0].Tokens == 0 {
		t.Fatalf("command skill %+v", sk)
	}
	if len(s.Turns[2].Skills) != 0 {
		t.Fatalf("built-in command counted as a skill: %+v", s.Turns[2].Skills)
	}
	if s.Turns[0].Corrects || s.Turns[1].Corrects || !s.Turns[3].Corrects {
		t.Fatalf("corrections %v %v %v", s.Turns[0].Corrects, s.Turns[1].Corrects, s.Turns[3].Corrects)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), "表单") || strings.Contains(string(b), "Use the scripts") {
		t.Fatal("prompt or skill text kept in session data")
	}
}

func TestCodexSkillReads(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want [][2]string
	}{
		{`"{\"cmd\":\"cat .codex/skills/dev-router/SKILL.md\",\"max_output_tokens\":12000}"`, [][2]string{{"dev-router", ".codex/skills/dev-router"}}},
		{`"{\"cmd\":\"sed -n '1,200p' /Users/u/.agents/skills/pdf/SKILL.md; rg x\"}"`, [][2]string{{"pdf", "/Users/u/.agents/skills/pdf"}}},
		{`"tools.exec_command({cmd:\"cat a/SKILL.md\"})"`, [][2]string{{"a", "a"}}},
		{`"{\"cmd\":\"rg -n name skills/x/SKILL.md\"}"`, nil},
	} {
		if got := skillReads(json.RawMessage(tc.raw)); len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
			t.Errorf("%s: %v, want %v", tc.raw, got, tc.want)
		}
	}
	s := parseLines(t, Codex,
		`{"type":"event_msg","timestamp":"2026-11-01T00:00:00Z","payload":{"type":"task_started","turn_id":"t1"}}`,
		`{"type":"response_item","timestamp":"2026-11-01T00:00:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>x</environment_context>"}]}}`,
		`{"type":"response_item","timestamp":"2026-11-01T00:00:01Z","payload":{"type":"function_call","name":"exec_command","call_id":"c1","arguments":"{\"cmd\":\"cat .codex/skills/pdf/SKILL.md\"}"}}`,
		`{"type":"response_item","timestamp":"2026-11-01T00:00:02Z","payload":{"type":"function_call_output","call_id":"c1","output":"# PDF skill\nRun scripts/fill.py"}}`,
		`{"type":"response_item","timestamp":"2026-11-01T00:00:03Z","payload":{"type":"function_call","name":"exec_command","call_id":"c2","arguments":"{\"cmd\":\"python .codex/skills/pdf/scripts/fill.py\"}"}}`,
		`{"type":"event_msg","timestamp":"2026-11-01T00:01:00Z","payload":{"type":"task_started","turn_id":"t2"}}`,
		`{"type":"response_item","timestamp":"2026-11-01T00:01:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"still failing"}]}}`,
	)
	sk := s.Turns[0].Skills
	if len(sk) != 1 || sk[0].Name != "pdf" || sk[0].Source != SkillByRead || sk[0].Tokens == 0 || sk[0].Uses != 1 {
		t.Fatalf("codex skill %+v", sk)
	}
	if s.Turns[0].Corrects || !s.Turns[1].Corrects {
		t.Fatal("codex correction not seen")
	}
}
