package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func transcriptOf(t *testing.T, path string, agent Agent) *Transcript {
	t.Helper()
	tr, err := ReadTranscript(context.Background(), path, agent)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func entries(tr *Transcript, kind EntryKind) []Entry {
	var out []Entry
	for _, e := range tr.Entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func TestReadTranscript(t *testing.T) {
	t.Run("Claude prompts, tools and context join statistics by ID", func(t *testing.T) {
		tr := transcriptOf(t, "testdata/claude.jsonl", Claude)
		users := entries(tr, EntryUser)
		if len(users) != 2 || users[0].Text != "PRIVATE_USER_PROMPT" || users[1].Text != "PRIVATE_SECOND_PROMPT" {
			t.Fatalf("prompts %+v", users)
		}
		if ctx := entries(tr, EntryContext); len(ctx) != 1 || ctx[0].Text != "PRIVATE_COMPACTION_SUMMARY" {
			t.Fatalf("compaction summary is not a prompt: %+v", ctx)
		}
		s := fixtureState(t, Claude, "claude.jsonl")
		for _, turn := range s.Session.Turns {
			for _, tool := range turn.Tools {
				found := false
				for _, e := range entries(tr, EntryTool) {
					if e.ToolID == tool.ID {
						found = e.Text == "PRIVATE_TOOL_OUTPUT" && !e.End.IsZero() && e.RequestID != ""
					}
				}
				if !found {
					t.Fatalf("tool %s has no replay content", tool.ID)
				}
			}
		}
	})
	t.Run("Codex responses collect their items and measured reasoning", func(t *testing.T) {
		tr := transcriptOf(t, "testdata/codex.jsonl", Codex)
		base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		think := entries(tr, EntryThinking)
		if len(think) != 1 || think[0].RequestID != "resp1" || !think[0].Start.Equal(base.Add(1500*time.Millisecond)) || !think[0].End.Equal(base.Add(2500*time.Millisecond)) {
			t.Fatalf("reasoning %+v", think)
		}
		reply := entries(tr, EntryAssistant)
		tool := entries(tr, EntryTool)
		if len(reply) != 1 || reply[0].Text != "PRIVATE_CODEX_REPLY" || reply[0].RequestID != "resp1" {
			t.Fatalf("reply %+v", reply)
		}
		if len(tool) != 1 || tool[0].Input != "PRIVATE_SCRIPT" || tool[0].Text != "PRIVATE_CODEX_TOOL_OUTPUT" || tool[0].RequestID != "resp1" || tool[0].End.Sub(tool[0].Start) != 5*time.Second {
			t.Fatalf("tool %+v", tool)
		}
		if users := entries(tr, EntryUser); len(users) != 1 || users[0].Text != "PRIVATE_CODEX_PROMPT" {
			t.Fatalf("prompt %+v", users)
		}
	})
	t.Run("an interruption notice ends the turn instead of starting one", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		put(t, path, []byte(strings.Join([]string{
			`{"type":"user","timestamp":"2026-11-01T00:00:00Z","uuid":"u1","message":{"role":"user","content":"do it"}}`,
			`{"type":"assistant","timestamp":"2026-11-01T00:00:02Z","uuid":"a1","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"text","text":"working"}]}}`,
			`{"type":"user","timestamp":"2026-11-01T00:00:03Z","uuid":"u2","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
			`{"type":"user","timestamp":"2026-11-01T00:00:09Z","uuid":"u3","message":{"role":"user","content":"next"}}`,
		}, "\n")+"\n"))
		st := parseState{Session: Session{Agent: Claude}}
		p := newParser(&st)
		b, _ := os.ReadFile(path)
		for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
			if err := p.line(line); err != nil {
				t.Fatal(err)
			}
		}
		if len(st.Session.Turns) != 2 || !st.Session.Turns[0].Interrupted || st.Session.Turns[1].Interrupted {
			t.Fatalf("turns %+v", st.Session.Turns)
		}
		tr := transcriptOf(t, path, Claude)
		if ev := entries(tr, EntryEvent); len(ev) != 1 || ev[0].Name != "已中断" || len(entries(tr, EntryUser)) != 2 {
			t.Fatalf("interruption replay %+v", tr.Entries)
		}
	})
	t.Run("oversized compaction keeps its event", func(t *testing.T) {
		home := t.TempDir()
		huge := `{"timestamp":"2026-11-01T00:00:23Z","type":"compacted","payload":{"replacement_history":["` + strings.Repeat("x", maxLineBytes+10) + `"]}}`
		b := fixture(t, "codex.jsonl")
		b = bytes.Replace(b, []byte(`{"type":"compacted","timestamp":"2026-11-01T00:00:23Z","payload":{"replacement_history":["PRIVATE_HISTORY"]}}`), []byte(huge), 1)
		path := filepath.Join(home, ".codex/sessions/2026/11/01/s.jsonl")
		put(t, path, b)
		s := refresh(t, reader(t, home, ""))
		if len(s.Sessions) != 1 || s.Sessions[0].Incomplete || len(s.Warnings) > 0 {
			t.Fatalf("oversized compaction marked incomplete: %+v %q", s.Sessions, s.Warnings)
		}
		compacts := 0
		for _, ev := range s.Sessions[0].Turns[1].Events {
			if ev.Kind == "compact" {
				compacts++
			}
		}
		if compacts != 1 {
			t.Fatalf("compaction event lost: %+v", s.Sessions[0].Turns[1].Events)
		}
		if tr := transcriptOf(t, path, Codex); tr.Skipped != 0 || len(entries(tr, EntryEvent)) != 2 {
			t.Fatalf("replay of oversized compaction %+v", tr)
		}
		// Split across bounded refreshes, the record still keeps its event.
		saved := maxRefreshRead
		defer func() { maxRefreshRead = saved }()
		// Larger than any accepted record, smaller than the oversized one.
		maxRefreshRead = maxLineBytes + 1024
		r := reader(t, home, filepath.Join(home, "cache.json"))
		s = refresh(t, r)
		for range 20 {
			if !s.More {
				break
			}
			s = refresh(t, reader(t, home, filepath.Join(home, "cache.json")))
		}
		if s.More || len(s.Sessions) != 1 || s.Sessions[0].Incomplete || len(s.Warnings) > 0 {
			t.Fatalf("split oversized record: more=%t %q", s.More, s.Warnings)
		}
		cached, _ := os.ReadFile(filepath.Join(home, "cache.json"))
		if bytes.Contains(cached, []byte("xxxx")) {
			t.Fatal("oversized record content reached the cache")
		}
	})
}

// A bounded refresh reads the newest logs first, whatever their agent, and
// reports More until the backlog is consumed.
func TestRefreshPrioritizesRecentLogs(t *testing.T) {
	saved := maxRefreshRead
	defer func() { maxRefreshRead = saved }()
	home := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for i := range 3 {
		path := filepath.Join(home, ".claude/projects/p", string(rune('a'+i))+".jsonl")
		put(t, path, fixture(t, "claude.jsonl"))
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	put(t, filepath.Join(home, ".codex/sessions/2026/11/01/new.jsonl"), fixture(t, "codex.jsonl"))
	maxRefreshRead = int64(len(fixture(t, "codex.jsonl")) + 10)
	r := reader(t, home, "")
	s := refresh(t, r)
	codex := false
	for _, src := range s.Sources {
		if src.Agent == Codex {
			codex = src.Supported
		}
	}
	if !codex || !s.More {
		t.Fatalf("newest Codex log starved by older Claude logs: more=%t sources=%+v", s.More, s.Sources)
	}
	for range 20 {
		if !s.More {
			break
		}
		s = refresh(t, r)
	}
	if s.More || s.Partial || len(s.Sessions) != 4 {
		t.Fatalf("backlog not consumed: more=%t partial=%t sessions=%d", s.More, s.Partial, len(s.Sessions))
	}
}
