package repair

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"knack/internal/fsutil"
	"knack/internal/manage"
	"knack/internal/skill"
)

func shortPath(t *testing.T, path string) string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil {
		if errors.Is(err, syscall.Errno(1)) || errors.Is(err, syscall.Errno(50)) || errors.Is(err, syscall.Errno(120)) {
			t.Skipf("GetShortPathName is unsupported: %v", err)
		}
		t.Fatal(err)
	}
	if n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("unexpected short path length: %d", n)
	}
	return syscall.UTF16ToString(buf[:n])
}

func TestWindowsRemovalShortPaths(t *testing.T) {
	for _, kind := range []struct {
		name string
		link func(*testing.T, string, string)
	}{
		{"symlink", makeSymlink},
		{"junction", makeJunction},
	} {
		t.Run(kind.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "removal home with long name")
			pool := filepath.Join(home, ".agents", "skills")
			agent := filepath.Join(home, ".claude", "skills")
			selected := filepath.Join(pool, "selected")
			writeSkill(t, selected, "selected")
			writeSkill(t, filepath.Join(agent, "local"), "local")
			longHome, err := filepath.EvalSymlinks(home)
			if err != nil {
				t.Fatal(err)
			}
			shortHome := shortPath(t, longHome)
			if fsutil.EqualPath(shortHome, longHome) {
				t.Skip("filesystem has no distinct 8.3 alias")
			}
			shortSelected := filepath.Join(shortHome, ".agents", "skills", "selected")
			kind.link(t, shortSelected, filepath.Join(agent, "through"))
			snap := skill.Scan(skill.Discover(home), nil)
			e := removalEntry(t, snap, selected)
			if r := RemovalFor(snap, e); !strings.Contains(r.Err, "through") {
				t.Fatalf("short target alias missed reference: %+v", r)
			}
			// A scan whose entire home uses an 8.3 component must still
			// match the long parent produced by removalReferences.
			shortSnap := skill.Scan(skill.Discover(shortHome), nil)
			shortEntry := removalEntry(t, shortSnap, shortSelected)
			if r := RemovalFor(shortSnap, shortEntry); !strings.Contains(r.Err, "through") {
				t.Fatalf("short scan home missed reference: %+v", r)
			}
			if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
				t.Fatal("fresh scan ignored a short-name reference")
			}

			// Compare the link slot itself using Lstat. Resolving either
			// slot to its final directory would incorrectly block bypass.
			source := filepath.Join(home, "source")
			writeSkill(t, source, "source")
			linkSlot := filepath.Join(pool, "link-slot")
			kind.link(t, source, linkSlot)
			kind.link(t, source, filepath.Join(agent, "bypass"))
			longSlot := filepath.Join(longHome, ".agents", "skills", "link-slot")
			shortSlot := filepath.Join(shortHome, ".agents", "skills", "link-slot")
			if yes, err := pathTraverses(shortSlot, longSlot); err != nil || !yes {
				t.Fatalf("short link-slot identity missed: %v, %v", yes, err)
			}
			if yes, err := pathTraverses(filepath.Join(agent, "bypass"), longSlot); err != nil || yes {
				t.Fatalf("final source was mistaken for the slot: %v, %v", yes, err)
			}
			// Keep the selected link dangling to ensure identity checks
			// do not require its final destination to exist.
			if err := os.Rename(source, filepath.Join(home, "moved-source")); err != nil {
				t.Fatal(err)
			}
			if yes, err := pathTraverses(shortSlot, longSlot); err != nil || !yes {
				t.Fatalf("broken short link-slot identity missed: %v, %v", yes, err)
			}
		})
	}
}

func TestWindowsTraversalParts(t *testing.T) {
	for _, tc := range []struct {
		path, root string
		parts      []string
	}{
		{`C:\Users\person\skills`, `C:\`, []string{"Users", "person", "skills"}},
		{`C:/Users/person/skills`, `C:\`, []string{"Users", "person", "skills"}},
		{`C:\`, `C:\`, nil},
		{`\\server\share\Users\skills`, `\\server\share\`, []string{"Users", "skills"}},
		{`\\server\share`, `\\server\share\`, nil},
		{`\\?\C:\Users\skills`, `\\?\C:\`, []string{"Users", "skills"}},
		{`\\?\UNC\server\share\skills`, `\\?\UNC\server\share\`, []string{"skills"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			root, parts := traversalParts(tc.path)
			if root != tc.root || !slices.Equal(parts, tc.parts) {
				t.Fatalf("traversalParts(%q) = %q, %q; want %q, %q", tc.path, root, parts, tc.root, tc.parts)
			}
		})
	}
}

func makeJunction(t *testing.T, target, path string) {
	t.Helper()
	// Junctions do not require the symlink privilege or Developer Mode.
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}

func TestWindowsRemovalLinkReferences(t *testing.T) {
	for _, kind := range []struct {
		name string
		link func(*testing.T, string, string)
	}{
		{"symlink", makeSymlink},
		{"junction", makeJunction},
	} {
		t.Run(kind.name, func(t *testing.T) {
			home, pool, agent := removalFixture(t)
			source := filepath.Join(home, "source")
			writeSkill(t, source, "source")
			selected := filepath.Join(pool, "selected")
			kind.link(t, source, selected)
			kind.link(t, source, filepath.Join(agent, "bypass"))
			snap := skill.Scan(skill.Discover(home), nil)
			e := removalEntry(t, snap, selected)
			if r := RemovalFor(snap, e); !r.Available || r.Err != "" {
				t.Fatalf("link sharing the final source blocked removal: %+v", r)
			}
			// Exercise drive roots again after following a link, using a
			// different case spelling for the selected slot.
			kind.link(t, strings.ToUpper(selected), filepath.Join(agent, "through"))
			// Junctions can also appear in an intermediate parent component.
			parent := filepath.Join(home, "pool-parent")
			kind.link(t, pool, parent)
			yes, err := pathTraverses(filepath.Join(parent, "SELECTED"), selected)
			if err != nil || !yes {
				t.Fatalf("intermediate link did not traverse the slot: %v, %v", yes, err)
			}
			fresh := skill.Scan(skill.Discover(home), nil)
			r := RemovalFor(fresh, removalEntry(t, fresh, selected))
			if !strings.Contains(r.Err, "through") || strings.Contains(r.Err, "bypass") {
				t.Fatalf("incorrect slot references: %+v", r)
			}
			if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
				t.Fatal("fresh revalidation ignored the new link")
			}
		})
	}
}

func TestWindowsBrokenChainCaseVariants(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken")
	makeSymlink(t, filepath.Join(dir, "missing"), broken)
	chain := filepath.Join(dir, "chain")
	makeSymlink(t, strings.ToUpper(broken), chain)
	for _, target := range []string{broken, strings.ToUpper(broken)} {
		got, err := pathTraverses(chain, target)
		if err != nil || !got {
			t.Fatalf("broken chain missed %q: %v, %v", target, got, err)
		}
	}
	if got, err := pathTraverses(chain, filepath.Join(dir, "other")); err != nil || got {
		t.Fatalf("unrelated broken chain: %v, %v", got, err)
	}
}

func TestWindowsApplyRemovalPathCase(t *testing.T) {
	home, pool, agent := removalFixture(t)
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, filepath.Join(agent, "local"))
	e.RootPath = strings.ToUpper(e.RootPath)
	e.Path = strings.ToUpper(e.Path)
	e.RealPath = strings.ToUpper(e.RealPath)
	if r := RemovalFor(snap, e); !r.Available || r.Err != "" {
		t.Fatalf("case variant rejected: %+v", r)
	}
	roots := slices.Clone(snap.Roots)
	for i := range roots {
		roots[i].Path = strings.ToUpper(roots[i].Path)
	}
	rec, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), roots...)
	if err != nil {
		t.Fatal(err)
	}
	if err := manage.Restore(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pool, "alpha", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
