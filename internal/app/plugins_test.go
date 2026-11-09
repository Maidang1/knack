package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func TestSettingsPluginsTab(t *testing.T) {
	a := New(t.TempDir())
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	click(t, tt, "设置")
	click(t, tt, "设置分类：插件")
	if a.settingsTab != 3 || !tt.HasText("从 GitHub 安装") || !tt.HasText("选择文件…") || !tt.HasText("选择目录…") {
		t.Fatalf("tab %d texts %q", a.settingsTab, tt.Texts())
	}
}

func TestInstallPluginFromFile(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() { session.LoadPlugins(filepath.Join(home, "missing"), home) })
	a := New(home)
	src := filepath.Join(home, "extra.js")
	body := []byte(`knack.registerAgent({id: "file-agent", name: "File Agent", list() { return [] }, read() { return [] }})`)
	if err := os.WriteFile(src, body, 0o600); err != nil {
		t.Fatal(err)
	}
	pending, err := session.OpenPlugin(src, a.pluginDir(), home)
	if err != nil {
		t.Fatal(err)
	}
	a.finishPluginInstall(pending, nil)
	if a.pluginErr != "" || a.notice != "已安装插件 File Agent" {
		t.Fatalf("err %q notice %q", a.pluginErr, a.notice)
	}
	if info := session.LookupAgent("file-agent"); info.Name != "File Agent" || info.Unsupported != "" {
		t.Fatalf("info %+v", info)
	}
	a.removePlugin("extra.js")
	if info := session.LookupAgent("file-agent"); info.Unsupported == "" {
		t.Fatalf("still loaded %+v", info)
	}
}
