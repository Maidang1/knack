package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"knack/internal/session"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"
)

type overviewAgent struct {
	id      session.Agent
	summary session.Summary
	last    time.Time
}
type overviewDay struct {
	date   string
	tokens []int64 // per overviewData.series
}
type overviewData struct {
	snapshot          *session.Snapshot
	period            int
	agent, date       string
	summary, previous session.Summary
	agents            []overviewAgent
	series            []session.Agent // chart series; "" collects the rest
	days              []overviewDay
	recent            []session.Session
	anomalies         map[string]bool
	anomalyCount      map[string]int
	timing            *usageTimingData // filled when a usage page first asks
}

func overviewFilter(period int, agent string, now time.Time) session.Filter {
	loc := now.Location()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	f := session.Filter{Location: loc}
	if period < 3 {
		days := []int{1, 7, 30}[max(0, period)]
		f.From = start.AddDate(0, 0, -days+1)
		f.To = start.AddDate(0, 0, 1)
	}
	if info, ok := session.AgentByName(agent); ok {
		f.Agent = info.ID
	} else if agent != "" && agent != allAgents {
		f.Agent = session.Agent(strings.ToLower(agent))
	}
	return f
}

func (a *App) overviewData() *overviewData {
	s := &a.sessions
	now := time.Now()
	date := now.Format("2006-01-02")
	agentFilter := a.sessionAgentFilter()
	if d := s.overview; d != nil && d.snapshot == s.snapshot && d.period == s.period && d.agent == agentFilter && d.date == date {
		return d
	}
	d := &overviewData{snapshot: s.snapshot, period: s.period, agent: agentFilter, date: date, anomalies: map[string]bool{}, anomalyCount: map[string]int{}}
	f := overviewFilter(s.period, agentFilter, now)
	d.summary = session.Aggregate(s.snapshot, f)
	if !f.From.IsZero() {
		previous := f
		previous.To = f.From
		previous.From = f.From.AddDate(0, 0, -[]int{1, 7, 30}[s.period])
		d.previous = session.Aggregate(s.snapshot, previous)
	}
	owners := map[string]string{}
	var own func(session.Session, string)
	own = func(s session.Session, owner string) {
		owners[string(s.Agent)+":"+s.ID] = owner
		for _, t := range s.Turns {
			for _, child := range t.Subagents {
				own(child.Session, owner)
			}
		}
	}
	if s.snapshot != nil {
		for _, sess := range s.snapshot.Sessions {
			own(sess, string(sess.Agent)+":"+sess.ID)
			if sess.UnsupportedReason != "" || f.Agent != "" && sess.Agent != f.Agent {
				continue
			}
			if !f.From.IsZero() && sess.End.Before(f.From) || !f.To.IsZero() && !sess.Start.Before(f.To) {
				continue
			}
			d.recent = append(d.recent, sess)
		}
		for _, child := range s.snapshot.UnlinkedSubagents {
			own(child, string(child.Agent)+":"+child.ParentID)
		}
	}
	for _, an := range d.summary.Anomalies {
		key := string(an.Agent) + ":" + an.SessionID
		if owner := owners[key]; owner != "" {
			key = owner
		}
		d.anomalies[key] = true
		d.anomalyCount[key]++
	}
	slices.SortStableFunc(d.recent, func(a, b session.Session) int { return b.End.Compare(a.End) })
	for _, info := range session.Agents() {
		agent := info.ID
		if info.Unsupported != "" || f.Agent != "" && f.Agent != agent {
			continue
		}
		af := f
		af.Agent = agent
		ag := overviewAgent{id: agent, summary: session.Aggregate(s.snapshot, af)}
		for _, sess := range d.recent {
			if sess.Agent == agent && sess.End.After(ag.last) {
				ag.last = sess.End
			}
		}
		d.agents = append(d.agents, ag)
	}
	slices.SortStableFunc(d.agents, func(a, b overviewAgent) int {
		if a.summary.Tokens.Total() > b.summary.Tokens.Total() {
			return -1
		}
		if a.summary.Tokens.Total() < b.summary.Tokens.Total() {
			return 1
		}
		return 0
	})
	// Keep at most 60 daily bars in the all-time view; totals still cover all
	// history. Empty days are explicit zero buckets inside the selected range.
	chartStart := f.From
	if chartStart.IsZero() {
		chartStart = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -59)
		if len(d.summary.Daily) > 0 {
			if first, e := time.ParseInLocation("2006-01-02", d.summary.Daily[0].Date, now.Location()); e == nil && first.After(chartStart) {
				chartStart = first
			}
		}
	}
	// Agents with usage become series, largest first; beyond chartSeries
	// named Agents the rest share one "其他" series.
	slot := map[session.Agent]int{}
	for _, ag := range d.agents {
		if ag.summary.Tokens.Total() == 0 {
			continue
		}
		if len(d.series) == chartSeries {
			d.series[chartSeries-1] = ""
			slot[ag.id] = chartSeries - 1
			continue
		}
		slot[ag.id] = len(d.series)
		d.series = append(d.series, ag.id)
	}
	byDate := map[string][]int64{}
	for _, ag := range d.agents {
		i, ok := slot[ag.id]
		if !ok {
			continue
		}
		for _, day := range ag.summary.Daily {
			tokens := byDate[day.Date]
			if tokens == nil {
				tokens = make([]int64, len(d.series))
				byDate[day.Date] = tokens
			}
			tokens[i] += day.Tokens.Total()
		}
	}
	for day := chartStart; !day.After(now); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		tokens := byDate[key]
		if tokens == nil {
			tokens = make([]int64, len(d.series))
		}
		d.days = append(d.days, overviewDay{date: key, tokens: tokens})
	}
	s.overview = d
	return d
}

// chartSeries bounds the stacked series of the daily chart, "其他" included.
const chartSeries = 4
const allAgents = "全部 Agent"

func sessionAgentName(id session.Agent) string { return session.LookupAgent(id).Name }

// agentOptions lists the Agent filter: all Agents, then those with logs.
func agentOptions() []string {
	out := []string{allAgents}
	for _, info := range session.Agents() {
		if info.Unsupported == "" {
			out = append(out, info.Name)
		}
	}
	return out
}
func tokenNumber(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// number sets a figure in the system face with digits of one width, so
// columns line up without the wide monospaced face, which also spreads the
// Han characters of labels such as 「2 小时前」.
func number(c *ui.Context, value string) ui.Element {
	return ui.Text(c, value).FontFeatures("tnum").SingleLine()
}
func overviewTitle(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(15).FontWeight(600).SingleLine()
}
func overviewLink(c *ui.Context, label string, do func()) {
	b := action.Button(c, label, action.ButtonProps{Variant: action.Link, Size: lobe.Small}).Label(label)
	if b.Clicked() {
		do()
		c.Invalidate()
	}
}

var periodLabels = []string{"今天", "7 天", "30 天", "全部"}

func (a *App) overviewToolbar(c *ui.Context) {
	segmented(c, "统计时间范围", &a.sessions.period, periodLabels...)
	selectField(c, &a.sessions.agent, agentOptions()).Label("统计 Agent").Width(116)
}

func recentLabel(s session.Session) string {
	if strings.TrimSpace(s.Title) != "" {
		return s.Title
	}
	if s.CWD != "" {
		return sessionAgentName(s.Agent) + " · " + filepath.Base(s.CWD)
	}
	return sessionAgentName(s.Agent) + " · " + s.ID
}

// The accessible name of a recent-session row; the title alone also names
// the session's row in the issues panel.
func recentRowLabel(s session.Session) string {
	return recentLabel(s) + " · " + sessionAgentName(s.Agent) + " · " + s.End.Local().Format("01/02 15:04")
}
