package repair

import (
	"os"
	"path/filepath"
	"testing"

	"knack/internal/fsutil"
	"knack/internal/skill"
)

func TestRepairRemovalRechecksReferences(t *testing.T) {
	home, pool, agent := removalFixture(t)
	path := filepath.Join(pool, "dead")
	makeSymlink(t, filepath.Join(home, "missing-repair-target"), path)
	snap := skill.Scan(skill.Discover(home), nil)
	entry := removalEntry(t, snap, path)
	fixes := For(snap, entry, home)
	if len(fixes) != 1 || fixes[0].Op != OpRemoveLink {
		t.Fatalf("fixes: %+v", fixes)
	}
	makeSymlink(t, path, filepath.Join(agent, "new-alias"))
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
	makeSymlink(t, target, filepath.Join(suite, "skills", "part"))
	snap := skill.Scan(skill.Discover(home), nil)
	e := removalEntry(t, snap, target)
	if r := RemovalFor(snap, e); !r.Available || r.Err != "" {
		t.Fatalf("nested reference blocked shallow removal: %+v", r)
	}
	if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), snap.Roots...); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(suite, "skills", "part")); err != nil || !fsutil.IsLink(filepath.Join(suite, "skills", "part"), info) {
		t.Fatalf("nested link changed: %v", err)
	}
}

func TestRemovalBlocksAgentLinkOnIncompleteScan(t *testing.T) {
	home, pool, agent := removalFixture(t)
	path := filepath.Join(agent, "alias")
	makeSymlink(t, filepath.Join(pool, "alpha"), path)
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
