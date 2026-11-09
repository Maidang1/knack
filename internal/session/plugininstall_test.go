package session

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePluginRepo(t *testing.T) {
	for _, in := range []string{
		"acme/demo",
		"https://github.com/acme/demo",
		"https://github.com/acme/demo/tree/main",
		"git@github.com:acme/demo.git",
	} {
		got, err := ParsePluginRepo(in)
		if err != nil || got != "acme/demo" {
			t.Fatalf("%s -> %s %v", in, got, err)
		}
	}
	if _, err := ParsePluginRepo("not a repo"); err == nil {
		t.Fatal("accepted")
	}
}

func TestPickPluginAsset(t *testing.T) {
	names := []string{"notes.txt", "plugin-linux-amd64.zip", "plugin.zip", "plugin-darwin-arm64.zip"}
	if pickPluginAsset(names, "darwin", "arm64") != 3 {
		t.Fatal("platform")
	}
	if pickPluginAsset([]string{"plugin.zip", "readme.zip"}, "windows", "amd64") != 0 {
		t.Fatal("generic")
	}
	if pickPluginAsset([]string{"plugin-linux-amd64.zip"}, "darwin", "arm64") != -1 {
		t.Fatal("other platforms only")
	}
}

func TestInstallPluginFromZipAndGitHub(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "plugins")
	script := "export function list() { return [] }\nexport function read() { return [] }\n"
	zipPath := filepath.Join(home, "plugin.zip")
	writePluginZip(t, zipPath, map[string]string{
		"plugin.json": `{"id":"zip-agent","name":"Zip Agent","main":"index.js"}`,
		"index.js":    script,
	})
	pending, err := OpenPlugin(zipPath, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ID != "zip-agent" || pending.Entry != "zip-agent" {
		t.Fatalf("pending %+v", pending)
	}
	pending.Repo, pending.Version = "acme/zip", "v1"
	if err := pending.Install(dir); err != nil {
		t.Fatal(err)
	}
	if problems := LoadPlugins(dir, home); len(problems) != 0 {
		t.Fatal(problems)
	}
	if info := LookupAgent("zip-agent"); info.Name != "Zip Agent" {
		t.Fatalf("info %+v", info)
	}
	status := PluginStatuses()
	if len(status) != 1 || status[0].ID != "zip-agent" {
		t.Fatalf("status %+v", status)
	}

	// A second copy from another origin cannot take the name.
	again, err := OpenPlugin(zipPath, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	again.Repo = "other/plugin"
	if err := again.Install(dir); err == nil || !strings.Contains(err.Error(), "已经安装") {
		t.Fatalf("err %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/demo/releases/latest":
			http.NotFound(w, r)
		case "/repos/acme/demo/commits/HEAD":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"sha":"abcdef1234567890"}`))
		default:
			if strings.Contains(r.URL.Path, "/zipball/") {
				writePluginZipTo(t, w, map[string]string{
					"plugin.json": `{"id":"hub-agent","name":"Hub Agent","main":"index.js"}`,
					"index.js":    script,
				})
				return
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	old := pluginGitHubAPI
	pluginGitHubAPI = srv.URL
	t.Cleanup(func() { pluginGitHubAPI = old })

	t.Cleanup(func() { LoadPlugins(filepath.Join(home, "missing"), home) })
	fetched, err := FetchPlugin("https://github.com/acme/demo", dir, home)
	if err != nil {
		t.Fatal(err)
	}
	if fetched.Version != "abcdef123456" || fetched.Repo != "acme/demo" {
		t.Fatalf("release %+v", fetched)
	}
	if err := fetched.Install(dir); err != nil {
		t.Fatal(err)
	}
	if problems := LoadPlugins(dir, home); len(problems) != 0 {
		t.Fatal(problems)
	}
	found := false
	for _, st := range PluginStatuses() {
		if st.ID == "hub-agent" && st.Repo == "acme/demo" && st.Version == "abcdef123456" {
			found = true
		}
	}
	if !found {
		t.Fatalf("status %+v", PluginStatuses())
	}
	if err := RemovePlugin(dir, "hub-agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hub-agent")); !os.IsNotExist(err) {
		t.Fatalf("remove: %v", err)
	}
}

func TestPluginZipRejectsALink(t *testing.T) {
	home := t.TempDir()
	archive := filepath.Join(home, "bad.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	h := &zip.FileHeader{Name: "escape"}
	h.SetMode(os.ModeSymlink)
	w, err := z.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("../x")); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := unzipPlugin(archive, filepath.Join(home, "out")); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("err %v", err)
	}
}

func writePluginZip(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	writePluginZipTo(t, f, files)
}

func writePluginZipTo(t *testing.T, w interface{ Write([]byte) (int, error) }, files map[string]string) {
	t.Helper()
	z := zip.NewWriter(&nopWriteCloser{w})
	for name, body := range files {
		f, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
}

type nopWriteCloser struct {
	w interface{ Write([]byte) (int, error) }
}

func (n nopWriteCloser) Write(p []byte) (int, error) { return n.w.Write(p) }
