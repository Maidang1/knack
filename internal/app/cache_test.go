package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"skill-manager/internal/manage"
	"skill-manager/internal/skill"
)

func TestViewCacheReusesRowsAndRefreshes(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	writeSkill(t, filepath.Join(pool, "alpha"), "alpha")
	writeSkill(t, filepath.Join(pool, "beta"), "beta")
	a := New(home)
	a.startScan()
	a.page = "pool"
	first := a.rows()
	second := a.rows()
	if &first[0] != &second[0] {
		t.Fatal("same view rebuilt rows")
	}
	snap, gen := a.snap, a.scanGen
	a.query = "beta"
	if rows := a.rows(); len(rows) != 1 || rows[0].name != "beta" {
		t.Fatalf("query cache: %+v", rows)
	}
	a.query = ""
	a.page = "issues"
	for _, r := range a.rows() {
		if r.kind == "" {
			t.Fatal("page cache returned pool rows")
		}
	}
	a.page = "pool"
	if a.snap != snap || a.scanGen != gen {
		t.Fatal("navigation rescanned")
	}
	writeSkill(t, filepath.Join(pool, "gamma"), "gamma")
	if len(a.rows()) != 2 {
		t.Fatal("display changed before explicit scan")
	}
	a.startScan()
	if len(a.rows()) != 3 {
		t.Fatal("scan failed to invalidate rows")
	}
}

func TestCachedRemovalStillRechecksDisk(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	agent := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(pool, "demo"), "demo")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.startScan()
	e, _ := a.snap.PoolEntry("demo")
	r := a.removalFor(e)
	if !r.Available || r.Err != "" {
		t.Fatalf("initial plan: %+v", r)
	}
	if err := os.Symlink(e.Path, filepath.Join(agent, "alias")); err != nil {
		t.Fatal(err)
	}
	if cached := a.removalFor(e); cached.Err != "" {
		t.Fatal("drawing reread disk")
	}
	a.confirmRemoval(e, r)
	if _, err := a.ask.run(); err == nil {
		t.Fatal("cached plan allowed unsafe removal")
	}
	if _, err := os.Stat(e.Path); err != nil {
		t.Fatal(err)
	}
	a.startScan()
	if r := a.removalFor(e); r.Err == "" {
		t.Fatal("scan did not invalidate removal plan")
	}
}

func TestInvocationChangeKeepsCatalog(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "demo"), "demo")
	a := New(home)
	a.startScan()
	snap, gen := a.snap, a.scanGen
	a.confirmInvocation("demo", manage.InvocationAuto, manage.InvocationManual)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "确认更改")
	if a.snap != snap || a.scanGen != gen {
		t.Fatal("invocation change rescanned skill tree")
	}
	if a.invocations["demo"] != manage.InvocationManual {
		t.Fatal("invocations not refreshed")
	}
	click(t, tt, "撤销")
	if a.snap != snap || a.scanGen != gen {
		t.Fatal("invocation undo rescanned skill tree")
	}
}

func TestReopenShowsCachedCatalogThenRefreshes(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	writeSkill(t, filepath.Join(pool, "old"), "old")
	first := New(home)
	first.startScan()
	if _, err := os.Stat(first.catalogPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(pool, "old")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(pool, "new"), "new")
	reopened := New(home)
	if !reopened.cached || reopened.snap == nil || reopened.scanGen != 0 {
		t.Fatal("reopen did not load cache before scan")
	}
	reopened.page = "pool"
	if rows := reopened.rows(); len(rows) != 1 || rows[0].name != "old" {
		t.Fatalf("cache display: %+v", rows)
	}
	old, _ := reopened.snap.PoolEntry("old")
	if reopened.removalFor(old).Available || len(reopened.fixesFor(old)) != 0 || len(reopened.repairPlan()) != 0 {
		t.Fatal("stale catalog enabled filesystem actions")
	}
	tt := ui.NewTester(reopened.View, 1200, 860)
	if !tt.HasText("缓存") || !tt.HasText("old") {
		t.Fatalf("cached initial view: %q", tt.Texts())
	}
	reopened.startScan()
	if reopened.cached || reopened.scanGen != 1 {
		t.Fatal("fresh scan did not replace cache")
	}
	if rows := reopened.rows(); len(rows) != 1 || rows[0].name != "new" {
		t.Fatalf("refreshed display: %+v", rows)
	}
	if poolEntry, ok := reopened.snap.PoolEntry("new"); !ok || poolEntry.RootRole != skill.RolePool {
		t.Fatal("missing fresh pool data")
	}
}
