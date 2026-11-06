package app

import (
	"os"
	"path/filepath"
	"strings"
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
	for _, want := range []string{"在 proj 里启动 Claude 时，会加载 3 个技能", "review 的查找路径", "1 项目：加载这一份", "2 全局：同名，跳过", "4 内置：不存在",
		"2 个同名副本仍占用磁盘", "项目版本覆盖全局、共享池", "同名只取最靠前的一层，其余版本仍在磁盘上。"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %q", want, tt.Texts())
		}
	}
	if !strings.Contains(strings.Join(tt.Texts(), "\n"), "1 个技能被更高层覆盖") {
		t.Fatal("missing override count")
	}
	click(t, tt, "tdd")
	if !tt.HasText("tdd 的查找路径") || !tt.HasText("2 全局：加载这一份") {
		t.Fatalf("row did not choose its lookup: %q", tt.Texts())
	}
	click(t, tt, "存在覆盖")
	if tt.HasText("tdd") || !tt.HasText("review 的查找路径") {
		t.Fatal("override filter kept single-layer skills")
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
	if !tt.HasText("1 项目：加载这一份") {
		t.Fatalf("project folder: %q", tt.Texts())
	}
	click(t, tt, "工作目录：~/code/proj")
	if a.eff.workDir != other {
		t.Fatalf("workdir: %q", a.eff.workDir)
	}
	if !tt.HasText("在 elsewhere 里启动 Claude 时，会加载 3 个技能") || !tt.HasText("2 全局：加载这一份") || !tt.HasText("全局版本覆盖共享池") {
		t.Fatalf("other folder: %q", tt.Texts())
	}
}
