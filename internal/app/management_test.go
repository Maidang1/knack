package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestRemoveSkillAndUndo(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "link"}[linked], func(t *testing.T) {
			home := t.TempDir()
			pool := filepath.Join(home, ".agents", "skills", "demo")
			entry := filepath.Join(home, ".claude", "skills", "demo")
			writeSkill(t, pool, "shared demo")
			label, confirm := "删除 skill", "确认删除"
			if linked {
				if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(pool, entry); err != nil {
					t.Fatal(err)
				}
				label, confirm = "从 Claude 移除", "确认移除"
			} else {
				writeSkill(t, entry, "local demo")
			}
			a := New(home)
			tt := ui.NewTester(a.View, 1200, 860)
			click(t, tt, "重新扫描")
			click(t, tt, "Claude")
			click(t, tt, label)
			click(t, tt, "取消")
			if _, err := os.Lstat(entry); err != nil {
				t.Fatal(err)
			}
			click(t, tt, label)
			click(t, tt, confirm)
			if _, err := os.Lstat(entry); !os.IsNotExist(err) {
				t.Fatalf("entry remained: %v", err)
			}
			if _, err := os.Stat(filepath.Join(pool, "SKILL.md")); err != nil {
				t.Fatal("shared source changed:", err)
			}
			click(t, tt, "撤销")
			if _, err := os.Stat(filepath.Join(entry, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClaudeInvocationSettingsAndUndo(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills", "demo")
	entry := filepath.Join(home, ".claude", "skills", "demo")
	writeSkill(t, pool, "shared demo")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pool, entry); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(pool, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "Claude")
	click(t, tt, "自动 + 手动调用")
	click(t, tt, "仅手动调用")
	click(t, tt, "取消")
	settings := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("cancel changed config: %v", err)
	}
	for _, mode := range []struct{ label, value string }{{"仅手动调用", "user-invocable-only"}, {"禁用", "off"}} {
		click(t, tt, "自动 + 手动调用")
		click(t, tt, mode.label)
		click(t, tt, "确认更改")
		body, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Overrides map[string]string `json:"skillOverrides"`
		}
		if err := json.Unmarshal(body, &config); err != nil {
			t.Fatal(err)
		}
		if config.Overrides["demo"] != mode.value {
			t.Fatalf("config: %s", body)
		}
		if !tt.HasText(mode.label) {
			t.Fatalf("preference did not refresh: %q", tt.Texts())
		}
		click(t, tt, "撤销")
		if !tt.HasText("自动 + 手动调用") {
			t.Fatalf("undo: %q", tt.Texts())
		}
	}
	after, err := os.ReadFile(filepath.Join(pool, "SKILL.md"))
	if err != nil || string(after) != string(before) {
		t.Fatal("changed shared SKILL.md")
	}
	click(t, tt, "共享池")
	if tt.HasText("Claude 调用方式") {
		t.Fatal("shared pool offered Claude-only preferences")
	}
}

func TestProtectedManagementEntries(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills", "demo")
	writeSkill(t, pool, "demo")
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pool, filepath.Join(claude, "alias")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(home, ".codex", "skills", "demo"), "codex")
	writeSkill(t, filepath.Join(home, ".claude", "skills", ".system", "shipped"), "builtin")
	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "共享池")
	if !tt.HasText("Claude") {
		t.Fatal("missing linked agent")
	}
	chooseMore(t, tt, "删除 skill")
	if !a.ask.open || a.ask.alt == "" {
		t.Fatal("referenced shared source did not offer a choice about its links")
	}
	click(t, tt, "取消")
	if _, err := os.Stat(pool); err != nil {
		t.Fatal(err)
	}
	click(t, tt, "Codex")
	if tt.HasText("Claude 调用方式") {
		t.Fatal("unsupported agent offered preferences")
	}
	click(t, tt, "Claude")
	click(t, tt, "shipped")
	if tt.HasText("删除 skill") || tt.HasText("Claude 调用方式") {
		t.Fatal("builtin offered management")
	}
}

func TestHideAgentFromSidebar(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "demo"), "local demo")
	writeSkill(t, filepath.Join(home, ".cline", "skills", "other"), "local other")
	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	if _, ok := tt.Find("Cline"); !ok {
		t.Fatalf("Cline missing: %q", tt.Texts())
	}
	click(t, tt, "设置")
	if _, ok := tt.Find("全部隐藏"); !ok {
		t.Fatalf("settings not open: %q", tt.Texts())
	}
	a.page = "root:cline-skills"
	a.setAgentShown("cline-skills", false)
	if !a.hidden["cline-skills"] || a.page != "overview" {
		t.Fatalf("hidden=%v page=%s", a.hidden, a.page)
	}
	click(t, tt, "共享池")
	for _, s := range tt.Texts() {
		if s == "Cline" {
			t.Fatalf("Cline still listed: %q", tt.Texts())
		}
	}
	if got := New(home).hidden; !got["cline-skills"] {
		t.Fatalf("not persisted: %v", got)
	}
}

func TestHideBrowseItems(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "demo"), "local demo")
	a := New(home)
	tt := ui.NewTester(a.View, 1200, 860)
	click(t, tt, "重新扫描")
	a.setAgentsShown([]string{"overview", "sessions", "issues", libraryPage, graphPage, usagePage, insightsPage}, false)
	if a.page != "pool" {
		t.Fatalf("page=%s", a.page)
	}
	a.setAgentShown("pool", false)
	if a.page != effectivePage {
		t.Fatalf("page=%s", a.page)
	}
	if b := New(home); !b.hidden["issues"] || b.page != effectivePage {
		t.Fatalf("hidden=%v page=%s", b.hidden, b.page)
	}
}

func linkedPool(t *testing.T) (home, pool, claude, codex string) {
	t.Helper()
	home = t.TempDir()
	pool = filepath.Join(home, ".agents", "skills", "demo")
	writeSkill(t, pool, "shared demo")
	claude = filepath.Join(home, ".claude", "skills", "demo")
	codex = filepath.Join(home, ".codex", "skills", "demo")
	for _, l := range []string{claude, codex} {
		if err := os.MkdirAll(filepath.Dir(l), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(pool, l); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func TestRemovePoolSkillWithLinksAndUndo(t *testing.T) {
	home, pool, claude, codex := linkedPool(t)
	a := New(home)
	tt := ui.NewTester(a.View, 1400, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "共享池")
	chooseMore(t, tt, "删除 skill")
	click(t, tt, "同时移除软链并删除")
	for _, p := range []string{pool, claude, codex} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s remained: %v", p, err)
		}
	}
	click(t, tt, "撤销")
	if _, err := os.Stat(filepath.Join(pool, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{claude, codex} {
		if got, err := os.Readlink(p); err != nil || got != pool {
			t.Fatalf("%s not restored: %q %v", p, got, err)
		}
	}
}

func TestRemovePoolSkillKeepingLinks(t *testing.T) {
	home, pool, claude, _ := linkedPool(t)
	a := New(home)
	tt := ui.NewTester(a.View, 1400, 860)
	click(t, tt, "重新扫描")
	click(t, tt, "共享池")
	chooseMore(t, tt, "删除 skill")
	click(t, tt, "只删除，保留软链")
	if _, err := os.Lstat(pool); !os.IsNotExist(err) {
		t.Fatalf("pool remained: %v", err)
	}
	if _, err := os.Lstat(claude); err != nil {
		t.Fatal("link was removed:", err)
	}
}

// chooseMore picks an item from the detail bar's 更多 menu.
func chooseMore(t *testing.T, tt *ui.Tester, item string) {
	t.Helper()
	click(t, tt, "更多")
	click(t, tt, item)
}
