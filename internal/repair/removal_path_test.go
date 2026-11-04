package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"knack/internal/manage"
	"knack/internal/skill"
)

func TestPathTraverses(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(dir, "selected")
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	makeSymlink(t, "source", selected)
	makeSymlink(t, "source", filepath.Join(dir, "bypass"))
	makeSymlink(t, "selected", filepath.Join(dir, "through"))
	makeSymlink(t, "missing", filepath.Join(dir, "broken"))
	makeSymlink(t, "broken", filepath.Join(dir, "broken-chain"))
	makeSymlink(t, "loop-b", filepath.Join(dir, "loop-a"))
	makeSymlink(t, "loop-a", filepath.Join(dir, "loop-b"))
	for _, tc := range []struct {
		name, path, target string
		want, wantErr      bool
	}{
		{"selected slot", selected, selected, true, false},
		{"descendant", filepath.Join(selected, "missing-child"), selected, true, false},
		{"relative link expansion", filepath.Join(dir, "through"), selected, true, false},
		{"bypass same source", filepath.Join(dir, "bypass"), selected, false, false},
		{"source bypasses selected", source, selected, false, false},
		{"prefix is not a slot", filepath.Join(dir, "selected-other"), selected, false, false},
		{"broken chain", filepath.Join(dir, "broken-chain"), filepath.Join(dir, "broken"), true, false},
		{"unrelated broken chain", filepath.Join(dir, "broken-chain"), selected, false, false},
		{"loop fails closed", filepath.Join(dir, "loop-a"), selected, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pathTraverses(tc.path, tc.target)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("pathTraverses(%q, %q) = %v, %v; want %v, error=%v",
					tc.path, tc.target, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestApplyRemovalWithUnrelatedLinks(t *testing.T) {
	home, pool, agent := removalFixture(t)
	target := filepath.Join(agent, "local")
	alias := filepath.Join(agent, "alias")
	broken := filepath.Join(agent, "broken")
	makeSymlink(t, filepath.Join(pool, "alpha"), alias)
	makeSymlink(t, filepath.Join(home, "missing"), broken)
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, target)
	if r := RemovalFor(snap, e); !r.Available || r.Err != "" {
		t.Fatalf("unrelated links blocked normal removal: %+v", r)
	}
	rec, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), snap.Roots...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("selected slot still present: %v", err)
	}
	for _, path := range []string{alias, broken, filepath.Join(pool, "alpha", "SKILL.md")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("unrelated entry changed: %s: %v", path, err)
		}
	}
	if err := manage.Restore(rec); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalLoopFailsClosed(t *testing.T) {
	home, pool, agent := removalFixture(t)
	makeSymlink(t, "loop-b", filepath.Join(agent, "loop-a"))
	makeSymlink(t, "loop-a", filepath.Join(agent, "loop-b"))
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, filepath.Join(pool, "alpha"))
	if r := RemovalFor(snap, e); r.Err == "" {
		t.Fatalf("link loop allowed removal: %+v", r)
	}
	if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
		t.Fatal("fresh revalidation allowed removal with a loop")
	}
	if _, err := os.Stat(e.Path); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalRejectsUnsafeSlots(t *testing.T) {
	home, _, agent := removalFixture(t)
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, filepath.Join(agent, "local"))
	for _, path := range []string{agent, filepath.Join(agent, "nested", "local"), filepath.Join(agent, ".system")} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			candidate := e
			candidate.Path, candidate.Name = path, filepath.Base(path)
			if r := RemovalFor(snap, candidate); r.Available {
				t.Fatalf("unsafe slot accepted: %+v", candidate)
			}
		})
	}
	// A newly created explicit dependency must block the apply-time scan too.
	body := "---\nrequires: [local]\n---\n"
	writeSkill(t, filepath.Join(agent, "consumer"), "consumer")
	if err := os.WriteFile(filepath.Join(agent, "consumer", "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil || !strings.Contains(err.Error(), "consumer") {
		t.Fatalf("fresh dependency was ignored: %v", err)
	}
}
