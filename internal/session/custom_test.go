package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const customAgents = `{"agents": [{
	"id": "my-agent",
	"name": "My Agent",
	"roots": ["~/.my-agent/logs"],
	"fields": {
		"time": "ts", "role": "role", "text": "content", "id": "id",
		"model": "model", "sessionId": "session", "cwd": "dir",
		"usage": {"input": "usage.prompt_tokens", "output": "usage.completion_tokens", "cacheRead": "usage.cached_tokens", "inputIncludesCache": true},
		"toolCalls": {"path": "tool_calls", "match": {"type": "function"}, "id": "id", "name": "function.name", "input": "function.arguments"},
		"toolResults": {"id": "tool_call_id", "output": "content"}
	}
}]}`

// useAgents registers the definitions for one test and removes them after.
func useAgents(t *testing.T, home, config string) []string {
	t.Helper()
	path := filepath.Join(home, "agents.json")
	put(t, path, []byte(config))
	t.Cleanup(func() { LoadAgentConfig(filepath.Join(home, "missing.json")) })
	return LoadAgentConfig(path)
}

func TestCustomAgent(t *testing.T) {
	home := t.TempDir()
	if problems := useAgents(t, home, customAgents); len(problems) != 0 {
		t.Fatal(problems)
	}
	if info := LookupAgent("my-agent"); info.Name != "My Agent" || info.Unsupported != "" || info.Skills || !info.OutputRate {
		t.Fatalf("registered info %+v", info)
	}
	if info, ok := AgentByName("my agent"); !ok || info.ID != "my-agent" {
		t.Fatal("lookup by name")
	}
	log := filepath.Join(home, ".my-agent/logs/2026/chat-1.jsonl")
	put(t, log, fixture(t, "custom.jsonl"))
	r := reader(t, home, filepath.Join(home, "cache.json"))
	snap := refresh(t, r)
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions %+v", snap.Sessions)
	}
	s := snap.Sessions[0]
	if s.Agent != "my-agent" || s.ID != "chat-1" || s.CWD != "/work/demo" || s.UnsupportedReason != "" || !s.Incomplete {
		t.Fatalf("session %+v", s)
	}
	if len(s.Turns) != 2 || len(s.Turns[0].Requests) != 2 || len(s.Turns[0].Tools) != 1 {
		t.Fatalf("turns %+v", s.Turns)
	}
	tool := s.Turns[0].Tools[0]
	if tool.Name != "shell" || tool.Elapsed.Value != 2*time.Second {
		t.Fatalf("tool %+v", tool)
	}
	// Inclusive input minus cache reads; the duplicated record counts once.
	if sum := Aggregate(snap, Filter{}); sum.Tokens != (Tokens{Input: 200, Output: 45, CacheRead: 120}) {
		t.Fatalf("tokens %+v", sum.Tokens)
	}
	if w := s.Turns[0].Waits; len(w) != 1 || w[0].Kind != "reply" {
		t.Fatalf("reply wait %+v", w)
	}
	var source Source
	for _, src := range snap.Sources {
		if src.Agent == "my-agent" {
			source = src
		}
	}
	if !source.Supported || source.Files != 1 {
		t.Fatalf("source %+v", source)
	}

	tr, err := ReadTranscript(context.Background(), log, "my-agent")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range tr.Entries {
		kinds = append(kinds, string(e.Kind))
		if e.Kind == EntryTool && (e.Text != "README.md\nmain.go" || !strings.Contains(e.Input, `"cmd": "ls"`) || e.RequestID != "m3") {
			t.Fatalf("tool entry %+v", e)
		}
	}
	if got := strings.Join(kinds, ","); got != "user,tool,assistant,user" {
		t.Fatalf("replay kinds %s", got)
	}

	t.Run("a changed mapping rebuilds its statistics", func(t *testing.T) {
		useAgents(t, home, strings.Replace(customAgents, `"inputIncludesCache": true`, `"inputIncludesCache": false`, 1))
		snap := refresh(t, r)
		if sum := Aggregate(snap, Filter{}); sum.Tokens.Input != 320 {
			t.Fatalf("not rebuilt %+v", sum.Tokens)
		}
	})
	t.Run("a removed Agent leaves cached and current totals", func(t *testing.T) {
		useAgents(t, home, `{"agents": []}`)
		cached, err := reader(t, home, filepath.Join(home, "cache.json")).Cached()
		if err != nil || cached != nil && len(cached.Sessions) != 0 {
			t.Fatalf("cached %+v %v", cached, err)
		}
		if snap := refresh(t, r); len(snap.Sessions) != 0 {
			t.Fatalf("still counted %+v", snap.Sessions)
		}
	})
}

func TestCustomAgentValidation(t *testing.T) {
	home := t.TempDir()
	problems := useAgents(t, home, `{"agents": [
		{"id": "Bad ID", "name": "x", "roots": ["a"], "fields": {"time": "t", "role": "r", "text": "c"}},
		{"id": "claude", "name": "Mine", "roots": ["a"], "fields": {"time": "t", "role": "r", "text": "c"}},
		{"id": "inside", "name": "Inside", "roots": ["~/.claude/projects/x"], "fields": {"time": "t", "role": "r", "text": "c"}},
		{"id": "escape", "name": "Escape", "roots": ["../outside"], "fields": {"time": "t", "role": "r", "text": "c"}},
		{"id": "partial", "name": "Partial", "roots": ["b"], "fields": {"time": "t"}},
		{"id": "ok", "name": "OK", "roots": ["c"], "fields": {"time": "t", "role": "r", "text": "c"}},
		{"id": "nested", "name": "Nested", "roots": ["c/d"], "fields": {"time": "t", "role": "r", "text": "c"}}
	]}`)
	if len(problems) != 6 {
		t.Fatalf("problems %q", problems)
	}
	for _, want := range []string{"id 只能", "重复", "重叠", "主目录", "fields", "重叠"} {
		found := false
		for _, p := range problems {
			found = found || strings.Contains(p, want)
		}
		if !found {
			t.Fatalf("missing %q in %q", want, problems)
		}
	}
	if LookupAgent("ok").Unsupported != "" || LookupAgent("partial").Unsupported == "" {
		t.Fatal("valid Agents must still load")
	}
	if snap := refresh(t, reader(t, home, "")); !strings.Contains(strings.Join(snap.Warnings, "\n"), "agents.json") {
		t.Fatalf("problems not in warnings %q", snap.Warnings)
	}
	put(t, filepath.Join(home, "agents.json"), []byte("{"))
	if problems := LoadAgentConfig(filepath.Join(home, "agents.json")); len(problems) != 1 || LookupAgent("ok").Unsupported == "" {
		t.Fatalf("invalid file %q", problems)
	}
	if err := os.Remove(filepath.Join(home, "agents.json")); err != nil {
		t.Fatal(err)
	}
	if problems := LoadAgentConfig(filepath.Join(home, "agents.json")); len(problems) != 0 {
		t.Fatal("a missing file is no problem")
	}
}
