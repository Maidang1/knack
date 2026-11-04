package skill

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"knack/internal/fsutil"
)

// Layer is one place an agent looks for a skill. When two layers hold the
// same name, the earlier layer wins and the later copies stay on disk
// unloaded.
//
// The order project, global, shared pool, built-in is the design's
// assumption. Each agent documents its own loading order; check it before
// relying on the verdict for an agent other than the one it was drawn for.
type Layer int

const (
	LayerProject Layer = iota
	LayerGlobal
	LayerPool
	LayerBuiltin
)

// Layers lists the layers in the order they win.
var Layers = []Layer{LayerProject, LayerGlobal, LayerPool, LayerBuiltin}

// Label is how the layer is named on screen.
func (l Layer) Label() string {
	switch l {
	case LayerProject:
		return "项目"
	case LayerGlobal:
		return "全局"
	case LayerPool:
		return "共享池"
	case LayerBuiltin:
		return "内置"
	}
	return ""
}

// Source is one copy of a skill in one layer.
type Source struct {
	Layer Layer
	Entry Entry
}

// Effective is one skill name as an agent sees it: Sources[0] is loaded,
// the rest are shadowed by it.
type Effective struct {
	Name        string
	Description string
	Sources     []Source
}

// Winner is the copy that gets loaded.
func (e Effective) Winner() Source { return e.Sources[0] }

// Shadowed are the copies hidden behind the winner.
func (e Effective) Shadowed() []Source { return e.Sources[1:] }

// In returns the copy in layer l.
func (e Effective) In(l Layer) (Source, bool) {
	for _, s := range e.Sources {
		if s.Layer == l {
			return s, true
		}
	}
	return Source{}, false
}

// LayerInfo describes one layer of a view.
type LayerInfo struct {
	Layer Layer
	Path  string // where the layer lives; "" when the agent has no such place
	Count int
}

// EffectiveView is what one agent loads for one working directory.
type EffectiveView struct {
	Agent   Root
	WorkDir string
	Layers  []LayerInfo
	Skills  []Effective
	// Missing names a skill that a loaded skill lists as a dependency but
	// that no layer has.
	Missing []string
}

// Overridden counts the skills with at least one shadowed copy.
func (v *EffectiveView) Overridden() int {
	n := 0
	for _, s := range v.Skills {
		if len(s.Sources) > 1 {
			n++
		}
	}
	return n
}

// ProjectRoot is the skills folder an agent reads inside workDir: the
// agent's own dot folder (~/.claude/skills becomes <workDir>/.claude/skills).
// Agents whose folder is nested deeper, or a workDir that is home itself,
// have none.
func ProjectRoot(home string, agent Root, workDir string) (Root, bool) {
	if workDir == "" || agent.Role != RoleAgent || agent.ID == windowsGooseID {
		return Root{}, false
	}
	home, workDir = filepath.Clean(home), filepath.Clean(workDir)
	if fsutil.EqualPath(workDir, home) {
		return Root{}, false
	}
	rel, err := filepath.Rel(home, agent.Path)
	if err != nil {
		return Root{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 2 || parts[0] == "." || parts[0] == ".." || !strings.HasPrefix(parts[0], ".") || parts[1] != "skills" {
		return Root{}, false
	}
	return Root{
		ID:   "project:" + agent.ID,
		Name: "项目",
		Path: filepath.Join(workDir, parts[0], parts[1]),
		Role: RoleAgent,
	}, true
}

// ScanDir reads the immediate skill slots of one directory, front matter
// only, as Scan does for a root. A missing directory has no entries.
func ScanDir(root Root) ([]Entry, error) {
	entries, err := scanRoot(root, newCache())
	if os.IsNotExist(err) {
		return nil, nil
	}
	return entries, err
}

// loads reports an entry an agent would pick up: a skill with a readable
// SKILL.md. Broken links and empty directories shadow nothing.
func loads(e Entry) bool { return e.Usable() && !e.Vacant }

// builtinFor lists the built-in roots that ship with agent: the ones under
// the same dot folder.
func builtinFor(home string, agent Root, roots []Root) []Root {
	dot := dotFolder(home, agent.Path)
	if dot == "" {
		return nil
	}
	var out []Root
	for _, r := range roots {
		if r.Role == RoleBuiltin && dotFolder(home, r.Path) == dot {
			out = append(out, r)
		}
	}
	return out
}

func dotFolder(home, path string) string {
	rel, err := filepath.Rel(filepath.Clean(home), path)
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if first == "." || first == ".." || !strings.HasPrefix(first, ".") {
		return ""
	}
	return first
}

// BuildEffective works out what agent loads in workDir from a finished
// scan plus a scan of the project folder. It reads nothing but the project
// folder's front matter.
func BuildEffective(snap *Snapshot, home string, agent Root, workDir string) (*EffectiveView, error) {
	v := &EffectiveView{Agent: agent, WorkDir: workDir}
	byLayer := map[Layer][]Entry{}
	paths := map[Layer]string{LayerGlobal: agent.Path}
	if proj, ok := ProjectRoot(home, agent, workDir); ok {
		entries, err := ScanDir(proj)
		if err != nil {
			return nil, err
		}
		byLayer[LayerProject] = entries
		paths[LayerProject] = proj.Path
	}
	byLayer[LayerGlobal] = snap.EntriesFor(agent.ID)
	for _, r := range snap.Roots {
		if r.Role == RolePool {
			byLayer[LayerPool] = append(byLayer[LayerPool], snap.EntriesFor(r.ID)...)
			paths[LayerPool] = r.Path
		}
	}
	for _, r := range builtinFor(home, agent, snap.Roots) {
		byLayer[LayerBuiltin] = append(byLayer[LayerBuiltin], snap.EntriesFor(r.ID)...)
		paths[LayerBuiltin] = r.Path
	}

	byName := map[string][]Source{}
	for _, l := range Layers {
		n := 0
		for _, e := range byLayer[l] {
			if !loads(e) {
				continue
			}
			n++
			byName[e.Name] = append(byName[e.Name], Source{Layer: l, Entry: e})
		}
		v.Layers = append(v.Layers, LayerInfo{Layer: l, Path: paths[l], Count: n})
	}
	for name, srcs := range byName {
		w := srcs[0].Entry
		v.Skills = append(v.Skills, Effective{Name: name, Description: w.Description, Sources: srcs})
	}
	// Winning layer first, then name, so the list reads as the ladder does.
	slices.SortFunc(v.Skills, func(a, b Effective) int {
		if c := int(a.Winner().Layer) - int(b.Winner().Layer); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})

	have := map[string]bool{}
	for name := range byName {
		have[name] = true
	}
	missing := map[string]bool{}
	for _, s := range v.Skills {
		for _, dep := range s.Winner().Entry.Requires {
			if !have[dep] {
				missing[dep] = true
			}
		}
	}
	for name := range missing {
		v.Missing = append(v.Missing, name)
	}
	slices.Sort(v.Missing)
	return v, nil
}

// AgeNote says how much older or newer a shadowed copy is than the one
// that wins, from the SKILL.md timestamps. Linked copies resolve to the
// same directory and say so instead.
func AgeNote(winner, other Entry) string {
	if winner.RealPath != "" && fsutil.EqualPath(winner.RealPath, other.RealPath) {
		return "指向同一个目录"
	}
	a, b := winner.DocModTime, other.DocModTime
	if a.IsZero() || b.IsZero() {
		return ""
	}
	d := a.Sub(b)
	switch {
	case d.Abs() < 24*time.Hour:
		return "与胜出的版本同一天修改"
	case d > 0:
		return "比胜出的版本旧 " + days(d) + " 天"
	default:
		return "比胜出的版本新 " + days(-d) + " 天"
	}
}

func days(d time.Duration) string { return strconv.Itoa(int(d / (24 * time.Hour))) }
