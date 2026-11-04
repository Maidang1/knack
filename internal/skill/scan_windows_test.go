package skill

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func junction(t *testing.T, path, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v\n%s", err, out)
	}
}

func TestScanWindowsJunctions(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills", "alpha")
	agent := filepath.Join(home, ".claude", "skills")
	external := filepath.Join(home, "external skill")
	missing := filepath.Join(home, "removed target")
	writeSkill(t, pool, "shared")
	writeSkill(t, filepath.Join(pool, "nested", "child"), "nested")
	writeSkill(t, external, "external")
	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	junction(t, filepath.Join(agent, "alpha"), pool)
	junction(t, filepath.Join(agent, "external"), external)
	junction(t, filepath.Join(agent, "gone"), missing)
	junction(t, filepath.Join(agent, ".system", "shipped"), pool)
	junction(t, filepath.Join(home, ".cursor", "skills-cursor", "alpha"), pool)
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	snap := Scan(Discover(home), nil)
	for _, tc := range []struct {
		root, name string
		kind       Kind
		relation   Relation
		system     bool
	}{
		{"claude-skills", "alpha", KindLink, RelLinked, false},
		{"claude-skills", "external", KindLink, RelExternal, false},
		{"claude-skills", "gone", KindBroken, RelBroken, false},
		{"claude-skills", "shipped", KindLink, RelSystem, true},
		{"cursor-skills-cursor", "alpha", KindLink, RelSystem, true},
	} {
		e := mustEntry(t, snap, tc.root, tc.name)
		if e.Kind != tc.kind || e.Relation != tc.relation || e.System != tc.system ||
			e.LinkTarget == "" || (tc.kind == KindLink && !e.Usable()) {
			t.Fatalf("junction scan: %+v", e)
		}
	}
	linked := mustEntry(t, snap, "claude-skills", "alpha")
	poolEntry, ok := snap.PoolEntry("alpha")
	if !ok {
		t.Fatal("scanned pool entry missing")
	}
	linked.LinkAbs = strings.ToUpper(linked.LinkAbs)
	if !pointsAtPool(linked, poolEntry) {
		t.Fatal("Windows pool comparison must ignore path case")
	}
	for _, e := range snap.Entries {
		if e.Name == "child" {
			t.Fatalf("junction caused recursive scan: %+v", e)
		}
	}
	for _, issue := range snap.Issues {
		if issue.Entry.System {
			t.Fatalf("read-only junction produced a repair issue: %+v", issue)
		}
	}
}

func TestTreeHashWindowsJunctionTarget(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	other := t.TempDir()
	link := filepath.Join(root, "resources")
	junction(t, link, target)
	before, err := TreeHash(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "body.txt"), []byte("not traversed"), 0o644); err != nil {
		t.Fatal(err)
	}
	same, err := TreeHash(root)
	if err != nil || same != before {
		t.Fatalf("junction target was traversed: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	junction(t, link, other)
	after, err := TreeHash(root)
	if err != nil || after == before {
		t.Fatalf("junction target missing from hash: %v", err)
	}
}
