package skill

import (
	"bufio"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// File graph nodes: a file of the skill, and a path a file names that is
// not there.
const (
	NodeFile        NodeKind = "file"
	NodeMissingFile NodeKind = "missing-file"
)

// Limits keep a file graph cheap on a skill that carries a large tree.
const (
	fileGraphFiles = 400
	fileGraphBytes = 256 << 10
	fileGraphDepth = 6
)

// textExts are the files read for references; others are nodes only when
// something points at them.
var textExts = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".sh": true, ".bash": true, ".zsh": true,
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".rb": true, ".go": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
}

var (
	urlRe      = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://\S+`)
	mdLinkRe   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	pathRe     = regexp.MustCompile(`(?:\.{1,2}/)?[A-Za-z0-9_][A-Za-z0-9_.-]*(?:/[A-Za-z0-9_][A-Za-z0-9_.-]*)*\.(?:md|markdown|txt|sh|bash|zsh|py|js|mjs|cjs|ts|rb|go|json|yaml|yml|toml|csv|html|css|sql|png|jpg|svg|pdf)\b`)
	sourceLine = regexp.MustCompile(`^\s*(?:source|\.)\s`)
)

// FileGraph is how the files of one skill point at each other, and the
// other skills in scope its SKILL.md names. Only files inside the skill's
// directory are read; a path one of them names that is not there is a
// missing node. Edges carry the line that makes the reference.
func FileGraph(e Entry, scope []Entry) (*Graph, error) {
	dir := e.Path
	if e.RealPath != "" {
		dir = e.RealPath
	}
	files := map[string]bool{}
	var order []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || strings.Count(rel, "/") >= fileGraphDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || len(order) >= fileGraphFiles {
			return nil
		}
		files[rel] = true
		order = append(order, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	g := &Graph{}
	kinds := map[string]NodeKind{"SKILL.md": NodeFile}
	seen := map[[2]string]bool{}
	add := func(from, to string, line int, via string, inferred bool) {
		key := [2]string{from, to}
		if from == to || seen[key] {
			return
		}
		seen[key] = true
		g.Edges = append(g.Edges, Edge{From: from, To: to, Kind: EdgeUses, Line: line, Via: via, Inferred: inferred})
	}
	for _, rel := range order {
		if !textExts[strings.ToLower(path.Ext(rel))] {
			continue
		}
		for _, ref := range fileRefs(filepath.Join(dir, filepath.FromSlash(rel))) {
			target, ok := resolveRef(rel, ref.target, files)
			switch {
			case ok:
				kinds[rel], kinds[target] = NodeFile, NodeFile
				add(rel, target, ref.line, ref.via, false)
			case target != "" && (ref.via == "链接" || strings.Contains(ref.target, "/")):
				kinds[rel] = NodeFile
				if _, known := kinds[target]; !known {
					kinds[target] = NodeMissingFile
				}
				add(rel, target, ref.line, ref.via, false)
			}
		}
	}
	// Other skills come from SKILL.md alone: the front matter's list is
	// explicit, a name in the body is inferred.
	if files["SKILL.md"] {
		names := map[string]Entry{}
		for _, x := range scope {
			if _, ok := names[strings.ToLower(x.Name)]; !ok && !strings.EqualFold(x.Name, e.Name) {
				names[strings.ToLower(x.Name)] = x
			}
		}
		text, lines := readCapped(filepath.Join(dir, "SKILL.md"))
		requires, mentions := ParseRefs(text)
		for _, name := range requires {
			if x, ok := names[strings.ToLower(name)]; ok {
				kinds[x.Name] = NodeSkill
				add("SKILL.md", x.Name, lineOf(lines, name), "frontmatter", false)
			} else if !strings.EqualFold(name, e.Name) {
				kinds[name] = NodeMissing
				add("SKILL.md", name, lineOf(lines, name), "frontmatter", false)
			}
		}
		for _, name := range mentions {
			if x, ok := names[strings.ToLower(name)]; ok {
				if _, listed := kinds[x.Name]; !listed {
					kinds[x.Name] = NodeSkill
				}
				add("SKILL.md", x.Name, lineOf(lines, name), "正文推断", true)
			}
		}
	}
	entries := map[string]Entry{}
	for _, x := range scope {
		if _, ok := entries[x.Name]; !ok {
			entries[x.Name] = x
		}
	}
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if a == "SKILL.md" || b == "SKILL.md" {
			return boolOrder(b == "SKILL.md", a == "SKILL.md")
		}
		return strings.Compare(a, b)
	})
	for _, name := range names {
		n := Node{Name: name, Kind: kinds[name]}
		switch n.Kind {
		case NodeSkill:
			n.Entry = entries[name]
		case NodeFile:
			n.Path = name
			if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
				n.Size = info.Size()
			}
		case NodeMissingFile:
			n.Path = name
		}
		g.Nodes = append(g.Nodes, n)
	}
	for _, rel := range order {
		if _, ok := kinds[rel]; !ok {
			g.Isolated = append(g.Isolated, rel)
		}
	}
	g.layout()
	return g, nil
}

func boolOrder(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

type fileRef struct {
	target, via string
	line        int
}

// fileRefs reads the paths a text file names, line by line: Markdown links,
// then path-like words. A shell source line says how a script uses another.
func fileRefs(file string) []fileRef {
	_, lines := readCapped(file)
	var out []fileRef
	script := !strings.EqualFold(filepath.Ext(file), ".md")
	for i, line := range lines {
		line = urlRe.ReplaceAllString(line, " ")
		for _, m := range mdLinkRe.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if cut := strings.IndexAny(target, "#?"); cut >= 0 {
				target = target[:cut]
			}
			if target != "" {
				out = append(out, fileRef{target, "链接", i + 1})
			}
		}
		via := "路径"
		if script && sourceLine.MatchString(line) {
			via = "source"
		}
		for _, m := range pathRe.FindAllStringIndex(line, -1) {
			if outside(line[:m[0]]) {
				continue
			}
			out = append(out, fileRef{line[m[0]:m[1]], via, i + 1})
		}
	}
	return out
}

// outside reports that a path-like word, after before, is not a path in the
// skill: glued to a variable, a home or an absolute path ($HOME/x.md,
// ~/x.md, /etc/x.md). A script's own folder, $(dirname "$0")/x.sh or
// ${DIR}/x.sh, still is.
func outside(before string) bool {
	if before == "" || !strings.ContainsRune("$~/@", rune(before[len(before)-1])) {
		return false
	}
	for _, own := range []string{")/", "}/", "\"/"} {
		if strings.HasSuffix(before, own) {
			return false
		}
	}
	return true
}

// resolveRef finds the file a reference means: beside the file that makes
// it, else from the skill's top. It returns the cleaned path and whether a
// file is there; a path that leaves the skill is dropped.
func resolveRef(from, ref string, files map[string]bool) (string, bool) {
	ref = strings.TrimPrefix(ref, "<")
	ref = strings.TrimSuffix(ref, ">")
	if ref == "" || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, "$") {
		return "", false
	}
	var first string
	for _, base := range []string{path.Dir(from), "."} {
		p := path.Clean(path.Join(base, ref))
		if p == ".." || strings.HasPrefix(p, "../") {
			continue
		}
		if files[p] {
			return p, true
		}
		if first == "" {
			first = p
		}
	}
	if first == "" {
		return "", false
	}
	// A missing path is read from the skill's top when the file sits in a
	// folder, as SKILL.md's paths are.
	if p := path.Clean(ref); !strings.HasPrefix(p, "../") && p != ".." {
		return p, false
	}
	return first, false
}

// readCapped reads up to fileGraphBytes of a text file, and its lines.
func readCapped(file string) (string, []string) {
	f, err := os.Open(file)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	var b strings.Builder
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), fileGraphBytes)
	for sc.Scan() && b.Len() < fileGraphBytes {
		lines = append(lines, sc.Text())
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	return b.String(), lines
}

// lineOf is the first line that names a skill, 0 when none does.
func lineOf(lines []string, name string) int {
	want := strings.ToLower(name)
	for i, l := range lines {
		if strings.Contains(strings.ToLower(l), want) {
			return i + 1
		}
	}
	return 0
}
