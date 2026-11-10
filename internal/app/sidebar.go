package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

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
				if agents := a.sidebarAgents(); len(agents) > 0 {
					a.navItem(c, agentsPage, "bot", "Agents", fmt.Sprint(len(agents)))
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
			if !a.hidden["sessions"] || !a.hidden[insightsPage] {
				sideHeading(c, "会话")
			}
			if !a.hidden["sessions"] {
				count := ""
				if s := a.sessions.snapshot; s != nil && len(s.Sessions) > 0 {
					count = groupDigits(len(s.Sessions))
				}
				a.navItem(c, "sessions", "messages", "全部会话", count)
			}
			if !a.hidden[insightsPage] {
				a.navItem(c, insightsPage, "wave", "诊断", "")
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
			if !a.hidden[storagePage] {
				sideHeading(c, "维护")
				a.navItem(c, storagePage, "disk", "磁盘清理", "")
			}
		}).Grow(1).Padding(10, 10, 12, 10).Background(ui.Transparent).Label("位置")
		a.sidebarFooter(c)
	})
}

// sidebarSearch filters the skills of the current list, or the sessions on
// the sessions page; from a page without a list it opens the library, whose
// list then offers the sessions the words find. ⌘K focuses it from anywhere.
func (a *App) sidebarSearch(c *ui.Context) {
	ui.Row(c).Padding(0, 10).Shrink(0).Children(func() {
		search := searchField(c, &a.query, "搜索技能", 0, field.SearchBarProps{Placeholder: "搜索", ShortKey: "mod+k", Variant: field.InputFilled})
		if search.Changed() {
			switch {
			case a.page == "sessions":
				a.sessions.query, a.sessions.open, a.sessions.replayOpen = a.query, false, false
			case !a.listPage():
				a.page = libraryPage
			}
		}
	})
}

// listPage reports whether the page lists skills that the search filters.
func (a *App) listPage() bool {
	return a.browsePage() || a.page == "issues" || a.page == effectivePage || a.page == libraryPage
}

func sideHeading(c *ui.Context, title string) {
	ui.Text(c, title).Padding(14, 8, 4, 8).FontSize(11.5).FontWeight(600).LetterSpacing(0.3).
		TextColor(token(c.Theme(), "ink-3")).SingleLine()
}

// sideRow styles a SidebarItem as the design's row. Stock items fill the
// chosen row with the solid accent; the design uses its pale wash instead.
func (a *App) sideRow(c *ui.Context, id, label string) ui.Element {
	t := c.Theme()
	item := ui.SidebarItem(c.Key(id), id, nil, "").Label(label).Height(28).Padding(0, 8).Gap(9).TextColor(t.Text)
	if a.activeNav(id) {
		item.Background(token(t, "selected"))
	}
	return item
}

func (a *App) navItem(c *ui.Context, id, icon, label, count string) ui.Element {
	t := c.Theme()
	item := a.sideRow(c, id, label)
	item.Children(func() {
		ui.Row(c).Grow(1000).Margin(0, 0, 0, -9).Gap(9).AlignItems(ui.Center).Children(func() {
			glyphColor := token(t, "ink-2")
			if a.activeNav(id) {
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
	if a.activeNav(id) {
		weight = 600
	}
	ui.Text(c, label).Grow(1).MinWidth(0).FontSize(13).FontWeight(weight).SingleLine()
}

func sideCount(c *ui.Context, count string) {
	if count != "" {
		ui.Text(c, count).FontFeatures("tnum").FontSize(12).TextColor(token(c.Theme(), "ink-3")).Shrink(0).SingleLine()
	}
}

// alertBadge counts what waits for the person, in the danger color.
func alertBadge(c *ui.Context, count string) {
	if count == "" {
		return
	}
	// The count pill of Lobe UI's badge.
	tk := lobe.TokensOf(c)
	ui.Row(c).Height(16).MinWidth(16).PaddingX(5).Radius(8).Center().Background(tk.Error).Shrink(0).Children(func() {
		ui.Text(c, count).FontFeatures("tnum").FontSize(11).FontWeight(500).TextColor(ui.RGB(255, 255, 255)).SingleLine().LineHeight(1)
	})
}

// agentItem marks an Agent with its own logo; one without a logo gets a dot
// in its color.
func (a *App) agentItem(c *ui.Context, r skill.Root) {
	id := "root:" + r.ID
	count := ""
	if a.snap != nil {
		if n := len(a.agentEntries(r)); n > 0 {
			count = fmt.Sprint(n)
		}
	}
	color := a.rootColor(c, r)
	logo := sideLogo(a.home, r)
	item := a.sideRow(c, id, r.Name)
	if a.page == id {
		// The sidebar's Agents entry already wears the selected wash.
		item.Background(c.Theme().SurfaceHover)
	}
	item.Children(func() {
		ui.Row(c).Grow(1000).Margin(0, 0, 0, -9).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(16, 16).Shrink(0).Center().Children(func() {
				if logo != nil {
					ui.Image(c, logo).Size(16, 16)
					return
				}
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
		build := func(m *menu) { a.repoMenu(m, path) }
		label := a.repoLabel(path)
		item := withTip(c, a.sideRow(c, id, label), path)
		contextMenu(c, item, build)
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
				a.dropdown(c, "repo-more:"+path, build, func(more ui.Element) {
					withTip(c, more.MinWidth(20).Height(16).Shrink(0).Justify(ui.End).AlignItems(ui.Center).
						Label("仓库操作："+path), "仓库操作")
					more.Children(func() {
						if item.Hovered() || more.FocusVisible() {
							ui.Icon(c, glyph("more")).FontSize(16).TextColor(token(t, "ink-2"))
						} else {
							sideCount(c, count)
						}
					})
				})
			})
		})
	}
	add := action.StyleButton(c, ui.ButtonBase(c), "", action.ButtonProps{Variant: action.Dashed, Size: lobe.Small}).
		Height(28).Gap(6).Label("添加仓库…").Disabled(a.mutating)
	add.Children(func() {
		ui.Icon(c, glyph("plus")).FontSize(14)
		ui.Text(c, "添加仓库").FontSize(13).SingleLine()
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
			feedback.Spin(c, feedback.SpinProps{CustomSize: 10}, nil).Label("正在扫描")
		} else {
			ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(token(t, color))
		}
		ui.Text(c, status).Grow(1).MinWidth(0).FontSize(12).TextColor(token(t, "ink-2")).SingleLine()
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

// iconButton is Lobe UI's action icon: a glyph that shows a fill under the
// pointer, named label, with tip as its tooltip.
func iconButton(c *ui.Context, icon, label, tip string) ui.Element {
	return styleIconButton(c, ui.ButtonBase(c), icon, label, tip)
}

// styleIconButton gives b, a button of any kind, iconButton's look.
func styleIconButton(c *ui.Context, b ui.Element, icon, label, tip string) ui.Element {
	return action.StyleActionIcon(c, b, glyph(icon), action.ActionIconProps{Title: tip, BlockSize: 24, GlyphSize: 15}).
		Label(label).Shrink(0)
}

func groupDigits(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
