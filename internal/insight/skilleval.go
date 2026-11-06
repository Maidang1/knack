package insight

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"knack/internal/session"
)

// Skill evaluation from real sessions. Skill authors evaluate a skill on
// two axes: whether its description makes the model load it when it should
// (triggering), and whether loading it makes the work go better
// (execution). Logs cannot rerun anything, but every load is an episode
// that already happened: who loaded it, what it cost, whether the work
// touched the skill's own files, and how the turn ended. Turns that loaded
// no skill are the baseline.

// Episode is one load of a skill and the rest of its turn.
type Episode struct {
	Agent                 session.Agent
	SessionID, Title, CWD string
	Turn                  int
	TurnID                string
	At                    time.Time
	Source                string
	Tokens                int64 // instructions loaded
	Uses                  int   // later calls into the skill's files
	Calls, Failed         int   // tool calls after the load, and those that failed; zero when the log does not record results
	Interrupted           bool
	Corrected             bool  // the next prompt pushed back
	TurnTokens            int64 // every token the turn's calls used
	// Subagent: the load happened in a sub-Agent; SessionID, Turn and
	// TurnID are then those of the top-level session that ran it.
	Subagent bool
}

// Clean is an episode whose turn ran without failed calls, interruption or
// correction.
func (e Episode) Clean() bool { return e.Failed == 0 && !e.Interrupted && !e.Corrected }

// Auto is a load the model chose; the alternative is the person typing
// /name because the model did not.
func (e Episode) Auto() bool { return e.Source != session.SkillByCommand }

// Outcome is the episode's result in a word.
func (e Episode) Outcome() string {
	switch {
	case e.Interrupted:
		return "中断"
	case e.Corrected:
		return "被纠正"
	case e.Failed > 0:
		return "有失败"
	}
	return "顺利"
}

// Window sums episodes or baseline turns.
type Window struct {
	Episodes, Clean, Calls, Failed int
	MedianTurnTokens               int64
}

func (w Window) CleanRate() float64 { return ratio(w.Clean, w.Episodes) }
func (w Window) FailRate() float64  { return ratio(w.Failed, w.Calls) }

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// SkillEval is a skill's episodes in the period, newest first.
type SkillEval struct {
	Name     string
	Agents   []session.Agent
	Episodes []Episode
	Auto     int
	Manual   int
	Used     int
	Window
	Interrupted, Corrected, WithFailures int
	LoadTokens                           int64 // all instructions loaded
}

// MedianLoad is the instructions one load puts into context.
func (e SkillEval) MedianLoad() int64 {
	var v []int64
	for _, ep := range e.Episodes {
		if ep.Tokens > 0 {
			v = append(v, ep.Tokens)
		}
	}
	return median64(v)
}

// Split divides the episodes at a time, such as the last edit of the
// skill: before and after.
func (e SkillEval) Split(at time.Time) (before, after Window) {
	var b, a []Episode
	for _, ep := range e.Episodes {
		if ep.At.Before(at) {
			b = append(b, ep)
		} else {
			a = append(a, ep)
		}
	}
	return window(b), window(a)
}

func window(eps []Episode) Window {
	var w Window
	var tokens []int64
	for _, ep := range eps {
		w.Episodes++
		if ep.Clean() {
			w.Clean++
		}
		w.Calls += ep.Calls
		w.Failed += ep.Failed
		tokens = append(tokens, ep.TurnTokens)
	}
	w.MedianTurnTokens = median64(tokens)
	return w
}

// SkillEvalReport holds every skill loaded in the period and the turns
// that loaded none.
type SkillEvalReport struct {
	Skills   []SkillEval
	Baseline Window
}

// Lookup finds a skill's evaluation by name.
func (r SkillEvalReport) Lookup(name string) (SkillEval, bool) {
	for _, e := range r.Skills {
		if e.Name == name {
			return e, true
		}
	}
	return SkillEval{}, false
}

// EvaluateSkills collects every skill load in the filter's period,
// including those in sub-Agents.
func EvaluateSkills(snap *session.Snapshot, f session.Filter) SkillEvalReport {
	a := newAnalysis(Options{Filter: f})
	byName := map[string][]Episode{}
	var baseline []Episode
	var walk func(s, root session.Session, rootTurn int)
	walk = func(s, root session.Session, rootTurn int) {
		for ti, t := range s.Turns {
			at := ti
			if s.ID != root.ID || s.Agent != root.Agent {
				at = rootTurn
			}
			for _, sub := range t.Subagents {
				walk(sub.Session, root, at)
			}
			if !a.included(t.Start) || (f.Agent != "" && s.Agent != f.Agent) {
				continue
			}
			corrected := ti+1 < len(s.Turns) && s.Turns[ti+1].Corrects
			// Logs that do not record tool results have no failures to count.
			outcomes := session.LookupAgent(s.Agent).ToolOutcome
			var tokens int64
			for _, r := range t.Requests {
				tokens += r.Tokens.Total()
			}
			if len(t.Skills) == 0 {
				calls, failed := 0, 0
				if outcomes {
					calls, failed = toolOutcome(t, time.Time{}, "")
				}
				baseline = append(baseline, Episode{Calls: calls, Failed: failed, Interrupted: t.Interrupted, Corrected: corrected, TurnTokens: tokens})
				continue
			}
			for _, sk := range t.Skills {
				ep := episode(t, sk)
				ep.Agent, ep.SessionID, ep.Title, ep.CWD = s.Agent, root.ID, root.Title, root.CWD
				ep.Turn, ep.TurnID, ep.Subagent = at, root.Turns[at].ID, at != ti || s.ID != root.ID
				ep.Corrected, ep.TurnTokens = corrected, tokens
				if !outcomes {
					ep.Calls, ep.Failed = 0, 0
				}
				byName[sk.Name] = append(byName[sk.Name], ep)
			}
		}
	}
	if snap != nil {
		for _, s := range snap.Sessions {
			walk(s, s, 0)
		}
		for _, s := range snap.UnlinkedSubagents {
			walk(s, s, 0)
		}
	}
	var out SkillEvalReport
	out.Baseline = window(baseline)
	for name, eps := range byName {
		slices.SortFunc(eps, func(x, y Episode) int { return y.At.Compare(x.At) })
		e := SkillEval{Name: name, Episodes: eps, Window: window(eps)}
		for _, ep := range eps {
			if !slices.Contains(e.Agents, ep.Agent) {
				e.Agents = append(e.Agents, ep.Agent)
			}
			if ep.Auto() {
				e.Auto++
			} else {
				e.Manual++
			}
			if ep.Uses > 0 {
				e.Used++
			}
			if ep.Interrupted {
				e.Interrupted++
			}
			if ep.Corrected {
				e.Corrected++
			}
			if ep.Failed > 0 {
				e.WithFailures++
			}
			e.LoadTokens += ep.Tokens
		}
		slices.Sort(e.Agents)
		out.Skills = append(out.Skills, e)
	}
	slices.SortFunc(out.Skills, func(x, y SkillEval) int {
		return cmp.Or(cmp.Compare(len(y.Episodes), len(x.Episodes)), strings.Compare(x.Name, y.Name))
	})
	return out
}

// episode measures what followed a skill load in its turn.
func episode(t session.Turn, sk session.SkillCall) Episode {
	calls, failed := toolOutcome(t, sk.At, sk.ToolID)
	return Episode{At: sk.At, Source: sk.Source, Tokens: sk.Tokens, Uses: sk.Uses, Calls: calls, Failed: failed, Interrupted: t.Interrupted}
}

func toolOutcome(t session.Turn, from time.Time, skip string) (calls, failed int) {
	for _, call := range t.Tools {
		if call.ID == skip || call.Start.Before(from) {
			continue
		}
		calls++
		if call.Failed || call.Interrupted || call.TimedOutAfter > 0 {
			failed++
		}
	}
	return calls, failed
}

func median64(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	v = slices.Clone(v)
	slices.Sort(v)
	return v[len(v)/2]
}
