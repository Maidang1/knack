package repair

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"knack/internal/fsutil"
	"knack/internal/manage"
	"knack/internal/skill"
)

func removalFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	agent := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(pool, "alpha"), "pool")
	writeSkill(t, filepath.Join(agent, "local"), "local")
	return home, pool, agent
}

func removalEntry(t *testing.T, snap *skill.Snapshot, path string) skill.Entry {
	t.Helper()
	for _, e := range snap.Entries {
		if fsutil.EqualPath(e.Path, path) {
			return e
		}
	}
	t.Fatalf("missing entry %s", path)
	return skill.Entry{}
}

func TestRemovalFor(t *testing.T) {
	t.Run("eligible and protected entries", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		makeSymlink(t, filepath.Join(pool, "alpha"), filepath.Join(agent, "alias"))
		makeSymlink(t, filepath.Join(home, "no-such-removal-target"), filepath.Join(agent, "broken"))
		if err := os.Mkdir(filepath.Join(agent, "empty"), 0755); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, filepath.Join(agent, ".system", "shipped"), "system")
		snap := skill.Scan(skill.Discover(home), nil)
		for _, name := range []string{"local", "alias", "broken", "empty"} {
			e := removalEntry(t, snap, filepath.Join(agent, name))
			r := RemovalFor(snap, e)
			if !r.Available || r.Err != "" {
				t.Fatalf("%s: %+v", name, r)
			}
			if name == "alias" && (r.Label != "从 Claude 移除" || r.Confirm != "确认移除") {
				t.Fatalf("wrong link labels: %+v", r)
			}
		}
		shipped := removalEntry(t, snap, filepath.Join(agent, ".system", "shipped"))
		if RemovalFor(snap, shipped).Available {
			t.Fatal("system entry eligible")
		}
		local := removalEntry(t, snap, filepath.Join(agent, "local"))
		for _, role := range []skill.Role{skill.RoleBuiltin, skill.RoleRepo} {
			local.RootRole = role
			if RemovalFor(snap, local).Available {
				t.Fatalf("eligible role %s", role)
			}
		}
	})
	t.Run("alias and chained references", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		alias := filepath.Join(agent, "other-name")
		makeSymlink(t, filepath.Join(pool, "alpha"), alias)
		makeSymlink(t, alias, filepath.Join(agent, "chain"))
		snap := skill.Scan(skill.Discover(home), nil)
		r := RemovalFor(snap, removalEntry(t, snap, filepath.Join(pool, "alpha")))
		if !strings.Contains(r.Err, "other-name") || !strings.Contains(r.Err, "chain") {
			t.Fatalf("missed references: %+v", r)
		}
		local := filepath.Join(agent, "local")
		makeSymlink(t, local, filepath.Join(pool, "local-alias"))
		snap = skill.Scan(skill.Discover(home), nil)
		if RemovalFor(snap, removalEntry(t, snap, local)).Err == "" {
			t.Fatal("removed referenced real directory")
		}
	})
	t.Run("pool dependency", func(t *testing.T) {
		home, pool, _ := removalFixture(t)
		writeSkill(t, filepath.Join(pool, "consumer"), "uses")
		if err := os.WriteFile(filepath.Join(pool, "consumer", "SKILL.md"), []byte("---\nname: consumer\nrequires: [alpha]\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		r := RemovalFor(snap, removalEntry(t, snap, filepath.Join(pool, "alpha")))
		if !strings.Contains(r.Err, "consumer") {
			t.Fatalf("missed dependency: %+v", r)
		}
	})
	t.Run("agent dependency", func(t *testing.T) {
		home, _, agent := removalFixture(t)
		writeSkill(t, filepath.Join(agent, "consumer"), "uses")
		if err := os.WriteFile(filepath.Join(agent, "consumer", "SKILL.md"), []byte("---\nname: consumer\nrequires: [local]\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		r := RemovalFor(snap, removalEntry(t, snap, filepath.Join(agent, "local")))
		if !strings.Contains(r.Err, "consumer") {
			t.Fatalf("missed agent dependency: %+v", r)
		}
	})
	t.Run("links bypassing removed link", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		source := filepath.Join(t.TempDir(), "source")
		writeSkill(t, source, "source")
		selected := filepath.Join(pool, "source")
		makeSymlink(t, source, selected)
		makeSymlink(t, source, filepath.Join(agent, "bypass"))
		snap := skill.Scan(skill.Discover(home), nil)
		r := RemovalFor(snap, removalEntry(t, snap, selected))
		if r.Err != "" || r.Label != "从共享池移除" {
			t.Fatalf("bypass blocked link: %+v", r)
		}
		makeSymlink(t, selected, filepath.Join(agent, "through"))
		snap = skill.Scan(skill.Discover(home), nil)
		r = RemovalFor(snap, removalEntry(t, snap, selected))
		if !strings.Contains(r.Err, "through") || strings.Contains(r.Err, "bypass") {
			t.Fatalf("wrong traversal refs: %+v", r)
		}
	})
	t.Run("broken pool and empty reference safety", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		broken := filepath.Join(pool, "dead")
		makeSymlink(t, filepath.Join(home, "no-such-removal-target"), broken)
		empty := filepath.Join(pool, "empty")
		if err := os.Mkdir(empty, 0755); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{broken, empty} {
			makeSymlink(t, path, filepath.Join(agent, "alias-"+filepath.Base(path)))
		}
		snap := skill.Scan(skill.Discover(home), nil)
		for _, path := range []string{broken, empty} {
			r := RemovalFor(snap, removalEntry(t, snap, path))
			if !r.Available || r.Err == "" {
				t.Fatalf("unsafe existing removal %+v", r)
			}
		}
	})
	t.Run("body mentions and cross-root dependencies ignored", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		writeSkill(t, filepath.Join(pool, "consumer"), "consumer")
		body := "---\nname: consumer\n---\nUse the `alpha` skill.\n"
		if err := os.WriteFile(filepath.Join(pool, "consumer", "SKILL.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, filepath.Join(agent, "remote"), "remote")
		if err := os.WriteFile(filepath.Join(agent, "remote", "SKILL.md"), []byte("---\nrequires: [alpha]\n---\n"), 0644); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		if r := RemovalFor(snap, removalEntry(t, snap, filepath.Join(pool, "alpha"))); !r.Available || r.Err != "" {
			t.Fatalf("out-of-scope references blocked removal: %+v", r)
		}
	})
	t.Run("unreadable dependency metadata", func(t *testing.T) {
		home, pool, _ := removalFixture(t)
		consumer := filepath.Join(pool, "consumer")
		if err := os.MkdirAll(filepath.Join(consumer, "SKILL.md"), 0755); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, filepath.Join(pool, "alpha"))
		if r := RemovalFor(snap, e); !strings.Contains(r.Err, "consumer") {
			t.Fatalf("incomplete metadata allowed removal: %+v", r)
		}
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), snap.Roots...); err == nil {
			t.Fatal("removed with incomplete dependency metadata")
		}
	})
	t.Run("incomplete scan", func(t *testing.T) {
		home, pool, _ := removalFixture(t)
		roots := append(skill.Discover(home), skill.Root{ID: "missing", Name: "Missing", Path: filepath.Join(home, ".missing", "skills"), Role: skill.RoleAgent})
		snap := skill.Scan(roots, nil)
		r := RemovalFor(snap, removalEntry(t, snap, filepath.Join(pool, "alpha")))
		if !strings.Contains(r.Err, "Missing") {
			t.Fatalf("incomplete scan allowed: %+v", r)
		}
	})
}

func TestApplyRemoval(t *testing.T) {
	t.Run("remove and restore", func(t *testing.T) {
		home, _, agent := removalFixture(t)
		path := filepath.Join(agent, "local")
		snap := skill.Scan(skill.Discover(home), nil)
		rec, err := ApplyRemoval(removalEntry(t, snap, path), home, nil, filepath.Join(home, "undo"))
		if err != nil {
			t.Fatal(err)
		}
		if err := manage.Restore(rec); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("changed document", func(t *testing.T) {
		home, _, agent := removalFixture(t)
		path := filepath.Join(agent, "local")
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, path)
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("changed document"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
			t.Fatal("removed changed content")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("nested resource edits ignored", func(t *testing.T) {
		home, _, agent := removalFixture(t)
		path := filepath.Join(agent, "local")
		resource := filepath.Join(path, "scripts", "tool.sh")
		if err := os.MkdirAll(filepath.Dir(resource), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resource, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, path)
		// Comparison hashes do not authorize or block lightweight removal.
		e.TreeHash, e.SkillHash = "old tree", "old document"
		if err := os.WriteFile(resource, []byte("new script contents"), 0644); err != nil {
			t.Fatal(err)
		}
		rec, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"))
		if err != nil {
			t.Fatal(err)
		}
		if err := manage.Restore(rec); err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(resource)
		if err != nil || string(contents) != "new script contents" {
			t.Fatalf("resource not preserved by undo: %q, %v", contents, err)
		}
	})
	t.Run("changed target", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		path := filepath.Join(agent, "alias")
		makeSymlink(t, filepath.Join(pool, "alpha"), path)
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, path)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		makeSymlink(t, filepath.Join(agent, "local"), path)
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
			t.Fatal("removed retargeted link")
		}
	})
	t.Run("new reference", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		path := filepath.Join(pool, "alpha")
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, path)
		makeSymlink(t, path, filepath.Join(agent, "new-ref"))
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
			t.Fatal("removed newly referenced skill")
		}
	})
	t.Run("expected root unavailable", func(t *testing.T) {
		home, pool, agent := removalFixture(t)
		roots := skill.Discover(home)
		snap := skill.Scan(roots, nil)
		e := removalEntry(t, snap, filepath.Join(pool, "alpha"))
		if err := os.Rename(agent, filepath.Join(home, "hidden-agent")); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo"), roots...); err == nil {
			t.Fatal("removed while previous agent root unavailable")
		}
		if _, err := os.Stat(e.Path); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty directory identity changed", func(t *testing.T) {
		home, _, agent := removalFixture(t)
		path := filepath.Join(agent, "kept")
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "notes"), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		snap := skill.Scan(skill.Discover(home), nil)
		e := removalEntry(t, snap, path)
		if err := os.WriteFile(filepath.Join(path, "new-entry"), []byte("new"), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyRemoval(e, home, nil, filepath.Join(home, "undo")); err == nil {
			t.Fatal("removed changed empty slot")
		}
	})
}
