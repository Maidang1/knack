package session

import (
	"bufio"
	"os"
	"testing"
	"time"
)

func fixtureState(t *testing.T, agent Agent, name string) *parseState {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s := &parseState{Session: Session{Agent: agent}}
	p := newParser(s)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if err := p.line(scanner.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParserLine(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		agent                          Agent
		fixture                        string
		turns, requests, tools, skills int
		tokens                         int64
		check                          func(*testing.T, *parseState)
	}{
		{"Claude phases", Claude, "claude.jsonl", 2, 6, 3, 1, 202, func(t *testing.T, s *parseState) {
			first := s.Session.Turns[0]
			if s.Session.Title != "测试会话" || s.Session.CWD != "/work/project" || s.Session.GitBranch != "main" {
				t.Fatal("missing session metadata")
			}
			if len(first.Requests) != 5 || first.Requests[0].Tokens.Output != 20 || first.Requests[0].Elapsed.Value != 3*time.Second {
				t.Fatalf("stream usage/duration: %+v", first.Requests)
			}
			r := first.Requests[0]
			if r.Thinking.Value != time.Second || r.Generation.Value != 2*time.Second || !r.Generation.Estimated {
				t.Fatalf("thinking/generation: %+v", r)
			}
			if rate, ok := r.OutputRate(); !ok || rate != 10 {
				t.Fatalf("rate=%v known=%v", rate, ok)
			}
			if !first.Requests[2].APIError || !first.Requests[3].Retry {
				t.Fatal("API failure must mark next request as inferred retry")
			}
			if first.Tools[0].PermissionWait || !first.Tools[1].PermissionWait || first.Tools[1].AgentID != "child-a" {
				t.Fatalf("permission/child: %+v", first.Tools)
			}
			if len(first.Waits) != 2 || first.Waits[0].Kind != "permission" || first.Waits[0].Elapsed.Value != 10*time.Second || first.Waits[1].Kind != "away" || first.Waits[1].Elapsed.Value != time.Minute {
				t.Fatalf("waits: %+v", first.Waits)
			}
			if !first.Elapsed.Known || first.Elapsed.Estimated || first.Elapsed.Value != 30*time.Second {
				t.Fatalf("turn duration %+v", first.Elapsed)
			}
			kinds := map[string]int{}
			for _, ev := range first.Events {
				kinds[ev.Kind]++
			}
			if kinds["api_error"] != 1 || kinds["compact"] != 1 || kinds["away_summary"] != 1 {
				t.Fatalf("events %v", kinds)
			}
			second := s.Session.Turns[1]
			if !second.Interrupted || !second.Tools[0].Interrupted || second.Tools[0].TimedOutAfter != 3*time.Second || second.Tools[0].Elapsed.Value != 3*time.Second {
				t.Fatalf("interruption: %+v", second)
			}
			if second.Skills != nil {
				t.Fatal("ordinary tool must not count as Skill")
			}
		}},
		{"Codex observed format", Codex, "codex.jsonl", 2, 2, 1, 0, 180, func(t *testing.T, s *parseState) {
			if s.Session.ID != "codex-one" || s.Session.GitBranch != "dev" {
				t.Fatal("Codex metadata")
			}
			first := s.Session.Turns[0]
			if first.ID != "ct1" || first.Tools[0].Elapsed.Value != 5*time.Second || first.Elapsed.Value != 13*time.Second {
				t.Fatalf("Codex durations %+v", first)
			}
			if first.Requests[0].Tokens != (Tokens{Input: 30, Output: 20, CacheRead: 60, CacheWrite: 10}) {
				t.Fatalf("inclusive input normalization %+v", first.Requests[0].Tokens)
			}
			// Requests run from the completed input (user message, tool output)
			// to the usage record; reasoning items give measured thinking time.
			for i, want := range []struct{ start, elapsed, think time.Duration }{{time.Second, 3 * time.Second, time.Second}, {9 * time.Second, 3 * time.Second, 0}} {
				r := first.Requests[i]
				base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
				if r.Model != "gpt-test" || !r.Start.Equal(base.Add(want.start)) || r.Elapsed.Value != want.elapsed || !r.Elapsed.Estimated || r.Thinking.Value != want.think || r.Generation.Value != want.elapsed-want.think {
					t.Fatalf("Codex request %d timing: %+v", i, r)
				}
			}
			if len(first.Waits) != 1 || first.Waits[0].Elapsed.Value != 7*time.Second {
				t.Fatalf("Codex wait %+v", first.Waits)
			}
			if !s.Session.Turns[1].Interrupted || s.Session.Turns[1].Elapsed.Value != 5*time.Second {
				t.Fatal("Codex aborted turn")
			}
		}},
		{"Factory hook and usage boundary", Factory, "factory.jsonl", 2, 3, 2, 1, 0, func(t *testing.T, s *parseState) {
			if s.Session.ID != "factory-one" || s.Session.Turns[0].ID != "fu1" {
				t.Fatal("hook should not create a user turn")
			}
			for _, turn := range s.Session.Turns {
				for _, r := range turn.Requests {
					if r.UsageKnown || r.Model != "custom:test-model" {
						t.Fatalf("Factory usage must remain unknown %+v", r)
					}
				}
			}
			if s.Session.Turns[0].Tools[0].Elapsed.Value != 5*time.Second || s.Session.Turns[0].Waits[0].Elapsed.Value != 10*time.Second {
				t.Fatal("Factory tool/reply durations")
			}
			if !s.Session.Turns[1].Tools[0].Failed {
				t.Fatal("Factory tool failure")
			}
		}},
		{"Unknown version", Codex, "unknown.jsonl", 0, 0, 0, 0, 0, func(t *testing.T, s *parseState) {
			if s.FormatKnown {
				t.Fatal("unknown format was accepted")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixtureState(t, tc.agent, tc.fixture)
			requests, tools, skills := 0, 0, 0
			var tokens int64
			for _, turn := range s.Session.Turns {
				requests += len(turn.Requests)
				tools += len(turn.Tools)
				skills += len(turn.Skills)
				for _, r := range turn.Requests {
					tokens += r.Tokens.Total()
				}
			}
			if len(s.Session.Turns) != tc.turns || requests != tc.requests || tools != tc.tools || skills != tc.skills || tokens != tc.tokens {
				t.Fatalf("turns=%d requests=%d tools=%d skills=%d tokens=%d", len(s.Session.Turns), requests, tools, skills, tokens)
			}
			tc.check(t, s)
		})
	}
	for _, line := range []string{"null", "[]", "broken", `{"timestamp":"bad"}`} {
		t.Run("invalid "+line, func(t *testing.T) {
			if err := newParser(&parseState{}).line([]byte(line)); err == nil {
				t.Fatal("accepted malformed record")
			}
		})
	}
	t.Run("duplicate and invalid timestamp", func(t *testing.T) {
		s := &parseState{Session: Session{Agent: Claude}}
		p := newParser(s)
		line := []byte(`{"type":"assistant","uuid":"same","timestamp":"bad","message":{"id":"r","model":"m","usage":{"input_tokens":1,"output_tokens":2},"content":[]}}`)
		for range 2 {
			if err := p.line(line); err != nil {
				t.Fatal(err)
			}
		}
		if len(s.Session.Turns[0].Requests) != 1 {
			t.Fatal("duplicate message")
		}
		if r := s.Session.Turns[0].Requests[0]; r.Elapsed.Known || !r.Start.IsZero() {
			t.Fatal("invented invalid timestamp duration")
		}
	})
}
