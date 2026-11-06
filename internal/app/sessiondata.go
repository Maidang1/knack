package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"knack/internal/insight"
	"knack/internal/session"
)

type sessionPhase int

const (
	phaseThink sessionPhase = iota
	phaseGen
	phaseTool
	phaseSub
	phaseWait
	phaseRetry
	phaseCount
)

var phaseKeys = [phaseCount]string{"phase-think", "phase-gen", "phase-tool", "phase-sub", "phase-wait", "phase-retry"}
var phaseNames = [phaseCount]string{"思考", "生成", "工具执行", "子 Agent", "等待你", "重试与压缩"}

type phaseClaim struct {
	start, end time.Time
	phase      int
}
type phaseTotals struct {
	values               [phaseCount]time.Duration
	total, away, unknown time.Duration
}
type sessionTurnView struct {
	turn    session.Turn
	steps   []sessionStep
	phases  phaseTotals
	index   int
	warning bool
	// active covers the turn's own work: from its start to the last request,
	// tool, sub-Agent or event. gap is what follows until the next turn,
	// waiting for the person's reply (or away), kept out of active time.
	active  phaseTotals
	gap     time.Duration
	gapAway bool
	tokens  session.Tokens
	usage   bool
}
type sessionTrack struct {
	session  session.Session
	unlinked bool
	note     string
}
type modelSpeed struct {
	name             string
	sessions         int
	requests         int
	output           int64
	usageKnown       bool
	samples          int
	p10, median, p90 float64
}
type callCount struct {
	name  string
	count int
	child bool
}
type sessionConcern struct {
	title, note, sessionID string
	turn                   int
	duration               session.Duration
	at                     time.Time
}
type sessionDetailData struct {
	session       session.Session
	summary       session.Summary
	tracks        []sessionTrack
	turns         []sessionTurnView
	phases        phaseTotals
	models        []modelSpeed
	skills, tools []callCount
	concerns      []sessionConcern
	toolCount     int
	diag          *insight.SessionDiagnosis // built when first shown
}

func sessionID(s session.Session) string { return string(s.Agent) + ":" + s.ID }
func sessionSpan(s session.Session) string {
	if s.Start.IsZero() || s.End.Before(s.Start) {
		return "时长暂不支持"
	}
	return "≈ " + session.FormatDuration(s.End.Sub(s.Start))
}

// This is a display projection of the immutable parsed snapshot. It does not
// read files, infer prompt content, or invent missing timing/call associations.
func sessionDetail(snapshot *session.Snapshot, root session.Session) *sessionDetailData {
	d := &sessionDetailData{session: root}
	parents := map[string]bool{}
	turnFor := map[string]int{}
	var track func(session.Session, bool, int)
	track = func(s session.Session, unlinked bool, parentTurn int) {
		if parents[s.ID] {
			return
		}
		parents[s.ID] = true
		d.tracks = append(d.tracks, sessionTrack{session: s, unlinked: unlinked})
		if parentTurn >= 0 {
			turnFor[s.ID] = parentTurn
		}
		for i, t := range s.Turns {
			for _, child := range t.Subagents {
				index := parentTurn
				if s.ID == root.ID {
					index = i
				}
				track(child.Session, unlinked, index)
			}
		}
	}
	track(root, false, -1)
	var orphans []session.Session
	if snapshot != nil {
		for changed := true; changed; {
			changed = false
			for _, s := range snapshot.UnlinkedSubagents {
				if s.Agent == root.Agent && parents[s.ParentID] && !parents[s.ID] {
					orphans = append(orphans, s)
					track(s, true, -1)
					changed = true
				}
			}
		}
	}
	d.summary = session.Aggregate(&session.Snapshot{Sessions: []session.Session{root}, UnlinkedSubagents: orphans}, session.Filter{})
	models := map[string]*modelSpeed{}
	rates := map[string][]float64{}
	skills, tools := map[string]*callCount{}, map[string]*callCount{}
	for _, tr := range d.tracks {
		for _, t := range tr.session.Turns {
			for _, r := range t.Requests {
				name := r.Model
				if name == "" {
					name = "未知模型"
				}
				m := models[name]
				if m == nil {
					m = &modelSpeed{name: name}
					models[name] = m
				}
				m.requests++
				m.output += r.Tokens.Output
				m.usageKnown = m.usageKnown || r.UsageKnown
				if value, ok := r.OutputRate(); ok {
					rates[name] = append(rates[name], value)
				}
			}
			for _, sk := range t.Skills {
				x := skills[sk.Name]
				if x == nil {
					x = &callCount{name: sk.Name}
					skills[sk.Name] = x
				}
				x.count++
				x.child = x.child || tr.session.ID != root.ID
			}
			for _, tool := range t.Tools {
				x := tools[tool.Name]
				if x == nil {
					x = &callCount{name: tool.Name}
					tools[tool.Name] = x
				}
				x.count++
				d.toolCount++
			}
		}
	}
	for name, m := range models {
		v := rates[name]
		slices.Sort(v)
		m.samples = len(v)
		if len(v) > 0 {
			m.p10 = percentile(v, .1)
			m.p90 = percentile(v, .9)
			m.median = percentile(v, .5)
			if len(v)%2 == 0 {
				m.median = (v[len(v)/2-1] + v[len(v)/2]) / 2
			}
		}
		d.models = append(d.models, *m)
	}
	slices.SortFunc(d.models, func(a, b modelSpeed) int {
		if a.output > b.output {
			return -1
		}
		if a.output < b.output {
			return 1
		}
		return strings.Compare(a.name, b.name)
	})
	d.skills = sortedCalls(skills)
	d.tools = sortedCalls(tools)
	for i := range d.tracks {
		d.tracks[i].note = trackNote(d.tracks[i])
	}
	var extra []phaseClaim
	for _, child := range orphans {
		extra = append(extra, phaseClaim{child.Start, child.End, int(phaseSub)})
	}
	all := slices.Clone(extra)
	for i, t := range root.Turns {
		claims := turnClaims(t)
		all = append(all, claims...)
		end := t.End
		for _, wait := range t.Waits {
			if wait.End.After(end) {
				end = wait.End
			}
		}
		for _, child := range t.Subagents {
			if child.Session.End.After(end) {
				end = child.Session.End
			}
		}
		if i+1 < len(root.Turns) && root.Turns[i+1].Start.After(t.Start) && root.Turns[i+1].Start.Before(end) {
			end = root.Turns[i+1].Start
		}
		if root.End.Before(end) {
			end = root.End
		}
		claims = append(claims, extra...)
		view := sessionTurnView{turn: t, steps: turnSteps(t), index: i, phases: partitionPhases(t.Start, end, claims)}
		activeEnd := turnActiveEnd(t)
		if activeEnd.After(end) {
			activeEnd = end
		}
		view.active = partitionPhases(t.Start, activeEnd, claims)
		if end.After(activeEnd) && !activeEnd.IsZero() {
			view.gap = end.Sub(activeEnd)
			view.gapAway = view.phases.away > 0
		}
		for _, r := range t.Requests {
			view.tokens.Input += r.Tokens.Input
			view.tokens.Output += r.Tokens.Output
			view.tokens.CacheRead += r.Tokens.CacheRead
			view.tokens.CacheWrite += r.Tokens.CacheWrite
			view.usage = view.usage || r.UsageKnown
		}
		d.turns = append(d.turns, view)
	}
	d.phases = partitionPhases(root.Start, root.End, all)
	for _, an := range d.summary.Anomalies {
		index := -1
		if an.SessionID == root.ID {
			for i, t := range root.Turns {
				if t.ID == an.TurnID {
					index = i
					break
				}
			}
		} else if i, ok := turnFor[an.SessionID]; ok {
			index = i
		}
		concern := sessionConcern{turn: index, duration: an.Elapsed, at: an.At, sessionID: an.SessionID}
		switch an.Kind {
		case "slow_model":
			concern.title = "模型请求慢"
			concern.note = "模型请求间隔估算"
		case "slow_tool":
			concern.title = "工具执行慢"
			concern.note = "工具调用到返回的间隔，估算"
		case "permission_wait":
			concern.title = "等待你确认"
			concern.note = "权限相关工具间隔，估算，含执行时间"
		case "retry":
			concern.title = "API 重试（推断）"
			concern.note = "前一次请求失败；退避和生成无法拆分"
		case "api_error":
			concern.title = "API 请求失败"
			concern.note = "日志标记为 API 错误"
		case "compact":
			concern.title = "上下文压缩"
			concern.note = "日志报告的压缩耗时"
		case "interrupted":
			concern.title = "已中断"
			concern.note = "日志报告调用或轮次被中断"
		case "timeout":
			concern.title = "工具超时"
			concern.note = "时长为日志报告的超时阈值"
		default:
			continue
		}
		if index < 0 {
			concern.note += " · 子 Agent 轮次关联未知"
		}
		for _, tr := range d.tracks {
			if tr.session.ID == an.SessionID {
				for _, t := range tr.session.Turns {
					for _, tool := range t.Tools {
						if tool.ID == an.ItemID && an.ItemID != "" {
							concern.note = tool.Name + " · " + concern.note
						}
					}
				}
			}
		}
		d.concerns = append(d.concerns, concern)
	}
	slices.SortStableFunc(d.concerns, func(a, b sessionConcern) int {
		if a.duration.Value > b.duration.Value {
			return -1
		}
		if a.duration.Value < b.duration.Value {
			return 1
		}
		return b.at.Compare(a.at)
	})
	for _, item := range d.concerns {
		if item.turn >= 0 {
			d.turns[item.turn].warning = true
		}
	}
	return d
}

// The last moment the turn did work. Reply waits and away summaries happen
// after it and are not part of it.
func turnActiveEnd(t session.Turn) time.Time {
	end := t.Start
	later := func(at time.Time) {
		if at.After(end) {
			end = at
		}
	}
	for _, r := range t.Requests {
		later(r.End)
	}
	for _, tool := range t.Tools {
		later(tool.End)
		later(tool.Start)
	}
	for _, sub := range t.Subagents {
		later(sub.Session.End)
	}
	for _, w := range t.Waits {
		if w.Kind == "permission" {
			later(w.End)
		}
	}
	for _, ev := range t.Events {
		if ev.Kind != "away_summary" {
			later(ev.At)
		}
	}
	return end
}

func percentile(sorted []float64, q float64) float64 {
	return sorted[max(0, int(math.Ceil(float64(len(sorted))*q))-1)]
}
func sortedCalls(m map[string]*callCount) []callCount {
	var out []callCount
	for _, v := range m {
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b callCount) int {
		if a.count != b.count {
			return b.count - a.count
		}
		return strings.Compare(a.name, b.name)
	})
	return out
}

func turnClaims(t session.Turn) []phaseClaim {
	var out []phaseClaim
	for _, r := range t.Requests {
		if !r.Elapsed.Known || r.End.IsZero() {
			continue
		}
		start := r.End.Add(-r.Elapsed.Value)
		if r.APIError || r.Retry {
			out = append(out, phaseClaim{start, r.End, int(phaseRetry)})
			continue
		}
		thinking := min(r.Thinking.Value, r.Elapsed.Value)
		if thinking > 0 {
			out = append(out, phaseClaim{start, start.Add(thinking), int(phaseThink)})
		}
		out = append(out, phaseClaim{start.Add(thinking), r.End, int(phaseGen)})
	}
	for _, tool := range t.Tools {
		kind := phaseTool
		if tool.AgentID != "" {
			kind = phaseSub
		}
		if tool.PermissionWait {
			kind = phaseWait
		}
		out = append(out, phaseClaim{tool.Start, tool.End, int(kind)})
	}
	for _, sub := range t.Subagents {
		out = append(out, phaseClaim{sub.Session.Start, sub.Session.End, int(phaseSub)})
	}
	for _, w := range t.Waits {
		kind := int(phaseWait)
		if w.Kind == "away" {
			kind = int(phaseCount)
		}
		out = append(out, phaseClaim{w.Start, w.End, kind})
	}
	for _, ev := range t.Events {
		if ev.Kind == "compact" && ev.Duration.Known && !ev.At.IsZero() {
			out = append(out, phaseClaim{ev.At.Add(-ev.Duration.Value), ev.At, int(phaseRetry)})
		}
	}
	return out
}

// Sweep line partitions wall time instead of adding concurrent durations. Human
// permission waiting overrides its enclosing tool; children override Agent tool
// wrappers. An away-summary gap and unobserved time remain separately labelled.
func partitionPhases(start, end time.Time, claims []phaseClaim) phaseTotals {
	var out phaseTotals
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return out
	}
	out.total = end.Sub(start)
	type edge struct {
		at           time.Time
		phase, delta int
	}
	edges := []edge{{at: start}, {at: end}}
	for _, claim := range claims {
		if claim.start.IsZero() || claim.end.IsZero() || !claim.end.After(claim.start) {
			continue
		}
		a, b := claim.start, claim.end
		if a.Before(start) {
			a = start
		}
		if b.After(end) {
			b = end
		}
		if !b.After(a) {
			continue
		}
		edges = append(edges, edge{a, claim.phase, 1}, edge{b, claim.phase, -1})
	}
	slices.SortFunc(edges, func(a, b edge) int { return a.at.Compare(b.at) })
	var active [phaseCount + 1]int
	last := start
	priority := []int{int(phaseWait), int(phaseRetry), int(phaseSub), int(phaseTool), int(phaseThink), int(phaseGen), int(phaseCount)}
	for _, e := range edges {
		delta := e.at.Sub(last)
		kind := -1
		for _, i := range priority {
			if active[i] > 0 {
				kind = i
				break
			}
		}
		if kind < 0 {
			out.unknown += delta
		} else if kind == int(phaseCount) {
			out.away += delta
		} else {
			out.values[kind] += delta
		}
		active[e.phase] += e.delta
		last = e.at
	}
	return out
}

func phaseDescription(p phaseTotals) string {
	parts := []string{"总计 ≈ " + session.FormatDuration(p.total)}
	for i, value := range p.values {
		if value > 0 {
			parts = append(parts, phaseNames[i]+" ≈ "+session.FormatDuration(value))
		}
	}
	if p.away > 0 {
		parts = append(parts, "离开相关间隔 ≈ "+session.FormatDuration(p.away))
	}
	if p.unknown > 0 {
		parts = append(parts, "未归因 "+session.FormatDuration(p.unknown))
	}
	return strings.Join(parts, " · ")
}

func turnLabel(t sessionTurnView) string {
	label := fmt.Sprintf("第 %d 轮 · 活跃 %s", t.index+1, activeText(t))
	if t.warning {
		label += " · 有耗时异常"
	}
	return label
}

func trackNote(track sessionTrack) string {
	var tokens int64
	known := false
	var rates []float64
	models := map[string]int64{}
	for _, turn := range track.session.Turns {
		for _, r := range turn.Requests {
			tokens += r.Tokens.Total()
			known = known || r.UsageKnown
			if value, ok := r.OutputRate(); ok {
				rates = append(rates, value)
			}
			models[r.Model] += r.Tokens.Total()
		}
	}
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if models[a] > models[b] {
			return -1
		}
		if models[a] < models[b] {
			return 1
		}
		return strings.Compare(a, b)
	})
	model := "模型未知"
	if len(names) > 0 && names[0] != "" {
		model = names[0]
	}
	usage := "用量暂不支持"
	if known {
		usage = tokenNumber(tokens) + " Token"
	}
	if len(rates) > 0 {
		slices.Sort(rates)
		median := percentile(rates, .5)
		if len(rates)%2 == 0 {
			median = (rates[len(rates)/2-1] + rates[len(rates)/2]) / 2
		}
		usage += fmt.Sprintf(" · ≈ %.0f tok/s", median)
	}
	note := sessionAgentName(track.session.Agent) + " · " + model + " · " + usage
	if track.unlinked {
		note += " · 轮次关联未知"
	}
	return note
}
