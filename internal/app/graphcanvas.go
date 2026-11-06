package app

import (
	"math"

	"github.com/egoist/mygo/ui"

	"knack/internal/skill"
)

const (
	graphMinZoom = 0.1
	graphMaxZoom = 3
)

type graphHighlight struct {
	nodes map[string]bool
	edges map[int]bool
}

// Each page keeps its own camera. Geometry and adjacency are rebuilt only
// when the graph changes; walking a highlighted path never reads disk.
type graphCanvasState struct {
	zoom, x, y         float32
	width, height      float32
	contentW, contentH float32
	dragging, placed   bool
	last               ui.Point
	hover, focus       string
	graph              *skill.Graph
	positions          map[string]spot
	in, out            map[string][]int
	highlight          graphHighlight
}

func (s *graphCanvasState) prepare(g *skill.Graph) {
	if s.zoom == 0 {
		s.zoom = 1
	}
	if s.graph == g {
		return
	}
	s.graph = g
	s.positions, s.contentW, s.contentH = placeGraph(g)
	s.in, s.out = map[string][]int{}, map[string][]int{}
	for i, e := range g.Edges {
		s.out[e.From] = append(s.out[e.From], i)
		s.in[e.To] = append(s.in[e.To], i)
	}
	s.hover, s.focus = "", ""
	s.highlight = graphHighlight{}
	s.placed = false
	s.constrain()
}

func (s *graphCanvasState) constrain() {
	if s.width <= 0 || s.height <= 0 {
		return
	}
	// Keep an edge of the graph within reach, even after a resize.
	dx, dy := s.width-s.contentW*s.zoom, s.height-s.contentH*s.zoom
	s.x = max(min(float32(0), dx)-64, min(s.x, max(float32(64), dx)))
	s.y = max(min(float32(0), dy)-64, min(s.y, max(float32(64), dy)))
}

func (s *graphCanvasState) zoomAt(value, x, y float32) {
	if value <= 0 || math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
		return
	}
	z := max(float32(graphMinZoom), min(value, graphMaxZoom))
	ratio := z / s.zoom
	s.x, s.y = x-(x-s.x)*ratio, y-(y-s.y)*ratio
	s.zoom = z
	s.constrain()
}

func (s *graphCanvasState) fit() {
	if s.width <= 0 || s.height <= 0 || s.contentW <= 0 || s.contentH <= 0 {
		return
	}
	s.zoom = max(float32(graphMinZoom), min(float32(1), min(s.width/s.contentW, s.height/s.contentH)))
	s.x, s.y = (s.width-s.contentW*s.zoom)/2, (s.height-s.contentH*s.zoom)/2
}

func (s *graphCanvasState) nodeAt(x, y float32) string {
	x, y = (x-s.x)/s.zoom, (y-s.y)/s.zoom
	for name, p := range s.positions {
		if x >= p.x && x <= p.x+nodeW && y >= p.y && y <= p.y+nodeH {
			return name
		}
	}
	return ""
}

func (s *graphCanvasState) highlightPath(focus string) graphHighlight {
	if s.focus == focus {
		return s.highlight
	}
	s.focus = focus
	h := graphHighlight{}
	if _, ok := s.positions[focus]; ok {
		h.nodes, h.edges = map[string]bool{focus: true}, map[int]bool{}
		// Walk each direction independently. A sibling that happens to
		// share a dependency is not on a path through the focused node.
		for _, upstream := range []bool{false, true} {
			seen := map[string]bool{focus: true}
			queue := []string{focus}
			for at := 0; at < len(queue); at++ {
				edges := s.out[queue[at]]
				if upstream {
					edges = s.in[queue[at]]
				}
				for _, i := range edges {
					e := s.graph.Edges[i]
					next := e.To
					if upstream {
						next = e.From
					}
					h.edges[i], h.nodes[next] = true, true
					if !seen[next] {
						seen[next] = true
						queue = append(queue, next)
					}
				}
			}
		}
	}
	s.highlight = h
	return h
}

func (s *graphCanvasState) input(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputZoom:
		s.zoomAt(s.zoom*ev.Scale, ev.X, ev.Y)
		return true
	case ui.InputScroll:
		if !ev.Precise || ev.Mods&(ui.Ctrl|ui.Cmd) != 0 {
			s.zoomAt(s.zoom*float32(math.Exp(float64(-ev.DY)*0.004)), ev.X, ev.Y)
		} else {
			s.x, s.y = s.x-ev.DX, s.y-ev.DY
			s.constrain()
		}
		return true
	case ui.InputPointerDown:
		if ev.Button == 2 || ev.Button == 0 && s.nodeAt(ev.X, ev.Y) == "" {
			s.dragging, s.last = true, ui.Point{X: ev.X, Y: ev.Y}
			return true
		}
	case ui.InputPointerMove:
		if s.dragging {
			s.x, s.y = s.x+ev.X-s.last.X, s.y+ev.Y-s.last.Y
			s.last = ui.Point{X: ev.X, Y: ev.Y}
			s.constrain()
			return true
		}
	case ui.InputPointerUp:
		if s.dragging {
			s.dragging = false
			return true
		}
	case ui.InputKeyDown:
		switch ev.Key {
		case ui.KeyEqual:
			s.zoomAt(s.zoom*1.2, s.width/2, s.height/2)
		case ui.KeyMinus:
			s.zoomAt(s.zoom/1.2, s.width/2, s.height/2)
		case ui.Key0:
			s.zoom, s.x, s.y = 1, 0, 0
		case ui.KeyHome:
			s.fit()
		case ui.KeyLeft:
			s.x += 40
		case ui.KeyRight:
			s.x -= 40
		case ui.KeyUp:
			s.y += 40
		case ui.KeyDown:
			s.y -= 40
		default:
			return false
		}
		s.constrain()
		return true
	}
	return false
}
