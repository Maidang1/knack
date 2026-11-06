package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputHash(t *testing.T) {
	a := inputHash([]byte(`{"command": "ls",  "cwd":"/x"}`))
	b := inputHash([]byte(`{"command":"ls","cwd":"/x"}`))
	if a == "" || a != b {
		t.Fatalf("formatting splits identical inputs: %q %q", a, b)
	}
	if inputHash([]byte(`{"command":"pwd"}`)) == a {
		t.Fatal("different inputs share a fingerprint")
	}
	if inputHash(nil) != "" || inputHash([]byte("null")) != "" {
		t.Fatal("a missing input has a fingerprint")
	}
	if len(a) != 16 || strings.Contains(a, "ls") {
		t.Fatalf("fingerprint %q is not a short hash", a)
	}
}

func TestToolCallsRecordInputFingerprints(t *testing.T) {
	s := &parseState{Session: Session{Agent: Claude}}
	p := newParser(s)
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-11-01T00:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-01T00:00:01Z","message":{"id":"m1","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-01T00:00:02Z","message":{"id":"m2","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command": "ls"}}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-11-01T00:00:03Z","message":{"id":"m3","content":[{"type":"tool_use","id":"t3","name":"Skill","input":{"skill":"pdf"}}]}}`,
	}
	for _, l := range lines {
		if err := p.line([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	tools := s.Session.Turns[0].Tools
	if len(tools) != 3 || tools[0].InputHash == "" || tools[0].InputHash != tools[1].InputHash || tools[2].InputHash == tools[0].InputHash {
		t.Fatalf("fingerprints %+v", tools)
	}
	if sk := s.Session.Turns[0].Skills; len(sk) != 1 || sk[0].Name != "pdf" {
		t.Fatalf("Skill calls still resolve their name: %+v", sk)
	}
}

func TestCodexReplayShowsSystemPromptOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	meta := `{"type":"session_meta","timestamp":"2026-11-01T00:00:00Z","payload":{"id":"c1","base_instructions":{"text":"PRIVATE_SYSTEM_PROMPT"}}}`
	legacy := `{"type":"session_meta","timestamp":"2026-11-01T00:00:05Z","payload":{"id":"c1","base_instructions":"PRIVATE_SYSTEM_PROMPT"}}`
	if err := os.WriteFile(path, []byte(meta+"\n"+legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := entries(transcriptOf(t, path, Codex), EntryContext)
	if len(ctx) != 1 || ctx[0].Name != "系统提示词" || ctx[0].Text != "PRIVATE_SYSTEM_PROMPT" {
		t.Fatalf("context %+v", ctx)
	}
	if err := os.WriteFile(path, []byte(legacy+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ctx := entries(transcriptOf(t, path, Codex), EntryContext); len(ctx) != 1 || ctx[0].Text != "PRIVATE_SYSTEM_PROMPT" {
		t.Fatalf("string form %+v", ctx)
	}
}
