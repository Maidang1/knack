package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func effectiveHome(t *testing.T) (home, work string) {
	t.Helper()
	home = t.TempDir()
	work = filepath.Join(home, "code", "proj")
	writeSkill(t, filepath.Join(work, ".claude", "skills", "review"), "project review")
	writeSkill(t, filepath.Join(home, ".claude", "skills", "review"), "global review")
	writeSkill(t, filepath.Join(home, ".claude", "skills", "tdd"), "global tdd")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "review"), "pool review")
	writeSkill(t, filepath.Join(home, ".agents", "skills", "writing"), "pool writing")
	return home, work
}

func TestEffectiveViewExplainsTheWinner(t *testing.T) {
	home, work := effectiveHome(t)
	a := New(home)
	a.SetWorkDir(work)
	tt := ui.NewTester(a.View, 1180, 760)
	click(t, tt, "重新扫描")
	click(t, tt, "生效视图")
	for _, want := range []string{"3 项生效", "1 项被覆盖", "为什么是这一个", "2 个同名副本仍占用磁盘", "项目 → 全局 → 共享池 → 内置"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	click(t, tt, "全貌")
	for _, want := range []string{"生效来源", "2 个副本", "同名只取最靠前的一层，其余版本仍在磁盘上。"} {
		if !tt.HasText(want) {
			t.Fatalf("matrix missing %q: %q", want, tt.Texts())
		}
	}
}

func TestEffectiveViewChangesWithTheFolder(t *testing.T) {
	home, work := effectiveHome(t)
	other := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.SetWorkDir(work)
	a.pickDirFn = func() (string, error) { return other, nil }
	tt := ui.NewTester(a.View, 1180, 760)
	click(t, tt, "重新扫描")
	click(t, tt, "生效视图")
	if !tt.HasText("1 项被覆盖") {
		t.Fatalf("project folder: %q", tt.Texts())
	}
	click(t, tt, "工作目录：~/code/proj")
	if a.eff.workDir != other {
		t.Fatalf("workdir: %q", a.eff.workDir)
	}
	if !tt.HasText("全局级") || !tt.HasText("覆盖 1 个同名") {
		t.Fatalf("other folder: %q", tt.Texts())
	}
}
