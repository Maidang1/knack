package skill

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"knack/internal/fsutil"
)

const (
	repoSkillCap  = 500
	maxHeaderSize = 64 * 1024
)

// A scan caches only front matter, keyed by resolved directory path.
type cache struct{ meta map[string]cachedMeta }
type cachedMeta struct {
	name, desc  string
	requires    []string
	hasDocument bool
	size        int64
	modTime     time.Time
	err         string
}

func newCache() *cache { return &cache{meta: map[string]cachedMeta{}} }

// Scan reads immediate skill slots and front matter only. .system is the
// one explicit read-only subdirectory. No body or attachment is read.
func Scan(roots []Root, repos []string) *Snapshot {
	snap := &Snapshot{At: time.Now()}
	c := newCache()
	snap.Roots = append(snap.Roots, roots...)
	for _, root := range roots {
		entries, err := scanRoot(root, c)
		if err != nil {
			snap.Warnings = append(snap.Warnings, root.Name+"："+err.Error())
			continue
		}
		snap.Entries = append(snap.Entries, entries...)
	}
	for _, repo := range repos {
		repo = filepath.Clean(repo)
		root := Root{ID: "repo:" + repo, Name: filepath.Base(repo), Path: repo, Role: RoleRepo}
		snap.Roots = append(snap.Roots, root)
		st, err := os.Stat(repo)
		if err != nil || !st.IsDir() {
			snap.Warnings = append(snap.Warnings, "仓库不存在："+repo)
			continue
		}
		entries, warns := scanRepo(root, c)
		snap.Warnings = append(snap.Warnings, warns...)
		snap.Entries = append(snap.Entries, entries...)
	}
	for _, e := range snap.Entries {
		if e.MetaErr != "" && e.Kind != KindBroken {
			snap.Warnings = append(snap.Warnings, e.Path+"："+e.MetaErr)
		}
	}
	analyze(snap)
	return snap
}

func scanRoot(root Root, c *cache) ([]Entry, error) {
	ents, err := os.ReadDir(root.Path)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, ent := range ents {
		name := ent.Name()
		if skipEntryName(name) {
			continue
		}
		p := filepath.Join(root.Path, name)
		if name == ".system" {
			out = append(out, scanSystem(root, p, c)...)
			continue
		}
		if e, ok := scanEntry(root, name, p, false, c); ok {
			out = append(out, e)
		}
	}
	sortEntries(out)
	return out, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name == entries[j].Name {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Name < entries[j].Name
	})
}

func scanSystem(root Root, dir string, c *cache) []Entry {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, ent := range ents {
		if skipEntryName(ent.Name()) {
			continue
		}
		if e, ok := scanEntry(root, ent.Name(), filepath.Join(dir, ent.Name()), true, c); ok {
			out = append(out, e)
		}
	}
	return out
}

func skipEntryName(name string) bool {
	switch name {
	case ".git", ".DS_Store", ".gitignore", ".gitmodules", "README.md", "readme.md",
		"LICENSE", "contributing.md", ".seed-fingerprint":
		return true
	}
	return strings.HasPrefix(name, ".") && name != ".system"
}

func scanEntry(root Root, name, path string, system bool, c *cache) (Entry, bool) {
	e := Entry{RootID: root.ID, RootName: root.Name, RootRole: root.Role, RootPath: root.Path,
		Name: name, Path: path, System: system || root.Role == RoleBuiltin}
	info, err := os.Lstat(path)
	if err != nil {
		return e, false
	}
	e.EntryModTime = info.ModTime()
	e.SlotPath, _ = physicalSlot(path)
	if fsutil.IsLink(path, info) {
		return scanLink(e, c), true
	}
	if !info.IsDir() {
		return e, false
	}
	return scanDir(e, path, c), true
}

func scanLink(e Entry, c *cache) Entry {
	tgt, err := os.Readlink(e.Path)
	if err != nil {
		e.Kind, e.MetaErr = KindBroken, err.Error()
		return e
	}
	e.LinkTarget = tgt
	if filepath.IsAbs(tgt) {
		e.LinkAbs = filepath.Clean(tgt)
	} else {
		parent, err := filepath.EvalSymlinks(filepath.Dir(e.Path))
		if err != nil {
			e.Kind, e.MetaErr = KindLink, err.Error()
			return e
		}
		e.LinkAbs = filepath.Clean(filepath.Join(parent, tgt))
	}
	// Normalize target ancestors too, but preserve the final pool slot even
	// when that slot is itself a link to a configured repository.
	if slot, err := physicalSlot(e.LinkAbs); err == nil {
		e.LinkAbs = slot
	}
	st, err := os.Stat(e.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			e.Kind = KindBroken
		} else {
			e.Kind = KindLink
		}
		e.MetaErr = err.Error()
		return e
	}
	e.Kind = KindLink
	if !st.IsDir() {
		e.MetaErr = "软链目标不是目录"
		return e
	}
	real, err := filepath.EvalSymlinks(e.Path)
	if err != nil {
		e.MetaErr = err.Error()
		return e
	}
	e.RealPath = real
	fillFromDir(&e, real, c)
	return e
}

func physicalSlot(path string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func scanDir(e Entry, dir string, c *cache) Entry {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		e.Kind, e.MetaErr = KindUnknown, err.Error()
		return e
	}
	e.RealPath = real
	fillFromDir(&e, real, c)
	if !e.HasDocument && e.MetaErr == "" {
		e.Kind = KindEmpty
		e.Vacant = vacant(dir)
	} else {
		e.Kind = KindSkill
	}
	return e
}

func fillFromDir(e *Entry, dir string, c *cache) {
	meta := c.front(dir)
	e.FrontName, e.Description, e.Requires = meta.name, meta.desc, meta.requires
	e.HasDocument, e.DocSize, e.DocModTime, e.MetaErr = meta.hasDocument, meta.size, meta.modTime, meta.err
}

func (c *cache) front(dir string) cachedMeta {
	if m, ok := c.meta[dir]; ok {
		return m
	}
	var m cachedMeta
	path := filepath.Join(dir, "SKILL.md")
	st, err := os.Stat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.err = err.Error()
		}
	} else {
		m.hasDocument, m.size, m.modTime = true, st.Size(), st.ModTime()
		if !st.Mode().IsRegular() {
			m.err = "SKILL.md 不是普通文件"
		} else {
			fm, readErr := readHeader(path)
			if readErr != nil {
				m.err = readErr.Error()
			} else {
				m.name = strings.TrimSpace(frontField(fm, "name"))
				m.desc = clip(strings.TrimSpace(frontField(fm, "description")), 2000)
				for _, key := range requireKeys {
					m.requires = append(m.requires, frontList(fm, key)...)
				}
				m.requires = dedupe(m.requires)
			}
		}
	}
	c.meta[dir] = m
	return m
}

// readHeader never parses the body. The limited reader bounds even a
// single very long line; the closing delimiter must be a complete line.
func readHeader(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, maxHeaderSize+1))
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	trimLine := func(s string) string { return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r") }
	if trimLine(strings.TrimPrefix(line, "\uFEFF")) != "---" {
		return "", nil
	}
	size := len(line)
	var fm strings.Builder
	for {
		line, err = r.ReadString('\n')
		size += len(line)
		if size > maxHeaderSize {
			return "", fmt.Errorf("SKILL.md front matter exceeds %d bytes", maxHeaderSize)
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		if trimLine(line) == "---" {
			return fm.String(), nil
		}
		fm.WriteString(strings.ReplaceAll(line, "\r\n", "\n"))
		if err == io.EOF {
			return "", errors.New("SKILL.md front matter has no closing ---")
		}
	}
}

func vacant(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, ent := range ents {
		if !skipTreeName(ent.Name()) {
			return false
		}
	}
	return true
}

// A configured repo can itself be a skill and can contain direct child
// skills. It is never searched recursively.
func scanRepo(root Root, c *cache) ([]Entry, []string) {
	var out []Entry
	var warns []string
	add := func(name, path string) bool {
		e, ok := scanEntry(root, name, path, false, c)
		if !ok || !e.HasDocument {
			return true
		}
		if len(out) == repoSkillCap {
			warns = append(warns, "只收录了前 500 个 skill："+root.Path)
			return false
		}
		out = append(out, e)
		return true
	}
	add(filepath.Base(root.Path), root.Path)
	ents, err := os.ReadDir(root.Path)
	if err != nil {
		return out, []string{root.Path + "：" + err.Error()}
	}
	for _, ent := range ents {
		if skipEntryName(ent.Name()) || ent.Name() == ".system" {
			continue
		}
		if !add(ent.Name(), filepath.Join(root.Path, ent.Name())) {
			break
		}
	}
	sortEntries(out)
	return out, warns
}
