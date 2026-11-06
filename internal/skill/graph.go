package skill

import (
	"slices"
	"strings"
)

// EdgeKind is why one skill points at another.
type EdgeKind string

const (
	EdgeUses     EdgeKind = "uses"     // front matter or body names the other skill
	EdgeContains EdgeKind = "contains" // the other skill lives under skills/ inside it
)

// NodeKind says what a graph node stands for.
type NodeKind string

const (
	NodeSkill   NodeKind = "skill"   // an entry of the root
	NodeMissing NodeKind = "missing" // a listed dependency that is not in the root
	NodeChild   NodeKind = "child"   // a skill nested inside another skill
)

// Edge points from the skill that needs to the one it needs.
type Edge struct {
	From, To string
	Kind     EdgeKind
	// Cycle marks an edge that closes a loop; it is left out of the layers.
	Cycle bool
	// In a file graph: the line of From that makes the reference, how it
	// does (链接, 路径, source, frontmatter, 正文推断), and whether it is only
	// read from prose.
	Line     int
	Via      string
	Inferred bool
}

// Node is one skill of a graph with the place it is laid out in: Layer is
// the column, Row the position in it.
type Node struct {
	Name  string
	Kind  NodeKind
	Entry Entry
	Layer int
	Row   int
	// In a file graph: the file's path inside the skill and its size.
	Path string
	Size int64
}

// Graph is how the skills of one root depend on each other. Only skills
// with at least one edge are nodes; the rest are Isolated.
type Graph struct {
	Nodes    []Node
	Edges    []Edge
	Isolated []string
	Layers   int
	Rows     int // the most nodes in one layer
}

// Node returns the node named name.
func (g *Graph) Node(name string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return Node{}, false
}

// Needs lists the names name uses.
func (g *Graph) Needs(name string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.From == name && e.Kind == EdgeUses {
			out = append(out, e.To)
		}
	}
	return out
}

// NeededBy lists the names that use name.
func (g *Graph) NeededBy(name string) []string {
	var out []string
	for _, e := range g.Edges {
		if e.To == name && e.Kind == EdgeUses {
			out = append(out, e.From)
		}
	}
	return out
}

// BuildGraph finds how entries point at each other and lays the result out
// in layers: a skill sits one column right of the furthest skill that needs
// it, so every arrow runs left to right unless it closes a loop.
func BuildGraph(entries []Entry) *Graph {
	g := &Graph{}
	byName := map[string]*Entry{}
	byLower := map[string]string{}
	for i := range entries {
		e := &entries[i]
		if _, ok := byName[e.Name]; ok {
			continue
		}
		byName[e.Name] = e
		byLower[strings.ToLower(e.Name)] = e.Name
	}
	extra := map[string]NodeKind{}
	seenEdge := map[[2]string]bool{}
	add := func(from, to string, kind EdgeKind) {
		key := [2]string{from, to}
		if from == to || seenEdge[key] {
			return
		}
		seenEdge[key] = true
		g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: kind})
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		e := byName[name]
		for _, req := range e.Requires {
			if target, ok := byLower[strings.ToLower(req)]; ok {
				add(name, target, EdgeUses)
				continue
			}
			extra[req] = NodeMissing
			add(name, req, EdgeUses)
		}
		for _, m := range e.Mentions {
			if target, ok := byLower[strings.ToLower(m)]; ok {
				add(name, target, EdgeUses)
			}
		}
		for _, child := range e.Children {
			if _, ok := byName[child]; !ok {
				if _, seen := extra[child]; !seen {
					extra[child] = NodeChild
				}
			}
			add(name, child, EdgeContains)
		}
	}
	connected := map[string]bool{}
	for _, e := range g.Edges {
		connected[e.From] = true
		connected[e.To] = true
	}
	for _, name := range names {
		if connected[name] {
			g.Nodes = append(g.Nodes, Node{Name: name, Kind: NodeSkill, Entry: *byName[name]})
		} else {
			g.Isolated = append(g.Isolated, name)
		}
	}
	extraNames := make([]string, 0, len(extra))
	for name := range extra {
		extraNames = append(extraNames, name)
	}
	slices.Sort(extraNames)
	for _, name := range extraNames {
		g.Nodes = append(g.Nodes, Node{Name: name, Kind: extra[name]})
	}
	g.layout()
	return g
}

func (g *Graph) layout() {
	if len(g.Nodes) == 0 {
		return
	}
	index := map[string]int{}
	for i, n := range g.Nodes {
		index[n.Name] = i
	}
	out := make([][]int, len(g.Nodes))
	for i := range g.Edges {
		e := &g.Edges[i]
		out[index[e.From]] = append(out[index[e.From]], i)
	}
	// A depth-first walk marks the edges that return to a skill still on
	// the path; without them the graph has no loop to lay out.
	const (
		fresh = iota
		onPath
		done
	)
	state := make([]int, len(g.Nodes))
	var walk func(v int)
	walk = func(v int) {
		state[v] = onPath
		for _, ei := range out[v] {
			to := index[g.Edges[ei].To]
			switch state[to] {
			case onPath:
				g.Edges[ei].Cycle = true
			case fresh:
				walk(to)
			}
		}
		state[v] = done
	}
	for v := range g.Nodes {
		if state[v] == fresh {
			walk(v)
		}
	}

	layer := make([]int, len(g.Nodes))
	indeg := make([]int, len(g.Nodes))
	for _, e := range g.Edges {
		if !e.Cycle {
			indeg[index[e.To]]++
		}
	}
	var queue []int
	for v, d := range indeg {
		if d == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, ei := range out[v] {
			e := g.Edges[ei]
			if e.Cycle {
				continue
			}
			to := index[e.To]
			layer[to] = max(layer[to], layer[v]+1)
			if indeg[to]--; indeg[to] == 0 {
				queue = append(queue, to)
			}
		}
	}

	cols := 0
	for _, l := range layer {
		cols = max(cols, l+1)
	}
	order := make([][]int, cols)
	for v := range g.Nodes {
		order[layer[v]] = append(order[layer[v]], v)
	}
	pos := make([]float64, len(g.Nodes))
	for _, col := range order {
		for i, v := range col {
			pos[v] = float64(i)
		}
	}
	neighbours := make([][]int, len(g.Nodes))
	for _, e := range g.Edges {
		if e.Cycle {
			continue
		}
		a, b := index[e.From], index[e.To]
		neighbours[a] = append(neighbours[a], b)
		neighbours[b] = append(neighbours[b], a)
	}
	// Sweeping the columns by the average place of their neighbours takes
	// most crossings out of the arrows.
	for pass := 0; pass < 4; pass++ {
		for _, col := range order {
			bary := map[int]float64{}
			for _, v := range col {
				if len(neighbours[v]) == 0 {
					bary[v] = pos[v]
					continue
				}
				sum := 0.0
				for _, n := range neighbours[v] {
					sum += pos[n]
				}
				bary[v] = sum / float64(len(neighbours[v]))
			}
			slices.SortStableFunc(col, func(a, b int) int {
				switch {
				case bary[a] < bary[b]:
					return -1
				case bary[a] > bary[b]:
					return 1
				}
				return strings.Compare(g.Nodes[a].Name, g.Nodes[b].Name)
			})
			for i, v := range col {
				pos[v] = float64(i)
			}
		}
	}
	for l, col := range order {
		g.Rows = max(g.Rows, len(col))
		for i, v := range col {
			g.Nodes[v].Layer, g.Nodes[v].Row = l, i
		}
	}
	g.Layers = cols
}
