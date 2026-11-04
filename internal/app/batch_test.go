package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"knack/internal/repair"
	"knack/internal/skill"
)

func issueBatchFixture(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	home := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		writeSkill(t, filepath.Join(home, ".agents", "skills", name), name+" description")
	}
	writeSkill(t, filepath.Join(home, ".claude", "skills", "local"), "local")
	a := New(home)
	a.startScan()
	tt := ui.NewTester(a.View, 1180, 760)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	return a, tt
}

func TestIssueMultiSelection(t *testing.T) {
	a, tt := issueBatchFixture(t)
	click(t, tt, "选择 beta")
	if got := a.chosenIssues(); len(got) != 1 || got[0].Name != "beta" || !tt.HasText("beta description") {
		t.Fatalf("checkbox does not select and show beta: %+v", got)
	}
	click(t, tt, "选择 alpha")
	if len(a.chosenIssues()) != 2 || !tt.HasText("已选择 2 项") || !tt.HasText("删除所选") {
		t.Fatal("checkboxes did not open batch actions")
	}
	click(t, tt, "选择 beta")
	if got := a.chosenIssues(); len(got) != 1 || got[0].Name != "alpha" || !tt.HasText("alpha description") {
		t.Fatalf("single detail after deselecting is wrong: %+v", got)
	}
	click(t, tt, "取消选择")
	click(t, tt, "alpha")
	if err := tt.ClickWith(ui.Cmd, "gamma"); err != nil {
		t.Fatal(err)
	}
	if len(a.chosenIssues()) != 2 {
		t.Fatal("Cmd-click did not add a row")
	}
	click(t, tt, "alpha")
	if err := tt.ClickWith(ui.Shift, "gamma"); err != nil {
		t.Fatal(err)
	}
	if len(a.chosenIssues()) != 3 {
		t.Fatal("Shift-click did not select the range")
	}
	click(t, tt, "取消选择")
	click(t, tt, "alpha")
	tt.Key(ui.Cmd, ui.KeyA)
	if len(a.chosenIssues()) != 3 {
		t.Fatal("Cmd+A did not select all")
	}
	click(t, tt, "搜索")
	tt.Type("beta")
	if len(a.chosenIssues()) != 0 {
		t.Fatal("query retained invisible choices")
	}
	click(t, tt, "全选")
	if got := a.chosenIssues(); len(got) != 1 || got[0].Name != "beta" {
		t.Fatalf("select all exceeded filtered results: %+v", got)
	}
	click(t, tt, "断链")
	if a.state("issues").chosen.Len() != 0 || tt.HasText("删除所选") {
		t.Fatal("category change retained choices")
	}
}

func TestIssueBatchRemovalAndUndo(t *testing.T) {
	a, tt := issueBatchFixture(t)
	click(t, tt, "选择 alpha")
	click(t, tt, "选择 beta")
	click(t, tt, "删除所选")
	if !strings.Contains(a.ask.message, "alpha") || strings.Contains(a.ask.message, "gamma") {
		t.Fatalf("confirmation exceeds selected items: %s", a.ask.message)
	}
	click(t, tt, "取消")
	if len(a.chosenIssues()) != 2 {
		t.Fatal("cancel lost choices")
	}
	click(t, tt, "删除所选")
	click(t, tt, "确认批量删除")
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Lstat(filepath.Join(a.home, ".agents", "skills", name)); !os.IsNotExist(err) {
			t.Fatalf("%s was not removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(a.home, ".agents", "skills", "gamma", "SKILL.md")); err != nil {
		t.Fatal("unselected skill was changed:", err)
	}
	if a.state("issues").chosen.Len() != 0 {
		t.Fatal("new scan retained old choices")
	}
	click(t, tt, "撤销")
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(a.home, ".agents", "skills", name, "SKILL.md")); err != nil {
			t.Fatal("batch undo did not restore", name, err)
		}
	}
}

func TestIssueBatchRemovalFreshReferenceAndPartialUndo(t *testing.T) {
	a, tt := issueBatchFixture(t)
	click(t, tt, "全选")
	click(t, tt, "删除所选")
	// A reference appears after confirmation was opened. Earlier successes
	// remain undoable, and the newly referenced entry must remain on disk.
	if err := os.Symlink(filepath.Join(a.home, ".agents", "skills", "beta"), filepath.Join(a.home, ".claude", "skills", "new-reference")); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "确认批量删除")
	if !tt.HasText("已删除 1/3 项") {
		t.Fatalf("partial failure not reported: %q", tt.Texts())
	}
	for _, name := range []string{"beta", "gamma"} {
		if _, err := os.Stat(filepath.Join(a.home, ".agents", "skills", name, "SKILL.md")); err != nil {
			t.Fatal("removed blocked or unprocessed entry:", name, err)
		}
	}
	click(t, tt, "撤销")
	if _, err := os.Stat(filepath.Join(a.home, ".agents", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatal("partial batch could not be undone:", err)
	}
}

func TestIssueBatchLinksAndUndo(t *testing.T) {
	a, tt := issueBatchFixture(t)
	click(t, tt, "全选")
	click(t, tt, "挂到 Agent")
	if err := tt.ChooseMenuItem("Claude（3 项）"); err != nil {
		t.Fatal(err)
	}
	// A conflicting copy added after confirmation must never be overwritten.
	writeSkill(t, filepath.Join(a.home, ".claude", "skills", "alpha"), "existing copy")
	click(t, tt, "确认批量挂出")
	for _, name := range []string{"beta", "gamma"} {
		path := filepath.Join(a.home, ".claude", "skills", name)
		if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("batch did not link", name, err)
		}
	}
	click(t, tt, "撤销")
	for _, name := range []string{"beta", "gamma"} {
		if _, err := os.Lstat(filepath.Join(a.home, ".claude", "skills", name)); !os.IsNotExist(err) {
			t.Fatal("batch undo kept link", name, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(a.home, ".claude", "skills", "alpha")); err != nil || !info.IsDir() {
		t.Fatal("same-name copy was changed:", err)
	}
}

func TestIssueBatchRepairOnlyChosen(t *testing.T) {
	a, tt := issueBatchFixture(t)
	for _, name := range []string{"blank-a", "blank-b", "blank-c"} {
		if err := os.MkdirAll(filepath.Join(a.home, ".agents", "skills", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.startScan()
	tt.Frame()
	click(t, tt, "空目录")
	click(t, tt, "选择 blank-a")
	click(t, tt, "选择 blank-b")
	if got := a.plan(a.issueScope()); len(got) != 2 {
		t.Fatalf("repair plan exceeds selection: %+v", got)
	}
	click(t, tt, "修复所选 2 项")
	click(t, tt, "确认修复")
	if _, err := os.Stat(filepath.Join(a.home, ".agents", "skills", "blank-c")); err != nil {
		t.Fatal("unselected issue was repaired:", err)
	}
	click(t, tt, "撤销")
}

func TestIssueBatchProtectionAndCachedState(t *testing.T) {
	a, tt := issueBatchFixture(t)
	click(t, tt, "全选")
	entries := a.chosenIssues()
	// An explicit same-root dependency blocks batch deletion.
	for i := range a.snap.Entries {
		if a.snap.Entries[i].Path == entries[0].Path {
			a.snap.Entries[i].Requires = []string{"beta"}
		}
	}
	a.cache.snap = nil
	if reason := a.batchRemovalError(entries); reason == "" {
		t.Fatal("batch ignored dependency protection")
	}
	a.confirmBatchRemoval(entries)
	if a.ask.open {
		t.Fatal("protected batch opened confirmation")
	}
	a.cached = true
	tt.Frame()
	click(t, tt, "删除所选")
	if a.ask.open {
		t.Fatal("cached batch deletion remained enabled")
	}
	a.confirmBatchLinks([]repair.Fix{repair.LinkToAgent(entries[0], a.roots(skill.RoleAgent)[0])}, "Claude", 0)
	if a.ask.open {
		t.Fatal("cached batch links opened confirmation")
	}
}

func TestIssueBatchLayout(t *testing.T) {
	for _, width := range []int{1180, 880} {
		for _, dark := range []bool{false, true} {
			a, tt := issueBatchFixture(t)
			tt.SetSize(width, 560)
			tt.SetDark(dark)
			for _, listWidth := range []float32{440, 180} {
				a.listW = listWidth
				tt.Frame()
				click(t, tt, "全选")
				for _, label := range []string{"全选", "取消选择", "选择 alpha", "挂到 Agent", "删除所选"} {
					r, ok := tt.Find(label)
					if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.X+r.W > float32(width) || r.Y < 36 || r.Y+r.H > 560-footerHeight {
						t.Errorf("width=%d/list=%g/dark=%t: %q is clipped: %+v", width, listWidth, dark, label, r)
					}
					if label == "选择 alpha" || label == "取消选择" || label == "全选" {
						if r.X+r.W > a.sidebarWidth()+a.listW+1 {
							t.Errorf("list control %q overflowed into details: %+v", label, r)
						}
					}
				}
			}
		}
	}
}
