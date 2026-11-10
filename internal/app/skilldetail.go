package app

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

	"knack/internal/repair"
	"knack/internal/session"
	"knack/internal/skill"
)

// splitBar reports a page whose title bar is split like the design's 技能库:
// the list's name over the list, the chosen skill's place and actions over
// its detail.
func (a *App) splitBar() bool {
	return a.page == libraryPage || a.page == "pool" || strings.HasPrefix(a.page, "repo:")
}

// splitContentBar is the title band of a split page. The divider meets the
// split below it.
func (a *App) splitContentBar(c *ui.Context, padStart float32) {
	t := c.Theme()
	lead := float32(0)
	if a.sideCollapsed || a.rail {
		lead = 28 + 8
	}
	ui.Row(c).Width(max(0, a.listW-padStart-8-lead)).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
		title := ui.Text(c, a.pageTitle()).FontSize(15).FontWeight(600).Shrink(1).MinWidth(0).SingleLine()
		if strings.HasPrefix(a.page, "repo:") {
			withTip(c, title, strings.TrimPrefix(a.page, "repo:"))
		}
		if a.snap != nil {
			n := len(a.rows())
			if a.page == libraryPage {
				n = a.libCounts[0]
			}
			number(c, fmt.Sprint(n)).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
		}
		ui.Spacer(c)
		if a.page == libraryPage {
			a.librarySortButton(c)
		}
	})
	ui.Box(c).Width(1).AlignSelf(ui.Stretch).Background(t.Border).Shrink(0)
	ui.Row(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 24).Gap(8).AlignItems(ui.Center).Children(func() {
		rows := a.rows()
		if r, ok := selected(rows, a.state(a.page).sel); ok && a.page != "issues" {
			a.detailBar(c, r.entry)
		}
	})
}

// compactDetail reports a detail pane too narrow for the design's side by
// side parts: the mount map drops its source cards, buttons their words.
func (a *App) compactDetail(c *ui.Context) bool {
	width, _ := c.Size()
	return width-a.sidebarWidth()-a.listW-1-64 < 560
}

// detailBar names where the chosen skill lives and holds what can be done
// to it: reveal it, the rest under 更多, and the one action that matters
// most in ink.
func (a *App) detailBar(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Label("位置：" + e.RootName + " / " + e.Name).Children(func() {
		ui.Text(c, strings.TrimSuffix(e.RootName, " 自带")).FontSize(13).TextColor(muted).Shrink(0).SingleLine()
		ui.Icon(c, glyph("chevron-right")).FontSize(11.5).TextColor(muted).Shrink(0)
		ui.Text(c, e.Name).FontSize(13).MinWidth(0).SingleLine()
	})
	ui.Box(c).Grow(1).MinWidth(8)
	compact := a.compactDetail(c)
	b := outlineButton(c, "reveal", "").Label("在访达中显示").Gap(6)
	if compact {
		b.Padding(0).Width(28).Justify(ui.Center)
		withTip(c, b, "在访达中显示")
	} else {
		withTip(c, b, e.Path)
	}
	b.Children(func() {
		ui.Icon(c, glyph("reveal")).FontSize(13)
		if !compact {
			ui.Text(c, "在访达中显示").FontSize(13).SingleLine()
		}
	})
	if b.Clicked() {
		a.reveal(e.Path)
	}
	readOnly := e.System || e.RootRole == skill.RoleBuiltin
	var fixes []repair.Fix
	if !readOnly {
		fixes = a.fixesFor(e)
	}
	removal := a.removalFor(e)
	a.dropdown(c, "detail-more", func(m *menu) {
		if m.Item("复制路径").Chosen() {
			a.copyPath(e.Path)
			c.Toast("已复制路径")
		}
		for _, f := range fixes[min(1, len(fixes)):] {
			if m.Item(f.Label).Disabled(a.scanning || a.mutating).Chosen() {
				a.confirmFix(f)
			}
		}
		if !readOnly && removal.Available && e.Kind != skill.KindBroken && !e.Vacant {
			m.Separator()
			if m.Item(removal.Label).Danger().Disabled(a.scanning || a.mutating || (removal.Err != "" && !removal.Unlinkable)).Chosen() {
				a.confirmRemoval(e, removal)
			}
		}
	}, func(b ui.Element) { styleIconButton(c, b, "more", "更多", "更多") })
	switch {
	case len(fixes) > 0:
		f := fixes[0]
		if primaryButton(c, f.Label+"|"+f.Title, "wand", f.Label).Disabled(a.scanning || a.mutating).Clicked() {
			a.confirmFix(f)
		}
	case !readOnly && e.RootRole == skill.RolePool && e.Usable() && !a.cached:
		free := a.freeAgents(e)
		if len(free) == 0 {
			return
		}
		a.dropdown(c, "detail-link-agent", func(m *menu) {
			for _, r := range free {
				if m.Item(r.Name).Chosen() {
					a.confirmFix(repair.LinkToAgent(e, r))
				}
			}
		}, func(b ui.Element) {
			stylePrimary(c, b.Disabled(a.scanning || a.mutating), "link", "挂到 Agent…").Label("挂到 Agent")
		})
	}
}

// freeAgents are the Agents with no entry of a pool skill's name yet.
func (a *App) freeAgents(pool skill.Entry) []skill.Root {
	var free []skill.Root
	for _, r := range a.roots(skill.RoleAgent) {
		taken := false
		for _, e := range a.entriesFor(r.ID) {
			if e.Name == pool.Name {
				taken = true
				break
			}
		}
		if !taken {
			free = append(free, r)
		}
	}
	return free
}

// dirStat is what a skill's folder holds.
type dirStat struct {
	files []dirFile
	size  int64
	more  bool // the walk stopped early
}

type dirFile struct {
	rel  string
	size int64
}

const dirStatFiles = 500

// dirStats walks a skill's folder once per scan.
func (a *App) dirStats(e skill.Entry) dirStat {
	cache := a.viewCache()
	if cache.dirs == nil {
		cache.dirs = map[string]dirStat{}
	}
	if s, ok := cache.dirs[e.Path]; ok {
		return s
	}
	dir := e.Path
	if e.RealPath != "" {
		dir = e.RealPath
	}
	var s dirStat
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if len(s.files) >= dirStatFiles {
			s.more = true
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		s.files = append(s.files, dirFile{filepath.ToSlash(rel), info.Size()})
		s.size += info.Size()
		return nil
	})
	cache.dirs[e.Path] = s
	return s
}

// skillMeta is the line under a skill's description: where its content
// lives, what its folder holds and when it last changed.
func (a *App) skillMeta(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	muted := token(t, "ink-3")
	where := e.Path
	if e.LinkAbs != "" {
		where = e.LinkAbs
	}
	item := func(icon, text, tip string) {
		withTip(c, ui.Row(c).Gap(5).AlignItems(ui.Center).MinWidth(0), tip).Children(func() {
			ui.Icon(c, glyph(icon)).FontSize(13).TextColor(muted).Shrink(0)
			number(c, text).FontSize(13).TextColor(muted).MinWidth(0)
		})
	}
	ui.Row(c).Gap(14).Wrap().AlignItems(ui.Center).MinWidth(0).Label("技能概况").Children(func() {
		item("folder", a.sourcePath(where), where)
		if e.Kind != skill.KindBroken {
			st := a.dirStats(e)
			files := fmt.Sprintf("%d 个文件", len(st.files))
			if st.more {
				files = fmt.Sprintf("%d+ 个文件", len(st.files))
			}
			text := files + " · " + byteSize(st.size)
			if e.HasDocument {
				text = "SKILL.md · " + text
			}
			item("file", text, "")
		}
		if at := latest(e.DocModTime, e.EntryModTime); !at.IsZero() {
			item("refresh", "修改于 "+modDate(at, time.Now()), at.Local().Format("2006/01/02 15:04"))
		}
	})
}

// sourcePath shows a path from the source it is in: repo/skill for a
// repository, else from home.
func (a *App) sourcePath(p string) string {
	for _, r := range a.repos {
		if rel, err := filepath.Rel(r.Path, p); err == nil && !strings.HasPrefix(rel, "..") {
			return a.repoLabel(r.Path) + "/" + filepath.ToSlash(rel)
		}
	}
	return shortPath(a.home, p)
}

func latest(times ...time.Time) time.Time {
	var out time.Time
	for _, at := range times {
		if at.After(out) {
			out = at
		}
	}
	return out
}

func modDate(at, now time.Time) string {
	at = at.Local()
	if at.Year() != now.Year() {
		return at.Format("2006 年 1 月 2 日")
	}
	return fmt.Sprintf("%d 月 %d 日", at.Month(), at.Day())
}

// copyBanner warns that an Agent keeps its own copy of a pool skill, with
// the way back to a link.
func (a *App) copyBanner(c *ui.Context, pool skill.Entry) {
	t := c.Theme()
	ink := token(t, "warn-ink")
	for _, x := range a.libraryReach(pool) {
		if x.Relation != skill.RelStale && x.Relation != skill.RelCopy {
			continue
		}
		copyEntry := x
		text := x.RootName + " 用的是一份独立副本，与共享池内容不一致。"
		if x.Relation == skill.RelCopy {
			text = x.RootName + " 用的是一份独立副本，还没有和共享池比较。"
		}
		ui.Row(c.Key("copy:"+x.Path)).Padding(10, 14).Gap(10).Radius(8).AlignItems(ui.Center).Background(token(t, "warn-bg")).
			Border(1, fade(t.Warning, 60)).Label("独立副本：" + x.RootName).Children(func() {
			ui.Icon(c, glyph("copy")).FontSize(15).TextColor(ink).Shrink(0)
			ui.Text(c, text).FontSize(13).TextColor(ink).Grow(1).MinWidth(0).MaxLines(2)
			if copyEntry.Relation == skill.RelCopy {
				state := a.comparisons[copyEntry.Path]
				busy := state != nil && state.loading
				if outlineButton(c, "compare:"+copyEntry.Path, "比较差异").Disabled(a.scanning || a.mutating || busy || a.cached).Clicked() {
					a.beginCompare(copyEntry)
				}
			}
			for _, f := range a.fixesFor(copyEntry) {
				if f.Label != "改为软链" {
					continue
				}
				link := action.Button(c.Key("relink:"+copyEntry.Path), f.Label, action.ButtonProps{Size: lobe.Small}).
					Label(f.Label + "：" + copyEntry.RootName).Disabled(a.scanning || a.mutating)
				if link.Clicked() {
					a.confirmFix(f)
				}
				break
			}
		})
	}
}

// mountMap is how a pool skill reaches each Agent, drawn as the design's
// map: the repository it comes from, the pool, and a line to every Agent
// that has it, with the Agents that do not one click from a link.
func (a *App) mountMap(c *ui.Context, pool skill.Entry) {
	t := c.Theme()
	agents := a.roots(skill.RoleAgent)
	if len(agents) == 0 {
		return
	}
	muted := token(t, "ink-3")
	found := make([]*skill.Entry, len(agents))
	for i, root := range agents {
		for _, e := range a.entriesFor(root.ID) {
			if e.Name == pool.Name {
				e := e
				found[i] = &e
				break
			}
		}
	}
	const rowH, rowGap = 34, 8
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "挂载关系").FontSize(15).FontWeight(600).Shrink(0)
			ui.Spacer(c)
			ui.Text(c, "一份内容，通过软链共享给多个 Agent").FontSize(13).TextColor(muted).Shrink(1).MinWidth(0).SingleLine()
		})
		if a.compactDetail(c) {
			// Narrow, the map is a grouped list: one panel, its rows parted
			// by hairlines rather than boxed again inside it.
			ui.Column(c).Padding(0, 4).Radius(10).Border(1, t.Border).Background(t.Surface).Label("挂载关系").Children(func() {
				for i, root := range agents {
					row := a.mountRow(c, pool, root, found[i], 40)
					row.Radius(0).Border(0, ui.Transparent).Background(ui.Transparent)
					if found[i] != nil && found[i].Relation == skill.RelStale {
						row.Background(token(t, "warn-bg"))
					}
					if i < len(agents)-1 {
						row.BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
					}
				}
			})
			return
		}
		ui.Row(c).Padding(18, 18).AlignItems(ui.Center).Radius(10).Border(1, t.Border).Background(t.Surface).Label("挂载关系").Children(func() {
			card := func(icon, title, text, tip string) {
				withTip(c, ui.Column(c).Width(168).Shrink(1).MinWidth(110).Padding(9, 12).Gap(3).Radius(8).Border(1, token(t, "line-strong")).Background(t.Background), tip).Children(func() {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, glyph(icon)).FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, title).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
					})
					ui.Text(c, text).Font(monoFont).FontSize(12).SingleLine()
				})
			}
			if pool.LinkAbs != "" {
				card("folder", "来源仓库", a.sourcePath(pool.LinkAbs), pool.LinkAbs)
				ui.Box(c).Width(48).Height(24).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
					y := r.Y + r.H - 6
					p.StrokePath((&ui.Path{}).MoveTo(r.X, y).LineTo(r.X+r.W, y), 1.2, token(t, "line-strong"))
					lw, _ := p.MeasureText(r.W, ui.Span{Text: "软链", Size: 11, Color: muted})
					p.RichText(r.X+(r.W-lw)/2, r.Y, r.W, ui.Span{Text: "软链", Size: 11, Color: muted})
				})
			}
			card("package", "共享池", shortPath(a.home, filepath.Dir(pool.Path)), filepath.Dir(pool.Path))
			ui.Box(c).Width(64).Shrink(0).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
				total := float32(len(agents)*rowH + (len(agents)-1)*rowGap)
				top := r.Y + (r.H-total)/2
				x0, y0 := r.X, r.Y+r.H/2
				for i, e := range found {
					if e == nil {
						continue
					}
					col := token(t, "line-strong")
					switch e.Relation {
					case skill.RelStale, skill.RelCopy:
						col = t.Warning
					case skill.RelBroken:
						col = t.Danger
					}
					y1 := top + float32(i)*(rowH+rowGap) + rowH/2
					x1 := r.X + r.W
					p.StrokePath((&ui.Path{}).MoveTo(x0, y0).CubeTo(x0+r.W/2, y0, x1-r.W/2, y1, x1, y1), 1.2, col)
				}
			})
			ui.Column(c).Grow(1).MinWidth(180).Gap(rowGap).Children(func() {
				for i, root := range agents {
					a.mountRow(c, pool, root, found[i], rowH)
				}
			})
		})
	})
}

func (a *App) mountRow(c *ui.Context, pool skill.Entry, root skill.Root, found *skill.Entry, height float32) ui.Element {
	t := c.Theme()
	muted := token(t, "ink-3")
	row := ui.Row(c.Key("mount:"+root.ID)).Height(height).Padding(0, 12).Gap(8).Radius(7).AlignItems(ui.Center).
		Background(t.Background).Border(1, token(t, "line-strong"))
	if found != nil && found.Relation == skill.RelStale {
		row.Background(token(t, "warn-bg")).Border(1, fade(t.Warning, 110))
	}
	row.Children(func() {
		ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(a.rootColor(c, root))
		name := ui.Text(c, root.Name).FontSize(13).Grow(1).MinWidth(40).SingleLine()
		status := func(icon, text string, color ui.Color) {
			withTip(c, ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(1).MinWidth(0), text).Children(func() {
				if icon != "" {
					ui.Icon(c, glyph(icon)).FontSize(13).TextColor(color).Shrink(0)
				}
				ui.Text(c, text).FontSize(13).TextColor(color).MinWidth(0).SingleLine()
			})
		}
		switch {
		case found == nil:
			name.TextColor(muted)
			if pool.Usable() && !a.cached {
				b := outlineButton(c, "mount:"+root.ID, "").Height(22).Padding(0, 8).Gap(4).Label("挂载到 " + root.Name).Disabled(a.scanning || a.mutating)
				b.Children(func() {
					ui.Icon(c, glyph("plus")).FontSize(11.5)
					ui.Text(c, "挂载").FontSize(12)
				})
				if b.Clicked() {
					a.confirmFix(repair.LinkToAgent(pool, root))
				}
			} else {
				status("", "未挂载", muted)
			}
		case found.Relation == skill.RelLinked:
			status("check", "软链", t.TextMuted)
		case found.Relation == skill.RelStale:
			status("alert", "独立副本 · 有差异", t.Warning)
		case found.Relation == skill.RelCopy:
			status("copy", "独立副本 · 待比较", t.Warning)
		case found.Relation == skill.RelSame:
			status("copy", "一致副本", t.TextMuted)
		case found.Relation == skill.RelBroken:
			status("unlink", "断链", t.Danger)
		default:
			status("", found.Relation.Label(), t.TextMuted)
		}
	})
	return row
}

// detailTabs are what the skill holds: its SKILL.md, its files, what it
// points at, and the sessions that called it.
func (a *App) detailTabs(c *ui.Context, e skill.Entry) {
	st := a.dirStats(e)
	usage, _, ok := a.skillUsageFor(e)
	sessions := ""
	if ok {
		sessions = fmt.Sprint(len(usage.sessions))
	}
	deps := ""
	var graph *skill.Graph
	if e.HasDocument {
		if state := a.fileGraphFor(e); state.graph != nil {
			graph = state.graph
			deps = fmt.Sprint(len(graph.Edges))
		}
	}
	names := []string{"SKILL.md", "文件", "依赖", "关联会话"}
	counts := []string{"", fmt.Sprint(len(st.files)), deps, sessions}
	a.detailTab = max(0, min(a.detailTab, len(names)-1))
	ui.Column(c).Gap(14).Children(func() {
		ui.Row(c).Children(func() {
			underlineTabs(c, "detail-tabs", "技能内容", "", &a.detailTab, names, counts)
		})
		switch a.detailTab {
		case 0:
			a.documentPane(c, e)
		case 1:
			a.fileList(c, e, st)
		case 2:
			a.depsPane(c, e, graph)
		case 3:
			a.usedInSessions(c, usage, ok)
		}
	})
}

func (a *App) fileList(c *ui.Context, e skill.Entry, st dirStat) {
	t := c.Theme()
	if len(st.files) == 0 {
		ui.Text(c, "目录里没有文件").FontSize(13).TextColor(t.TextMuted)
		return
	}
	dir := e.Path
	if e.RealPath != "" {
		dir = e.RealPath
	}
	ui.Column(c).Label("技能文件").Children(func() {
		for _, f := range st.files {
			file := filepath.Join(dir, filepath.FromSlash(f.rel))
			b := withTip(c, ui.ButtonBase(c.Key("file:"+f.rel)).Padding(7, 6).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
				Label("打开 "+f.rel), file)
			if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
			b.Children(func() {
				ui.Icon(c, glyph(nodeGlyph(skill.Node{Name: f.rel, Kind: skill.NodeFile}))).FontSize(13).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, f.rel).Font(monoFont).FontSize(13).Grow(1).MinWidth(0).SingleLine()
				number(c, byteSize(f.size)).FontSize(11.5).TextColor(token(t, "ink-3"))
			})
			if b.Clicked() {
				a.openPath(file)
			}
		}
	})
	if st.more {
		ui.Textf(c, "只列出前 %d 个文件", dirStatFiles).FontSize(11.5).TextColor(token(t, "ink-3"))
	}
}

func (a *App) depsPane(c *ui.Context, e skill.Entry, g *skill.Graph) {
	t := c.Theme()
	ui.Column(c).Gap(10).Children(func() {
		switch {
		case !e.HasDocument:
			ui.Text(c, "没有 SKILL.md，无法分析引用").FontSize(13).TextColor(t.TextMuted)
		case g == nil:
			ui.Text(c, "正在读取技能目录…").FontSize(13).TextColor(t.TextMuted)
		default:
			var items [][2]string
			for _, edge := range g.Edges {
				note := edge.Via
				if edge.Line > 0 {
					note = fmt.Sprintf("%s 第 %d 行 · %s", edge.From, edge.Line, edge.Via)
				}
				items = append(items, [2]string{edge.To, note})
			}
			if len(items) == 0 {
				ui.Text(c, "SKILL.md 没有引用目录里的其他文件或其他技能").FontSize(13).TextColor(t.TextMuted)
			}
			refList(c, "引用", items)
			a.missingCallout(c, g)
		}
		if e.HasDocument && outlineButton(c, "show-graph", "在依赖图中查看").Height(28).AlignSelf(ui.Start).Clicked() {
			a.showGraph(e)
			c.Invalidate()
		}
	})
}

func (a *App) usedInSessions(c *ui.Context, usage *skillUsageData, ok bool) {
	t := c.Theme()
	switch {
	case !ok:
		ui.Text(c, "这个来源的调用记录暂不支持").FontSize(13).TextColor(t.TextMuted)
		return
	case len(usage.sessions) == 0:
		ui.Text(c, "这段时间内没有会话调用它").FontSize(13).TextColor(t.TextMuted)
		return
	}
	muted := token(t, "ink-3")
	ui.Column(c).Label("使用会话").Children(func() {
		for _, use := range usage.sessions {
			label := "使用会话 · " + use.label
			b := ui.ButtonBase(c.Key(use.id+use.label)).Label(label).Padding(8, 6).Gap(10).AlignItems(ui.Center).
				BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Disabled(use.id == "")
			if b.Hovered() {
				b.Background(t.SurfaceHover)
			}
			b.Children(func() {
				withTip(c, ui.Text(c, use.label).FontSize(13).Grow(1).MinWidth(0).SingleLine(), use.label)
				number(c, fmt.Sprintf("%s · %d 次", use.last.Local().Format("01/02 15:04"), use.calls)).FontSize(11.5).TextColor(muted)
			})
			if b.Clicked() {
				a.openSession(use.id)
				c.Invalidate()
			}
		}
	})
}

// skillUsageFor is a skill's calls in the usage period, and whether its
// source records calls at all.
func (a *App) skillUsageFor(e skill.Entry) (*skillUsageData, int, bool) {
	agent := session.Agent("")
	if e.RootRole == skill.RoleAgent || e.RootRole == skill.RoleBuiltin {
		agent = session.Agent(agentKey(a.home, e.RootPath))
		if skillSupport(agent) != "" {
			return nil, 0, false
		}
	}
	days := []int{7, 30, 90}[max(0, min(a.skillPeriod, 2))]
	return a.sessionActivity().skillUsage(agent, skillNames(e), skillRange(days, time.Now())), days, true
}
