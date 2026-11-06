// Package session reads local agent logs. It stores statistics and identifiers,
// never prompts, reasoning, tool arguments, or tool output. It has no network IO.
package session

import "time"

type Agent string

const (
	Claude  Agent = "claude"
	Codex   Agent = "codex"
	Factory Agent = "factory"
	Cursor  Agent = "cursor"
)

// Tokens uses disjoint buckets: Input excludes cache reads and cache writes.
// This normalizes Claude's exclusive and Codex's inclusive input token counts.
type Tokens struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

func (t Tokens) Total() int64      { return t.Input + t.Output + t.CacheRead + t.CacheWrite }
func (t Tokens) InputTotal() int64 { return t.Input + t.CacheRead + t.CacheWrite }
func (t *Tokens) add(v Tokens) {
	t.Input += v.Input
	t.Output += v.Output
	t.CacheRead += v.CacheRead
	t.CacheWrite += v.CacheWrite
}

type Duration struct {
	Value     time.Duration
	Known     bool
	Estimated bool
}

type Request struct {
	ID         string
	Model      string
	Start, End time.Time
	Tokens     Tokens
	UsageKnown bool
	Elapsed    Duration
	Thinking   Duration
	Generation Duration
	APIError   bool
	Retry      bool // inferred: the preceding request failed
}

func (r Request) OutputRate() (float64, bool) {
	if !r.UsageKnown || !r.Generation.Known || r.Generation.Value <= 0 || r.Tokens.Output <= 0 || r.APIError {
		return 0, false
	}
	return float64(r.Tokens.Output) / r.Generation.Value.Seconds(), true
}

type ToolCall struct {
	ID, Name                             string
	Start, End                           time.Time
	Elapsed                              Duration
	PermissionWait                       bool
	PermissionDecision, PermissionSource string
	Interrupted, Failed                  bool
	TimedOutAfter                        time.Duration
	AgentID                              string // explicit toolUseResult.agentId, never inferred from prompt text
}

type SkillCall struct {
	Name, ToolID string
	At           time.Time
}

type Wait struct {
	Start, End time.Time
	Elapsed    Duration
	Kind       string // permission, reply, or away (whole reply gap, not measured absence)
	ToolID     string
}

type Event struct {
	Kind     string // compact, api_error, interrupted, timeout, away_summary
	At       time.Time
	Duration Duration
}

type Turn struct {
	ID          string
	Start, End  time.Time
	Elapsed     Duration // turn_duration / task_complete when available
	Requests    []Request
	Tools       []ToolCall
	Skills      []SkillCall
	Waits       []Wait
	Events      []Event
	Subagents   []Subagent
	Interrupted bool
}

type Subagent struct {
	ID, ToolID string
	Session    Session
}

// Unsupported is per capability so missing usage cannot look like zero usage.
type Capability struct {
	Name      string
	Supported bool
	Reason    string
}

type Session struct {
	ID                    string
	Agent                 Agent
	Path                  string
	ParentID, AgentID     string
	Title, CWD, GitBranch string
	Start, End            time.Time
	Turns                 []Turn
	Capabilities          []Capability
	UnsupportedReason     string
	Incomplete            bool
}

type Source struct {
	Agent     Agent
	Files     int
	Supported bool
	Reason    string
}

type Snapshot struct {
	Sessions          []Session // top-level sessions only; children live in Turn.Subagents
	UnlinkedSubagents []Session // explicit parent exists, but no reliable turn link
	Sources           []Source
	Warnings          []string
	At                time.Time
	Partial           bool
	ReadBytes         int64 // this refresh only; unchanged logs read zero bytes
	More              bool  // read limits stopped this refresh; another refresh continues
}

func estimated(d time.Duration) Duration {
	if d < 0 {
		return Duration{}
	}
	return Duration{Value: d, Known: true, Estimated: true}
}

// FoldLongStep is the v2 rule; preference persistence belongs to the UI layer.
func FoldLongStep(step, turn time.Duration) bool {
	return step > time.Minute && turn > 0 && step > turn/2
}
