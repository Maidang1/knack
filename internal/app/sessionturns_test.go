package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Two sub-Agents dispatched together, then a plain turn.
func subagentFixture(t *testing.T) *App {
	t.Helper()
	a := New(t.TempDir())
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	dur := func(s int) session.Duration {
		return session.Duration{Value: time.Duration(s) * time.Second, Known: true, Estimated: true}
	}
	request := func(id string, from, to int) session.Request {
		return session.Request{ID: id, Model: "claude-sonnet", End: at(to), Elapsed: dur(to - from), Generation: dur(to - from), Tokens: session.Tokens{Input: 400, Output: 120, CacheRead: 1600}, UsageKnown: true}
	}
	tool := func(id, name string, from, to int) session.ToolCall {
		return session.ToolCall{ID: id, Name: name, Start: at(from), End: at(to), Elapsed: dur(to - from)}
	}
	child := func(id string, from, to int) session.Session {
		return session.Session{ID: id, Agent: session.Claude, Path: "/logs/" + id + ".jsonl", Start: at(from), End: at(to), Turns: []session.Turn{{
			ID: id + "-turn", Start: at(from), End: at(to),
			Requests: []session.Request{request(id+"-r1", from, from+6), request(id+"-r2", to-10, to)},
			Tools:    []session.ToolCall{tool(id+"-grep", "Grep", from+6, from+8), tool(id+"-read", "Read", from+8, from+10)},
		}}}
	}
	turn := session.Turn{ID: "dispatch", Start: start, End: at(100),
		Requests: []session.Request{request("main1", 0, 8), request("main2", 84, 100)},
		Tools:    []session.ToolCall{tool("bash", "Bash", 8, 30), tool("task-a", "Task", 30, 84), tool("task-b", "Task", 32, 68)},
		Subagents: []session.Subagent{
			{ID: "agent-a", ToolID: "task-a", Session: child("agent-a", 30, 84)},
			{ID: "agent-b", ToolID: "task-b", Session: child("agent-b", 32, 68)},
		},
	}
	plain := session.Turn{ID: "plain", Start: at(110), End: at(130), Requests: []session.Request{request("main3", 110, 130)}}
	root := session.Session{ID: "parent", Agent: session.Claude, Title: "子 Agent 示例", Start: start, End: at(130), Turns: []session.Turn{turn, plain}}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root))
	a.chooseSessionTurn(sessionDetail(a.sessions.snapshot, root), 0)
	return a
}

func TestSessionSubagents(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := subagentFixture(t)
				tt := ui.NewTester(a.View, width, 1100)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				turn := a.sessions.detail.turns[0]
				groups := subagentGroups(turn.steps)
				first := -1
				for i, g := range groups {
					if g > 0 {
						first = i
					}
				}
				if first < 0 || groups[first] != 2 || turn.steps[first].child == nil || turn.steps[first+1].child == nil {
					t.Fatalf("parallel sub-Agents not grouped: %v", groups)
				}
				// The heading spans the first start to the last end.
				if _, ok := tt.Find("并行 2 个子 Agent · ≈ 54s"); !ok {
					t.Fatalf("missing group heading: %q", tt.Texts())
				}
				if !tt.HasText("2 个子 Agent 并行") {
					t.Fatal("turn summary does not count its sub-Agents")
				}
				// Opening a sub-Agent nests its own steps under it, with what it used.
				sub := turn.steps[first]
				click(t, tt, stepLabel(first, sub))
				if _, ok := tt.Find("子 Agent 步骤"); !ok {
					t.Fatal("sub-Agent did not open")
				}
				row, _ := tt.Find(stepLabel(first, sub))
				for _, s := range childSteps(sub.child) {
					r, ok := tt.Find("子 Agent 步骤 · " + s.name + " · " + stepDuration(s))
					if !ok || r.Y < row.Y+row.H || r.X+r.W > float32(width)+1 {
						t.Fatalf("child step %s not nested under its sub-Agent: %+v", s.name, r)
					}
				}
				if !tt.HasText("4.2K") || !tt.HasText("80%") || !tt.HasText("定位子会话日志") {
					t.Fatalf("missing sub-Agent usage: %q", tt.Texts())
				}
				var revealed string
				a.revealFn = func(path string) { revealed = path }
				click(t, tt, "定位子会话日志")
				if revealed != sub.child.Path {
					t.Fatal("reveal opened the wrong log")
				}
				exportSessionPreview(t, tt, fmt.Sprintf("subagents-%d-dark-%t.png", width, dark))
				// The filter keeps only turns with sub-Agents.
				click(t, tt, "子 Agent 1")
				if _, ok := tt.Find(turnLabel(a.sessions.detail.turns[1])); ok {
					t.Fatal("filter kept a turn without sub-Agents")
				}
				if _, ok := tt.Find(turnLabel(turn)); !ok {
					t.Fatal("filter hid the sub-Agent turn")
				}
			})
		}
	}
}

func TestTurnListCollapse(t *testing.T) {
	a := New(t.TempDir())
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	var turns []session.Turn
	for i := range 20 {
		from := start.Add(time.Duration(i) * time.Minute)
		turns = append(turns, session.Turn{ID: fmt.Sprintf("t%d", i), Start: from, End: from.Add(30 * time.Second)})
	}
	root := session.Session{ID: "long", Agent: session.Claude, Start: start, End: start.Add(20 * time.Minute), Turns: turns}
	a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
	a.openSession(sessionID(root))
	d := sessionDetail(a.sessions.snapshot, root)
	a.chooseSessionTurn(d, 17)
	tt := ui.NewTester(a.View, 1180, 900)
	d = a.sessions.detail
	if _, ok := tt.Find(turnLabel(d.turns[12])); ok {
		t.Fatal("long list not collapsed")
	}
	if _, ok := tt.Find(turnLabel(d.turns[17])); !ok {
		t.Fatal("collapsed list hid the chosen turn")
	}
	click(t, tt, "显示其余 7 轮")
	if _, ok := tt.Find(turnLabel(d.turns[12])); !ok {
		t.Fatal("expanding did not list every turn")
	}
}
