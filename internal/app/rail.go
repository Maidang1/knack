package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/layout"
)

// Below railBelow the sidebar narrows to a rail of icons, as the design's
// narrow window; its search button brings the full sidebar back until the
// page changes.
const (
	railBelow = 900
	railWidth = 56
)

// updateRail decides, once a frame, whether the sidebar is a rail.
func (a *App) updateRail(c *ui.Context) {
	if a.page != a.railPage {
		a.railPage, a.railPeek = a.page, false
	}
	width, _ := c.Size()
	below := float32(railBelow)
	if _, ok := a.agentPageRoot(); ok {
		below = agentRailBelow
	}
	a.rail = !a.sideCollapsed && !a.railPeek && width < below
	a.agentListW = agentListWide
	if width < railBelow {
		a.agentListW = agentListNarrow
	}
}

func (a *App) railSidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(railWidth).Shrink(0).AlignSelf(ui.Stretch).Background(token(t, "sidebar")).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).
		AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(controlsBandHeight(c.TitleBar())).Shrink(0).AlignSelf(ui.Stretch).DragWindow()
		search := railButton(c, "rail-search", "search", "搜索技能", "搜索技能（⌘K）", false)
		if search.Clicked() || c.Shortcut(ui.Cmd, ui.KeyK) {
			a.railPeek = true
			c.Invalidate()
		}
		ui.Sidebar(c, &a.page, func() {
			ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if !a.hidden["overview"] {
					a.railItem(c, "overview", "home", "首页", ui.Color{})
				}
				if !a.hidden["issues"] {
					dot := ui.Color{}
					if a.snap != nil && len(a.snap.Issues) > 0 {
						dot = t.Danger
					}
					a.railItem(c, "issues", "inbox", "待处理", dot)
				}
				if len(a.sidebarAgents()) > 0 {
					a.railItem(c, agentsPage, "bot", "Agents", ui.Color{})
				}
				railDivider(c)
				if !a.hidden[libraryPage] {
					a.railItem(c, libraryPage, "pool", "技能库", ui.Color{})
				}
				a.railItem(c, effectivePage, "effective", "生效视图", ui.Color{})
				if !a.hidden[graphPage] {
					a.railItem(c, graphPage, "graph", "依赖图", ui.Color{})
				}
				if !a.hidden["sessions"] {
					a.railItem(c, "sessions", "messages", "全部会话", ui.Color{})
					a.railItem(c, usagePage, "chart", "用量", ui.Color{})
				}
				if !a.hidden[insightsPage] {
					a.railItem(c, insightsPage, "wave", "诊断", ui.Color{})
				}
				if !a.hidden[storagePage] {
					a.railItem(c, storagePage, "disk", "磁盘清理", ui.Color{})
				}
				railDivider(c)
				if !a.hidden["pool"] {
					a.railItem(c, "pool", "package", "共享池", ui.Color{})
				}
				for _, r := range a.repos {
					path := r.Path
					item := withTip(c, a.railItem(c, "repo:"+path, "folder", a.repoLabel(path), ui.Color{}), path)
					contextMenu(c, item, func(m *menu) { a.repoMenu(m, path) })
				}
				if railButton(c, "rail-add", "plus", "添加仓库…", "添加仓库", a.mutating).Clicked() {
					a.beginAddRepo()
					c.Invalidate()
				}
			})
		}).Grow(1).AlignSelf(ui.Stretch).Padding(6, 0).Background(ui.Transparent).Label("位置")
		ui.Column(c).Shrink(0).Gap(2).Padding(6, 0, 10, 0).Children(func() {
			busy := a.scanning || a.sessions.loading
			if railButton(c, "rail-scan", "refresh", "重新扫描", "重新扫描（⌘R）", busy || a.mutating).Clicked() {
				a.startScan()
			}
			if railButton(c, "rail-gear", "gear", "设置", "设置（⌘,）", false).Clicked() {
				a.openSettings()
				c.Invalidate()
			}
		})
	})
}

func railButton(c *ui.Context, key, icon, label, tip string, disabled bool) ui.Element {
	return action.ActionIcon(c.Key(key), glyph(icon), action.ActionIconProps{Title: tip, BlockSize: 32, GlyphSize: 17}).
		Width(36).Label(label).Disabled(disabled).Shrink(0)
}

func railDivider(c *ui.Context) {
	layout.Divider(c, layout.DividerProps{}).AlignSelf(ui.Center).Width(20).Margin(6, 0)
}

// railItem is a page as an icon; dot, when set, marks it as needing
// attention.
func (a *App) railItem(c *ui.Context, id, icon, label string, dot ui.Color) ui.Element {
	t := c.Theme()
	item := a.railRow(c, id, label)
	railContent(c, item, func() {
		color := token(t, "ink-2")
		if a.activeNav(id) {
			color = t.Text
		}
		ui.Icon(c, glyph(icon)).FontSize(17).TextColor(color)
	})
	if dot.A > 0 {
		item.Draw(func(p *ui.Painter, r ui.Rect) {
			p.Fill(ui.Rect{X: r.X + r.W/2 + 5, Y: r.Y + 6, W: 7, H: 7}, dot, 3.5)
		})
	}
	return item
}

func (a *App) railRow(c *ui.Context, id, label string) ui.Element {
	t := c.Theme()
	item := withTip(c, ui.SidebarItem(c.Key("rail:"+id), id, nil, "").Label(label), label).Size(36, 32).Padding(0).Gap(0).
		Radius(7).AlignItems(ui.Center).Justify(ui.Center)
	if a.activeNav(id) {
		item.Background(token(t, "selected"))
	}
	return item
}

// railContent centers a rail item's mark. A SidebarItem leads with its own
// growing (here empty) label, which the far greater grow outweighs.
func railContent(c *ui.Context, item ui.Element, mark func()) {
	item.Children(func() {
		ui.Row(c).Grow(1000).Justify(ui.Center).AlignItems(ui.Center).Children(mark)
	})
}
