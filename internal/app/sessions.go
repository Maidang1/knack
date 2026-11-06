package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

type sessionState struct {
	reader          *session.Reader
	snapshot        *session.Snapshot
	loading, cached bool
	err             string
	period          int
	agent           string
	day             int
	overview        *overviewData
	browser         *sessionBrowserData
	query           string
	hits            sessionHits // sessions the sidebar's query matches
	anomaliesOnly   bool
	selected        string
	index           int
	list            ui.ListState
	open            bool // a session's page shows in place of the list
	replayOpen      bool // its conversation shows in place of its page
	replayQuery     string
	replayFilter    int
	selectedTurn    string
	selectedStep    int
	stepOpen        bool // the chosen step shows its content under its row
	focusTurn       bool
	turnFilter      int    // the turn list shows all, sub-Agent or confirmation turns
	allTurns        bool   // a long turn list shows every turn
	allFindings     string // the session whose findings all show
	detail          *sessionDetailData
	detailSource    *session.Snapshot
	replay          []*replayState
	expanded        map[string]bool // replay text blocks shown in full
	agentProblems   []string        // invalid definitions in agents.json
}

func (a *App) loadSessions() {
	// User-defined Agents register before the first read; see docs/agents.md.
	problems := session.LoadAgentConfig(filepath.Join(a.home, ".config", "skill-manager", "agents.json"))
	path := filepath.Join(a.home, ".cache", "skill-manager", "sessions.json")
	r, err := session.NewReader(a.home, path)
	a.sessions = sessionState{reader: r, period: 2, agent: allAgents, day: -1, index: -1, agentProblems: problems}
	if err != nil {
		a.sessions.err = "无法读取会话缓存，将重新读取本地日志"
		return
	}
	snap, err := r.Cached()
	if err != nil {
		a.sessions.err = err.Error()
		return
	}
	a.sessions.snapshot = snap
	a.sessions.cached = snap != nil
}

func (a *App) startSessionScan() {
	if a.sessions.loading || a.sessions.reader == nil {
		return
	}
	a.sessions.loading = true
	run := func() {
		snap, err := a.sessions.reader.Refresh(context.Background())
		a.post(func() {
			a.sessions.loading = false
			a.sessions.err = ""
			if err != nil {
				a.sessions.err = err.Error()
			}
			if snap != nil {
				a.sessions.snapshot = snap
				a.sessions.cached = false
			}
			a.sessions.overview = nil
			// Each refresh reads a bounded slice so the UI updates progressively;
			// keep going until the backlog is consumed.
			if err == nil && snap != nil && snap.More && snap.ReadBytes > 0 {
				a.startSessionScan()
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

type sessionBrowserData struct {
	view          *overviewData
	query         string
	anomaliesOnly bool
	rows          []session.Session
}

func (a *App) sessionRows() []session.Session {
	s := &a.sessions
	view := a.overviewData()
	if s.browser != nil && s.browser.view == view && s.browser.query == s.query && s.browser.anomaliesOnly == s.anomaliesOnly {
		return s.browser.rows
	}
	cache := &sessionBrowserData{view: view, query: s.query, anomaliesOnly: s.anomaliesOnly}
	f := overviewFilter(s.period, a.sessionAgentFilter(), time.Now())
	if s.snapshot != nil {
		for _, row := range s.snapshot.Sessions {
			if f.Agent != "" && row.Agent != f.Agent {
				continue
			}
			if !f.From.IsZero() && row.End.Before(f.From) || !f.To.IsZero() && !row.Start.Before(f.To) {
				continue
			}
			if s.anomaliesOnly && !view.anomalies[sessionID(row)] {
				continue
			}
			if !matchSession(s.query, row) {
				continue
			}
			cache.rows = append(cache.rows, row)
		}
	}
	slices.SortStableFunc(cache.rows, func(a, b session.Session) int { return b.End.Compare(a.End) })
	s.index = -1
	for i, row := range cache.rows {
		if sessionID(row) == s.selected {
			s.index = i
			break
		}
	}
	if s.index < 0 && len(cache.rows) > 0 {
		s.index = 0
	}
	s.browser = cache
	return cache.rows
}

// openSession shows a session's page. Its turn is the one with the longest
// concern unless one is chosen after.
func (a *App) openSession(id string) {
	s := &a.sessions
	a.page = "sessions"
	s.selected = id
	s.open = true
	s.replayOpen = false
	s.query = ""
	s.anomaliesOnly = false
	s.browser = nil
	s.selectedTurn = ""
	// Direct links must also work after a narrower list filter was selected.
	s.period = 3
	s.agent = allAgents
}

func (a *App) openSessionAnomalies(view *overviewData) {
	for _, row := range view.recent {
		if view.anomalies[sessionID(row)] {
			a.openSession(sessionID(row))
			a.sessions.anomaliesOnly = true
			return
		}
	}
	a.page = "sessions"
	a.sessions.open = false
	a.sessions.anomaliesOnly = true
	a.sessions.browser = nil
	a.notice = "当前日志中没有可打开的主会话；子 Agent 可能缺少父会话日志"
}

func (a *App) selectedSessionData(rows []session.Session) *sessionDetailData {
	s := &a.sessions
	if s.index < 0 || s.index >= len(rows) {
		return nil
	}
	row := rows[s.index]
	if s.detail == nil || s.detailSource != s.snapshot || sessionID(row) != sessionID(s.detail.session) {
		if s.selected != sessionID(row) {
			s.selectedTurn = ""
		}
		s.selected = sessionID(row)
		s.detail = sessionDetail(s.snapshot, row)
		s.detailSource = s.snapshot
	}
	return s.detail
}

// closeSession steps back: from the conversation to the session's page,
// from the page to the list.
func (a *App) closeSession() {
	s := &a.sessions
	if s.replayOpen {
		s.replayOpen = false
	} else {
		s.open = false
	}
	s.focusTurn = true
}

// stepSession opens the session by moves rows away in the list.
func (a *App) stepSession(by int) {
	s := &a.sessions
	rows := a.sessionRows()
	if i := s.index + by; i >= 0 && i < len(rows) {
		s.index = i
		s.selected = sessionID(rows[i])
		s.selectedTurn = ""
	}
}

func (a *App) sessionsPane(c *ui.Context) {
	s := &a.sessions
	rows := a.sessionRows()
	width, _ := c.Size()
	content := width - a.sidebarWidth()
	d := a.selectedSessionData(rows)
	switch {
	case !s.open:
		ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.sessionList(c, rows) })
	case d == nil:
		ui.Column(c).Grow(1).Padding(24).Gap(10).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "没有匹配的会话").FontSize(17).FontWeight(600)
			ui.Text(c, "调整时间范围、Agent 或搜索条件").FontSize(13).TextColor(c.Theme().TextMuted)
		})
	case s.replayOpen:
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() { a.sessionConversation(c, d, content) })
	default:
		ui.Column(c).Grow(1).MinHeight(0).Children(func() { a.sessionPage(c, d, content) })
	}
	// Escape steps back from within the page, and from anywhere else unless
	// a focused control or a dialog takes it first.
	if s.open && !a.settingsOpen && !a.ask.open && (a.repoDialog == nil || !a.repoDialog.open) {
		c.OnShortcut(0, ui.KeyEscape, a.closeSession)
		if c.Shortcut(0, ui.KeyEscape) {
			a.closeSession()
			c.Invalidate()
		}
	}
}

// Columns of the session list after the session's own, which takes the rest.
var sessionColumns = []struct {
	title string
	width float32
}{{"Agent", 120}, {"轮次", 56}, {"时长", 84}, {"Token", 74}, {"最近活跃", 104}}

func (a *App) sessionList(c *ui.Context, rows []session.Session) {
	s := &a.sessions
	t := c.Theme()
	muted := token(t, "ink-3")
	view := a.overviewData()
	ui.Column(c).Fill().Padding(20, 32, 0, 32).Gap(12).Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			field := ui.SearchField(c, &s.query).Label("搜索会话").Placeholder("标题、Agent、仓库或分支").Width(300).Height(30).Radius(7).
				Background(t.Surface).Border(1, t.Border).PlaceholderColor(muted)
			if field.Changed() {
				a.query = s.query
			}
			ui.Checkbox(c, &s.anomaliesOnly, "只看耗时异常")
			ui.Spacer(c)
			ui.Textf(c, "%d 个会话 · 按最近活跃排序", len(rows)).FontSize(13).TextColor(muted)
		})
		if s.err != "" {
			ui.Text(c, s.err).FontSize(13).TextColor(t.Warning).MaxLines(2)
		}
		a.partialBanner(c)
		ui.Row(c).Height(28).Gap(12).Padding(0, 8, 0, 23).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, "会话").FontSize(11.5).TextColor(muted).Grow(1)
			for i, col := range sessionColumns {
				h := ui.Text(c, col.title).FontSize(11.5).TextColor(muted).Width(col.width).Shrink(0)
				if i > 0 {
					h.TextAlign(ui.End)
				}
			}
		})
		s.list.Key = func(i int) any {
			if i < 0 || i >= len(rows) {
				return i
			}
			return sessionID(rows[i])
		}
		s.list.Label = func(i int) string {
			if i < 0 || i >= len(rows) {
				return ""
			}
			return recentLabel(rows[i]) + " · " + sessionAgentName(rows[i].Agent) + " · " + sessionSpan(rows[i])
		}
		s.list.Selected = &s.index
		now := time.Now()
		submitted := false
		ui.Themed(c, quietList(t)).Grow(1).MinHeight(0).Background(ui.Transparent).Children(func() {
			list := ui.List(c, &s.list, len(rows), func(i int) {
				row := rows[i]
				warning := view.anomalies[sessionID(row)]
				line := ui.Row(c.Key("row:"+sessionID(row))).Height(52).Gap(12).Padding(0, 8).Radius(6).AlignItems(ui.Center).Border(1, ui.Transparent)
				switch {
				case i == s.index:
					// The list gives its chosen row the accent; the row covers it
					// with the quiet fill every list uses, and keeps the ink.
					line.Background(token(t, "selected")).TextColor(t.Text)
				case line.Hovered():
					line.Background(t.SurfaceHover)
				}
				line.Children(func() {
					ui.Box(c).Width(3).Height(30).Radius(2).Shrink(0).Background(agentColor(row.Agent, t.Dark))
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, recentLabel(row)).FontSize(13).MinWidth(0).SingleLine().Tooltip(recentLabel(row))
							if warning {
								ui.Icon(c, glyph("issues")).FontSize(13).TextColor(t.Warning).Label("有耗时异常").Shrink(0)
							}
						})
						meta := []string{}
						if row.CWD != "" {
							meta = append(meta, filepath.Base(row.CWD))
						}
						if row.GitBranch != "" {
							meta = append(meta, row.GitBranch)
						}
						if n := a.childCount(row); n > 0 {
							meta = append(meta, fmt.Sprintf("%d 子 Agent", n))
						}
						ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(muted).SingleLine()
					})
					cell := func(i int, value string) ui.Element {
						return number(c, value).FontSize(13).TextColor(t.TextMuted).Width(sessionColumns[i].width).Shrink(0).TextAlign(ui.End).SingleLine()
					}
					ui.Text(c, sessionAgentName(row.Agent)).FontSize(13).TextColor(t.TextMuted).Width(sessionColumns[0].width).Shrink(0).SingleLine()
					cell(1, fmt.Sprint(len(row.Turns)))
					span := cell(2, strings.TrimPrefix(sessionSpan(row), "≈ "))
					if warning {
						span.TextColor(t.Warning)
					}
					tokens := "—"
					if total, known := sessionTokens(row); known {
						tokens = tokenNumber(total)
					}
					cell(3, tokens)
					active := "—"
					if !row.End.IsZero() {
						active = ago(now, row.End)
					}
					cell(4, active)
				})
				if line.Clicked() {
					s.index = i
					a.openSession(sessionID(row))
					c.Invalidate()
				}
			}).Grow(1).Gap(2).Padding(4, 0).Label("会话列表")
			submitted = list.Submitted()
		})
		if submitted && s.index >= 0 && s.index < len(rows) {
			a.openSession(sessionID(rows[s.index]))
			c.Invalidate()
		}
		// Native List drops its old key when a search removes the selection.
		// Select the first matching row after that reconciliation.
		clamp(&s.index, len(rows))
		var missing []string
		for _, info := range session.Agents() {
			if info.Unsupported != "" {
				missing = append(missing, info.Name)
			}
		}
		if len(missing) > 0 {
			ui.Text(c, strings.Join(missing, "、")+" 没有本地会话日志，暂不支持").FontSize(11.5).TextColor(muted).Padding(8, 0, 12, 0).MaxLines(2)
		}
	})
}

// sessionTokens adds the usage the session's own requests recorded.
func sessionTokens(row session.Session) (int64, bool) {
	var total int64
	known := false
	for _, turn := range row.Turns {
		for _, r := range turn.Requests {
			if r.UsageKnown {
				total += r.Tokens.Total()
				known = true
			}
		}
	}
	return total, known
}

// childCount is how many sub-Agents a session started, linked or not.
func (a *App) childCount(row session.Session) int {
	n := 0
	for _, tr := range row.Turns {
		n += len(tr.Subagents)
	}
	if a.sessions.snapshot != nil {
		for _, child := range a.sessions.snapshot.UnlinkedSubagents {
			if child.Agent == row.Agent && child.ParentID == row.ID {
				n++
			}
		}
	}
	return n
}

func (a *App) chooseSessionTurn(d *sessionDetailData, index int) {
	if index < 0 || index >= len(d.turns) {
		return
	}
	a.sessions.selectedTurn = d.turns[index].turn.ID
	a.sessions.selectedStep, a.sessions.stepOpen = 0, false
}

// partialBanner says the session statistics stopped short of every log,
// and offers to read again. While a read is running it says nothing.
func (a *App) partialBanner(c *ui.Context) {
	snap := a.sessions.snapshot
	if snap == nil || !snap.Partial || a.sessions.loading {
		return
	}
	t := c.Theme()
	ink := token(t, "warn-ink")
	ui.Column(c).Shrink(0).Padding(14, 16).Gap(8).Radius(8).Background(token(t, "warn-bg")).Label("会话统计还没读完").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph("hourglass")).FontSize(15).TextColor(t.Warning)
			ui.Text(c, "会话统计还没读完").FontSize(13).FontWeight(600).TextColor(ink)
		})
		ui.Text(c, "部分日志未能读取，下面的数字暂时偏少。重新读取可再次尝试。").FontSize(13).LineHeight(1.5).TextColor(ink)
		if outlineButton(c, "read-again", "继续读取").AlignSelf(ui.Start).Disabled(a.scanning || a.mutating).Clicked() {
			a.startScan()
		}
	})
}

type sessionHits struct {
	query string
	snap  *session.Snapshot
	n     int
}

// matchSession reports whether a session answers a search.
func matchSession(query string, row session.Session) bool {
	return matchQuery(query, recentLabel(row), row.CWD, row.GitBranch, row.ID, sessionAgentName(row.Agent))
}

// sessionMatches counts the sessions of any time a query finds.
func (a *App) sessionMatches(query string) int {
	s := &a.sessions
	if s.snapshot == nil || strings.TrimSpace(query) == "" {
		return 0
	}
	if s.hits.query == query && s.hits.snap == s.snapshot {
		return s.hits.n
	}
	n := 0
	for _, row := range s.snapshot.Sessions {
		if matchSession(query, row) {
			n++
		}
	}
	s.hits = sessionHits{query, s.snapshot, n}
	return n
}

// searchSessions shows the sessions a query finds, over all of time.
func (a *App) searchSessions(query string) {
	s := &a.sessions
	a.page, s.query, s.open, s.replayOpen = "sessions", query, false, false
	s.period = len(periodLabels) - 1
}
