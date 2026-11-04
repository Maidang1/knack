package app

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skill-manager/internal/skill"
)

func graphFixture(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	home := t.TempDir()
	for name, deps := range map[string]string{"alpha": "beta, side", "beta": "gamma", "gamma": "", "side": "", "other": "gamma"} {
		dir := filepath.Join(home, ".agents", "skills", name)
		writeSkill(t, dir, name)
		body := fmt.Sprintf("---\nname: %s\ndescription: %s\nrequires: [%s]\n---\n", name, name, deps)
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := New(home)
	a.startScan()
	a.page = "pool"
	a.state("pool").view = 1
	tt := ui.NewTester(a.View, 1180, 760)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	return a, tt
}

func near(t *testing.T, name string, got, want float32) {
	t.Helper()
	// Transformed text and element bounds snap to subpixel positions.
	if math.Abs(float64(got-want)) > 0.5 {
		t.Fatalf("%s = %.3f, want %.3f", name, got, want)
	}
}

func center(t *testing.T, tt *ui.Tester, name string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing %s: %+v", name, r)
	}
	return r.X + r.W/2, r.Y + r.H/2
}

func TestGraphHighlightPaths(t *testing.T) {
	g := skill.BuildGraph([]skill.Entry{
		{Name: "a", Requires: []string{"b", "side"}},
		{Name: "b", Requires: []string{"c"}, Children: []string{"nested"}},
		{Name: "c", Requires: []string{"missing"}},
		{Name: "other", Requires: []string{"c"}},
		{Name: "side"},
	})
	s := graphCanvasState{}
	s.prepare(g)
	h := s.highlightPath("b")
	for _, name := range []string{"a", "b", "c", "missing", "nested"} {
		if !h.nodes[name] {
			t.Errorf("path omitted %s", name)
		}
	}
	for _, name := range []string{"other", "side"} {
		if h.nodes[name] {
			t.Errorf("path included unrelated branch %s", name)
		}
	}
	for i, e := range g.Edges {
		want := e.From != "other" && e.To != "side"
		if h.edges[i] != want {
			t.Errorf("edge %+v highlighted=%t, want %t", e, h.edges[i], want)
		}
	}
	if len(s.highlightPath("").nodes) != 0 {
		t.Fatal("highlight did not clear")
	}
	cycle := skill.BuildGraph([]skill.Entry{
		{Name: "a", Requires: []string{"b"}},
		{Name: "b", Requires: []string{"a", "missing"}},
	})
	s.prepare(cycle)
	h = s.highlightPath("a")
	if len(h.nodes) != 3 || len(h.edges) != 3 {
		t.Fatalf("cycle traversal: %+v", h)
	}
	s.prepare(g)
	if len(s.highlightPath("a").nodes) != 6 {
		t.Fatal("graph replacement reused stale highlight")
	}
}

func TestGraphCameraGeometry(t *testing.T) {
	s := graphCanvasState{zoom: 1, x: -100, y: -120, width: 600, height: 400, contentW: 1600, contentH: 1200}
	x, y := float32(280), float32(160)
	wx, wy := (x-s.x)/s.zoom, (y-s.y)/s.zoom
	s.zoomAt(2, x, y)
	near(t, "anchored x", (x-s.x)/s.zoom, wx)
	near(t, "anchored y", (y-s.y)/s.zoom, wy)
	s.zoomAt(200, x, y)
	near(t, "maximum zoom", s.zoom, graphMaxZoom)
	s.zoomAt(0.001, x, y)
	near(t, "minimum zoom", s.zoom, graphMinZoom)
	s.zoomAt(float32(math.NaN()), x, y)
	near(t, "invalid zoom ignored", s.zoom, graphMinZoom)
	s.fit()
	if s.contentW*s.zoom > s.width+1 || s.contentH*s.zoom > s.height+1 {
		t.Fatal("fit clips graph")
	}
}

func TestGraphKeyboardButtonsAndPageState(t *testing.T) {
	a, tt := graphFixture(t)
	s := &a.state("pool").graph
	viewport, _ := tt.Find("依赖图画布")
	tt.ClickAt(viewport.X+100, viewport.Y+viewport.H-40)
	tt.Key(0, ui.KeyEqual)
	near(t, "keyboard zoom", s.zoom, 1.2)
	tt.Key(0, ui.Key0)
	near(t, "keyboard reset", s.zoom, 1)
	tt.Key(0, ui.KeyRight)
	near(t, "keyboard pan", s.x, -40)
	tt.Key(0, ui.KeyHome)
	if s.contentW*s.zoom > s.width+1 {
		t.Fatal("keyboard fit failed")
	}
	z := s.zoom
	click(t, tt, "放大依赖图")
	near(t, "zoom button", s.zoom, z*1.2)
	click(t, tt, "缩小依赖图")
	near(t, "zoom out button", s.zoom, z)
	x, y := s.x, s.y
	click(t, tt, "列表")
	click(t, tt, "依赖图")
	near(t, "return zoom", s.zoom, z)
	near(t, "return x", s.x, x)
	near(t, "return y", s.y, y)
	other := &a.state("root:other").graph
	other.prepare(s.graph)
	near(t, "other page camera", other.zoom, 1)
	near(t, "other page pan", other.x, 0)
	click(t, tt, "重置依赖图缩放")
	// Tester.Scroll deliberately supplies no modifiers; exercise the
	// Windows Ctrl-scroll event shape directly at the input boundary.
	s.input(ui.InputEvent{Kind: ui.InputScroll, Precise: true, Mods: ui.Ctrl, DY: -30, X: 200, Y: 100})
	tt.Frame()
	if s.zoom <= 1 {
		t.Fatal("Ctrl + precise scroll did not zoom")
	}
	s.zoom = graphMaxZoom
	tt.Frame()
	click(t, tt, "放大依赖图")
	near(t, "disabled zoom in", s.zoom, graphMaxZoom)
	s.zoom = graphMinZoom
	tt.Frame()
	click(t, tt, "缩小依赖图")
	near(t, "disabled zoom out", s.zoom, graphMinZoom)
}

func TestGraphNarrowWindow(t *testing.T) {
	a, tt := graphFixture(t)
	tt.SetSize(880, 560)
	if a.graphW > 880-a.sidebarWidth()-280 {
		t.Fatal("narrow window squeezed the detail pane")
	}
	for _, name := range []string{"缩小依赖图", "重置依赖图缩放", "放大依赖图", "适应全图"} {
		r, ok := tt.Find(name)
		if !ok || r.W < 24 || r.H < 24 || r.X < a.sidebarWidth() || r.X+r.W > a.sidebarWidth()+a.graphW+1 || r.Y+r.H > 560-footerHeight {
			t.Errorf("narrow window clipped %s: %+v", name, r)
		}
	}
	click(t, tt, "适应全图")
	s := &a.state("pool").graph
	if s.contentW*s.zoom > s.width+1 || s.contentH*s.zoom > s.height+1 {
		t.Fatal("narrow fit clips graph")
	}
}

func TestGraphMiddleDragAndReleaseOutside(t *testing.T) {
	s := graphCanvasState{zoom: 1, width: 600, height: 400, contentW: 1600, contentH: 1200}
	if !s.input(ui.InputEvent{Kind: ui.InputPointerDown, Button: 2, X: 200, Y: 100}) {
		t.Fatal("middle button did not start panning")
	}
	s.input(ui.InputEvent{Kind: ui.InputPointerMove, Button: 2, X: 150, Y: 80})
	near(t, "middle pan x", s.x, -50)
	near(t, "middle pan y", s.y, -20)
	s.input(ui.InputEvent{Kind: ui.InputPointerUp, Button: 2})
	if s.dragging {
		t.Fatal("middle button did not release")
	}
	a, tt := graphFixture(t)
	viewport, _ := tt.Find("依赖图画布")
	x, y := viewport.X+30, viewport.Y+viewport.H-20
	tt.Press(x, y)
	tt.Move(20, y)
	tt.Release(20, y)
	if a.state("pool").graph.dragging {
		t.Fatal("release outside left the graph dragging")
	}
}

func TestGraphGesturesAndHover(t *testing.T) {
	a, tt := graphFixture(t)
	gen, snap := a.scanGen, a.snap
	ps := a.state("pool")
	s := &ps.graph
	x, y := center(t, tt, "beta")
	tt.Move(x, y)
	if s.hover != "beta" {
		t.Fatalf("hover=%q", s.hover)
	}
	if !s.highlight.nodes["alpha"] || !s.highlight.nodes["gamma"] || s.highlight.nodes["other"] || s.highlight.nodes["side"] {
		t.Fatalf("hover chain=%v", s.highlight.nodes)
	}
	if ps.node != "" {
		t.Fatal("hover selected a node")
	}
	tt.Move(20, 20)
	if s.hover != "" || len(s.highlight.nodes) != 0 {
		t.Fatal("unselected hover did not clear on exit")
	}
	tt.Move(x, y)
	click(t, tt, "beta")
	if ps.node != "beta" {
		t.Fatal("click stopped selecting nodes")
	}
	tt.Move(20, 20)
	if s.hover != "" || !s.highlight.nodes["gamma"] {
		t.Fatal("leaving graph did not restore selected path")
	}
	x, y = center(t, tt, "beta")
	before, _ := tt.Find("beta")
	tt.Pinch(x, y, 1.25)
	near(t, "pinch zoom", s.zoom, 1.25)
	x2, y2 := center(t, tt, "beta")
	near(t, "pinch node anchor x", x2, x)
	near(t, "pinch node anchor y", y2, y)
	after, _ := tt.Find("beta")
	near(t, "transformed node width", after.W, before.W*1.25)
	if s.hover != "beta" {
		t.Fatal("hover lost after zoom")
	}
	panX, panY := s.x, s.y
	tt.Scroll(x2, y2, 30, 20)
	if s.x != panX-30 || s.y != panY-20 {
		t.Fatal("touchpad deltas were not applied exactly")
	}
	x3, y3 := center(t, tt, "beta")
	near(t, "touchpad pan x", x3, x2-30)
	near(t, "touchpad pan y", y3, y2-20)
	near(t, "pan preserves zoom", s.zoom, 1.25)
	z := s.zoom
	tt.Wheel(x3, y3, 0, -50)
	if s.zoom <= z {
		t.Fatal("mouse wheel did not zoom in")
	}
	click(t, tt, "重置依赖图缩放")
	near(t, "reset zoom", s.zoom, 1)
	near(t, "reset x", s.x, 0)
	near(t, "reset y", s.y, 0)
	viewport, _ := tt.Find("依赖图画布")
	px, py := viewport.X+100, viewport.Y+viewport.H-40
	tt.Press(px, py)
	tt.Move(px-45, py-30)
	tt.Release(px-45, py-30)
	near(t, "mouse drag x", s.x, -45)
	near(t, "mouse drag y", s.y, -30)
	if s.dragging || ps.node != "beta" {
		t.Fatal("drag changed selection or stayed captured")
	}
	click(t, tt, "适应全图")
	if s.contentW*s.zoom > s.width+1 || s.contentH*s.zoom > s.height+1 {
		t.Fatal("fit button did not fit")
	}
	x, y = center(t, tt, "gamma")
	tt.Move(x, y)
	if s.hover != "gamma" {
		t.Fatal("hover hit test wrong after fitting")
	}
	click(t, tt, "gamma")
	if ps.node != "gamma" {
		t.Fatal("node click failed after transform")
	}
	z = s.zoom
	tt.Pinch(30, 30, 2)
	near(t, "pinch outside graph", s.zoom, z)
	if a.scanGen != gen || a.snap != snap {
		t.Fatal("graph interactions rescanned the catalog")
	}
}
