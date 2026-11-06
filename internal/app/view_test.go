package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestOverviewScanRelinkAndUndo(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(pool, "beta"), "beta skill")
	writeSkill(t, filepath.Join(pool, "cloud"), "pool cloud")
	writeSkill(t, filepath.Join(pool, "alpha"), "only in the pool")
	copyDirFile(t, filepath.Join(pool, "beta", "SKILL.md"), filepath.Join(claude, "beta", "SKILL.md"))
	writeSkill(t, filepath.Join(claude, "cloud"), "agent cloud")
	writeSkill(t, filepath.Join(claude, "extra"), "claude only")
	if err := os.MkdirAll(filepath.Join(pool, "blank"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/skill-manager-missing", filepath.Join(pool, "gone")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(pool, "observe"), "observe")
	if err := os.Symlink("/tmp/skill-manager-missing-observe", filepath.Join(claude, "observe")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(home, ".grok", "bundled", "skills", "shipped"), "bundled")

	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	if a.page != "overview" || !tt.HasText("每日 Token") {
		t.Fatalf("home before scan: %q", tt.Texts())
	}
	click(t, tt, "重新扫描")
	if !tt.HasText("问题") || !tt.HasText("总览") {
		t.Fatalf("after scan: %q", tt.Texts())
	}

	click(t, tt, "问题")
	if !tt.HasText("一键修复 3 项") || tt.HasText("替换 1 个不一致副本") {
		t.Fatalf("issues: %q", tt.Texts())
	}
	click(t, tt, "Claude")
	click(t, tt, "beta")
	if tt.HasText("改为软链") {
		t.Fatal("copy was compared during scan")
	}
	click(t, tt, "比较共享池内容")
	if !tt.HasText("改为软链") {
		t.Fatalf("duplicate detail: %q", tt.Texts())
	}
	click(t, tt, "改为软链")
	if !tt.HasText("确认改为软链") || !tt.HasText("内容一致") {
		t.Fatalf("confirm: %q", tt.Texts())
	}
	click(t, tt, "确认改为软链")
	if !tt.HasText("撤销") {
		t.Fatalf("toast: %q", tt.Texts())
	}
	info, err := os.Lstat(filepath.Join(claude, "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("beta is still a real directory")
	}
	info, err = os.Lstat(filepath.Join(claude, "cloud"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("stale cloud was changed")
	}

	click(t, tt, "撤销")
	if !tt.HasText("已撤销") {
		t.Fatalf("after undo: %q", tt.Texts())
	}
	info, err = os.Lstat(filepath.Join(claude, "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatal("undo did not restore the directory")
	}
}

func TestStaleConfirmAndBrokenRetarget(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(pool, "cloud"), "pool cloud")
	writeSkill(t, filepath.Join(claude, "cloud"), "agent cloud")
	writeSkill(t, filepath.Join(pool, "observe"), "observe")
	if err := os.Symlink("/tmp/skill-manager-missing-observe", filepath.Join(claude, "observe")); err != nil {
		t.Fatal(err)
	}

	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "Claude")
	click(t, tt, "cloud")
	click(t, tt, "比较共享池内容")
	click(t, tt, "改为软链")
	if !tt.HasText("目录内容不一致") || !tt.HasText("确认改为软链") {
		t.Fatalf("stale confirm: %q", tt.Texts())
	}
	click(t, tt, "取消")
	info, err := os.Lstat(filepath.Join(claude, "cloud"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("cancel replaced the stale copy")
	}

	click(t, tt, "observe")
	click(t, tt, "改指向共享池")
	if !tt.HasText("确认改指向") {
		t.Fatalf("retarget confirm: %q", tt.Texts())
	}
	click(t, tt, "确认改指向")
	target, err := os.Readlink(filepath.Join(claude, "observe"))
	if err != nil {
		t.Fatal(err)
	}
	abs := target
	if !filepath.IsAbs(target) {
		abs = filepath.Join(claude, target)
	}
	if filepath.Clean(abs) != filepath.Join(pool, "observe") {
		t.Fatalf("observe points at %s", abs)
	}
}

func TestEmptyDirAndBuiltinReadOnly(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(pool, "blank"), 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(pool, "scratch")
	if err := os.MkdirAll(kept, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kept, "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(home, ".grok", "bundled", "skills", "shipped"), "bundled")

	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "问题")
	click(t, tt, "空目录")
	click(t, tt, "blank")
	if !tt.HasText("移走空目录") {
		t.Fatalf("blank: %q", tt.Texts())
	}
	click(t, tt, "移走空目录")
	click(t, tt, "确认移走")
	if _, err := os.Lstat(filepath.Join(pool, "blank")); !os.IsNotExist(err) {
		t.Fatalf("blank still there: %v", err)
	}
	click(t, tt, "撤销")
	if _, err := os.Lstat(filepath.Join(pool, "blank")); err != nil {
		t.Fatal(err)
	}

	click(t, tt, "问题")
	click(t, tt, "空目录")
	click(t, tt, "scratch")
	if !tt.HasText("不会自动移走") {
		t.Fatalf("scratch: %q", tt.Texts())
	}
	if tt.HasText("移走空目录") {
		t.Fatal("offered to remove a directory that still has files")
	}

	click(t, tt, "Grok")
	if !tt.HasText("只展示") || tt.HasText("改为软链") {
		t.Fatalf("builtin: %q", tt.Texts())
	}
}

func TestSidebarCollapse(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "alpha"), "alpha")
	a := New(home)
	a.startScan()
	tt := ui.NewTester(a.View, 1180, 760)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})

	if !tt.HasText("生效视图") {
		t.Fatalf("sidebar before collapsing: %q", tt.Texts())
	}
	open, ok := tt.Find("收起侧边栏")
	if !ok {
		t.Fatalf("toggle before collapsing: %q", tt.Texts())
	}
	click(t, tt, "收起侧边栏")
	if tt.HasText("生效视图") || tt.HasText("收起侧边栏") {
		t.Fatalf("sidebar after collapsing: %q", tt.Texts())
	}
	if !tt.HasText("展开侧边栏") {
		t.Fatalf("toggle after collapsing: %q", tt.Texts())
	}
	// The toggle stays on the traffic lights' line and clears them.
	shut, ok := tt.Find("展开侧边栏")
	if !ok || shut.X < 70 {
		t.Fatalf("toggle under the window controls: %+v %v", shut, ok)
	}
	if d := shut.Y - open.Y; d < -2 || d > 2 {
		t.Fatalf("toggle moved when collapsed: open y=%.1f, shut y=%.1f", open.Y, shut.Y)
	}
	click(t, tt, "展开侧边栏")
	if !tt.HasText("生效视图") || !tt.HasText("收起侧边栏") {
		t.Fatalf("sidebar after expanding: %q", tt.Texts())
	}
}

func TestToolbarRowAlignment(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "alpha"), "alpha")
	for _, bar := range []struct {
		name   string
		room   ui.TitleBar
		center float32
	}{
		{"inset", ui.TitleBar{Height: 52, Left: 70}, 18},
		{"right-controls", ui.TitleBar{Height: 44, Right: 120}, 22},
		{"fullscreen", ui.TitleBar{}, 22},
	} {
		for _, width := range []int{1180, 880} {
			for _, collapsed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/collapsed=%t", bar.name, width, collapsed), func(t *testing.T) {
					a := New(home)
					a.startScan()
					a.page = effectivePage
					a.sideCollapsed = collapsed
					a.SetWorkDir(filepath.Join(home, "projects", "工具栏对齐检查"))
					tt := ui.NewTester(a.View, width, 760)
					tt.SetTitleBar(bar.room)
					toggle := "收起侧边栏"
					if collapsed {
						toggle = "展开侧边栏"
					}
					labels := []string{toggle, "Agent：Claude", "工作目录：" + shortPath(home, a.eff.workDir)}
					if collapsed {
						labels = append(labels, "生效视图")
					}
					for _, label := range labels {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.H <= 0 {
							t.Fatalf("missing toolbar control %q: %+v", label, r)
						}
						if d := r.Y + r.H/2 - bar.center; d < -1 || d > 1 {
							t.Errorf("%q center y=%.1f, want %.1f: %+v", label, r.Y+r.H/2, bar.center, r)
						}
						if collapsed && (r.X < bar.room.Left || r.X+r.W > float32(width)-bar.room.Right) {
							t.Errorf("%q overlaps window controls: %+v", label, r)
						}
					}
					count, ok := tt.Find("1 项生效")
					if !ok {
						t.Fatal("missing effective list header")
					}
					// The first tab keeps the old header's alignment with
					// the sidebar, without making the title bar taller.
					if want := 2*bar.center + 10; count.Y < want-1 || count.Y > want+1 {
						t.Errorf("list header y=%.1f, want %.1f", count.Y, want)
					}
				})
			}
		}
	}
}

func TestAddRepoLinkAndRemove(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "demo")
	writeSkill(t, filepath.Join(repo, "gamma"), "from the repo")

	a := New(home)
	a.pickDirFn = func() (string, error) { return repo, nil }
	var copied string
	a.copyFn = func(s string) { copied = s }
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "添加仓库…")
	if !tt.HasText("gamma") || !tt.HasText("挂到共享池") {
		t.Fatalf("repo: %q", tt.Texts())
	}
	click(t, tt, "挂到共享池")
	if !tt.HasText("其他 agent 的目录保持原样") || !tt.HasText("确认挂到共享池") {
		t.Fatalf("link explanation: %q", tt.Texts())
	}
	click(t, tt, "确认挂到共享池")
	got, err := os.Readlink(filepath.Join(pool, "gamma"))
	if err != nil {
		t.Fatal(err)
	}
	abs := got
	if !filepath.IsAbs(got) {
		abs = filepath.Join(pool, got)
	}
	if filepath.Clean(abs) != filepath.Join(repo, "gamma") {
		t.Fatalf("pool gamma points at %s", abs)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "gamma")); !os.IsNotExist(err) {
		t.Fatal("linking the pool also created an agent entry")
	}

	click(t, tt, "不再扫描")
	click(t, tt, "确认不再扫描")
	if _, err := os.Stat(filepath.Join(repo, "gamma", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), repo) {
		t.Fatalf("repo still configured: %s", body)
	}

	a.pickDirFn = func() (string, error) { return pool, nil }
	click(t, tt, "添加仓库…")
	if !tt.HasText("已经在扫描范围内") {
		t.Fatalf("pool rejected: %q", tt.Texts())
	}

	click(t, tt, "共享池")
	click(t, tt, "更多")
	click(t, tt, "复制路径")
	if copied == "" {
		t.Fatal("copy was not called")
	}
}

func TestSearchFiltersTheList(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	writeSkill(t, filepath.Join(pool, "alpha"), "alpha")
	writeSkill(t, filepath.Join(pool, "beta"), "beta")
	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "共享池")
	click(t, tt, "搜索")
	tt.Type("beta")
	if !tt.HasText("beta") || tt.HasText("alpha") {
		t.Fatalf("search: %q", tt.Texts())
	}
}

func TestRepairAllAndUndo(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	claude := filepath.Join(home, ".claude", "skills")
	writeSkill(t, filepath.Join(pool, "beta"), "beta skill")
	writeSkill(t, filepath.Join(pool, "cloud"), "pool cloud")
	writeSkill(t, filepath.Join(pool, "observe"), "observe")
	copyDirFile(t, filepath.Join(pool, "beta", "SKILL.md"), filepath.Join(claude, "beta", "SKILL.md"))
	writeSkill(t, filepath.Join(claude, "cloud"), "agent cloud")
	if err := os.Symlink("/tmp/skill-manager-missing-observe", filepath.Join(claude, "observe")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pool, "blank"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/tmp/skill-manager-missing", filepath.Join(pool, "gone")); err != nil {
		t.Fatal(err)
	}

	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "问题")
	if !tt.HasText("一键修复 3 项") {
		t.Fatalf("repair bar: %q", tt.Texts())
	}
	click(t, tt, "一键修复 3 项")
	if !tt.HasText("确认修复") {
		t.Fatalf("batch confirm: %q", tt.Texts())
	}
	click(t, tt, "确认修复")

	if info, err := os.Lstat(filepath.Join(claude, "beta")); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("uncompared beta was modified: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "observe", "SKILL.md")); err != nil {
		t.Fatalf("observe was not retargeted: %v", err)
	}
	for _, gone := range []string{"gone", "blank"} {
		if _, err := os.Lstat(filepath.Join(pool, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s is still in the pool: %v", gone, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(claude, "cloud")); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a copy with different content was replaced without asking")
	}

	click(t, tt, "撤销")
	if info, err := os.Lstat(filepath.Join(claude, "beta")); err != nil || !info.IsDir() {
		t.Fatalf("undo did not bring beta back: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(pool, "gone")); err != nil {
		t.Fatalf("undo did not bring the broken link back: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(pool, "blank")); err != nil {
		t.Fatalf("undo did not bring the empty directory back: %v", err)
	}

	click(t, tt, "Claude")
	click(t, tt, "cloud")
	click(t, tt, "比较共享池内容")
	click(t, tt, "改为软链")
	click(t, tt, "确认改为软链")
	if info, err := os.Lstat(filepath.Join(claude, "cloud")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("cloud was not replaced after confirming: %v", err)
	}
}

func TestDependencyGraphView(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "proj")
	writeSkill(t, filepath.Join(repo, "helper"), "helps")
	writeSkill(t, filepath.Join(repo, "solo"), "alone")
	lead := filepath.Join(repo, "lead")
	if err := os.MkdirAll(lead, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: lead\ndescription: \"leads\"\nrequires: [helper, absent]\n---\n\nRun `helper` first.\n"
	if err := os.WriteFile(filepath.Join(lead, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(home)
	a.pickDirFn = func() (string, error) { return repo, nil }
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "添加仓库…")
	click(t, tt, "依赖图")
	if !tt.HasText("2 个 skill 有依赖关系 · 1 个互相独立 · 1 个依赖找不到") {
		t.Fatalf("graph header: %q", tt.Texts())
	}
	for _, name := range []string{"lead", "helper", "absent"} {
		if !tt.HasText(name) {
			t.Fatalf("node %s missing: %q", name, tt.Texts())
		}
	}
	if tt.HasText("solo") {
		t.Fatal("a skill with no dependencies was drawn")
	}
	click(t, tt, "helper")
	if !tt.HasText("被 lead 依赖") {
		t.Fatalf("detail of helper: %q", tt.Texts())
	}
	click(t, tt, "absent")
	if !tt.HasText("找不到") {
		t.Fatalf("detail of absent: %q", tt.Texts())
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("click %s: %v\ntexts: %q", label, err, tt.Texts())
	}
}

func writeSkill(t *testing.T, dir, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(dir) + "\ndescription: \"" + desc + "\"\n---\n\n# " + filepath.Base(dir) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyDirFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedLogosParse(t *testing.T) {
	for prefix, dir := range map[string]string{"logo:": "logos", "color:": "logos/color"} {
		files, err := logoFS.ReadDir(dir)
		if err != nil || len(files) == 0 {
			t.Fatalf("no logos in %s: %v", dir, err)
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if name := prefix + strings.TrimSuffix(f.Name(), ".svg"); svgs[name] == nil {
				t.Errorf("%s/%s did not parse", dir, f.Name())
			}
		}
	}
}
