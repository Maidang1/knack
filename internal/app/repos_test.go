package app

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"knack/internal/store"
)

func repoMenuChoice(t *testing.T, tt *ui.Tester, path, choice string) {
	t.Helper()
	click(t, tt, "仓库操作："+path)
	click(t, tt, choice)
}

func TestRepositoryRecordCRUD(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "projects", "demo")
	next := filepath.Join(home, "projects", "next")
	writeSkill(t, filepath.Join(repo, "alpha"), "original skill")
	writeSkill(t, filepath.Join(next, "beta"), "new scan scope")
	a := New(home)
	a.pickDirFn = func() (string, error) { return repo, nil }
	var copied, revealed string
	a.copyFn = func(path string) { copied = path }
	a.revealFn = func(path string) { revealed = path }
	tt := ui.NewTester(a.View, 1180, 560)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	if _, ok := tt.Find("添加仓库…"); !ok {
		t.Fatalf("missing add button: %q", tt.Texts())
	}
	click(t, tt, "添加仓库…")
	if a.page != "repo:"+repo || len(a.repos) != 1 || !tt.HasText("alpha") {
		t.Fatalf("adding: %+v %q", a.repos, tt.Texts())
	}
	added := a.repos[0].Added

	repoMenuChoice(t, tt, repo, "查看路径…")
	if !tt.HasText("仓库信息") || !tt.HasText(repo) {
		t.Fatalf("details: %q", tt.Texts())
	}
	click(t, tt, "复制路径")
	if copied != repo {
		t.Fatalf("copied %q", copied)
	}
	click(t, tt, "完成")
	if err := tt.RightClick("demo"); err != nil {
		t.Fatal(err)
	}
	// The detail bar has a button of the same name, so choose by keys.
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if revealed != repo {
		t.Fatalf("revealed %q", revealed)
	}

	repoMenuChoice(t, tt, repo, "编辑仓库…")
	click(t, tt, "仓库显示名称")
	tt.Type("我的项目")
	click(t, tt, "保存")
	if a.repoLabel(repo) != "我的项目" || a.pageTitle() != "我的项目" {
		t.Fatalf("rename: %+v %q", a.repos, a.pageTitle())
	}
	if got := New(home).repoLabel(repo); got != "我的项目" {
		t.Fatalf("persisted name: %q", got)
	}

	repoMenuChoice(t, tt, repo, "编辑仓库…")
	click(t, tt, "仓库显示名称")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("取消的名称")
	click(t, tt, "取消")
	if a.repoLabel(repo) != "我的项目" {
		t.Fatal("cancel changed name")
	}

	repoMenuChoice(t, tt, repo, "编辑仓库…")
	a.pickDirFn = func() (string, error) { return next, nil }
	click(t, tt, "选择目录…")
	click(t, tt, "保存")
	if a.page != "repo:"+next || a.repos[0].Path != next || !a.repos[0].Added.Equal(added) {
		t.Fatalf("changed path: %q %+v", a.page, a.repos)
	}
	if !tt.HasText("beta") || len(a.snap.EntriesFor("repo:"+repo)) != 0 {
		t.Fatalf("stale scan scope: %q", tt.Texts())
	}

	repoMenuChoice(t, tt, next, "移除仓库…")
	click(t, tt, "取消")
	if len(a.repos) != 1 {
		t.Fatal("cancel removed repository")
	}
	repoMenuChoice(t, tt, next, "移除仓库…")
	if !tt.HasText("仓库里的文件还在") {
		t.Fatal("missing file safety explanation")
	}
	click(t, tt, "确认不再扫描")
	if len(a.repos) != 0 || a.page == "repo:"+next {
		t.Fatalf("remove: %+v %q", a.repos, a.page)
	}
	if len(New(home).repos) != 0 {
		t.Fatal("removal not persisted")
	}
	for path, text := range map[string]string{filepath.Join(repo, "alpha", "SKILL.md"): "original skill", filepath.Join(next, "beta", "SKILL.md"): "new scan scope"} {
		body, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(body), text) {
			t.Fatalf("repository file changed: %s %v", path, err)
		}
	}
}

func TestRepositoryUpdateValidation(t *testing.T) {
	home := t.TempDir()
	repo, other := filepath.Join(home, "projects", "demo"), filepath.Join(home, "projects", "other")
	pool := filepath.Join(home, ".agents", "skills")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	writeSkill(t, filepath.Join(other, "beta"), "beta")
	writeSkill(t, filepath.Join(pool, "gamma"), "gamma")
	a := New(home)
	a.acceptRepo(repo, nil)
	a.acceptRepo(other, nil)
	before := append([]store.Repo(nil), a.repos...)
	for _, tc := range []struct{ name, path string }{
		{"empty", ""}, {"relative", "relative/path"}, {"duplicate", other}, {"global", pool},
		{"missing", filepath.Join(home, "missing")}, {"file", filepath.Join(repo, "alpha", "SKILL.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := a.updateRepo(repo, "changed", tc.path); err == nil {
				t.Fatal("accepted invalid path")
			}
			if !reflect.DeepEqual(a.repos, before) {
				t.Fatal("invalid update changed records")
			}
		})
	}
	if err := a.updateRepo(repo, "   ", repo); err != nil {
		t.Fatal(err)
	}
	if a.repoLabel(repo) != "demo" {
		t.Fatal("empty name should use directory name")
	}
	if err := a.updateRepo("missing", "changed", repo); err == nil {
		t.Fatal("updated missing record")
	}
	a.mutating = true
	if err := a.updateRepo(repo, "changed", repo); err == nil {
		t.Fatal("updated while mutating")
	}
	a.mutating = false
	// A display-only rename must not scan or require a missing folder.
	missing := filepath.Join(home, "offline")
	a.repos = []store.Repo{{Path: missing}}
	snap := a.snap
	if err := a.updateRepo(missing, "离线仓库", missing); err != nil {
		t.Fatal(err)
	}
	if a.snap != snap {
		t.Fatal("name-only update rescanned")
	}
}

func TestRepositorySaveFailureKeepsState(t *testing.T) {
	home := t.TempDir()
	repo, next := filepath.Join(home, "demo"), filepath.Join(home, "next")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	writeSkill(t, filepath.Join(next, "beta"), "beta")
	a := New(home)
	a.acceptRepo(repo, nil)
	before, page, snap := append([]store.Repo(nil), a.repos...), a.page, a.snap
	// A directory cannot be replaced by Save's temporary file.
	a.configPath = t.TempDir()
	if err := a.updateRepo(repo, "changed", next); err == nil {
		t.Fatal("save should fail")
	}
	if err := a.removeRepo(repo); err == nil {
		t.Fatal("remove save should fail")
	}
	if !reflect.DeepEqual(a.repos, before) || a.page != page || a.snap != snap {
		t.Fatal("failed save changed app state")
	}
}

func TestRepositoryUpdateDuringScan(t *testing.T) {
	home := t.TempDir()
	repo, next := filepath.Join(home, "demo"), filepath.Join(home, "next")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	writeSkill(t, filepath.Join(next, "beta"), "beta")
	a := New(home)
	a.acceptRepo(repo, nil)
	a.scanning = true
	gen := a.scanGen
	if err := a.updateRepo(repo, "新目录", "~/next"); err != nil {
		t.Fatal(err)
	}
	if a.scanGen != gen+1 || !a.rescanPending || a.repos[0].Path != next {
		t.Fatalf("old scan not invalidated: gen=%d pending=%t repos=%+v", a.scanGen, a.rescanPending, a.repos)
	}
}

func TestRepositoryLongNameLayout(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "demo")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	a := New(home)
	a.acceptRepo(repo, nil)
	name := strings.Repeat("很长的仓库显示名称", 12)
	if err := a.updateRepo(repo, name, repo); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1180, 560)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	for _, label := range []string{"仓库操作：" + repo, "重新扫描", "搜索"} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > 880 {
			t.Errorf("long name pushed %q out of view: %+v", label, r)
		}
	}
}

func TestRepositoryEditErrorAndPickerCancel(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "demo")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	a := New(home)
	a.acceptRepo(repo, nil)
	tt := ui.NewTester(a.View, 1180, 560)
	repoMenuChoice(t, tt, repo, "编辑仓库…")
	a.pickDirFn = func() (string, error) { return "", nil }
	click(t, tt, "选择目录…")
	if a.repoDialog.path != repo || a.repoDialog.picking {
		t.Fatal("cancel changed path or left picker busy")
	}
	click(t, tt, "仓库扫描路径")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("relative")
	click(t, tt, "保存")
	if !a.repoDialog.open || !tt.HasText("请输入完整的绝对路径") || a.repos[0].Path != repo {
		t.Fatalf("invalid edit: %+v %q", a.repoDialog, tt.Texts())
	}
	click(t, tt, "取消")
	// A late native picker result must not edit a new dialog.
	a.openRepoDialog(repo, true)
	a.pickDirFn = func() (string, error) {
		a.openRepoDialog(repo, true)
		return filepath.Join(home, "other"), nil
	}
	a.beginPickRepoPath()
	if a.repoDialog.path != repo {
		t.Fatal("stale picker changed a new dialog")
	}
}

func TestRepositoryAddHoverAndKeyboard(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "demo")
	writeSkill(t, filepath.Join(repo, "alpha"), "alpha")
	a := New(home)
	a.acceptRepo(repo, nil)
	tt := ui.NewTester(a.View, 1180, 560)
	label := "仓库操作：" + repo
	more, ok := tt.Find(label)
	if !ok || more.W <= 0 || more.H <= 0 {
		t.Fatalf("missing repository menu: %+v", more)
	}
	// Compare the slot's pixels: at rest it shows the count, and the menu
	// icon replaces it on hover and keyboard focus.
	pixels := func() []byte {
		img := tt.Image()
		var out []byte
		for y := int(more.Y); y < int(more.Y+more.H); y++ {
			start := img.PixOffset(int(more.X), y)
			end := img.PixOffset(int(more.X+more.W), y)
			out = append(out, img.Pix[start:end]...)
		}
		return out
	}
	tt.Move(870, 550)
	resting := pixels()
	row, _ := tt.Find("demo")
	tt.Move(row.X+30, row.Y+row.H/2)
	if bytes.Equal(resting, pixels()) {
		t.Fatal("hovering the row must show its menu")
	}
	tt.Move(870, 550)
	if !bytes.Equal(resting, pixels()) {
		t.Fatal("the menu must hide when the pointer leaves")
	}
	// Tester.Focused also includes focused ancestors such as the row and
	// the sidebar, so the row must not be what has the focus.
	focused := func() bool { return tt.Focused(label) && !tt.Focused("demo") }
	for i := 0; i < 40 && !focused(); i++ {
		tt.Key(0, ui.KeyTab)
	}
	if !focused() || bytes.Equal(resting, pixels()) {
		t.Fatal("the menu must be visible to keyboard users")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("添加仓库…") {
		t.Fatal("add should follow the repositories in the tab order")
	}
	if delta := (more.Y + more.H/2) - (row.Y + row.H/2); delta < -1 || delta > 1 {
		t.Fatalf("repository menu not centered: row=%+v more=%+v", row, more)
	}
}
