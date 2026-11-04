package skill

import (
	"os"
	"path/filepath"
	"testing"

	"knack/internal/fsutil"
)

func TestScanLinkPhysicalParent(t *testing.T) {
	for _, poolLink := range []bool{false, true} {
		name := "directory pool slot"
		if poolLink {
			name = "pool slot links to repo"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			pool := filepath.Join(home, ".agents", "skills", "alpha")
			repo := filepath.Join(home, "repo", "alpha")
			if poolLink {
				writeSkill(t, repo, "repo skill")
				if err := os.MkdirAll(filepath.Dir(pool), 0o755); err != nil {
					t.Fatal(err)
				}
				linkRel(t, filepath.Dir(pool), "alpha", repo)
			} else {
				writeSkill(t, pool, "shared skill")
			}
			physicalAgent := filepath.Join(home, "redirected", "agent", "skills")
			if err := os.MkdirAll(physicalAgent, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Dir(physicalAgent), filepath.Join(home, ".claude")); err != nil {
				t.Fatal(err)
			}
			parent, err := filepath.EvalSymlinks(physicalAgent)
			if err != nil {
				t.Fatal(err)
			}
			poolSlot, err := physicalSlot(pool)
			if err != nil {
				t.Fatal(err)
			}
			linkRel(t, parent, "alpha", poolSlot)
			// A direct repository link must stay external even if the pool
			// slot resolves to that same repository directory.
			if poolLink {
				linkRel(t, parent, "bypass", repo)
			}
			snap := Scan(Discover(home), nil)
			e := mustEntry(t, snap, "claude-skills", "alpha")
			if e.Kind != KindLink || e.Relation != RelLinked || !e.Usable() ||
				!fsutil.EqualPath(e.LinkAbs, poolSlot) {
				t.Fatalf("physical-relative link misclassified: %+v, want target %s", e, poolSlot)
			}
			// Give the bypass the pool skill's name to test target identity,
			// independently of the name-based pool lookup.
			if poolLink {
				bypass := mustEntry(t, snap, "claude-skills", "bypass")
				poolEntry := mustEntry(t, snap, "pool", "alpha")
				if pointsAtPool(bypass, poolEntry) {
					t.Fatal("direct repo link mistaken for the pool slot")
				}
			}
			// Cached analysis must retain the physical slot identity without
			// reading a filesystem that may since have changed.
			cached := FromCatalog(snap.Roots, snap.Entries, nil, snap.At)
			if mustEntry(t, cached, "claude-skills", "alpha").Relation != RelLinked {
				t.Fatal("catalog lost pool slot identity")
			}
		})
	}
}
