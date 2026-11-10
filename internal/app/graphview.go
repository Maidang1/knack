package app

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

	"knack/internal/skill"
)

const (
	nodeW    = 232
	nodeH    = 36
	colGap   = 72
	rowGap   = 24
	graphPad = 32
)

// graphPage is the design's 依赖图: the files of one skill and what they
// point at, with the chosen file's references beside it.
const graphPage = "graph"

type spot struct{ x, y float32 }

// placeGraph gives each node its top left corner inside a canvas, and the
// canvas its size. Columns are centered on the tallest one.
func placeGraph(g *skill.Graph) (map[string]spot, float32, float32) {
	count := make([]int, g.Layers)
	for _, n := range g.Nodes {
		count[n.Layer]++
	}
	step := float32(nodeH + rowGap)
	pos := make(map[string]spot, len(g.Nodes))
	for _, n := range g.Nodes {
		free := float32(g.Rows-count[n.Layer]) * step / 2
		pos[n.Name] = spot{
			x: graphPad + float32(n.Layer)*(nodeW+colGap),
			y: graphPad + free + float32(n.Row)*step,
		}
	}
	w := 2*graphPad + float32(g.Layers)*nodeW + float32(max(g.Layers-1, 0))*colGap
	h := 2*graphPad + float32(g.Rows)*nodeH + float32(max(g.Rows-1, 0))*rowGap
	return pos, w, h
}

// browsePage is a page that lists the skills of one directory.
func (a *App) browsePage() bool {
	return a.page == "pool" || strings.HasPrefix(a.page, "root:") || strings.HasPrefix(a.page, "repo:")
}

// showGraph opens the 依赖图 page on a skill.
func (a *App) showGraph(e skill.Entry) {
	a.graphPick = e.Path
	a.page = graphPage
	a.state(graphPage).node = "SKILL.md"
}

// graphEntry is the skill the 依赖图 page shows: the one picked, else the
// first of the library.
func (a *App) graphEntry() (skill.Entry, bool) {
	entries := a.libraryEntries()
	for _, e := range entries {
		if e.Path == a.graphPick {
			return e, true
		}
	}
	for _, e := range entries {
		if e.HasDocument {
			a.graphPick = e.Path
			return e, true
		}
	}
	return skill.Entry{}, false
}

// fileGraphFor reads a skill's files in the background; until they are
// read it returns nil.
func (a *App) fileGraphFor(e skill.Entry) *graphState {
	key := fmt.Sprintf("file:%s|%s|%d", e.Path, e.TreeHash, e.DocModTime.UnixNano())
	if state := a.graphs[key]; state != nil {
		return state
	}
	state := &graphState{loading: true}
	a.graphs[key] = state
	scope := append([]skill.Entry(nil), a.libraryEntries()...)
	run := func() {
		g, err := skill.FileGraph(e, scope)
		a.post(func() {
			state.loading = false
			state.graph = g
			if err != nil {
				state.err = "无法读取这个技能的目录：" + err.Error()
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
	return state
}

// graphPicker chooses the skill in the content bar.
func (a *App) graphPicker(c *ui.Context) {
	t := c.Theme()
	e, ok := a.graphEntry()
	name := "选择技能"
	if ok {
		name = e.Name
	}
	a.dropdown(c, "graph-pick", func(m *menu) {
		entries := a.libraryEntries()
		count := map[string]int{}
		for _, x := range entries {
			count[x.Name]++
		}
		for _, x := range entries {
			if !x.HasDocument {
				continue
			}
			label := x.Name
			if count[x.Name] > 1 {
				label += " · " + x.RootName
			}
			if m.Item(label).Checked(x.Path == a.graphPick).Chosen() {
				a.showGraph(x)
				c.Invalidate()
			}
		}
	}, func(b ui.Element) {
		action.StyleButton(c, b, "", action.ButtonProps{Size: lobe.Small}).Height(28).Gap(6).
			Label("依赖图技能：" + name).MaxWidth(260).Children(func() {
			ui.Text(c, name).FontSize(13).MinWidth(0).SingleLine()
			ui.Icon(c, glyph("chevron-down")).FontSize(11.5).TextColor(token(t, "ink-3")).Shrink(0)
		})
	})
}

// graphLegend names the three kinds of line.
func (a *App) graphLegend(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(14).AlignItems(ui.Center).Shrink(0).Children(func() {
		a.legend(c, "明确引用", t.TextMuted, false)
		a.legend(c, "正文推断", token(t, "line-strong"), false)
		a.legend(c, "缺失", t.Danger, false)
	})
}

func (a *App) graphPage(c *ui.Context) {
	t := c.Theme()
	ps := a.state(graphPage)
	e, ok := a.graphEntry()
	if !ok || a.snap == nil {
		emptyState(c, "没有技能", "graph", "还没有可以查看的技能", a.emptyText(), nil)
		return
	}
	state := a.fileGraphFor(e)
	if state.graph == nil {
		ui.Column(c).Fill().Center().Children(func() {
			if state.err != "" {
				ui.Text(c, state.err).FontSize(13).TextColor(t.Warning)
			} else {
				feedback.Spin(c, feedback.SpinProps{CustomSize: 16}, nil).Label("正在读取技能目录")
			}
		})
		return
	}
	g := state.graph
	if _, found := g.Node(ps.node); !found {
		ps.node = "SKILL.md"
	}
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.graphCanvas(c, ps, e, g) })
		width, _ := c.Size()
		if width-a.sidebarWidth() >= 760 {
			ui.Column(c).Width(300).Shrink(0).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() { a.graphDetail(c, e, g, ps.node) })
		}
	})
}

func (a *App) graphCanvas(c *ui.Context, ps *pageState, e skill.Entry, g *skill.Graph) {
	t := c.Theme()
	s := &ps.graph
	s.prepare(g)
	viewport := ui.Box(c.Key("graph-viewport")).Grow(1).MinHeight(0).Clip().Focusable().Label("依赖图画布").
		Description("拖拽空白处平移，滚轮缩放；触摸板双指平移、捏合缩放。方向键平移，+/- 缩放，0 重置，Home 适应全图。").
		HandleInput(s.input).Cursor(ui.CursorGrab)
	if s.dragging && !viewport.Pressed() {
		s.dragging = false
	}
	if s.dragging {
		viewport.Cursor(ui.CursorGrabbing)
	}
	x, y, over := viewport.PointerPosition()
	s.hover = ""
	if over && viewport.Hovered() && !s.dragging {
		s.hover = s.nodeAt(x, y)
	}
	focus := s.hover
	if focus == "" {
		focus = ps.node
	}
	highlight := s.highlightPath(focus)
	viewport.Draw(func(_ *ui.Painter, r ui.Rect) {
		if s.width != r.W || s.height != r.H || !s.placed {
			s.width, s.height = r.W, r.H
			x, y := s.x, s.y
			if !s.placed {
				// The first view shows the whole graph, centered.
				s.placed = true
				s.fit()
			}
			s.constrain()
			if x != s.x || y != s.y {
				c.Invalidate()
			}
		}
	})
	viewport.Children(func() {
		canvas := ui.Box(c).Absolute().Size(s.contentW, s.contentH).TransformOrigin(0, 0).
			Transform(ui.Scaling(s.zoom, s.zoom).Then(ui.Translation(s.x, s.y)))
		canvas.Draw(func(p *ui.Painter, r ui.Rect) { a.drawEdges(c, p, r, g, s.positions, highlight) })
		canvas.Children(func() {
			for _, n := range g.Nodes {
				a.graphNode(c, ps, g, n, s.positions[n.Name], highlight)
			}
		})
	})
	ui.Row(c).Padding(10, 24, 14, 24).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, glyph("info")).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
		note := "只分析同一技能目录内的文件；指向其他技能的引用来自正文推断。"
		if n := len(g.Isolated); n > 0 {
			note += fmt.Sprintf("另有 %d 个文件没有被引用。", n)
		}
		withTip(c, ui.Text(c, note).FontSize(12).TextColor(token(t, "ink-3")).Grow(1).MinWidth(0).SingleLine(), strings.Join(g.Isolated, "\n"))
		a.zoomControls(c, s)
	})
}

func (a *App) zoomControls(c *ui.Context, s *graphCanvasState) {
	// Lobe UI's action icon group: borderless icons on one bordered surface.
	tk := lobe.TokensOf(c)
	ui.Toolbar(c, func() {
		zoom := func(icon, label string) ui.Element {
			return action.ActionIcon(c.Key("zoom:"+label), glyph(icon), action.ActionIconProps{BlockSize: 26, GlyphSize: 14}).Label(label)
		}
		if zoom("minus", "缩小依赖图").Disabled(s.zoom <= graphMinZoom).Clicked() {
			s.zoomAt(s.zoom/1.2, s.width/2, s.height/2)
		}
		reset := withTip(c, action.StyleButton(c, ui.ButtonBase(c.Key("zoom:reset")), "", action.ButtonProps{Variant: action.Text, Size: lobe.Small}).
			Size(48, 26).Padding(0).Justify(ui.Center).Label("重置依赖图缩放"), "重置到 100%")
		reset.Children(func() { number(c, fmt.Sprintf("%.0f%%", s.zoom*100)).FontSize(12) })
		if reset.Clicked() {
			s.zoom, s.x, s.y = 1, 0, 0
		}
		if zoom("plus", "放大依赖图").Disabled(s.zoom >= graphMaxZoom).Clicked() {
			s.zoomAt(s.zoom*1.2, s.width/2, s.height/2)
		}
		if withTip(c, zoom("expand", "适应全图"), "适应全图").Clicked() {
			s.fit()
		}
	}).Padding(2).Gap(2).Radius(tk.BorderRadius).Background(tk.BgContainer).Border(1, tk.BorderSecondary).Shrink(0).Label("依赖图缩放")
}

func (a *App) legend(c *ui.Context, label string, col ui.Color, dashed bool) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(20, 10).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			path := (&ui.Path{}).MoveTo(r.X, r.Y+r.H/2).LineTo(r.X+r.W, r.Y+r.H/2)
			if dashed {
				path = path.Cap(ui.ButtCap).Dash(0, 4, 3)
			}
			p.StrokePath(path, 1.5, col)
		})
		ui.Text(c, label).FontSize(13).TextColor(t.TextMuted)
	})
}

func (a *App) drawEdges(c *ui.Context, p *ui.Painter, r ui.Rect, g *skill.Graph, pos map[string]spot, highlight graphHighlight) {
	t := c.Theme()
	missing := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Kind == skill.NodeMissing || n.Kind == skill.NodeMissingFile {
			missing[n.Name] = true
		}
	}
	for pass := 0; pass < 2; pass++ {
		// The highlighted chain goes last, over unrelated edges.
		for i, e := range g.Edges {
			from, ok1 := pos[e.From]
			to, ok2 := pos[e.To]
			if !ok1 || !ok2 {
				continue
			}
			hot := highlight.edges[i]
			if hot != (pass == 1) {
				continue
			}
			x0, y0 := r.X+from.x+nodeW, r.Y+from.y+nodeH/2
			x1, y1 := r.X+to.x, r.Y+to.y+nodeH/2
			col, width := t.TextMuted, float32(1.2)
			switch {
			case e.Cycle || missing[e.To]:
				col = t.Danger
			case e.Inferred:
				col = token(t, "line-strong")
			case hot:
				col = t.Accent
			}
			if hot {
				width = 1.8
			} else if len(highlight.nodes) > 0 {
				col.A = 70
			}
			reach := max((x1-x0)/2, 36)
			path := (&ui.Path{}).MoveTo(x0, y0).CubeTo(x0+reach, y0, x1-reach, y1, x1, y1)
			if e.Kind == skill.EdgeContains || e.Cycle {
				path = path.Cap(ui.ButtCap).Dash(0, 5, 4)
			}
			p.StrokePath(path, width, col)
		}
	}
}

// nodeGlyph is a node's icon: a script, a document, another skill, or a
// path that is not there.
func nodeGlyph(n skill.Node) string {
	switch n.Kind {
	case skill.NodeMissingFile:
		return "file-question"
	case skill.NodeMissing:
		return "missing"
	case skill.NodeSkill:
		return "pool"
	case skill.NodeChild:
		return "nested"
	}
	switch strings.ToLower(path.Ext(n.Name)) {
	case ".sh", ".bash", ".zsh", ".py", ".js", ".mjs", ".cjs", ".ts", ".rb", ".go":
		return "terminal"
	}
	return "file"
}

// inferredOnly reports that every reference to a node is read from prose.
func inferredOnly(g *skill.Graph, name string) bool {
	seen := false
	for _, e := range g.Edges {
		if e.To == name {
			if !e.Inferred {
				return false
			}
			seen = true
		}
	}
	return seen
}

// graphNode is a file of the skill, drawn as the design's chip: mono path,
// accent when chosen, red and dashed when it is not there.
func (a *App) graphNode(c *ui.Context, ps *pageState, g *skill.Graph, n skill.Node, at spot, highlight graphHighlight) {
	t := c.Theme()
	chosen := ps.node == n.Name
	lit := highlight.nodes[n.Name]
	gone := n.Kind == skill.NodeMissing || n.Kind == skill.NodeMissingFile
	chip := withTip(c, ui.ButtonBase(c.Key("node-"+n.Name)).Absolute().Left(at.x).Top(at.y).Size(nodeW, nodeH).
		Padding(0, 12).Gap(8).AlignItems(ui.Center).Radius(7).Cursor(ui.CursorPointer).Label(n.Name), n.Name)
	fg, iconColor := t.Text, t.TextMuted
	switch {
	case chosen && n.Name == "SKILL.md":
		chip.Background(t.Accent).Border(1, t.Accent)
		fg, iconColor = t.AccentText, t.AccentText
	case gone:
		chip.Background(token(t, "danger-bg")).Border(1, t.Danger)
		fg, iconColor = t.Danger, t.Danger
	case chosen:
		chip.Background(token(t, "accent-bg")).Border(2, t.Accent)
	case n.Name == "SKILL.md":
		chip.Background(t.Text).Border(1, t.Text)
		fg, iconColor = token(t, "on-ink"), token(t, "on-ink")
	case lit:
		chip.Background(t.Surface).Border(1.5, t.Accent)
	default:
		chip.Background(t.Surface).Border(1, token(t, "line-strong"))
	}
	if len(highlight.nodes) > 0 && !lit && !chosen {
		chip.Opacity(0.4)
	}
	if chip.Clicked() {
		ps.node = n.Name
		c.Invalidate()
	}
	chip.Children(func() {
		ui.Icon(c, glyph(nodeGlyph(n))).FontSize(13).TextColor(iconColor).Shrink(0)
		ui.Text(c, n.Name).Font(monoFont).FontSize(13).TextColor(fg).Grow(1).MinWidth(0).SingleLine()
		if n.Kind == skill.NodeSkill && inferredOnly(g, n.Name) {
			ui.Text(c, "推断").FontSize(11).TextColor(token(t, "ink-3")).Shrink(0)
		}
	})
}

// graphDetail is the chosen node: where it is, who points at it and what it
// points at, and the files the skill names that are not there.
func (a *App) graphDetail(c *ui.Context, e skill.Entry, g *skill.Graph, name string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	n, ok := g.Node(name)
	ui.Scroll(c.Key("graph-detail")).Fill().Label("依赖图节点详情").Children(func() {
		ui.Column(c).Padding(24, 24).Gap(18).Children(func() {
			if !ok {
				ui.Text(c, "点一个节点，看它引用了什么、被谁引用。").FontSize(13).TextColor(t.TextMuted)
				return
			}
			ui.Column(c).Gap(5).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph(nodeGlyph(n))).FontSize(15).TextColor(t.TextMuted).Shrink(0)
					withTip(c, ui.Text(c, path.Base(n.Name)).FontSize(15).FontWeight(600).MinWidth(0).SingleLine(), n.Name)
				})
				sub := ""
				switch n.Kind {
				case skill.NodeFile:
					sub = e.Name + "/" + n.Name + " · " + byteSize(n.Size)
				case skill.NodeMissingFile:
					sub = e.Name + "/" + n.Name + " · 目录里没有这个文件"
				case skill.NodeSkill:
					sub = n.Entry.RootName + " · " + shortPath(a.home, n.Entry.Path)
				case skill.NodeMissing:
					sub = "技能库里没有这个技能"
				}
				withTip(c, number(c, sub).FontSize(12).TextColor(muted).MinWidth(0), sub)
			})
			var by, uses [][2]string
			for _, edge := range g.Edges {
				if edge.To == name {
					where := "推断"
					if edge.Line > 0 {
						where = fmt.Sprintf("第 %d 行", edge.Line)
					}
					by = append(by, [2]string{edge.From, where})
				}
				if edge.From == name {
					uses = append(uses, [2]string{edge.To, edge.Via})
				}
			}
			refList(c, "被谁引用", by)
			refList(c, "引用了", uses)
			a.missingCallout(c, g)
			switch n.Kind {
			case skill.NodeFile:
				dir := e.Path
				if e.RealPath != "" {
					dir = e.RealPath
				}
				file := filepath.Join(dir, filepath.FromSlash(n.Name))
				if withTip(c, outlineButton(c, "graph-open", "用默认编辑器打开").Height(28).AlignSelf(ui.Start), file).Clicked() {
					a.openPath(file)
				}
			case skill.NodeSkill:
				target := n.Entry
				if outlineButton(c, "graph-follow", "查看它的依赖图").Height(28).AlignSelf(ui.Start).Clicked() {
					a.showGraph(target)
					c.Invalidate()
				}
			}
		})
	})
}

// refList is a titled list of paths, each with a note at its end.
func refList(c *ui.Context, title string, items [][2]string) {
	if len(items) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).Label(title).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(t.TextMuted).Padding(0, 0, 6, 0)
		for _, item := range items {
			ui.Row(c).Padding(7, 0).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				withTip(c, ui.Text(c, item[0]).Font(monoFont).FontSize(13).Grow(1).MinWidth(0).SingleLine(), item[0])
				ui.Text(c, item[1]).FontSize(11.5).TextColor(token(t, "ink-3")).Shrink(0)
			})
		}
	})
}

// missingCallout states the paths the skill names that are not there.
func (a *App) missingCallout(c *ui.Context, g *skill.Graph) {
	var lines []string
	files, skills := 0, 0
	for _, n := range g.Nodes {
		switch n.Kind {
		case skill.NodeMissingFile:
			files++
		case skill.NodeMissing:
			skills++
		default:
			continue
		}
		for _, edge := range g.Edges {
			if edge.To != n.Name {
				continue
			}
			where := edge.From
			if edge.Line > 0 {
				where += fmt.Sprintf(" 第 %d 行", edge.Line)
			}
			if n.Kind == skill.NodeMissingFile {
				lines = append(lines, where+"引用了 "+n.Name+"，目录里没有这个文件。")
			} else {
				lines = append(lines, where+"依赖 "+n.Name+"，技能库里没有这个技能。")
			}
		}
	}
	if len(lines) == 0 {
		return
	}
	var title []string
	if files > 0 {
		title = append(title, fmt.Sprintf("%d 个缺失文件", files))
	}
	if skills > 0 {
		title = append(title, fmt.Sprintf("%d 个找不到的技能", skills))
	}
	feedback.Alert(c, "这个技能有 "+strings.Join(title, "、"), feedback.AlertProps{Type: feedback.Error, Description: strings.Join(lines, "\n")}).
		FontSize(13).Label("缺失引用")
}

func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// depTextLines are the dependency lines of the detail pane's info card.
func (a *App) depTextLines(c *ui.Context, e skill.Entry) []detailLine {
	if a.snap == nil {
		return nil
	}
	t := c.Theme()
	g := a.snap.GraphFor(e.RootID)
	needs, by := g.Needs(e.Name), g.NeededBy(e.Name)
	var out []detailLine
	if len(needs) > 0 {
		out = append(out, detailLine{"依赖 " + strings.Join(needs, "、"), t.TextMuted})
	}
	if len(by) > 0 {
		out = append(out, detailLine{"被 " + strings.Join(by, "、") + " 依赖", t.TextMuted})
	}
	var gone []string
	for _, name := range needs {
		if n, ok := g.Node(name); ok && n.Kind == skill.NodeMissing {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		out = append(out, detailLine{"找不到 " + strings.Join(gone, "、"), t.Danger})
	}
	return out
}
