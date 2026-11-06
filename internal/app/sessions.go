package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
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
	anomaliesOnly   bool
	selected        string
	index           int
	list            ui.ListState
	listW           float32
	tab             int
	selectedTurn    string
	roundOpen       bool
	selectedStep    int
	focusTurn       bool
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
	a.sessions = sessionState{reader: r, period: 2, agent: allAgents, day: -1, index: -1, listW: 300, agentProblems: problems}
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
			if !matchQuery(s.query, recentLabel(row), row.CWD, row.GitBranch, row.ID, sessionAgentName(row.Agent)) {
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

func (a *App) openSession(id string, tab int) {
	s := &a.sessions
	a.page = "sessions"
	s.selected = id
	s.tab = tab
	s.query = ""
	s.anomaliesOnly = false
	s.browser = nil
	s.roundOpen = false
	s.selectedTurn = ""
	// Direct links must also work after a narrower list filter was selected.
	s.period = 3
	s.agent = allAgents
}

func (a *App) openSessionAnomalies(view *overviewData) {
	for _, row := range view.recent {
		if view.anomalies[sessionID(row)] {
			a.openSession(sessionID(row), 1)
			a.sessions.anomaliesOnly = true
			return
		}
	}
	a.page = "sessions"
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
			s.roundOpen = false
		}
		s.selected = sessionID(row)
		s.detail = sessionDetail(s.snapshot, row)
		s.detailSource = s.snapshot
	}
	return s.detail
}

func (a *App) sessionsPane(c *ui.Context) {
	s := &a.sessions
	rows := a.sessionRows()
	width, _ := c.Size()
	content := width - a.sidebarWidth()
	s.listW = max(220, min(s.listW, content*.45))
	pane := ui.Split(c, &s.listW, func() { a.sessionList(c, rows) }, func() {
		d := a.selectedSessionData(rows)
		if d == nil {
			ui.Column(c).Fill().Padding(24).Gap(10).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "没有匹配的会话").FontSize(17).FontWeight(600)
				ui.Text(c, "调整时间范围、Agent 或搜索条件").FontSize(12).TextColor(c.Theme().TextMuted)
			})
			return
		}
		a.sessionDetailPane(c, d, content-s.listW)
	}).Grow(1).MinHeight(0)
	if s.roundOpen && !a.settingsOpen && !a.ask.open && (a.repoDialog == nil || !a.repoDialog.open) {
		pane.OnShortcut(0, ui.KeyEscape, a.closeSessionRound)
	}
}

func (a *App) sessionList(c *ui.Context, rows []session.Session) {
	s := &a.sessions
	t := c.Theme()
	view := a.overviewData()
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(12, 12, 8, 12).Gap(8).Children(func() {
			ui.Textf(c, "%d 个会话", len(rows)).FontSize(13).FontWeight(600)
			ui.Text(c, "按最近活跃排序 · 分叉数为子 Agent").FontSize(11).TextColor(t.TextMuted)
			if s.err != "" {
				ui.Text(c, s.err).FontSize(11).TextColor(t.Warning).MaxLines(2)
			}
			if s.snapshot != nil && s.snapshot.Partial {
				ui.Text(c, "日志尚未全部读取，当前结果不完整").FontSize(11).TextColor(t.TextMuted).MaxLines(2)
			}
			ui.SearchField(c, &s.query).Label("搜索会话").Placeholder("标题、Agent、仓库或分支").MinWidth(0)
			ui.Checkbox(c, &s.anomaliesOnly, "只看耗时异常")
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
		list := ui.List(c, &s.list, len(rows), func(i int) {
			row := rows[i]
			selected := i == s.index
			warning := view.anomalies[sessionID(row)]
			box := ui.Column(c).Gap(6).Padding(10, 10).Radius(5)
			if selected {
				box.Background(t.Accent).TextColor(t.AccentText)
			}
			box.Children(func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.Text(c, recentLabel(row)).FontSize(12).Grow(1).MinWidth(0).SingleLine().Tooltip(recentLabel(row))
					if warning {
						color := t.Warning
						if selected {
							color = ui.Hex("#FFE6A8")
						}
						ui.Icon(c, glyph("alert")).FontSize(13).TextColor(color).Label("有耗时异常")
					}
				})
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					label := sessionAgentName(row.Agent)
					if row.CWD != "" {
						label += " · " + filepath.Base(row.CWD)
					}
					text := ui.Text(c, label).FontSize(11).Grow(1).MinWidth(0).SingleLine()
					if !selected {
						text.TextColor(t.TextMuted)
					}
					children := 0
					for _, tr := range row.Turns {
						children += len(tr.Subagents)
					}
					if a.sessions.snapshot != nil {
						for _, child := range a.sessions.snapshot.UnlinkedSubagents {
							if child.Agent == row.Agent && child.ParentID == row.ID {
								children++
							}
						}
					}
					if children > 0 {
						number(c, fmt.Sprintf("⑂ %d", children)).FontSize(10).Tooltip("子 Agent 数量")
					}
					duration := number(c, sessionSpan(row)).FontSize(11)
					if warning {
						color := t.Warning
						if selected {
							color = ui.Hex("#FFE6A8")
						}
						duration.TextColor(color)
					} else if !selected {
						duration.TextColor(t.TextMuted)
					}
				})
				if !row.End.IsZero() {
					text := number(c, row.End.Local().Format("01/02 15:04")).FontSize(10)
					if !selected {
						text.TextColor(t.TextMuted)
					}
				}
			})
		}).Grow(1).Padding(4, 8).Gap(3).Label("会话列表")
		if list.Changed() {
			s.roundOpen = false
			s.selectedTurn = ""
			c.Invalidate()
		}
		// Native List drops its old key when a search removes the selection.
		// Select the first matching row after that reconciliation, so the detail
		// never stays empty alongside a nonempty filtered list.
		clamp(&s.index, len(rows))
		ui.Text(c, "Cursor 没有本地会话日志，暂不支持").FontSize(10).TextColor(t.TextMuted).Padding(8, 12).MaxLines(2)
	})
}

func (a *App) sessionDetailPane(c *ui.Context, d *sessionDetailData, width float32) {
	s := &a.sessions
	t := c.Theme()
	root := d.session
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(14, 16, 8, 16).Gap(7).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, recentLabel(root)).FontSize(16).FontWeight(600).Grow(1).MinWidth(0).SingleLine().Tooltip(recentLabel(root))
				if ui.Button(c, "打开日志").FontSize(11).Tooltip("在文件管理器中定位原始日志").Clicked() {
					a.reveal(root.Path)
				}
			})
			where := sessionAgentName(root.Agent)
			if root.CWD != "" {
				where += " · " + shortPath(a.home, root.CWD)
			}
			ui.Text(c, where).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(root.CWD)
			meta := sessionSpan(root)
			if root.GitBranch != "" {
				meta = root.GitBranch + " · " + meta
			}
			if !root.Start.IsZero() && !root.End.IsZero() {
				meta = root.Start.Local().Format("01/02 15:04") + "–" + root.End.Local().Format("15:04") + " · " + meta
			}
			number(c, meta).FontSize(11).TextColor(t.TextMuted)
			tabs := ui.TabsBase(c.Key("session-detail-tabs"), &s.tab, 2)
			tabs.List.Gap(6).Label("会话详情标签").Children(func() {
				for i, label := range []string{"概览", "轮次"} {
					tab := tabs.Tab(i).Padding(5, 12).Radius(5).Border(1, ui.Transparent).Label(label)
					if i == s.tab {
						tab.Border(1, t.Accent).Background(fade(t.Accent, 24))
					}
					tab.Children(func() { ui.Text(c, label).FontSize(12).FontWeight(600) })
				}
			})
			if tabs.List.Changed() {
				s.roundOpen = false
				c.Invalidate()
			}
		})
		ui.Scroll(c.Key("session-detail:" + s.selected + fmt.Sprint(s.tab, s.roundOpen))).Grow(1).MinHeight(0).Label("会话详情内容").Children(func() {
			ui.Column(c).Padding(8, 16, 20, 16).Gap(18).Children(func() {
				if root.UnsupportedReason != "" {
					ui.Text(c, root.UnsupportedReason).FontSize(12).TextColor(t.TextMuted)
					return
				}
				if root.Incomplete {
					ui.Text(c, "当前会话记录不完整").FontSize(11).TextColor(t.Warning)
				}
				if s.tab == 0 {
					a.sessionOverview(c, d, width)
				} else if s.roundOpen {
					a.sessionRound(c, d, width)
				} else {
					a.sessionTiming(c, d)
				}
			})
		})
	})
}

func (a *App) chooseSessionTurn(d *sessionDetailData, index int) {
	if index < 0 || index >= len(d.turns) {
		return
	}
	a.sessions.selectedTurn = d.turns[index].turn.ID
	a.sessions.roundOpen = true
	a.sessions.selectedStep = 0
}
