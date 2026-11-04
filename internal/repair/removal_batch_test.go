package repair

import (
	"os"
	"path/filepath"
	"skill-manager/internal/skill"
	"testing"
)

func TestRepairRemovalRechecksReferences(t *testing.T) {
	home, pool, agent := removalFixture(t)
	path := filepath.Join(pool, "dead")
	if err := os.Symlink("/missing-repair-target", path); err != nil {
		t.Fatal(err)
	}
	snap := skill.Scan(skill.Discover(home), nil)
	entry := removalEntry(t, snap, path)
	fixes := For(snap, entry, home)
	if len(fixes) != 1 || fixes[0].Op != OpRemoveLink {
		t.Fatalf("fixes: %+v", fixes)
	}
	if err := os.Symlink(path, filepath.Join(agent, "new-alias")); err != nil {
		t.Fatal(err)
	}
	if recs, err := ApplyAll(fixes, filepath.Join(home, "undo")); err == nil || len(recs) != 0 {
		t.Fatalf("batch ignored new reference: %v, %+v", err, recs)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("batch removed referenced pool link:", err)
	}
	snap = skill.Scan(skill.Discover(home), nil)
	if fixes := For(snap, removalEntry(t, snap, path), home); len(fixes) != 0 {
		t.Fatalf("unsafe fix offered: %+v", fixes)
	}
}

func TestRemovalIgnoresNestedSkillLinks(t *testing.T) {
	home, pool, agent := removalFixture(t)
	target := filepath.Join(pool, "alpha")
	suite := filepath.Join(agent, "suite")
	writeSkill(t, suite, "suite")
	if err := os.MkdirAll(filepath.Join(suite, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(suite, "skills", "part")); err != nil {
		t.Fatal(err)
	}
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, target)
	if r := RemovalFor(snap, e); !r.Available || r.Err != "" {
		t.Fatalf("nested reference blocked shallow removal: %+v", r)
	}
	if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), snap.Roots...); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(suite, "skills", "part")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("nested link changed: %v", err)
	}
}

func TestRemovalBlocksAgentLinkOnIncompleteScan(t *testing.T) {
	home, pool, agent := removalFixture(t)
	path := filepath.Join(agent, "alias")
	if err := os.Symlink(filepath.Join(pool, "alpha"), path); err != nil {
		t.Fatal(err)
	}
	roots := append(skill.Discover(home), skill.Root{ID: "missing", Name: "Missing", Role: skill.RoleAgent, Path: filepath.Join(home, ".missing", "skills")})
	snap := skill.Scan(roots, nil)
	e := removalEntry(t, snap, path)
	if r := RemovalFor(snap, e); r.Err == "" {
		t.Fatal("incomplete scan allowed agent link removal")
	}
	if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), snap.Roots...); err == nil {
		t.Fatal("removed link with unreadable known root")
	}
}
