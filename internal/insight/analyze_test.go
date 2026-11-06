package insight

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"knack/internal/session"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func req(id, model string, at time.Time, tokens session.Tokens) session.Request {
	return session.Request{ID: id, Model: model, End: at, Tokens: tokens, UsageKnown: true}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPricesLookup(t *testing.T) {
	p := Prices(DefaultPrices)
	for model, want := range map[string]float64{
		"claude-opus-5-5":                4,
		"claude-opus-5":                  5,
		"claude-opus-4-1-20250805":       15,
		"claude-sonnet-4-5-20250929":     3,
		"custom:proxy/claude-sonnet-5-5": 2,
		"Claude-Haiku-4-5":               1,
	} {
		got, ok := p.Lookup(model)
		if !ok || got.Input != want {
			t.Errorf("%s: %v %v, want input %v", model, got, ok, want)
		}
	}
	if p, ok := p.Lookup("<synthetic>"); !ok || p != (Price{}) {
		t.Fatal("placeholder replies cost nothing")
	}
	for _, model := range []string{"", "gpt-6.1-sol", "kimi-for-coding"} {
		if _, ok := p.Lookup(model); ok {
			t.Errorf("%q has a price", model)
		}
	}
}

func TestLoadPrices(t *testing.T) {
	dir := t.TempDir()
	if p, err := LoadPrices(filepath.Join(dir, "missing.json")); err != nil || len(p) != len(DefaultPrices) {
		t.Fatalf("missing file: %v %d", err, len(p))
	}
	path := filepath.Join(dir, "prices.json")
	os.WriteFile(path, []byte(`{"models":{"GPT-6.1-Sol":{"input":1.5,"output":12,"cache_read":0.15},"claude-opus-5-5":{"input":1,"output":1}}}`), 0o600)
	p, err := LoadPrices(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := p.Lookup("custom:magpie/codex/gpt-6.1-sol"); !ok || got.Output != 12 {
		t.Fatalf("user price %v %v", got, ok)
	}
	if got, _ := p.Lookup("claude-opus-5-5"); got.Input != 1 {
		t.Fatalf("user price does not override default: %v", got)
	}
	os.WriteFile(path, []byte(`{"models":{"x":{"input":-1}}}`), 0o600)
	if _, err := LoadPrices(path); err == nil {
		t.Fatal("negative price accepted")
	}
}

func TestAnalyzeCost(t *testing.T) {
	child := session.Session{ID: "child", Agent: session.Claude, Start: t0, End: t0, Turns: []session.Turn{{
		Requests: []session.Request{req("c1", "claude-haiku-4-5", t0, session.Tokens{Input: 1_000_000})},
	}}}
	s := session.Session{ID: "s1", Agent: session.Claude, CWD: "/work/app", Start: t0, End: t0.Add(time.Hour), Turns: []session.Turn{{
		ID: "turn1", Start: t0,
		Requests: []session.Request{
			req("r1", "claude-opus-5-5", t0, session.Tokens{Input: 1_000_000, Output: 1_000_000, CacheRead: 1_000_000, CacheWrite: 1_000_000}),
			req("r2", "gpt-6.1-sol", t0, session.Tokens{Input: 500}),
			{ID: "r3", Model: "claude-opus-5-5", End: t0}, // unmetered
		},
		Subagents: []session.Subagent{{ID: "child", Session: child}},
	}}}
	r := Analyze(&session.Snapshot{Sessions: []session.Session{s}}, Options{})
	opus := 4 + 20 + 0.2 + 5.0
	if !near(r.Cost.Total, opus+1) {
		t.Fatalf("total %v", r.Cost.Total)
	}
	if !near(r.Cost.CacheSavings, 4-0.2) {
		t.Fatalf("savings %v", r.Cost.CacheSavings)
	}
	if r.Cost.UnpricedTokens.Input != 500 || len(r.Cost.UnpricedModels) != 1 || r.Cost.UnpricedModels[0] != "gpt-6.1-sol" {
		t.Fatalf("unpriced %+v %v", r.Cost.UnpricedTokens, r.Cost.UnpricedModels)
	}
	if len(r.Cost.Sessions) != 1 || !near(r.Cost.Sessions[0].SubagentShare, 1) || r.Cost.Sessions[0].UnpricedTokens != 500 {
		t.Fatalf("sub-Agent cost belongs to its session: %+v", r.Cost.Sessions)
	}
	if len(r.Projects) != 1 || r.Projects[0].Name != "app" || r.Projects[0].Sessions != 1 || !near(r.Projects[0].Cost, opus+1) || r.Projects[0].UnpricedTokens != 500 {
		t.Fatalf("projects %+v", r.Projects)
	}
	if len(r.Cost.Daily) != 1 || r.Cost.Daily[0].Date != "2026-11-01" {
		t.Fatalf("daily %+v", r.Cost.Daily)
	}

	f := session.Filter{From: t0.AddDate(0, 0, 1), To: t0.AddDate(0, 0, 2)}
	if r := Analyze(&session.Snapshot{Sessions: []session.Session{s}}, Options{Filter: f}); r.Cost.Total != 0 || len(r.Projects) != 0 {
		t.Fatalf("filter ignored: %v %+v", r.Cost.Total, r.Projects)
	}
}

func TestAnalyzeContext(t *testing.T) {
	mk := func(id, cwd string, baseline, later int64, compact bool) session.Session {
		turn := session.Turn{Start: t0, Requests: []session.Request{
			{ID: id + "-err", End: t0, UsageKnown: true, APIError: true, Tokens: session.Tokens{Input: 999_999}},
			req(id+"a", "m", t0, session.Tokens{Input: 100, CacheWrite: baseline - 100}),
			req(id+"b", "m", t0, session.Tokens{CacheRead: later}),
		}}
		if compact {
			turn.Events = []session.Event{{Kind: "compact", At: t0}}
		}
		return session.Session{ID: id, Agent: session.Codex, CWD: cwd, Start: t0, End: t0, Turns: []session.Turn{turn}}
	}
	snap := &session.Snapshot{Sessions: []session.Session{
		mk("a", "/w/x", 20_000, 50_000, true),
		mk("b", "/w/x", 30_000, 40_000, false),
		mk("c", "/w/y", 10_000, 12_000, false),
	}}
	r := Analyze(snap, Options{})
	if len(r.Context.Sessions) != 3 || r.Context.Sessions[0].ID != "b" || r.Context.Sessions[0].Baseline != 30_000 || r.Context.Sessions[0].Peak != 40_000 {
		t.Fatalf("sessions %+v", r.Context.Sessions)
	}
	x := r.Context.ByProject[0]
	if x.Name != "x" || x.Sessions != 2 || x.Min != 20_000 || x.Median != 20_000 || x.Compactions != 1 {
		t.Fatalf("project group %+v", x)
	}
	if len(r.Context.ByAgent) != 1 || r.Context.ByAgent[0].Sessions != 3 {
		t.Fatalf("agent group %+v", r.Context.ByAgent)
	}
}

func TestAnalyzeFriction(t *testing.T) {
	sec := session.Duration{Value: time.Second, Known: true}
	tools := []session.ToolCall{
		{ID: "1", Name: "Bash", Start: t0, InputHash: "h1", Failed: true},
		{ID: "2", Name: "Bash", Start: t0, InputHash: "h1"},
		{ID: "3", Name: "Bash", Start: t0, InputHash: "h1"},
		{ID: "4", Name: "Bash", Start: t0, InputHash: "h1"},
		{ID: "5", Name: "Read", Start: t0, InputHash: "h2", PermissionWait: true, Elapsed: sec},
		{ID: "6", Name: "mcp__github__get_issue", Start: t0, Interrupted: true},
		{ID: "7", Name: "mcp__github__list_prs", Start: t0, TimedOutAfter: time.Second},
		{ID: "8", Name: "write_stdin", Start: t0, InputHash: "poll"},
		{ID: "9", Name: "write_stdin", Start: t0, InputHash: "poll"},
		{ID: "10", Name: "write_stdin", Start: t0, InputHash: "poll"},
		{ID: "11", Name: "AskUserQuestion", Start: t0, InputHash: "q", PermissionWait: true, Elapsed: session.Duration{Value: time.Hour, Known: true}},
	}
	s := session.Session{ID: "s", Agent: session.Claude, Start: t0, End: t0, Turns: []session.Turn{
		{ID: "t1", Start: t0, Tools: tools, Interrupted: true,
			Events:   []session.Event{{Kind: "api_error", At: t0}},
			Waits:    []session.Wait{{Kind: "permission", End: t0, Elapsed: sec, ToolID: "5"}, {Kind: "permission", End: t0, Elapsed: session.Duration{Value: time.Hour, Known: true}, ToolID: "11"}},
			Requests: []session.Request{{ID: "r", End: t0, Retry: true}}},
		{ID: "t2", Start: t0},
	}}
	r := Analyze(&session.Snapshot{Sessions: []session.Session{s}}, Options{}).Friction
	if r.Turns != 2 || r.InterruptedTurns != 1 || r.ToolCalls != 11 || r.FailedCalls != 3 || r.APIErrors != 1 || r.Retries != 1 {
		t.Fatalf("totals %+v", r)
	}
	if r.PermissionWaits != 1 || r.PermissionTime != time.Second { // questions are answers, not permission prompts
		t.Fatalf("permission %+v", r)
	}
	if !r.RepeatKnown || len(r.Loops) != 1 || r.Loops[0].Tool != "Bash" /* polling is not a loop */ || r.Loops[0].Repeats != 4 || r.Loops[0].TurnNumber != 1 {
		t.Fatalf("loops %+v", r.Loops)
	}
	if r.Tools[0].Name != "Bash" || r.Tools[0].Repeats != 3 || r.Tools[0].FailureRate() != 0.25 {
		t.Fatalf("tools %+v", r.Tools)
	}
	if len(r.MCP) != 1 || r.MCP[0].Server != "github" || r.MCP[0].Calls != 2 || len(r.MCP[0].Tools) != 2 {
		t.Fatalf("mcp %+v", r.MCP)
	}
}
