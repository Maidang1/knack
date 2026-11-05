package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/manage"
	"knack/internal/repair"
	"knack/internal/skill"
)

type issueFilter struct {
	id, label string
}

var issueFilters = []issueFilter{
	{id: "", label: "全部"},
	{id: string(skill.IssueBroken), label: "断链"},
	{id: string(skill.IssueStale), label: "不一致"},
	{id: string(skill.IssueDuplicate), label: "一致副本"},
	{id: string(skill.IssueEmpty), label: "空目录"},
	{id: string(skill.IssueOrphan), label: "未挂出"},
}

type row struct {
	key      string
	name     string
	badge    string
	tone     string
	glyph    string
	subtitle string
	entry    skill.Entry
	kind     skill.IssueKind
}

// View draws the window: a source list over the sidebar material, and an
// opaque content pane with the toolbar in the title-bar band.
func (a *App) View(c *ui.Context) {
	if a.completed != nil {
		result := a.completed
		a.completed = nil
		a.finish(c, result.done, result.records, result.err)
	}
	if c.Shortcut(ui.Cmd, ui.KeyR) && !a.scanning {
		a.startScan()
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.openSettings()
	}
	if a.notice != "" {
		c.Toast(a.notice)
		a.notice = ""
	}
	if a.frosted(c) {
		c.Root().Background(ui.Transparent)
	}
	// The themed wrapper paints nothing itself: the sidebar keeps the
	// window's vibrancy, the content pane draws its own background.
	ui.Themed(c, appTheme(c.Theme())).Fill().Background(ui.Transparent).Children(func() {
		if a.sideCollapsed {
			a.content(c)
		} else {
			ui.Split(c, &a.sideW, func() { a.sidebar(c) }, func() { a.content(c) }).Fill()
		}
		a.dialogs(c)
		a.settingsDialog(c)
		a.repositoryDialog(c)
	})
}

// footerHeight is shared by the status bar and the sidebar's footer.
const footerHeight = 32

const toolbarControlHeight = 28

// bandHeight is the title-bar band. A hidden inset title bar reports its
// own height; headless tests have none, and 44 matches a compact toolbar.
func bandHeight(bar ui.TitleBar) float32 {
	if bar.Height > 0 {
		return bar.Height
	}
	return 44
}

func (a *App) contentBar(c *ui.Context) {
	t := c.Theme()
	bar := c.TitleBar()
	padEnd := bar.Right
	if padEnd < 12 {
		padEnd = 12
	}
	// With the sidebar collapsed the content starts at the window's edge,
	// so the title bar's controls set the leading padding.
	padStart := float32(16)
	if a.sideCollapsed && bar.Left > padStart {
		padStart = bar.Left
	}
	// The whole band is draggable, including its padding and gaps. Child
	// controls take their own presses before this background drag handle.
	ui.Row(c).Height(controlsBandHeight(bar)).Shrink(0).Padding(0, padEnd, 0, padStart).Gap(8).AlignItems(ui.Center).DragWindow().
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if a.sideCollapsed {
			a.sidebarToggle(c)
		}
		title := ui.Text(c, a.pageTitle()).FontSize(13).FontWeight(600).Shrink(0).SingleLine()
		if strings.HasPrefix(a.page, "repo:") {
			title.Shrink(1).MinWidth(0)
		}
		if a.page == effectivePage {
			ui.Toolbar(c, func() { a.effectiveToolbar(c) }).Label("生效范围").Padding(0).Gap(4).Margin(0, 0, 0, 8).MinWidth(0)
		}
		ui.Box(c).Grow(1).MinWidth(8)
	})
}

// viewTabs puts navigation beside the content it changes, without a
// toolbar face. TabsBase keeps arrow-key navigation and selected states.
func viewTabs(c *ui.Context, selected *int, labels ...string) {
	t := c.Theme()
	tabs := ui.TabsBase(c, selected, len(labels))
	tabs.List.Gap(16).Label("视图").Children(func() {
		for i, label := range labels {
			on := i == *selected
			tab := tabs.Tab(i).Height(24).AlignItems(ui.Center).Radius(3)
			color := t.TextMuted
			if on || tab.Hovered() {
				color = t.Text
			}
			tab.Children(func() { ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(color).SingleLine() })
			if on {
				tab.DrawOver(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 2, W: r.W, H: 2}, t.Text, 1)
				})
			}
		}
	})
	if tabs.List.Changed() {
		c.Invalidate()
	}
}

func (a *App) browseTabs(c *ui.Context) {
	if a.browsePage() {
		ui.Row(c).Padding(6, 16, 4, 16).Children(func() {
			viewTabs(c, &a.state(a.page).view, "列表", "依赖图")
		})
	}
}

func (a *App) listSearch(c *ui.Context) {
	placeholder := "搜索名称、说明或路径"
	if a.page == effectivePage {
		placeholder = "搜索生效 skill"
		if a.eff.matrix == 1 {
			placeholder = "搜索 skill"
		}
	}
	ui.Row(c).Padding(0, 16, 8, 16).Children(func() {
		ui.SearchField(c, &a.query).Grow(1).MinWidth(0).Height(toolbarControlHeight).
			Border(0, ui.Transparent).Background(c.Theme().SurfaceHover).Label("搜索").Placeholder(placeholder)
	})
}

// Global actions have one implementation, in the sidebar footer or the
// status bar when the sidebar is hidden.
func (a *App) footerActions(c *ui.Context) {
	ui.Toolbar(c, func() {
		scan := ui.Button(c, "").Height(toolbarControlHeight).Padding(0, 8).
			Disabled(a.scanning || a.mutating).Label("重新扫描").Tooltip("重新扫描（⌘R）")
		scan.Children(func() {
			if a.scanning {
				ui.Spinner(c).Size(14, 14).Label("正在扫描")
			} else {
				ui.Icon(c, glyph("refresh")).FontSize(14)
			}
			ui.Text(c, "重新扫描").FontSize(12).SingleLine()
		})
		if scan.Clicked() {
			a.startScan()
		}
		ui.Spacer(c)
		pick := ui.Button(c, "").Size(toolbarControlHeight, toolbarControlHeight).Padding(0).
			Label("设置").Tooltip("设置（⌘,）")
		pick.Children(func() { ui.Icon(c, glyph("gear")) })
		if pick.Clicked() {
			a.openSettings()
			c.Invalidate()
		}
	}).Label("全局操作").Padding(0).Gap(8).Grow(1)
}

// sidebarToggle shows or hides the sidebar, leaving the content the whole
// window when it is hidden.
func (a *App) sidebarToggle(c *ui.Context) {
	label := "收起侧边栏"
	if a.sideCollapsed {
		label = "展开侧边栏"
	}
	ui.Toolbar(c, func() {
		b := ui.Button(c, "").Label(label)
		b.Children(func() { ui.Icon(c, glyph("sidebar")).FontSize(18) })
		if b.Clicked() {
			a.sideCollapsed = !a.sideCollapsed
			c.Invalidate()
		}
	}).Label("侧边栏").Shrink(0)
}

// sidebarWidth is the room the sidebar takes: none while it is collapsed.
func (a *App) sidebarWidth() float32 {
	if a.sideCollapsed {
		return 0
	}
	return a.sideW
}

// barStatus explains in the status bar why the page may not be live:
// a change is being applied, or what shows is the startup cache.
func (a *App) barStatus(c *ui.Context) {
	t := c.Theme()
	switch {
	case a.mutating:
		ui.Spinner(c).Label("正在处理")
		ui.Text(c, "正在处理…").FontSize(12).TextColor(t.TextMuted).Shrink(0)
	case a.cached:
		ui.Text(c, "缓存").FontSize(12).TextColor(t.TextMuted).Shrink(0)
	}
}

func (a *App) warnings(c *ui.Context) {
	if a.snap == nil || len(a.snap.Warnings) == 0 {
		return
	}
	t := c.Theme()
	n := min(len(a.snap.Warnings), 3)
	for _, w := range a.snap.Warnings[:n] {
		ui.Text(c, w).Padding(6, 16, 0, 16).FontSize(12).TextColor(t.Warning).SingleLine()
	}
	if extra := len(a.snap.Warnings) - n; extra > 0 {
		ui.Textf(c, "还有 %d 条提示", extra).Padding(2, 16, 0, 16).FontSize(12).TextColor(t.TextMuted)
	}
}

// controlsBandHeight keeps every toolbar control on the traffic lights'
// line, without carrying the inset macOS title bar's extra bottom room
// into the gap before the content.
func controlsBandHeight(bar ui.TitleBar) float32 {
	h := bandHeight(bar)
	if bar.Left > 0 {
		h = max(h-16, 0)
	}
	return h
}

// SidebarSection sizes its header from the system font and disclosure arrow.
// Align that header's center with the 24-point tabs, including CJK fallbacks.
func (a *App) sidebarTopInset(c *ui.Context) float32 {
	t := c.Theme()
	title := "浏览"
	collapsible := false
	if a.hidden["issues"] && a.hidden["pool"] {
		collapsible = true
		switch {
		case len(a.roots(skill.RoleAgent)) > 0:
			title = "Agent"
		case len(a.roots(skill.RoleBuiltin)) > 0:
			title = "自带"
		default:
			return 6 // The custom repository header has a fixed 24-point height.
		}
	}
	_, height := c.MeasureText(0, ui.Span{Text: title, Size: t.FontSize - 1, Weight: 600})
	if collapsible {
		height = max(height, t.Space(4))
	}
	return max(0, 6+12-t.Space(1)-height/2)
}

func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	frosted := a.frosted(c)
	col := ui.Column(c).Fill()
	if !frosted {
		col.Background(t.Surface)
	}
	col.Children(func() {
		// The band keeps the traffic lights clear and carries the control
		// that hides the sidebar, at its trailing edge as in a source list.
		ui.Row(c).Height(controlsBandHeight(c.TitleBar())).Shrink(0).Padding(0, 6).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Box(c).Grow(1)
			a.sidebarToggle(c)
		})
		side := ui.Sidebar(c, &a.page, func() {
			if !a.hidden["issues"] || !a.hidden["pool"] {
				ui.SidebarSection(c, "浏览", nil, func() {
					if !a.hidden["issues"] {
						issues := ui.SidebarItem(c, "issues", glyph("issues"), "问题")
						if a.snap != nil && len(a.snap.Issues) > 0 {
							issues.Children(func() { ui.Badge(c, fmt.Sprint(len(a.snap.Issues))) })
						}
					}
					if !a.hidden["pool"] {
						pool := ui.SidebarItem(c, "pool", glyph("pool"), "共享池")
						if a.snap != nil {
							if n := len(a.snap.EntriesFor("pool")); n > 0 {
								pool.Children(func() { ui.Badge(c, fmt.Sprint(n)) })
							}
						}
					}
				})
			}
			if agents := a.roots(skill.RoleAgent); len(agents) > 0 {
				ui.SidebarSection(c, "Agent", &a.agentsOpen, func() {
					ui.SidebarItem(c, effectivePage, glyph("effective"), "生效视图")
					for _, r := range agents {
						a.sideRoot(c, r)
					}
				})
			}
			if builtins := a.roots(skill.RoleBuiltin); len(builtins) > 0 {
				ui.SidebarSection(c, "自带", &a.builtinsOpen, func() {
					for _, r := range builtins {
						a.sideRoot(c, r)
					}
				})
			}
			a.repoSection(c)
		}).Grow(1).Padding(a.sidebarTopInset(c), t.Space(2.5), t.Space(2.5), t.Space(2.5)).Label("位置")
		if frosted {
			side.Background(ui.Transparent)
		}
		// The footer is as tall as the status bar beside it, so the rule
		// above both runs straight across the window.
		ui.Row(c).Height(footerHeight).Shrink(0).Padding(0, 6).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			a.footerActions(c)
		})
	})
}

func (a *App) sideRoot(c *ui.Context, r skill.Root) {
	id := "root:" + r.ID
	badge := 0
	if a.snap != nil {
		badge = a.snap.IssueCount(r.ID)
	}
	logo, plain := sideLogo(a.home, r)
	if logo == nil {
		item := ui.SidebarItem(c.Key(id), id, glyph(lookOf(a.home, r).glyph), r.Name)
		if badge > 0 {
			item.Children(func() { ui.Badge(c, fmt.Sprint(badge)) })
		}
		return
	}
	// A sidebar icon is tinted by the theme, so a logo in its own colors is
	// drawn as the item's content instead.
	ui.SidebarItem(c.Key(id), id, nil, "").Label(r.Name).Children(func() {
		ui.Row(c).Grow(1000).Margin(0, 0, 0, -8).AlignItems(ui.Center).Gap(8).Children(func() {
			img := ui.Image(c, logo).Size(16, 16).Shrink(0)
			if plain {
				img.TextColor(c.Theme().Text)
			}
			ui.Text(c, r.Name).Grow(1).SingleLine()
			if badge > 0 {
				ui.Badge(c, fmt.Sprint(badge))
			}
		})
	})
}

func (a *App) roots(role skill.Role) []skill.Root {
	if a.snap == nil {
		return nil
	}
	var out []skill.Root
	for _, r := range a.snap.Roots {
		if r.Role == role && !(role != skill.RolePool && a.hidden[r.ID]) {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) content(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.contentBar(c)
		if a.loadErr != "" {
			ui.Text(c, a.loadErr).Padding(6, 16, 0, 16).TextColor(t.Danger)
		}
		a.warnings(c)
		if a.allClear(c) {
			a.statusBar(c)
			return
		}
		if a.page == effectivePage {
			a.effectivePane(c)
			a.statusBar(c)
			return
		}
		split := &a.listW
		if a.inGraph() {
			split = &a.graphW
			width, _ := c.Size()
			a.graphW = min(a.graphW, max(280, width-a.sidebarWidth()-280-2))
		} else {
			// Leave room for management controls in a narrow window.
			width, _ := c.Size()
			a.listW = min(a.listW, max(180, width-a.sidebarWidth()-320-2))
		}
		ui.Column(c).Width(*split).Shrink(0).Children(func() { a.browseTabs(c) })
		ui.Split(c, split, func() {
			if a.inGraph() {
				a.graphPane(c)
				return
			}
			a.listPane(c)
		}, func() {
			if a.inGraph() {
				a.graphDetail(c)
				return
			}
			a.detailPane(c)
		}).Grow(1)
		a.statusBar(c)
	})
}

func (a *App) countKind(kind skill.IssueKind) int {
	if a.snap == nil {
		return 0
	}
	n := 0
	for _, issue := range a.snap.Issues {
		if issue.Kind == kind {
			n++
		}
	}
	return n
}

func (a *App) listPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		if sub := a.pageSubtitle(); sub != "" {
			ui.Text(c, sub).Padding(10, 16, 6, 16).FontSize(12).TextColor(t.TextMuted).MaxLines(2)
		}
		if a.page == "issues" {
			labels := make([]string, len(issueFilters))
			for i, f := range issueFilters {
				labels[i] = f.label
			}
			filters := ui.Segmented(c, &a.filter, labels...).Margin(0, 16, 8, 16).Wrap().Label("问题类别")
			if filters.Changed() {
				c.Invalidate()
			}
		}
		a.listSearch(c)
		rows := a.rows()
		ps := a.state(a.page)
		clamp(&ps.sel, len(rows))
		if a.page == "issues" {
			a.syncIssueSelection(rows)
			a.repairBar(c)
			a.issueSelectionBar(c, rows)
		}
		if path, ok := strings.CutPrefix(a.page, "repo:"); ok {
			if ui.Button(c, "不再扫描").Margin(8, 16, 4, 16).AlignSelf(ui.Start).Disabled(a.scanning || a.mutating).Clicked() {
				a.confirmRepoRemoval(path)
			}
		}
		ps.list.Key = func(i int) any {
			if i < 0 || i >= len(rows) {
				return i
			}
			return rows[i].key
		}
		ps.list.Label = func(i int) string {
			if i < 0 || i >= len(rows) {
				return ""
			}
			return rows[i].name
		}
		ps.list.Selected = &ps.sel
		if a.page == "issues" {
			ps.list.Selection = &ps.chosen
		}
		list := ui.List(c, &ps.list, len(rows), func(i int) {
			selected := i == ps.sel
			if a.page == "issues" {
				selected = ps.chosen.Has(rows[i].key)
			}
			a.paintRow(c, rows[i], selected)
		}).Grow(1).Padding(4, 8).Gap(2).Children(func() {
			if len(rows) == 0 {
				ui.Column(c).Padding(40, 12).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph("empty")).FontSize(26).TextColor(t.TextMuted)
					ui.Text(c, a.emptyText()).FontSize(12).TextColor(t.TextMuted)
				})
			}
		})
		if list.Changed() {
			c.Invalidate()
		}
	})
}

func (a *App) paintRow(c *ui.Context, r row, selected bool) {
	t := c.Theme()
	ui.Row(c).Padding(7, 10).Gap(10).Children(func() {
		if a.page == "issues" {
			ps := a.state(a.page)
			checked := ps.chosen.Has(r.key)
			if ui.Checkbox(c, &checked, "").Label("选择 " + r.name).Shrink(0).Changed() {
				if checked {
					ps.chosen.Add(r.key)
				} else {
					ps.chosen.Remove(r.key)
				}
				c.Invalidate()
			}
		}
		if r.glyph != "" {
			icon := ui.Icon(c, glyph(r.glyph)).FontSize(15).Shrink(0).Margin(2, 0, 0, 0)
			if !selected {
				icon.TextColor(toneColor(t, r.tone))
			}
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.name).Grow(1).SingleLine()
				if r.badge != "" {
					if selected {
						ui.Text(c, r.badge).FontSize(11).Shrink(0)
					} else {
						pill(c, r.badge, toneColor(t, r.tone))
					}
				}
			})
			if r.subtitle != "" {
				sub := ui.Text(c, r.subtitle).FontSize(12).SingleLine()
				if !selected {
					sub.TextColor(t.TextMuted)
				}
			}
		})
	})
}

func (a *App) detailPane(c *ui.Context) {
	t := c.Theme()
	rows := a.rows()
	ps := a.state(a.page)
	clamp(&ps.sel, len(rows))
	ui.Scroll(c).Fill().Children(func() {
		if a.page == "issues" {
			if entries := a.chosenIssues(); len(entries) > 1 {
				a.issueBatchDetail(c, entries)
				return
			} else if len(entries) == 1 {
				for _, r := range rows {
					if r.entry.Path == entries[0].Path {
						a.detail(c, r)
						return
					}
				}
			}
		}
		sel, ok := selected(rows, ps.sel)
		if !ok {
			ui.Column(c).Padding(48, 24).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, glyph("nested")).FontSize(28).TextColor(t.TextMuted)
				ui.Text(c, "在左边选择一个 skill。").FontSize(12).TextColor(t.TextMuted)
			})
			return
		}
		a.detail(c, sel)
	})
}

func (a *App) detail(c *ui.Context, r row) {
	t := c.Theme()
	e := r.entry
	ui.Column(c).Padding(24, 28).Gap(14).MaxWidth(680).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			tile(c, lookOf(a.home, skill.Root{ID: e.RootID, Name: e.RootName, Path: e.RootPath, Role: e.RootRole}), 28)
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, e.Name).FontSize(17).FontWeight(600).SingleLine()
				if r.badge != "" {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						if r.glyph != "" {
							ui.Icon(c, glyph(r.glyph)).FontSize(12).TextColor(toneColor(t, r.tone)).Shrink(0)
						}
						pill(c, r.badge, toneColor(t, r.tone))
					})
				}
			})
		})
		a.actions(c, e)
		if e.Description != "" {
			card(c).Padding(12, 14).Children(func() {
				ui.Text(c, e.Description).FontSize(13).LineHeight(1.5).MaxLines(12)
			})
		}
		a.invocationSettings(c, e)
		a.infoCard(c, r)
		a.documentPane(c, e)
		if a.browsePage() && !a.inGraph() && a.hasDeps(e) {
			if ui.Button(c, "在依赖图中查看").AlignSelf(ui.Start).Clicked() {
				a.showGraph(e.Name)
				c.Invalidate()
			}
		}
	})
}

// detailLine is one line of the info card, in the color it calls for.
type detailLine struct {
	s   string
	col ui.Color
}

// infoCard is the panel of facts about an entry: where it is, what it
// points at, what it relates to, what it contains.
func (a *App) infoCard(c *ui.Context, r row) {
	t := c.Theme()
	e := r.entry
	var lines []detailLine
	add := func(s string, col ui.Color) {
		if s != "" {
			lines = append(lines, detailLine{s, col})
		}
	}
	add(e.RootName+" · "+shortPath(a.home, e.Path), t.TextMuted)
	if e.LinkTarget != "" {
		add("指向 "+e.LinkTarget, t.TextMuted)
	}
	if a.page == "pool" {
		add(a.linkedText(e), t.TextMuted)
	}
	if e.FrontName != "" && e.FrontName != e.Name {
		add("frontmatter 名称 "+e.FrontName, t.TextMuted)
	}
	if len(e.Children) > 0 {
		add("包含 "+strings.Join(e.Children, "、"), t.TextMuted)
	}
	for _, l := range a.depTextLines(c, e) {
		add(l.s, l.col)
	}
	if e.TreeHash != "" {
		add("内容 "+skill.ShortHash(e.TreeHash), t.TextMuted)
	}
	if e.MetaErr != "" {
		add(e.MetaErr, t.Danger)
	}
	if e.HashErr != "" {
		add(e.HashErr, t.Danger)
	}
	if len(lines) == 0 {
		return
	}
	box := card(c)
	box.Dividers(1, t.Border)
	box.Children(func() {
		for _, l := range lines {
			ui.Text(c, l.s).Padding(8, 14).FontSize(12).TextColor(l.col).Selectable()
		}
	})
}

// linkedText says which agents have a symlink to a pool entry.
func (a *App) linkedText(e skill.Entry) string {
	names := a.snap.Linked[e.Name]
	if len(names) == 0 {
		return "没有 agent 软链到这里"
	}
	return "软链来自 " + strings.Join(names, "、")
}

// hasDeps reports the entry has dependencies worth drawing in the graph.
func (a *App) hasDeps(e skill.Entry) bool {
	if a.snap == nil {
		return false
	}
	g := a.snap.GraphFor(e.RootID)
	return len(g.Needs(e.Name)) > 0 || len(g.NeededBy(e.Name)) > 0 || len(e.Children) > 0
}

func (a *App) actions(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	readOnly := e.System || e.RootRole == skill.RoleBuiltin
	if readOnly {
		ui.Text(c, "自带 skill 只展示，这里不会修改它。").FontSize(12).TextColor(t.TextMuted)
	}
	ui.Row(c).Gap(8).Wrap().AlignSelf(ui.Start).AlignItems(ui.Center).Children(func() {
		if !readOnly {
			for i, f := range a.fixesFor(e) {
				a.fixButton(c, f, i == 0)
			}
			if e.RootRole == skill.RolePool && e.Usable() && !a.cached {
				a.linkAgentMenu(c, e)
			}
			a.removalButton(c, e)
		}
		ui.PopoverMenuButton(c, "更多", func(m *ui.Menu) {
			if m.Item("在访达中显示").Chosen() {
				a.reveal(e.Path)
			}
			if m.Item("复制路径").Chosen() {
				a.copyPath(e.Path)
				c.Toast("已复制路径")
			}
		}).Label("更多")
	})
	a.compareButton(c, e)
	a.actionNotes(c, e)
	a.removalNote(c, e)
}

// actionNotes says why an entry has no fix when the user has to decide.
func (a *App) actionNotes(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	note := func(s string) { ui.Text(c, s).FontSize(12).TextColor(t.TextMuted) }
	switch e.RootRole {
	case skill.RoleAgent:
		switch {
		case e.Relation == skill.RelLocal:
			note("这是这个 agent 独有的，共享池里没有同名的一份。")
		case e.Relation == skill.RelEmpty && !e.Vacant:
			note("目录里还有文件，没有 SKILL.md。不会自动移走。")
		}
	case skill.RolePool:
		if e.Relation == skill.RelEmpty && !e.Vacant {
			note("目录里还有文件，没有 SKILL.md。不会自动移走。")
		}
	case skill.RoleRepo:
		if pool, has := a.snap.PoolEntry(e.Name); has && pool.Kind != skill.KindBroken {
			if pool.Usable() {
				note("共享池里已经有可用的这一份。")
			} else {
				note("共享池里这个名字已经被占着。")
			}
		}
	}
}

// linkAgentMenu lets the user show a pool skill in an agent that has no
// entry of that name yet.
func (a *App) linkAgentMenu(c *ui.Context, pool skill.Entry) {
	var free []skill.Root
	for _, r := range a.roots(skill.RoleAgent) {
		taken := false
		for _, e := range a.snap.EntriesFor(r.ID) {
			if e.Name == pool.Name {
				taken = true
				break
			}
		}
		if !taken {
			free = append(free, r)
		}
	}
	if len(free) == 0 {
		return
	}
	menu := ui.MenuButton(c, "挂到 Agent", func(m *ui.Menu) {
		for _, r := range free {
			if m.Item(r.Name).Chosen() {
				a.confirmFix(repair.LinkToAgent(pool, r))
			}
		}
	})
	menu.AlignSelf(ui.Start).Disabled(a.scanning || a.mutating)
}

// fixButton is one fix. The first on a page is the default button, filled;
// a window has only one.
func (a *App) fixButton(c *ui.Context, f repair.Fix, primary bool) {
	key := c.Key(f.Label + "|" + f.Title)
	var b ui.Element
	if primary {
		b = ui.PrimaryButton(key, f.Label)
	} else {
		b = ui.Button(key, f.Label)
	}
	if b.AlignSelf(ui.Start).Disabled(a.scanning || a.mutating).Clicked() {
		a.confirmFix(f)
	}
}

func (a *App) confirmFix(f repair.Fix) {
	a.confirm(f.Title, f.Message, f.Confirm, f.Done, func() ([]manage.Record, error) {
		rec, err := repair.Apply(f, a.undoRoot)
		if err != nil {
			return nil, err
		}
		return []manage.Record{rec}, nil
	})
}

// issueScope is the id of the issue filter in effect, "" for all.
func (a *App) issueScope() string {
	if a.filter < 0 || a.filter >= len(issueFilters) {
		return ""
	}
	return issueFilters[a.filter].id
}

// plan lists the fixes for issues of kind, or of every kind when it is "".
func (a *App) plan(kind string) []repair.Fix {
	var chosen map[string]bool
	if a.page == "issues" && a.state("issues").chosen.Len() > 0 {
		chosen = make(map[string]bool)
		for _, e := range a.chosenIssues() {
			chosen[e.Path] = true
		}
	}
	var out []repair.Fix
	for _, f := range a.repairPlan() {
		if (kind == "" || string(f.Issue) == kind) && (chosen == nil || chosen[f.Entry.Path]) {
			out = append(out, f)
		}
	}
	return out
}

func (a *App) confirmBatch(safe []repair.Fix, label string) {
	n := len(safe)
	a.confirm(
		fmt.Sprintf("%s %d 项？", label, n),
		"将执行："+strings.Join(repair.Summary(safe), "；")+"。原来的内容都进撤销记录，可以一次撤销。",
		"确认修复",
		fmt.Sprintf("已修复 %d 项", n),
		func() ([]manage.Record, error) { return repair.ApplyAll(safe, a.undoRoot) },
	)
}

func (a *App) confirmReplace(review []repair.Fix) {
	n := len(review)
	a.confirm(
		fmt.Sprintf("替换 %d 个不一致的副本？", n),
		"这些副本和共享池同名，但目录内容不一致。改成软链后，现在这些份会进撤销记录，这里跟着共享池。",
		"确认替换",
		fmt.Sprintf("已替换 %d 个副本", n),
		func() ([]manage.Record, error) { return repair.ApplyAll(review, a.undoRoot) },
	)
}

// repairBar is the fix for the issues listed below it: the ones that lose
// nothing in one click, the ones that replace a different copy on their own.
func (a *App) repairBar(c *ui.Context) {
	if a.snap == nil || len(a.snap.Issues) == 0 {
		return
	}
	t := c.Theme()
	kind := a.issueScope()
	safe, review := repair.Split(a.plan(kind))
	card(c).Margin(10, 16, 6, 16).Padding(12, 14).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			tintedTile(c, "wand", t.Accent, 30)
			msg := "这些问题需要你决定如何处理，可在列表中选择多项。"
			switch {
			case len(safe) > 0 && len(review) > 0:
				msg = fmt.Sprintf("%d 项可以直接修复，%d 个不一致的副本要你确认", len(safe), len(review))
			case len(safe) > 0:
				msg = fmt.Sprintf("%d 项可以直接修复，每一步都能撤销", len(safe))
			case len(review) > 0:
				msg = fmt.Sprintf("%d 个不一致的副本要你确认后替换", len(review))
			}
			ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted).Grow(1)
		})
		if len(safe) == 0 && len(review) == 0 {
			return
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if len(safe) > 0 {
				label := fmt.Sprintf("一键修复 %d 项", len(safe))
				if kind != "" {
					label = fmt.Sprintf("修复这 %d 项", len(safe))
				}
				fix := ui.Button(c, "").Disabled(a.scanning || a.mutating).Label(label)
				fix.Children(func() {
					ui.Icon(c, glyph("wand"))
					ui.Text(c, label).SingleLine()
				})
				if fix.Clicked() {
					a.confirmBatch(append([]repair.Fix(nil), safe...), "修复")
				}
			}
			if len(review) > 0 {
				label := fmt.Sprintf("替换 %d 个不一致副本", len(review))
				if ui.Button(c, label).Disabled(a.scanning || a.mutating).Clicked() {
					a.confirmReplace(append([]repair.Fix(nil), review...))
				}
			}
		})
	})
}

func (a *App) confirm(title, message, ok, done string, run func() ([]manage.Record, error)) {
	a.ask = ask{open: true, title: title, message: message, ok: ok, done: done, run: run}
}

func (a *App) dialogs(c *ui.Context) {
	if !a.ask.open {
		return
	}
	chosen := a.sheet(c)
	switch chosen {
	case 1, 2:
		if a.mutating {
			return
		}
		task := a.ask
		if chosen == 2 {
			task.run, task.done = task.altRun, task.altDone
		}
		if a.win == nil || task.onUI {
			recs, err := task.run()
			a.finish(c, task.done, recs, err)
			return
		}
		a.beginMutation()
		go func() {
			recs, err := task.run()
			a.post(func() {
				a.mutating = false
				a.completed = &actionResult{done: task.done, records: recs, err: err}
			})
		}()
	}
}

func (a *App) finish(c *ui.Context, done string, recs []manage.Record, err error) {
	if err != nil && len(recs) == 0 {
		c.Toast(err.Error())
		if a.rescanPending {
			a.startScan()
		}
		c.Invalidate()
		return
	}
	if len(recs) == 0 {
		// The action already reported what it did, such as dropping a repo.
		c.Invalidate()
		return
	}
	msg := done
	if err != nil {
		msg = done + "。" + err.Error()
	}
	if len(recs) > 0 {
		kept := append([]manage.Record(nil), recs...)
		c.ToastAction(msg, "撤销", func() { a.undo(kept) })
	} else if msg != "" {
		c.Toast(msg)
	}
	a.refreshAfter(recs)
	c.Invalidate()
}

func (a *App) buildRows() []row {
	if a.snap == nil {
		return nil
	}
	if a.page == "issues" {
		return a.issueRows()
	}
	var entries []skill.Entry
	switch a.page {
	case "pool":
		entries = a.entriesFor("pool")
	default:
		root, ok := a.rootByPage(a.page)
		if !ok {
			return nil
		}
		entries = a.entriesFor(root.ID)
	}
	var out []row
	for _, e := range entries {
		if !matchQuery(a.query, e.Name, e.Description, e.RootName, e.Path, e.LinkTarget, e.FrontName) {
			continue
		}
		out = append(out, a.entryRow(e))
	}
	return out
}

func (a *App) issueRows() []row {
	kind := ""
	if a.filter >= 0 && a.filter < len(issueFilters) {
		kind = issueFilters[a.filter].id
	}
	var out []row
	for _, issue := range a.snap.Issues {
		if kind != "" && string(issue.Kind) != kind {
			continue
		}
		e := issue.Entry
		if !matchQuery(a.query, e.Name, e.Description, e.RootName, e.Path, issue.Detail) {
			continue
		}
		out = append(out, row{
			key:      e.Path + "\x00" + string(issue.Kind),
			name:     e.Name,
			badge:    issue.Kind.Label(),
			tone:     e.Relation.Tone(),
			glyph:    issueGlyph(issue.Kind),
			subtitle: e.RootName + " · " + oneLine(issue.Detail),
			entry:    e,
			kind:     issue.Kind,
		})
	}
	return out
}

func (a *App) entrySubtitle(e skill.Entry) string {
	if a.page == "pool" {
		n := 0
		if a.snap != nil {
			n = len(a.snap.Linked[e.Name])
		}
		tail := "没有 agent 软链"
		if n > 0 {
			tail = fmt.Sprintf("%d 个 agent 软链", n)
		}
		if e.Description == "" {
			return tail
		}
		return oneLine(e.Description) + " · " + tail
	}
	if e.Description == "" {
		return e.Relation.Label()
	}
	return e.Relation.Label() + " · " + oneLine(e.Description)
}

func (a *App) pageTitle() string {
	switch a.page {
	case effectivePage:
		return "生效视图"
	case "pool":
		return "共享池"
	case "issues":
		return "问题"
	default:
		if path, ok := strings.CutPrefix(a.page, "repo:"); ok {
			return a.repoLabel(path)
		}
		if r, ok := a.rootByPage(a.page); ok {
			return r.Name
		}
		return "Skill"
	}
}

func (a *App) pageSubtitle() string {
	switch a.page {
	case "pool":
		if p, ok := a.poolPath(); ok {
			return shortPath(a.home, p)
		}
		return shortPath(a.home, filepath.Join(a.home, ".agents", "skills"))
	case "issues":
		return "断链、空目录，以及没有 agent 挂上的共享池 skill；实体副本在详情中按需比较"
	default:
		if r, ok := a.rootByPage(a.page); ok {
			return shortPath(a.home, r.Path)
		}
	}
	return ""
}

func (a *App) emptyText() string {
	if a.scanning && a.snap == nil {
		return "正在扫描。"
	}
	if a.snap == nil {
		return "还没有扫描结果。"
	}
	if strings.TrimSpace(a.query) != "" {
		return "没有匹配的 skill。"
	}
	if a.page == "issues" {
		return "没有这类问题。"
	}
	return "这里是空的。"
}

// entryRow is how the list shows an entry.
func (a *App) entryRow(e skill.Entry) row {
	return row{
		key:      e.Path,
		name:     e.Name,
		badge:    e.Relation.Label(),
		tone:     e.Relation.Tone(),
		glyph:    relationGlyph(e.Relation),
		subtitle: a.entrySubtitle(e),
		entry:    e,
	}
}

func selected(rows []row, sel int) (row, bool) {
	if sel < 0 || sel >= len(rows) {
		return row{}, false
	}
	return rows[sel], true
}

func clamp(sel *int, n int) {
	if n == 0 {
		*sel = -1
		return
	}
	if *sel < 0 || *sel >= n {
		*sel = 0
	}
}

func toneColor(t *ui.Theme, tone string) ui.Color {
	switch tone {
	case "danger":
		return t.Danger
	case "warning":
		return t.Warning
	case "ok":
		return t.Success
	default:
		return t.TextMuted
	}
}

// allClear fills the content pane when the home page has nothing to show:
// before the first scan, or after one that found no issue. It reports
// whether it drew.
func (a *App) allClear(c *ui.Context) bool {
	if a.page != "issues" || strings.TrimSpace(a.query) != "" {
		return false
	}
	if a.snap != nil && len(a.snap.Issues) > 0 {
		return false
	}
	t := c.Theme()
	ui.Column(c).Grow(1).Gap(6).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if a.snap == nil {
			msg := "点「重新扫描」读取磁盘，⌘R 同样会扫描。"
			if a.scanning {
				msg = "正在读取磁盘。"
			}
			ui.Text(c, "还没有扫描结果").FontSize(17).FontWeight(600)
			ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
			return
		}
		ui.Icon(c, glyph("check")).FontSize(36).TextColor(t.Success)
		ui.Text(c, "没有待处理的问题").FontSize(17).FontWeight(600)
		ui.Textf(c, "共享池 %d 项，%d 个 Agent，%d 个仓库都已核对。",
			len(a.snap.EntriesFor("pool")), len(a.roots(skill.RoleAgent)), len(a.repos)).FontSize(13).TextColor(t.TextMuted)
	})
	return true
}

// statusBar is the strip along the foot of the content pane.
func (a *App) statusBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Height(footerHeight).Shrink(0).Padding(0, 16).Gap(8).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, "以磁盘目录为准").FontSize(11).TextColor(t.TextMuted)
		ui.Box(c).Grow(1)
		a.barStatus(c)
		if a.sideCollapsed {
			ui.Row(c).Width(160).Margin(0, 0, 0, 16).Children(func() { a.footerActions(c) })
		}
	})
}

// sheet asks the question in a.ask as a macOS sheet: a narrow panel hung
// from the top of the window with the cancel button beside a default
// button that names the action. It returns 1 when the default button was
// pressed, 0 when cancelled, and -1 while it is still open.
func (a *App) sheet(c *ui.Context) int {
	t := c.Theme()
	chosen := -1
	ui.DialogBase(c, &a.ask.open, func(backdrop ui.Element, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.12)).Justify(ui.Start)
		panel.Width(a.sheetWidth()).MaxWidth(520).Margin(bandHeight(c.TitleBar()), 0, 0, 0).Padding(18, 20, 16, 20).Gap(8).Radius(12).
			Background(t.Surface).Border(1, t.Border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.2)).
			Label(a.ask.title)
		ui.Text(c, a.ask.title).FontSize(13).FontWeight(600)
		if a.ask.message != "" {
			ui.Text(c, a.ask.message).FontSize(12).LineHeight(1.5).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				chosen = 0
			}
			if a.ask.alt != "" && ui.Button(c, a.ask.alt).Clicked() {
				chosen = 2
			}
			if ui.PrimaryButton(c, a.ask.ok).AutoFocus().Clicked() {
				chosen = 1
			}
		})
	})
	if chosen != -1 {
		a.ask.open = false
	}
	return chosen
}

// sheetWidth makes room for a second choice beside Cancel and the default.
func (a *App) sheetWidth() float32 {
	if a.ask.alt != "" {
		return 520
	}
	return 380
}
