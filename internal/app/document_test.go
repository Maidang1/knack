package app

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentLoadsOnlyWhenRequestedAndRefreshes(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "skills", "demo")
	writeSkill(t, path, "demo")
	mdPath := filepath.Join(path, "SKILL.md")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mdPath, append(md, []byte("\nUNIQUE_BODY_CONTENT\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.startScan()
	if len(a.documents) != 0 {
		t.Fatal("scan loaded document cache")
	}
	entries := a.snap.EntriesFor("claude-skills")
	if len(entries) != 1 {
		t.Fatalf("entries: %+v", entries)
	}
	e := entries[0]
	if e.TreeHash != "" || e.SkillHash != "" {
		t.Fatal("scan hashed contents")
	}
	state := a.documentFor(e)
	if state.loading || state.err != "" || state.document.Text == "" {
		t.Fatalf("body load: %+v", state)
	}
	if a.documentFor(e) != state {
		t.Fatal("detail reread unchanged document")
	}
	if err := os.WriteFile(mdPath, append(md, []byte("\nNEW_BODY_CONTENT\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	a.startScan()
	next := a.documentFor(a.snap.EntriesFor("claude-skills")[0])
	if next == state || next.document.Text == state.document.Text {
		t.Fatal("new scan failed to invalidate detail cache")
	}
}

func TestBodyReferencesLoadOnlyForGraph(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(root, "helper"), "helps")
	writeSkill(t, filepath.Join(root, "lead"), "leads")
	mdPath := filepath.Join(root, "lead", "SKILL.md")
	md, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mdPath, append(md, []byte("\nUse the `helper` skill.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.startScan()
	if len(a.snap.GraphFor("claude-skills").Edges) != 0 {
		t.Fatal("list scanned body references")
	}
	if len(a.graphFor("claude-skills").Edges) == 0 {
		t.Fatal("requested graph did not infer body references")
	}
	if len(a.snap.GraphFor("claude-skills").Edges) != 0 {
		t.Fatal("graph inference mutated catalog")
	}
	if len(a.documents) != 0 {
		t.Fatal("graph loaded all details cache")
	}
}

func TestDocumentRejectsRetargetedSelection(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	writeSkill(t, filepath.Join(pool, "one"), "one")
	writeSkill(t, filepath.Join(pool, "two"), "two")
	agent := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(agent, "selected")
	if err := os.Symlink(filepath.Join(pool, "one"), path); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.startScan()
	e := a.snap.EntriesFor("claude-skills")[0]
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(pool, "two"), path); err != nil {
		t.Fatal(err)
	}
	if state := a.documentFor(e); state.err == "" {
		t.Fatal("cached old entry accepted new target body")
	}
}

func TestFailedActionDrainsPendingScan(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".agents", "skills", "demo"), "demo")
	a := New(home)
	a.startScan()
	gen := a.scanGen
	a.rescanPending = true
	a.completed = &actionResult{err: fmt.Errorf("rejected stale entry")}
	_ = ui.NewTester(a.View, 1200, 860)
	if a.scanGen != gen+1 || a.rescanPending {
		t.Fatal("failed operation lost pending refresh")
	}
}
