package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/session"
	"knack/internal/skill"
)

// The sidebar follows the Sidebar component of the design: a search field,
// then places grouped under quiet headings, then a status footer. Rows are
// 28 tall; the chosen one is a raised white row with an ink icon.
func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(token(t, "sidebar")).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		// The band keeps the traffic lights clear and carries the control
		// that hides the sidebar, at its trailing edge as in a source list.
		ui.Row(c).Height(controlsBandHeight(c.TitleBar())).Shrink(0).Padding(0, 6).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Box(c).Grow(1)
			a.sidebarToggle(c)
		})
		a.sidebarSearch(c)
		ui.Sidebar(c, &a.page, func() {
			ui.Column(c).Gap(2).Children(func() {
				if !a.hidden["overview"] {
					a.navItem(c, "overview", "home", "首页", "")
				}
				if !a.hidden["issues"] {
					count := ""
					if a.snap != nil && len(a.snap.Issues) > 0 {
						count = fmt.Sprint(len(a.snap.Issues))
					}
					a.navItem(c, "issues", "inbox", "待处理", "").Children(func() { alertBadge(c, count) })
				}
			})
			sideHeading(c, "技能")
			if !a.hidden[libraryPage] {
				count := ""
				if n := len(a.libraryEntries()); n > 0 {
					count = groupDigits(n)
				}
				a.navItem(c, libraryPage, "pool", "技能库", count)
			}
			a.navItem(c, effectivePage, "effective", "生效视图", "")
			if !a.hidden[graphPage] {
				a.navItem(c, graphPage, "graph", "依赖图", "")
			}
			if !a.hidden["sessions"] || !a.hidden[usagePage] || !a.hidden[insightsPage] {
				sideHeading(c, "会话")
			}
			if !a.hidden["sessions"] {
				count := ""
				if s := a.sessions.snapshot; s != nil && len(s.Sessions) > 0 {
					count = groupDigits(len(s.Sessions))
				}
				a.navItem(c, "sessions", "messages", "全部会话", count)
			}
			if !a.hidden[usagePage] {
				a.navItem(c, usagePage, "chart", "用量与耗时", "")
			}
			if !a.hidden[insightsPage] {
				a.navItem(c, insightsPage, "wave", "诊断", "")
			}
			if agents := a.sidebarAgents(); len(agents) > 0 {
				sideHeading(c, "AGENTS")
				for _, root := range agents {
					a.agentItem(c, root)
				}
			}
			sideHeading(c, "来源")
			if !a.hidden["pool"] {
				count := ""
				if a.snap != nil {
					count = fmt.Sprint(len(a.snap.EntriesFor("pool")))
				}
				a.navItem(c, "pool", "package", "共享池", count)
			}
			a.repoItems(c)
		}).Grow(1).Padding(10, 10, 12, 10).Background(ui.Transparent).Label("位置")
		a.sidebarFooter(c)
	})
}

// sidebarSearch filters the skills of the current list, or the sessions on
// the sessions page; from a page without a list it opens the library, whose
// list then offers the sessions the words find. ⌘K focuses it from anywhere.
func (a *App) sidebarSearch(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(0, 10).Shrink(0).Children(func() {
		field := ui.SearchField(c, &a.query).Grow(1).Height(30).Radius(7).Background(searchBackground(t)).
			Border(1, t.Border).Placeholder("搜索技能、会话、路径").PlaceholderColor(token(t, "ink-3")).Label("搜索技能")
		if c.Shortcut(ui.Cmd, ui.KeyK) {
			field.Focus()
		}
		if field.Changed() {
			switch {
			case a.page == "sessions":
				a.sessions.query, a.sessions.open, a.sessions.replayOpen = a.query, false, false
			case !a.listPage():
				a.page = libraryPage
			}
		}
		field.Children(func() {
			if a.query == "" {
				ui.Text(c, "⌘K").Font(monoFont).FontSize(11).TextColor(token(t, "ink-3")).Shrink(0)
			}
		})
	})
}

func searchBackground(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(255, 255, 255, 0.05)
	}
	return ui.RGBA(255, 255, 255, 0.7)
}

// listPage reports whether the page lists skills that the search filters.
func (a *App) listPage() bool {
	return a.browsePage() || a.page == "issues" || a.page == effectivePage || a.page == libraryPage
}

func sideHeading(c *ui.Context, title string) {
	ui.Text(c, title).Padding(14, 8, 4, 8).FontSize(11).FontWeight(600).LetterSpacing(0.3).
		TextColor(token(c.Theme(), "ink-3")).SingleLine()
}

// sideRow styles a SidebarItem as the design's row. Stock items fill the
// chosen row with the accent; the design raises it instead.
func (a *App) sideRow(c *ui.Context, id, label string) ui.Element {
	t := c.Theme()
	item := ui.SidebarItem(c.Key(id), id, nil, "").Label(label).Height(28).Padding(0, 8).Gap(9).TextColor(t.Text)
	if a.page == id {
		item.Background(token(t, "raised")).Border(1, t.Border)
	}
	return item
}

func (a *App) navItem(c *ui.Context, id, icon, label, count string) ui.Element {
	t := c.Theme()
	item := a.sideRow(c, id, label)
	item.Children(func() {
		ui.Row(c).Grow(1000).Margin(0, 0, 0, -9).Gap(9).AlignItems(ui.Center).Children(func() {
			glyphColor := token(t, "ink-2")
			if a.page == id {
				glyphColor = t.Text
			}
			ui.Icon(c, glyph(icon)).FontSize(16).TextColor(glyphColor)
			a.sideLabel(c, id, label)
			sideCount(c, count)
		})
	})
	return item
}

func (a *App) sideLabel(c *ui.Context, id, label string) {
	weight := 400
	if a.page == id {
		weight = 600
	}
	ui.Text(c, label).Grow(1).MinWidth(0).FontSize(13).FontWeight(weight).SingleLine()
}

func sideCount(c *ui.Context, count string) {
	if count != "" {
		ui.Text(c, count).Font(monoFont).FontSize(11.5).TextColor(token(c.Theme(), "ink-3")).Shrink(0).SingleLine()
	}
}

// alertBadge counts what waits for the person, in the danger color.
func alertBadge(c *ui.Context, count string) {
	if count == "" {
		return
	}
	t := c.Theme()
	ui.Box(c).Padding(1, 6).Radius(9).Background(t.Danger).Shrink(0).Children(func() {
		ui.Text(c, count).Font(monoFont).FontSize(11).FontWeight(600).TextColor(token(t, "on-ink")).SingleLine()
	})
}

// agentItem marks an Agent with a dot in its data color, so the sidebar
// matches the charts; a logo would add a second color system.
func (a *App) agentItem(c *ui.Context, r skill.Root) {
	id := "root:" + r.ID
	count := ""
	if a.snap != nil {
		if n := len(a.agentEntries(r)); n > 0 {
			count = fmt.Sprint(n)
		}
	}
	color := a.rootColor(c, r)
	a.sideRow(c, id, r.Name).Children(func() {
		ui.Row(c).Grow(1000).Margin(0, 0, 0, -9).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(color)
			})
			a.sideLabel(c, id, r.Name)
			sideCount(c, count)
		})
	})
}

// rootColor is an Agent's data color: the session palette's for Agents with
// logs or a design color, else the color it is known by.
func (a *App) rootColor(c *ui.Context, r skill.Root) ui.Color {
	key := agentKey(a.home, r.Path)
	if _, ok := sessionPalette["agent-"+key]; ok {
		return sessionColor("agent-"+key, c.Theme().Dark)
	}
	if _, ok := session.AgentByName(key); ok {
		return agentColor(session.Agent(key), c.Theme().Dark)
	}
	return lookOf(a.home, r).color
}

func (a *App) repoItems(c *ui.Context) {
	t := c.Theme()
	for _, r := range a.repos {
		path := r.Path
		id := "repo:" + path
		count := ""
		if a.snap != nil {
			if n := len(a.entriesFor(id)); n > 0 {
				count = fmt.Sprint(n)
			}
		}
		menu := func(m *ui.Menu) { a.repoMenu(m, path) }
		label := a.repoLabel(path)
		item := a.sideRow(c, id, label).Tooltip(path).ContextMenu(menu)
		item.Children(func() {
			ui.Row(c).Grow(1000).Margin(0, 0, 0, -9).Gap(9).AlignItems(ui.Center).Children(func() {
				glyphColor := token(t, "ink-2")
				if a.page == id {
					glyphColor = t.Text
				}
				ui.Icon(c, glyph("folder")).FontSize(16).TextColor(glyphColor)
				a.sideLabel(c, id, label)
				// The count's place holds the menu button, which shows its
				// icon on hover and keyboard focus.
				more := ui.ButtonBase(c).MinWidth(20).Height(16).Shrink(0).Justify(ui.End).AlignItems(ui.Center).
					Label("仓库操作：" + path).Tooltip("仓库操作").Menu(menu)
				more.Children(func() {
					if item.Hovered() || more.FocusVisible() {
						ui.Icon(c, glyph("more")).FontSize(16).TextColor(token(t, "ink-2"))
					} else {
						sideCount(c, count)
					}
				})
			})
		})
	}
	add := ui.ButtonBase(c).Height(28).Padding(0, 8).Gap(9).Radius(t.Radius).Justify(ui.Start).
		Label("添加仓库…").Disabled(a.mutating)
	if add.Hovered() {
		add.Background(t.SurfaceHover)
	}
	add.Children(func() {
		ui.Icon(c, glyph("plus")).FontSize(16).TextColor(token(t, "ink-2"))
		ui.Text(c, "添加仓库").FontSize(13).TextColor(token(t, "ink-2")).SingleLine()
	})
	if add.Clicked() {
		a.beginAddRepo()
		c.Invalidate()
	}
}

// sidebarFooter tells whether what shows is current, and holds the two
// global actions.
func (a *App) sidebarFooter(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(footerHeight).Shrink(0).Margin(0, 10).Padding(0, 4, 0, 8).Gap(8).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		busy := a.scanning || a.sessions.loading
		status, color := a.scanStatus(time.Now())
		if busy {
			ui.Spinner(c).Size(10, 10).Label("正在扫描")
		} else {
			ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(token(t, color))
		}
		ui.Text(c, status).Grow(1).MinWidth(0).FontSize(11.5).TextColor(token(t, "ink-2")).SingleLine()
		ui.Toolbar(c, func() {
			scan := iconButton(c, "refresh", "重新扫描", "重新扫描（⌘R）").Disabled(busy || a.mutating)
			if scan.Clicked() {
				a.startScan()
			}
			if iconButton(c, "gear", "设置", "设置（⌘,）").Clicked() {
				a.openSettings()
				c.Invalidate()
			}
		}).Label("全局操作").Padding(0).Gap(2).Shrink(0)
	})
}

// scanStatus is the footer's line and the token of its dot.
func (a *App) scanStatus(now time.Time) (string, string) {
	switch {
	case a.scanning || a.sessions.loading:
		return "正在扫描…", "ink-3"
	case a.mutating:
		return "正在处理…", "ink-3"
	case a.snap == nil:
		return "还没有扫描", "ink-3"
	case a.cached:
		return "缓存 · 正在等待校验", "warn"
	case len(a.snap.Warnings) > 0:
		return "已扫描 · 有 " + fmt.Sprint(len(a.snap.Warnings)) + " 条提示", "warn"
	}
	return "已扫描 · " + ago(now, a.snap.At), "ok"
}

func ago(now, at time.Time) string {
	d := now.Sub(at)
	switch {
	case at.IsZero():
		return "刚刚"
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d/time.Hour))
	}
	return at.Local().Format("01/02 15:04")
}

// iconButton is a quiet square button showing only a glyph.
func iconButton(c *ui.Context, icon, label, tip string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c).Size(24, 24).Radius(5).Center().Label(label).Tooltip(tip)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, glyph(icon)).FontSize(14).TextColor(token(t, "ink-3")) })
	return b
}

func groupDigits(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
