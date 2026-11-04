package repair

import (
	"os"
	"path/filepath"
	"testing"

	"skill-manager/internal/manage"
	"skill-manager/internal/skill"
)

func writeSkill(t *testing.T, dir, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(dir) + "\ndescription: \"" + desc + "\"\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (home string, snap *skill.Snapshot) {
	t.Helper()
	home = t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	for _, n := range []string{"same", "stale", "fixme", "orphan"} {
		writeSkill(t, filepath.Join(pool, n), "pool "+n)
	}
	writeSkill(t, filepath.Join(claude, "same"), "pool same")
	writeSkill(t, filepath.Join(claude, "stale"), "different")
	if err := os.Symlink("/tmp/repair-missing", filepath.Join(claude, "fixme")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/repair-missing-pool", filepath.Join(pool, "dead")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claude, "blank"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(claude, "kept"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "kept", "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap = skill.Scan(skill.Discover(home), nil)
	for i, entry := range snap.Entries {
		if entry.RootRole != skill.RoleAgent || entry.Kind != skill.KindSkill {
			continue
		}
		pool, ok := snap.PoolEntry(entry.Name)
		if !ok {
			continue
		}
		copy, comparedPool, err := skill.CompareCopies(entry, pool)
		if err != nil {
			t.Fatal(err)
		}
		snap.Entries[i] = copy
		snap.Entries[snap.PoolIndex[entry.Name]] = comparedPool
	}
	return home, skill.FromCatalog(snap.Roots, snap.Entries, snap.Warnings, snap.At)
}

func TestPlanSplitsSafeFromReview(t *testing.T) {
	home, snap := fixture(t)
	plan := Plan(snap, home)
	safe, review := Split(plan)

	ops := map[string]Op{}
	for _, f := range safe {
		ops[f.Entry.Name] = f.Op
	}
	want := map[string]Op{"same": OpRelink, "fixme": OpRetarget, "dead": OpRemoveLink, "blank": OpRemoveDir}
	if len(ops) != len(want) {
		t.Fatalf("safe fixes %v, want %v", ops, want)
	}
	for name, op := range want {
		if ops[name] != op {
			t.Errorf("%s: %s, want %s", name, ops[name], op)
		}
	}
	if len(review) != 1 || review[0].Entry.Name != "stale" {
		t.Fatalf("review %+v", review)
	}
	for _, f := range plan {
		switch f.Entry.Name {
		case "kept", "orphan":
			t.Errorf("%s has no automatic fix, got %s", f.Entry.Name, f.Op)
		}
	}
}

func TestApplyAllAndRestore(t *testing.T) {
	home, snap := fixture(t)
	undo := filepath.Join(home, "undo")
	safe, _ := Split(Plan(snap, home))
	recs, err := ApplyAll(safe, undo)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(safe) {
		t.Fatalf("%d records for %d fixes", len(recs), len(safe))
	}
	claude := filepath.Join(home, ".claude", "skills")
	if info, err := os.Lstat(filepath.Join(claude, "same")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("same was not relinked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "fixme", "SKILL.md")); err != nil {
		t.Fatalf("fixme does not reach the pool: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(claude, "stale")); err != nil {
		t.Fatal("a stale copy was touched by the safe batch")
	}
	again := skill.Scan(skill.Discover(home), nil)
	if safeLeft, _ := Split(Plan(again, home)); len(safeLeft) != 0 {
		t.Fatalf("safe fixes remain after repairing: %+v", safeLeft)
	}
	for i := len(recs) - 1; i >= 0; i-- {
		if err := manage.Restore(recs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Lstat(filepath.Join(claude, "same")); err != nil || !info.IsDir() {
		t.Fatalf("restore did not bring back the copy: %v", err)
	}
}

func TestLinkToAgent(t *testing.T) {
	home, snap := fixture(t)
	pool, _ := snap.PoolEntry("orphan")
	var claude skill.Root
	for _, r := range snap.Roots {
		if r.Role == skill.RoleAgent {
			claude = r
		}
	}
	rec, err := Apply(LinkToAgent(pool, claude), filepath.Join(home, "undo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claude.Path, "orphan", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := manage.Restore(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(claude.Path, "orphan")); !os.IsNotExist(err) {
		t.Fatalf("restore left the link behind: %v", err)
	}

	same, _ := snap.PoolEntry("same")
	if _, err := Apply(LinkToAgent(same, claude), filepath.Join(home, "undo")); err == nil {
		t.Fatal("linked over a real directory")
	}
}

func TestApply(t *testing.T) {
	for _, tc := range []struct {
		name, copyName, changed string
	}{
		{"matching copy", "same", ""},
		{"different copy", "stale", ""},
		{"changed copy resource", "same", "copy"},
		{"changed pool resource", "same", "pool"},
		{"changed differing pool", "stale", "pool"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, snap := fixture(t)
			path := filepath.Join(home, ".claude", "skills", tc.copyName)
			e := removalEntry(t, snap, path)
			fixes := For(snap, e, home)
			if len(fixes) != 1 || fixes[0].Op != OpRelink {
				t.Fatalf("fixes: %+v", fixes)
			}
			if tc.changed != "" {
				changed := path
				if tc.changed == "pool" {
					changed = filepath.Join(home, ".agents", "skills", tc.copyName)
				}
				if err := os.WriteFile(filepath.Join(changed, "script.sh"), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			undo := filepath.Join(home, "undo")
			rec, err := Apply(fixes[0], undo)
			if tc.changed != "" {
				if err == nil {
					t.Fatal("relinked after confirmed content changed")
				}
				if info, err := os.Lstat(path); err != nil || !info.IsDir() {
					t.Fatalf("copy was changed: %v", err)
				}
				if records, err := os.ReadDir(undo); err == nil && len(records) != 0 {
					t.Fatalf("unexpected undo records: %v", records)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := manage.Restore(rec); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(path); err != nil || !info.IsDir() {
				t.Fatalf("copy not restored: %v", err)
			}
		})
	}
}

func TestFor(t *testing.T) {
	home, snap := fixture(t)
	path := filepath.Join(home, ".claude", "skills", "same")
	e := removalEntry(t, snap, path)
	if fixes := For(snap, e, home); len(fixes) != 1 || !fixes[0].Safe || fixes[0].TargetHash == "" {
		t.Fatalf("compared copy has no safe relink: %+v", fixes)
	}
	e.Relation = skill.RelCopy
	e.TreeHash, e.SkillHash = "", ""
	if fixes := For(snap, e, home); len(fixes) != 0 {
		t.Fatalf("uncompared copy offered repair: %+v", fixes)
	}
	e.Relation = skill.RelSame
	if fixes := For(snap, e, home); len(fixes) != 0 {
		t.Fatalf("missing comparison hash offered repair: %+v", fixes)
	}
}
