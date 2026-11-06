package insight

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"knack/internal/session"
)

// Options scope an analysis. Requests count by completion time and tools by
// start time, the same attribution session.Aggregate uses.
type Options struct {
	Filter session.Filter
	Prices Prices
}

type Report struct {
	Cost     CostReport
	Context  ContextReport
	Friction FrictionReport
	Projects []Project
}

type ModelCost struct {
	Model    string
	Requests int
	Tokens   session.Tokens
	Cost     float64
	Priced   bool
}

type NamedCost struct {
	Name string
	Cost float64
}

type DailyCost struct {
	Date string
	Cost float64
}

type SessionCost struct {
	Agent           session.Agent
	ID, Title, CWD  string
	End             time.Time
	Tokens          session.Tokens
	Cost            float64
	UnpricedTokens  int64
	SubagentShare   float64 // part of Cost spent by sub-Agents
	RequestsMetered int
}

// CostReport estimates spend. Tokens of models without a price stay out of
// Total and are reported separately, so a missing price never reads as $0.
type CostReport struct {
	Total, CacheSavings float64
	PricedTokens        session.Tokens
	UnpricedTokens      session.Tokens
	ByModel             []ModelCost
	ByAgent             []NamedCost
	Daily               []DailyCost
	Sessions            []SessionCost // most expensive first
	UnpricedModels      []string
}

// SessionContext is how much input a session carried. Baseline is the input
// of its first metered request: the system prompt, tool definitions,
// instruction files and skill list, plus the first prompt.
type SessionContext struct {
	Agent          session.Agent
	ID, Title, CWD string
	Start          time.Time
	Baseline, Peak int64
	Compactions    int
	Requests       int
}

type ContextGroup struct {
	Name             string
	Sessions         int
	Median, P90, Min int64 // baseline
	MedianPeak       int64
	Compactions      int
}

type ContextReport struct {
	Sessions  []SessionContext // largest baseline first
	ByAgent   []ContextGroup
	ByProject []ContextGroup
}

type ToolFriction struct {
	Name                                string
	Calls, Failed, Interrupted, TimeOut int
	PermissionWaits, Repeats            int
	PermissionTime, Elapsed             time.Duration
}

func (t ToolFriction) FailureRate() float64 {
	if t.Calls == 0 {
		return 0
	}
	return float64(t.Failed+t.Interrupted+t.TimeOut) / float64(t.Calls)
}

type MCPUsage struct {
	Agent  session.Agent
	Server string
	Calls  int
	Tools  []string
}

// Loop is a turn that made the same tool call several times in a row.
type Loop struct {
	Agent      session.Agent
	SessionID  string
	Title, CWD string
	TurnID     string
	Tool       string
	Repeats    int
	At         time.Time
	TurnNumber int
}

type FrictionReport struct {
	Turns, InterruptedTurns         int
	ToolCalls, FailedCalls          int
	APIErrors, Retries, Compactions int
	PermissionWaits                 int
	PermissionTime                  time.Duration
	Tools                           []ToolFriction
	MCP                             []MCPUsage
	Loops                           []Loop
	RepeatKnown                     bool // some tool calls record an input fingerprint
}

type Project struct {
	Path, Name     string
	Agents         []string
	Sessions       int
	Turns          int
	Tokens         session.Tokens
	Cost           float64
	UnpricedTokens int64
	Last           time.Time
	ToolCalls      int
	FailedCalls    int
	Interrupted    int
	Skills         []NamedCount
	MedianBaseline int64
}

type NamedCount struct {
	Name  string
	Count int
}

// LoopRepeats is how many identical consecutive tool calls make a loop.
const LoopRepeats = 3

// pollingTools wait on or read from something still running; calling them
// again with the same input is how they work, not a loop.
var pollingTools = map[string]bool{"write_stdin": true, "wait": true, "BashOutput": true, "TaskOutput": true, "Monitor": true}

// InteractiveTools ask the person something. Their confirmation time is
// the person answering, not a permission prompt.
var InteractiveTools = map[string]bool{"AskUserQuestion": true, "ExitPlanMode": true, "EnterPlanMode": true, "AskUser": true}

// Analyze reads every top-level session and its sub-Agents once.
func Analyze(snap *session.Snapshot, opt Options) Report {
	a := newAnalysis(opt)
	if snap != nil {
		for _, s := range snap.Sessions {
			a.session(s)
		}
		for _, s := range snap.UnlinkedSubagents {
			a.orphan(s)
		}
	}
	return a.finish()
}

type projectState struct {
	project   Project
	agents    map[string]bool
	skills    map[string]int
	baselines []int64
}

type analysis struct {
	opt       Options
	cost      CostReport
	models    map[string]*ModelCost
	agentCost map[string]float64
	daily     map[string]float64
	sessions  []SessionCost
	unpriced  map[string]bool
	ctx       []SessionContext
	friction  FrictionReport
	tools     map[string]*ToolFriction
	mcp       map[mcpKey]map[string]int
	projects  map[string]*projectState
}

func newAnalysis(opt Options) *analysis {
	if opt.Prices == nil {
		opt.Prices = Prices(DefaultPrices)
	}
	return &analysis{opt: opt, models: map[string]*ModelCost{}, agentCost: map[string]float64{}, daily: map[string]float64{},
		unpriced: map[string]bool{}, tools: map[string]*ToolFriction{}, mcp: map[mcpKey]map[string]int{}, projects: map[string]*projectState{}}
}

func (a *analysis) included(at time.Time) bool {
	f := a.opt.Filter
	if at.IsZero() {
		return f.From.IsZero() && f.To.IsZero()
	}
	return (f.From.IsZero() || !at.Before(f.From)) && (f.To.IsZero() || at.Before(f.To))
}

func (a *analysis) overlaps(s session.Session) bool {
	f := a.opt.Filter
	if s.Start.IsZero() || s.End.IsZero() {
		return f.From.IsZero() && f.To.IsZero()
	}
	return (f.From.IsZero() || !s.End.Before(f.From)) && (f.To.IsZero() || s.Start.Before(f.To))
}

func (a *analysis) wanted(s session.Session) bool {
	return s.UnsupportedReason == "" && (a.opt.Filter.Agent == "" || a.opt.Filter.Agent == s.Agent)
}

func (a *analysis) project(s session.Session) *projectState {
	path := filepath.Clean(strings.TrimSpace(s.CWD))
	if strings.TrimSpace(s.CWD) == "" {
		path = ""
	}
	p := a.projects[path]
	if p == nil {
		name := filepath.Base(path)
		if path == "" {
			name = "未知目录"
		}
		p = &projectState{project: Project{Path: path, Name: name}, agents: map[string]bool{}, skills: map[string]int{}}
		a.projects[path] = p
	}
	return p
}

func (a *analysis) session(s session.Session) {
	if !a.wanted(s) {
		return
	}
	sc := &SessionCost{Agent: s.Agent, ID: s.ID, Title: s.Title, CWD: s.CWD, End: s.End}
	p := a.project(s)
	if a.overlaps(s) {
		p.project.Sessions++
		p.agents[session.LookupAgent(s.Agent).Name] = true
		if s.End.After(p.project.Last) {
			p.project.Last = s.End
		}
		if c, ok := a.context(s); ok {
			a.ctx = append(a.ctx, c)
			p.baselines = append(p.baselines, c.Baseline)
		}
	}
	a.work(s, sc, p, false)
	if sc.Cost > 0 || sc.Tokens.Total() > 0 {
		a.sessions = append(a.sessions, *sc)
	}
}

// orphan is a sub-Agent without a reliable parent turn: its work counts,
// but it is not a session of its own.
func (a *analysis) orphan(s session.Session) {
	if !a.wanted(s) {
		return
	}
	sc := &SessionCost{Agent: s.Agent, ID: s.ParentID, CWD: s.CWD, End: s.End}
	a.work(s, sc, a.project(s), true)
}

func (a *analysis) work(s session.Session, sc *SessionCost, p *projectState, child bool) {
	for ti, t := range s.Turns {
		if a.included(t.Start) && !child {
			a.friction.Turns++
			p.project.Turns++
			if t.Interrupted {
				a.friction.InterruptedTurns++
				p.project.Interrupted++
			}
		}
		for _, r := range t.Requests {
			if !a.included(r.End) {
				continue
			}
			a.request(s, r, sc, p, child)
		}
		a.toolCalls(s, ti, t, p)
		for _, ev := range t.Events {
			if !a.included(ev.At) {
				continue
			}
			switch ev.Kind {
			case "api_error":
				a.friction.APIErrors++
			case "compact":
				a.friction.Compactions++
			}
		}
		for _, w := range t.Waits {
			if w.Kind == "permission" && a.included(w.End) && w.Elapsed.Known && !InteractiveTools[toolName(t, w.ToolID)] {
				a.friction.PermissionTime += w.Elapsed.Value
			}
		}
		for _, sk := range t.Skills {
			if a.included(sk.At) {
				p.skills[sk.Name]++
			}
		}
		for _, sub := range t.Subagents {
			if a.wanted(sub.Session) {
				a.work(sub.Session, sc, p, true)
			}
		}
	}
}

func (a *analysis) request(s session.Session, r session.Request, sc *SessionCost, p *projectState, child bool) {
	if r.Retry {
		a.friction.Retries++
	}
	if !r.UsageKnown {
		return
	}
	name := cmp.Or(r.Model, "未知模型")
	m := a.models[name]
	if m == nil {
		m = &ModelCost{Model: name}
		a.models[name] = m
	}
	m.Requests++
	m.Tokens = addTokens(m.Tokens, r.Tokens)
	sc.Tokens = addTokens(sc.Tokens, r.Tokens)
	sc.RequestsMetered++
	p.project.Tokens = addTokens(p.project.Tokens, r.Tokens)
	price, ok := a.opt.Prices.Lookup(r.Model)
	if !ok {
		a.unpriced[name] = true
		a.cost.UnpricedTokens = addTokens(a.cost.UnpricedTokens, r.Tokens)
		sc.UnpricedTokens += r.Tokens.Total()
		p.project.UnpricedTokens += r.Tokens.Total()
		return
	}
	m.Priced = true
	cost := price.Cost(r.Tokens)
	m.Cost += cost
	a.cost.Total += cost
	a.cost.PricedTokens = addTokens(a.cost.PricedTokens, r.Tokens)
	a.cost.CacheSavings += float64(r.Tokens.CacheRead) * (price.Input - price.CacheRead) / 1e6
	a.agentCost[session.LookupAgent(s.Agent).Name] += cost
	sc.Cost += cost
	if child {
		sc.SubagentShare += cost
	}
	p.project.Cost += cost
	if !r.End.IsZero() {
		loc := a.opt.Filter.Location
		if loc == nil {
			loc = time.Local
		}
		a.daily[r.End.In(loc).Format("2006-01-02")] += cost
	}
}

func (a *analysis) toolCalls(s session.Session, ti int, t session.Turn, p *projectState) {
	run, last := 0, ""
	flagged := map[string]bool{}
	for _, call := range t.Tools {
		if !a.included(call.Start) {
			continue
		}
		name := cmp.Or(call.Name, "未知工具")
		tf := a.tools[name]
		if tf == nil {
			tf = &ToolFriction{Name: name}
			a.tools[name] = tf
		}
		tf.Calls++
		a.friction.ToolCalls++
		p.project.ToolCalls++
		failed := call.Failed || call.Interrupted || call.TimedOutAfter > 0
		if call.Failed {
			tf.Failed++
		}
		if call.Interrupted {
			tf.Interrupted++
		}
		if call.TimedOutAfter > 0 {
			tf.TimeOut++
		}
		if failed {
			a.friction.FailedCalls++
			p.project.FailedCalls++
		}
		if call.PermissionWait && !InteractiveTools[name] {
			tf.PermissionWaits++
			a.friction.PermissionWaits++
			if call.Elapsed.Known {
				tf.PermissionTime += call.Elapsed.Value
			}
		}
		if call.Elapsed.Known {
			tf.Elapsed += call.Elapsed.Value
		}
		if server, tool, ok := mcpTool(name); ok {
			key := mcpKey{s.Agent, server}
			if a.mcp[key] == nil {
				a.mcp[key] = map[string]int{}
			}
			a.mcp[key][tool]++
		}
		if call.InputHash == "" || pollingTools[name] {
			run, last = 0, ""
			continue
		}
		a.friction.RepeatKnown = true
		key := name + "\x00" + call.InputHash
		if key == last {
			run++
			tf.Repeats++
		} else {
			run, last = 1, key
		}
		if run == LoopRepeats && !flagged[key] {
			flagged[key] = true
			a.friction.Loops = append(a.friction.Loops, Loop{Agent: s.Agent, SessionID: s.ID, Title: s.Title, CWD: s.CWD, TurnID: t.ID, Tool: name, Repeats: run, At: call.Start, TurnNumber: ti + 1})
		} else if run > LoopRepeats {
			a.friction.Loops[len(a.friction.Loops)-1].Repeats = run
		}
	}
}

type mcpKey struct {
	agent  session.Agent
	server string
}

func toolName(t session.Turn, id string) string {
	for _, call := range t.Tools {
		if call.ID == id {
			return call.Name
		}
	}
	return ""
}

// mcpTool splits Claude Code's mcp__<server>__<tool> tool names.
func mcpTool(name string) (string, string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", "", false
	}
	server, tool, ok := strings.Cut(rest, "__")
	if !ok || server == "" {
		return "", "", false
	}
	return server, tool, true
}

func (a *analysis) context(s session.Session) (SessionContext, bool) {
	c := SessionContext{Agent: s.Agent, ID: s.ID, Title: s.Title, CWD: s.CWD, Start: s.Start}
	found := false
	for _, t := range s.Turns {
		for _, ev := range t.Events {
			if ev.Kind == "compact" {
				c.Compactions++
			}
		}
		for _, r := range t.Requests {
			if !r.UsageKnown || r.APIError {
				continue
			}
			in := r.Tokens.InputTotal()
			if in == 0 {
				continue
			}
			c.Requests++
			if !found {
				c.Baseline, found = in, true
			}
			c.Peak = max(c.Peak, in)
		}
	}
	return c, found
}

func (a *analysis) finish() Report {
	var r Report
	r.Cost = a.cost
	for _, m := range a.models {
		r.Cost.ByModel = append(r.Cost.ByModel, *m)
	}
	slices.SortFunc(r.Cost.ByModel, func(x, y ModelCost) int {
		if x.Cost != y.Cost {
			return cmp.Compare(y.Cost, x.Cost)
		}
		if x.Tokens.Total() != y.Tokens.Total() {
			return cmp.Compare(y.Tokens.Total(), x.Tokens.Total())
		}
		return strings.Compare(x.Model, y.Model)
	})
	for name, cost := range a.agentCost {
		r.Cost.ByAgent = append(r.Cost.ByAgent, NamedCost{Name: name, Cost: cost})
	}
	slices.SortFunc(r.Cost.ByAgent, func(x, y NamedCost) int { return cmp.Or(cmp.Compare(y.Cost, x.Cost), strings.Compare(x.Name, y.Name)) })
	for date, cost := range a.daily {
		r.Cost.Daily = append(r.Cost.Daily, DailyCost{Date: date, Cost: cost})
	}
	slices.SortFunc(r.Cost.Daily, func(x, y DailyCost) int { return strings.Compare(x.Date, y.Date) })
	r.Cost.Sessions = a.sessions
	slices.SortFunc(r.Cost.Sessions, func(x, y SessionCost) int {
		return cmp.Or(cmp.Compare(y.Cost, x.Cost), cmp.Compare(y.Tokens.Total(), x.Tokens.Total()), y.End.Compare(x.End))
	})
	for name := range a.unpriced {
		r.Cost.UnpricedModels = append(r.Cost.UnpricedModels, name)
	}
	slices.Sort(r.Cost.UnpricedModels)

	r.Context.Sessions = a.ctx
	slices.SortFunc(r.Context.Sessions, func(x, y SessionContext) int {
		return cmp.Or(cmp.Compare(y.Baseline, x.Baseline), y.Start.Compare(x.Start))
	})
	r.Context.ByAgent = a.contextGroups(func(c SessionContext) string { return session.LookupAgent(c.Agent).Name })
	r.Context.ByProject = a.contextGroups(func(c SessionContext) string { return projectName(c.CWD) })

	r.Friction = a.friction
	for _, t := range a.tools {
		r.Friction.Tools = append(r.Friction.Tools, *t)
	}
	slices.SortFunc(r.Friction.Tools, func(x, y ToolFriction) int {
		fx, fy := x.Failed+x.Interrupted+x.TimeOut, y.Failed+y.Interrupted+y.TimeOut
		return cmp.Or(cmp.Compare(fy, fx), cmp.Compare(y.Calls, x.Calls), strings.Compare(x.Name, y.Name))
	})
	for key, tools := range a.mcp {
		u := MCPUsage{Agent: key.agent, Server: key.server}
		for tool, n := range tools {
			u.Calls += n
			u.Tools = append(u.Tools, tool)
		}
		slices.Sort(u.Tools)
		r.Friction.MCP = append(r.Friction.MCP, u)
	}
	slices.SortFunc(r.Friction.MCP, func(x, y MCPUsage) int {
		return cmp.Or(cmp.Compare(y.Calls, x.Calls), strings.Compare(x.Server, y.Server))
	})
	slices.SortStableFunc(r.Friction.Loops, func(x, y Loop) int { return y.At.Compare(x.At) })

	for _, p := range a.projects {
		if p.project.Sessions == 0 && p.project.Tokens.Total() == 0 && p.project.ToolCalls == 0 {
			continue
		}
		for name := range p.agents {
			p.project.Agents = append(p.project.Agents, name)
		}
		slices.Sort(p.project.Agents)
		for name, n := range p.skills {
			p.project.Skills = append(p.project.Skills, NamedCount{Name: name, Count: n})
		}
		slices.SortFunc(p.project.Skills, func(x, y NamedCount) int { return cmp.Or(y.Count-x.Count, strings.Compare(x.Name, y.Name)) })
		if len(p.baselines) > 0 {
			slices.Sort(p.baselines)
			p.project.MedianBaseline = p.baselines[len(p.baselines)/2]
		}
		r.Projects = append(r.Projects, p.project)
	}
	slices.SortFunc(r.Projects, func(x, y Project) int {
		return cmp.Or(cmp.Compare(y.Cost, x.Cost), cmp.Compare(y.Tokens.Total(), x.Tokens.Total()), cmp.Compare(y.Sessions, x.Sessions), strings.Compare(x.Path, y.Path))
	})
	return r
}

func projectName(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return "未知目录"
	}
	return filepath.Base(filepath.Clean(cwd))
}

func (a *analysis) contextGroups(key func(SessionContext) string) []ContextGroup {
	groups := map[string][]SessionContext{}
	for _, c := range a.ctx {
		k := key(c)
		groups[k] = append(groups[k], c)
	}
	var out []ContextGroup
	for name, list := range groups {
		base := make([]int64, len(list))
		peak := make([]int64, len(list))
		g := ContextGroup{Name: name, Sessions: len(list)}
		for i, c := range list {
			base[i], peak[i] = c.Baseline, c.Peak
			g.Compactions += c.Compactions
		}
		slices.Sort(base)
		slices.Sort(peak)
		g.Median, g.P90, g.Min = quantile(base, .5), quantile(base, .9), base[0]
		g.MedianPeak = quantile(peak, .5)
		out = append(out, g)
	}
	slices.SortFunc(out, func(x, y ContextGroup) int {
		return cmp.Or(cmp.Compare(y.Median, x.Median), strings.Compare(x.Name, y.Name))
	})
	return out
}

func quantile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * q)
	return sorted[i]
}

func addTokens(a, b session.Tokens) session.Tokens {
	return session.Tokens{Input: a.Input + b.Input, Output: a.Output + b.Output, CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite}
}
