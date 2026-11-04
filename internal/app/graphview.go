package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/skill"
)

const (
	nodeW    = 196
	nodeH    = 34
	colGap   = 88
	rowGap   = 12
	graphPad = 24
)

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

func (a *App) inGraph() bool {
	if !a.browsePage() {
		return false
	}
	return a.state(a.page).view == 1
}

// browsePage is a page that lists the skills of one directory.
func (a *App) browsePage() bool {
	return a.page == "pool" || strings.HasPrefix(a.page, "root:") || strings.HasPrefix(a.page, "repo:")
}

func (a *App) showGraph(name string) {
	ps := a.state(a.page)
	ps.view = 1
	ps.node = name
}

func (a *App) graphPane(c *ui.Context) {
	t := c.Theme()
	ps := a.state(a.page)
	ui.Column(c).Fill().Children(func() {
		root, ok := a.rootByPage(a.page)
		if !ok || a.snap == nil {
			ui.Text(c, a.emptyText()).Padding(16).TextColor(t.TextMuted)
			return
		}
		g := a.graphFor(root.ID)
		if state := a.graphs[root.ID]; state != nil {
			if state.loading {
				ui.Text(c, "正在读取正文依赖…").Padding(10, 16).FontSize(12).TextColor(t.TextMuted)
			}
			if state.err != "" {
				ui.Text(c, state.err).Padding(10, 16).FontSize(12).TextColor(t.Warning)
			}
		}
		if len(g.Nodes) == 0 {
			ui.Text(c, "这里的 skill 之间没有发现依赖。").Padding(16, 16, 0, 16).Bold()
			ui.Text(c, "列表只读取头部 dependencies / requires 字段。打开依赖图后，才读取正文并推断其中提到的其他 skill。").
				Padding(6, 16, 0, 16).FontSize(12).TextColor(t.TextMuted)
			return
		}
		skills := 0
		missing := 0
		for _, n := range g.Nodes {
			switch n.Kind {
			case skill.NodeSkill:
				skills++
			case skill.NodeMissing:
				missing++
			}
		}
		line := fmt.Sprintf("%d 个 skill 有依赖关系 · %d 个互相独立", skills, len(g.Isolated))
		if missing > 0 {
			line += fmt.Sprintf(" · %d 个依赖找不到", missing)
		}
		ui.Text(c, line).Padding(10, 16, 0, 16).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Padding(8, 16, 6, 16).Gap(16).AlignItems(ui.Center).Children(func() {
			a.legend(c, "使用", t.TextMuted, false)
			a.legend(c, "包含子 skill", t.TextMuted, true)
			a.legend(c, "循环依赖", t.Danger, true)
		})
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
			if s.width != r.W || s.height != r.H {
				s.width, s.height = r.W, r.H
				x, y := s.x, s.y
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
					a.graphNode(c, ps, n, s.positions[n.Name], highlight)
				}
			})
		})
		ui.Row(c).Padding(4, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "拖拽平移 · 滚轮 / 捏合缩放").FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			ui.Toolbar(c, func() {
				if ui.Button(c, "−").Size(28, 28).Padding(0).Label("缩小依赖图").Disabled(s.zoom <= graphMinZoom).Clicked() {
					s.zoomAt(s.zoom/1.2, s.width/2, s.height/2)
				}
				if ui.Button(c, fmt.Sprintf("%.0f%%", s.zoom*100)).Width(54).Height(28).Padding(0).Label("重置依赖图缩放").Tooltip("重置到 100%").Clicked() {
					s.zoom, s.x, s.y = 1, 0, 0
				}
				if ui.Button(c, "+").Size(28, 28).Padding(0).Label("放大依赖图").Disabled(s.zoom >= graphMaxZoom).Clicked() {
					s.zoomAt(s.zoom*1.2, s.width/2, s.height/2)
				}
				if ui.Button(c, "适应").Height(28).Padding(0, 8).Label("适应全图").Clicked() {
					s.fit()
				}
			}).Padding(0).Shrink(0).Label("依赖图缩放")
		})
	})
}

func (a *App) legend(c *ui.Context, label string, col ui.Color, dashed bool) {
	t := c.Theme()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(28, 10).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			path := (&ui.Path{}).MoveTo(r.X, r.Y+r.H/2).LineTo(r.X+r.W, r.Y+r.H/2)
			if dashed {
				path = path.Cap(ui.ButtCap).Dash(0, 4, 3)
			}
			p.StrokePath(path, 1.5, col)
		})
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
	})
}

func (a *App) drawEdges(c *ui.Context, p *ui.Painter, r ui.Rect, g *skill.Graph, pos map[string]spot, highlight graphHighlight) {
	t := c.Theme()
	muted := t.TextMuted
	muted.A = 140
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
			col, width := muted, float32(1.4)
			switch {
			case e.Cycle:
				col = t.Danger
			case hot:
				col, width = t.Accent, 2
			}
			if hot {
				width = 2.2
			} else if len(highlight.nodes) > 0 {
				col.A = 40
			}
			reach := max((x1-x0)/2, 36)
			path := (&ui.Path{}).MoveTo(x0, y0).CubeTo(x0+reach, y0, x1-reach, y1, x1, y1)
			if e.Kind == skill.EdgeContains || e.Cycle {
				path = path.Cap(ui.ButtCap).Dash(0, 5, 4)
			}
			p.StrokePath(path, width, col)
			arrow := (&ui.Path{}).MoveTo(x1, y1).LineTo(x1-8, y1-4.5).LineTo(x1-8, y1+4.5).Close()
			p.FillPath(arrow, col)
		}
	}
}

func (a *App) graphNode(c *ui.Context, ps *pageState, n skill.Node, at spot, highlight graphHighlight) {
	t := c.Theme()
	chosen := ps.node == n.Name
	border := t.Border
	icon := "book"
	switch n.Kind {
	case skill.NodeMissing:
		border, icon = t.Danger, "missing"
	case skill.NodeChild:
		icon = "nested"
	}
	if chosen || highlight.nodes[n.Name] {
		border = t.Accent
	}
	chip := ui.ButtonBase(c.Key("node-"+n.Name)).Absolute().Left(at.x).Top(at.y).Size(nodeW, nodeH).
		Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(8).Cursor(ui.CursorPointer).Label(n.Name).Tooltip(n.Name)
	bg := t.Surface
	if chosen || highlight.nodes[n.Name] {
		bg = t.Accent.Mix(t.Surface, 0.92)
	}
	if len(highlight.nodes) > 0 && !highlight.nodes[n.Name] {
		chip.Opacity(0.3)
	}
	chip.Background(bg).Border(1, border)
	if n.Kind == skill.NodeMissing {
		chip.BorderStyle(ui.BorderDashed)
	}
	if chip.Clicked() {
		ps.node = n.Name
		c.Invalidate()
	}
	chip.Children(func() {
		col := t.TextMuted
		if n.Kind == skill.NodeMissing {
			col = t.Danger
		}
		ui.Icon(c, glyph(icon)).FontSize(14).TextColor(col).Shrink(0)
		ui.Text(c, n.Name).Grow(1).SingleLine().FontSize(13)
	})
}

// graphDetail is the right-hand pane of the dependency graph.
func (a *App) graphDetail(c *ui.Context) {
	t := c.Theme()
	ps := a.state(a.page)
	root, ok := a.rootByPage(a.page)
	ui.Scroll(c).Fill().Children(func() {
		if !ok || a.snap == nil || ps.node == "" {
			ui.Text(c, "点一个节点，看它依赖什么、被谁依赖。").Padding(20).TextColor(t.TextMuted)
			return
		}
		g := a.graphFor(root.ID)
		n, found := g.Node(ps.node)
		if !found {
			ui.Text(c, "点一个节点，看它依赖什么、被谁依赖。").Padding(20).TextColor(t.TextMuted)
			return
		}
		switch n.Kind {
		case skill.NodeSkill:
			a.detail(c, a.entryRow(n.Entry))
		case skill.NodeMissing:
			ui.Column(c).Padding(22, 28).Gap(12).MaxWidth(640).Children(func() {
				ui.Text(c, n.Name).FontSize(22).Bold()
				ui.Text(c, "找不到").TextColor(t.Danger)
				ui.Text(c, "有 skill 把它写成依赖，但是这个目录里没有同名的 skill。").FontSize(13)
				a.neighbours(c, g, n.Name)
			})
		default:
			ui.Column(c).Padding(22, 28).Gap(12).MaxWidth(640).Children(func() {
				ui.Text(c, n.Name).FontSize(22).Bold()
				ui.Text(c, "子 skill").TextColor(t.TextMuted)
				ui.Text(c, "它放在上级 skill 的 skills/ 子目录里，随上级 skill 一起使用。").FontSize(13)
				a.neighbours(c, g, n.Name)
			})
		}
	})
}

// neighbours says who points at a node and what it points at.
func (a *App) neighbours(c *ui.Context, g *skill.Graph, name string) {
	t := c.Theme()
	var parents []string
	for _, e := range g.Edges {
		if e.To == name && e.Kind == skill.EdgeContains {
			parents = append(parents, e.From)
		}
	}
	if len(parents) > 0 {
		ui.Text(c, "位于 "+strings.Join(parents, "、")).FontSize(12).TextColor(t.TextMuted)
	}
	if by := g.NeededBy(name); len(by) > 0 {
		ui.Text(c, "被 "+strings.Join(by, "、")+" 依赖").FontSize(12).TextColor(t.TextMuted)
	}
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
