package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/manage"
	"knack/internal/repair"
	"knack/internal/session"
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
	section  string
	// For issues: the fix that loses nothing, if there is one, and how
	// many rows its section has.
	fix   *repair.Fix
	count int
	// For the library: the Agents' entries of the skill, and the worst
	// thing wrong with it or them.
	reach   []skill.Entry
	problem skill.Relation
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
		a.updateRail(c)
		switch {
		case a.sideCollapsed:
			a.content(c)
		case a.rail:
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				a.railSidebar(c)
				ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.content(c) })
			})
		default:
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
	padStart := float32(32)
	if a.sideCollapsed && bar.Left > padStart {
		padStart = bar.Left
	}
	if a.rail {
		padStart = max(20, bar.Left-railWidth+8)
	}
	if _, ok := a.agentPageRoot(); ok {
		padStart = 32 // the Agent list stands between this bar and the window's edge
	}
	// The whole band is draggable, including its padding and gaps. Child
	// controls take their own presses before this background drag handle.
	ui.Row(c).Height(controlsBandHeight(bar)).Shrink(0).Padding(0, max(padEnd, 32), 0, padStart).Gap(8).AlignItems(ui.Center).DragWindow().
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		if a.sideCollapsed || a.rail {
			a.sidebarToggle(c)
		}
		if a.page == "sessions" && a.sessions.open {
			a.sessionBar(c)
			return
		}
		if a.splitBar() {
			a.splitContentBar(c, padStart)
			return
		}
		title := ui.Text(c, a.pageTitle()).FontSize(15).FontWeight(600).Shrink(0).SingleLine()
		if strings.HasPrefix(a.page, "repo:") {
			title.Shrink(1).MinWidth(0)
		}
		if a.page == "issues" && a.snap != nil && len(a.snap.Issues) > 0 {
			ui.Textf(c, "%d 项", len(a.snap.Issues)).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
		}
		if root, ok := a.agentPageRoot(); ok {
			a.agentMeta(c, root)
		}
		if a.page == graphPage {
			a.graphPicker(c)
		}
		ui.Box(c).Grow(1).MinWidth(8)
		if a.page == storagePage {
			a.storageHeadline(c, a.storage.scanning || a.mutating || a.sessions.loading)
		}
		if a.page == effectivePage {
			ui.Toolbar(c, func() { a.effectiveToolbar(c) }).Label("生效范围").Padding(0).Gap(8).MinWidth(0)
		}
		if a.page == graphPage {
			a.graphLegend(c)
		}
		if a.page == "overview" || a.page == "sessions" || a.page == insightsPage {
			a.overviewToolbar(c)
		} else if root, ok := a.agentPageRoot(); ok && a.state(a.page).agentTab != 0 && logSupport(session.Agent(agentKey(a.home, root.Path))) == "" {
			segmented(c, "统计时间范围", &a.sessions.period, periodLabels...)
		}
	})
}

// viewTabs puts navigation beside the content it changes, without a
// toolbar face. TabsBase keeps arrow-key navigation and selected states.
func viewTabs(c *ui.Context, selected *int, labels ...string) {
	segmented(c, "视图", selected, labels...)
}

func (a *App) listSearch(c *ui.Context) {
	placeholder := "搜索名称、说明或路径"
	t := c.Theme()
	ui.Row(c).Padding(0, 16, 8, 16).Children(func() {
		ui.SearchField(c, &a.query).Grow(1).MinWidth(0).Height(30).Radius(7).Background(t.Surface).Border(1, t.Border).
			PlaceholderColor(token(t, "ink-3")).Label("搜索").Placeholder(placeholder)
	})
}

// Global actions have one implementation, in the sidebar footer or the
// status bar when the sidebar is hidden.
func (a *App) footerActions(c *ui.Context) {
	ui.Toolbar(c, func() {
		scan := ui.Button(c, "").Height(toolbarControlHeight).Padding(0, 8).
			Disabled(a.scanning || a.sessions.loading || a.mutating).Label("重新扫描").Tooltip("重新扫描（⌘R）")
		scan.Children(func() {
			if a.scanning || a.sessions.loading {
				ui.Spinner(c).Size(14, 14).Label("正在扫描")
			} else {
				ui.Icon(c, glyph("refresh")).FontSize(15)
			}
			ui.Text(c, "重新扫描").FontSize(13).SingleLine()
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
	var inner float32
	if _, ok := a.agentPageRoot(); ok {
		inner = a.agentListW
	}
	if a.sideCollapsed {
		return inner
	}
	if a.rail {
		return railWidth + inner
	}
	return a.sideW + inner
}

// barStatus explains in the status bar why the page may not be live:
// a change is being applied, or what shows is the startup cache.
func (a *App) barStatus(c *ui.Context) {
	t := c.Theme()
	switch {
	case a.mutating:
		ui.Spinner(c).Label("正在处理")
		ui.Text(c, "正在处理…").FontSize(13).TextColor(t.TextMuted).Shrink(0)
	case a.cached:
		ui.Text(c, "缓存").FontSize(13).TextColor(t.TextMuted).Shrink(0)
	}
}

func (a *App) warnings(c *ui.Context) {
	if a.snap == nil || len(a.snap.Warnings) == 0 {
		return
	}
	t := c.Theme()
	ink := token(t, "warn-ink")
	n := min(len(a.snap.Warnings), 3)
	ui.Row(c).Shrink(0).Margin(12, 32, 0, 32).Padding(10, 14).Gap(10).Radius(8).Background(token(t, "warn-bg")).Label("扫描提示").Children(func() {
		ui.Icon(c, glyph("alert")).FontSize(15).TextColor(t.Warning).Shrink(0).Margin(1, 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			for _, w := range a.snap.Warnings[:n] {
				ui.Text(c, w).FontSize(13).TextColor(ink).SingleLine().Tooltip(w)
			}
			if extra := len(a.snap.Warnings) - n; extra > 0 {
				ui.Textf(c, "还有 %d 条提示", extra).FontSize(12).TextColor(ink)
			}
		})
	})
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
	a.settleAgentsPage()
	if _, ok := a.agentPageRoot(); ok {
		ui.Row(c).Fill().Background(c.Theme().Background).AlignItems(ui.Stretch).Children(func() {
			a.agentList(c)
			ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.contentPane(c) })
		})
		return
	}
	a.contentPane(c)
}

func (a *App) contentPane(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.contentBar(c)
		if a.loadErr != "" {
			ui.Text(c, a.loadErr).Padding(6, 16, 0, 16).TextColor(t.Danger)
		}
		a.warnings(c)
		if root, ok := a.agentPageRoot(); ok {
			a.agentTabs(c, root)
		}
		if a.page == "overview" {
			a.overviewPane(c)
			a.statusBar(c)
			return
		}
		if a.page == storagePage {
			a.storagePane(c)
			a.statusBar(c)
			return
		}
		if a.page == "sessions" {
			a.sessionsPane(c)
			a.statusBar(c)
			return
		}
		if a.page == agentsPage {
			emptyState(c, "还没有 Agent", "bot", t.TextMuted, "还没有发现 Agent", "扫描到 Agent 的技能目录后，它们会出现在这里。", nil)
			a.statusBar(c)
			return
		}
		if a.page == insightsPage {
			a.insightsPane(c)
			a.statusBar(c)
			return
		}
		if a.page == graphPage {
			a.graphPage(c)
			a.statusBar(c)
			return
		}
		if root, ok := a.agentPageRoot(); ok && a.state(a.page).agentTab != 0 {
			if reason := logSupport(session.Agent(agentKey(a.home, root.Path))); reason != "" {
				a.noLogs(c, root, reason)
			} else if a.state(a.page).agentTab == 1 {
				a.sessionsPane(c)
			} else {
				a.agentUsagePane(c, root)
			}
			a.statusBar(c)
			return
		}
		if a.allClear(c) {
			a.statusBar(c)
			return
		}
		if a.page == effectivePage {
			a.effectivePane(c)
			a.statusBar(c)
			return
		}
		// Leave room for management controls in a narrow window.
		width, _ := c.Size()
		a.listW = min(a.listW, max(180, width-a.sidebarWidth()-320-2))
		ui.Split(c, &a.listW, func() { a.listPane(c) }, func() { a.detailPane(c) }).Grow(1)
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
			ui.Text(c, sub).Padding(10, 16, 6, 16).FontSize(13).TextColor(t.TextMuted).MaxLines(2)
		}
		if a.page == "issues" {
			labels := make([]string, len(issueFilters))
			for i, f := range issueFilters {
				labels[i] = f.label
			}
			ui.Row(c).Padding(12, 16, 8, 16).Children(func() { segmentedFill(c, "问题类别", &a.filter, labels...) })
		}
		rows := a.rows()
		if a.page == libraryPage {
			a.libraryHead(c)
		}
		a.listSearch(c)
		a.sessionSearchLink(c)
		ps := a.state(a.page)
		clamp(&ps.sel, len(rows))
		if a.page == "issues" {
			a.syncIssueSelection(rows)
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
		// The rows draw their own choice (selectedRow), under the item and
		// not its section heading, so the list's accent fill stays clear.
		lt := t
		if a.page != "issues" {
			lt = quietList(t)
		}
		ui.Themed(c, lt).Grow(1).MinHeight(0).Background(ui.Transparent).Children(func() { a.listRows(c, rows) })
		if a.page == "issues" {
			a.anomalyGroup(c)
			a.repairBar(c)
		}
	})
}

// listRows is the list of the list pane.
func (a *App) listRows(c *ui.Context, rows []row) {
	t := c.Theme()
	ps := a.state(a.page)
	{
		list := ui.List(c, &ps.list, len(rows), func(i int) {
			selected := i == ps.sel
			if a.page == "issues" {
				selected = ps.chosen.Has(rows[i].key)
			}
			if a.page == "issues" {
				a.paintIssueRow(c, rows[i], selected)
				return
			}
			if a.page == libraryPage {
				a.paintLibraryRow(c, rows[i], selected)
				return
			}
			if rows[i].section != "" {
				ui.Text(c, rows[i].section).FontSize(11.5).FontWeight(600).LetterSpacing(0.3).TextColor(token(t, "ink-3")).Padding(14, 10, 4, 10)
			}
			a.paintRow(c, rows[i], selected)
		}).Grow(1).Padding(4, 8).Gap(2).Children(func() {
			if len(rows) == 0 {
				ui.Column(c).Padding(40, 12).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph("empty")).FontSize(26).TextColor(t.TextMuted)
					ui.Text(c, a.emptyText()).FontSize(13).TextColor(t.TextMuted)
				})
			}
		})
		if list.Changed() {
			c.Invalidate()
		}
	}
}

func (a *App) paintRow(c *ui.Context, r row, selected bool) {
	t := c.Theme()
	selectedRow(c, selected).Padding(7, 10).Gap(10).Children(func() {
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
			ui.Icon(c, glyph(r.glyph)).FontSize(15).Shrink(0).Margin(2, 0, 0, 0).TextColor(toneColor(t, r.tone))
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.name).FontSize(13).FontWeight(500).Grow(1).SingleLine()
				if _, ok := a.agentPageRoot(); ok {
					ui.Text(c, a.skillListUsage(r.entry)).FontSize(11).SingleLine().Tooltip("近 30 天 · 按日志中的 Skill 调用名匹配")
				}
				if r.badge != "" {
					pill(c, r.badge, toneColor(t, r.tone))
				}
			})
			if r.subtitle != "" {
				ui.Text(c, r.subtitle).FontSize(12).TextColor(t.TextMuted).SingleLine()
			}
		})
	})
}

func (a *App) detailPane(c *ui.Context) {
	t := c.Theme()
	rows := a.rows()
	ps := a.state(a.page)
	clamp(&ps.sel, len(rows))
	ui.Scroll(c).Fill().Label("技能详情内容").Children(func() {
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
				ui.Text(c, "在左边选择一个 skill。").FontSize(13).TextColor(t.TextMuted)
			})
			return
		}
		a.detail(c, sel)
	})
}

// detail is the design's skill page: the name in the face of a directory,
// what it does, where it lives, then how it reaches the Agents, how it is
// used and what it says.
func (a *App) detail(c *ui.Context, r row) {
	t := c.Theme()
	e := r.entry
	ui.Column(c).Padding(26, 32, 36, 32).Gap(18).MaxWidth(820).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, e.Name).FontSize(22).FontWeight(600).MinWidth(0).SingleLine().Tooltip(e.Name)
				if r.badge != "" {
					tone := toneColor(t, r.tone)
					ui.Row(c).Gap(5).Padding(2, 8).Radius(5).AlignItems(ui.Center).Background(fade(tone, 26)).Shrink(0).Children(func() {
						if r.glyph != "" {
							ui.Icon(c, glyph(r.glyph)).FontSize(13).TextColor(tone)
						}
						ui.Text(c, r.badge).FontSize(12).TextColor(tone).SingleLine()
					})
				}
			})
			if e.Description != "" {
				ui.Text(c, e.Description).FontSize(14).LineHeight(1.55).TextColor(t.TextMuted).MaxLines(6).Selectable()
			}
			a.skillMeta(c, e)
		})
		if a.splitBar() {
			if e.System || e.RootRole == skill.RoleBuiltin {
				ui.Text(c, "自带 skill 只展示，这里不会修改它。").FontSize(13).TextColor(t.TextMuted)
			}
			a.actionNotes(c, e)
			if !a.removalFor(e).Unlinkable {
				a.removalNote(c, e)
			}
		} else {
			a.actions(c, e)
		}
		if e.RootRole == skill.RolePool && a.snap != nil {
			a.copyBanner(c, e)
			a.mountMap(c, e)
		} else {
			a.compareButton(c, e)
		}
		if e.Usable() {
			a.skillUsage(c, e)
		}
		a.invocationSettings(c, e)
		a.infoCard(c, r, a.splitBar())
		a.detailTabs(c, e)
	})
}

// detailLine is one line of the info card, in the color it calls for.
type detailLine struct {
	s   string
	col ui.Color
}

// infoCard is the panel of facts about an entry: where it is, what it
// points at, what it relates to, what it contains. Brief leaves out where
// it is and which Agents link it, which the meta line and the mount map
// already say.
func (a *App) infoCard(c *ui.Context, r row, brief bool) {
	t := c.Theme()
	e := r.entry
	var lines []detailLine
	add := func(s string, col ui.Color) {
		if s != "" {
			lines = append(lines, detailLine{s, col})
		}
	}
	if !brief {
		add(e.RootName+" · "+shortPath(a.home, e.Path), t.TextMuted)
	}
	gone := e.Kind == skill.KindBroken && e.LinkTarget != ""
	if gone {
		add("指向 "+e.LinkTarget+"，目标已不存在", t.Danger)
	} else if e.LinkTarget != "" {
		add("指向 "+e.LinkTarget, t.TextMuted)
	}
	if e.RootRole == skill.RolePool && !brief {
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
	if e.MetaErr != "" && !gone {
		add(e.MetaErr, t.Danger)
	}
	if e.HashErr != "" {
		add(e.HashErr, t.Danger)
	}
	if len(lines) == 0 {
		return
	}
	ui.Column(c).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Label("详细信息").Children(func() {
		for _, l := range lines {
			ui.Text(c, l.s).Padding(8, 0).FontSize(13).TextColor(l.col).Selectable().BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
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
		ui.Text(c, "自带 skill 只展示，这里不会修改它。").FontSize(13).TextColor(t.TextMuted)
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
	a.actionNotes(c, e)
	a.removalNote(c, e)
}

// actionNotes says why an entry has no fix when the user has to decide.
func (a *App) actionNotes(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	note := func(s string) { ui.Text(c, s).FontSize(13).TextColor(t.TextMuted) }
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
	free := a.freeAgents(pool)
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
	if a.page == libraryPage {
		return a.libraryRows()
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
		entries = a.agentEntries(root)
	}
	var out []row
	readonly := false
	for _, e := range entries {
		if !matchQuery(a.query, e.Name, e.Description, e.RootName, e.Path, e.LinkTarget, e.FrontName) {
			continue
		}
		r := a.entryRow(e)
		if (e.System || e.RootRole == skill.RoleBuiltin) && !readonly {
			r.section = "自带 · 只读"
			readonly = true
		}
		out = append(out, r)
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
	case storagePage:
		return "磁盘清理"
	case "overview":
		return "首页"
	case "sessions":
		return "全部会话"
	case effectivePage:
		return "生效视图"
	case "pool":
		return "共享池"
	case "issues":
		return "待处理"
	case libraryPage:
		return "技能库"
	case graphPage:
		return "依赖图"
	case agentsPage:
		return "Agents"
	case insightsPage:
		return "诊断"
	default:
		if path, ok := strings.CutPrefix(a.page, "repo:"); ok {
			return a.repoLabel(path)
		}
		if r, ok := a.rootByPage(a.page); ok {
			return strings.TrimSuffix(r.Name, " 自带")
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
	switch {
	case a.snap == nil && a.scanning:
		emptyState(c, "正在扫描", "search", t.TextMuted, "正在查找本机的技能", "只读取文件头部元数据，不改动任何文件。", func() {
			ui.Spinner(c).Size(16, 16).Label("正在扫描")
		})
	case a.snap == nil:
		emptyState(c, "还没有扫描结果", "search", t.TextMuted, "还没有扫描结果", "点「重新扫描」读取磁盘，⌘R 同样会扫描。", func() {
			if primaryButton(c, "first-scan", "refresh", "开始扫描").Clicked() {
				a.startScan()
			}
		})
	default:
		emptyState(c, "没有待处理的问题", "check", t.Success, "没有待处理的问题",
			fmt.Sprintf("共享池 %d 项，%d 个 Agent，%d 个仓库都已核对。", len(a.snap.EntriesFor("pool")), len(a.roots(skill.RoleAgent)), len(a.repos)), nil)
	}
	return true
}

// statusBar is the strip along the foot of the content pane. The sidebar's
// footer carries the scan status, so the strip shows only while it is
// collapsed.
func (a *App) statusBar(c *ui.Context) {
	if !a.sideCollapsed {
		return
	}
	t := c.Theme()
	ui.Row(c).Height(footerHeight).Shrink(0).Padding(0, 16).Gap(8).AlignItems(ui.Center).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if a.page == "overview" || a.page == "sessions" {
			text := "本地只读 · " + strings.Join(agentOptions()[1:], " / ")
			if a.sessions.loading {
				text = "正在增量读取本地会话…"
			} else if a.sessions.cached {
				text = "会话缓存 · 正在等待校验"
			}
			ui.Text(c, text).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			if snap := a.sessions.snapshot; snap != nil && !snap.At.IsZero() {
				files := 0
				for _, source := range snap.Sources {
					files += source.Files
				}
				ui.Textf(c, "%d 个会话文件 · %s", files, snap.At.Format("15:04")).FontSize(11.5).TextColor(t.TextMuted)
			}
		} else {
			ui.Text(c, "以磁盘目录为准").FontSize(11.5).TextColor(t.TextMuted)
		}
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
		panel.Width(a.sheetWidth()).MaxWidth(520).Margin(bandHeight(c.TitleBar()), 0, 0, 0).Padding(20, 22, 18, 22).Gap(8).Radius(12).
			Background(t.Surface).Border(1, t.Border).Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.2)).
			Label(a.ask.title)
		ui.Text(c, a.ask.title).FontSize(15).FontWeight(600)
		// A message may end with the list of what it touches, after a
		// blank line; that part is shown as paths.
		message, list, _ := strings.Cut(a.ask.message, "\n\n")
		if message != "" {
			ui.Text(c, message).FontSize(13).LineHeight(1.6).TextColor(t.TextMuted)
		}
		if list != "" {
			ui.Column(c).Gap(5).Padding(4, 0).Children(func() {
				for _, line := range strings.Split(list, "\n") {
					if strings.HasSuffix(line, "：") {
						ui.Text(c, line).FontSize(13).TextColor(t.TextMuted)
						continue
					}
					ui.Text(c, line).Font(monoFont).FontSize(13).SingleLine().Tooltip(line)
				}
			})
		}
		ui.Row(c).Gap(8).Justify(ui.End).Margin(10, 0, 0, 0).Children(func() {
			if outlineButton(c, "ask-cancel", "取消").Height(28).Clicked() {
				chosen = 0
			}
			if a.ask.alt != "" && outlineButton(c, "ask-alt", a.ask.alt).Height(28).Clicked() {
				chosen = 2
			}
			if primaryButton(c, "ask-ok", "", a.ask.ok).AutoFocus().Clicked() {
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
