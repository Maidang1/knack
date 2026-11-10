package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"knack/internal/manage"
	"knack/internal/storage"
)

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDataDirs(t *testing.T) {
	t.Run("fresh install", func(t *testing.T) {
		home := t.TempDir()
		config, cache, err := dataDirs(home)
		if err != nil || config != filepath.Join(home, ".config", "knack") || cache != filepath.Join(home, ".cache", "knack") {
			t.Fatalf("config=%s cache=%s err=%v", config, cache, err)
		}
		// Nothing is created until something is saved.
		if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
			t.Fatal("created a directory on a fresh home")
		}
	})
	t.Run("legacy directories move across", func(t *testing.T) {
		home := t.TempDir()
		writeFile(t, filepath.Join(home, ".config", "skill-manager", "repos.json"), `{"repos":[]}`)
		writeFile(t, filepath.Join(home, ".config", "skill-manager", "plugins", "a.js"), "plugin")
		writeFile(t, filepath.Join(home, ".cache", "skill-manager", "sessions.json"), "cache")
		config, cache, err := dataDirs(home)
		if err != nil {
			t.Fatal(err)
		}
		if readFile(t, filepath.Join(config, "plugins", "a.js")) != "plugin" || readFile(t, filepath.Join(cache, "sessions.json")) != "cache" {
			t.Fatal("legacy files did not move")
		}
		for _, old := range []string{".config", ".cache"} {
			if _, err := os.Lstat(filepath.Join(home, old, "skill-manager")); !os.IsNotExist(err) {
				t.Fatalf("%s/skill-manager remains", old)
			}
		}
		// A second launch has nothing left to do.
		if again, _, err := dataDirs(home); err != nil || again != config {
			t.Fatalf("second launch: %s %v", again, err)
		}
	})
	t.Run("existing directory keeps its own entries", func(t *testing.T) {
		home := t.TempDir()
		// Something else, such as saved window state, may create the new
		// directory before the first launch after the rename.
		writeFile(t, filepath.Join(home, ".config", "knack", "window-state.json"), "window")
		writeFile(t, filepath.Join(home, ".config", "knack", "settings.json"), "new")
		writeFile(t, filepath.Join(home, ".config", "skill-manager", "settings.json"), "old")
		writeFile(t, filepath.Join(home, ".config", "skill-manager", "repos.json"), "repos")
		config, _, err := dataDirs(home)
		if err != nil {
			t.Fatal(err)
		}
		if readFile(t, filepath.Join(config, "settings.json")) != "new" || readFile(t, filepath.Join(config, "repos.json")) != "repos" || readFile(t, filepath.Join(config, "window-state.json")) != "window" {
			t.Fatal("merge changed or missed an entry")
		}
		// The entry that lost to an existing one stays in the old directory.
		if readFile(t, filepath.Join(home, ".config", "skill-manager", "settings.json")) != "old" {
			t.Fatal("old entry was not kept")
		}
	})
	t.Run("cleanup backups stay restorable", func(t *testing.T) {
		home, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		l := storage.DefaultLocations(home)
		log := filepath.Join(l.Claude, "debug", "old.log")
		writeFile(t, log, strings.Repeat("private conversation\n", 2000))
		at := time.Now().Add(-60 * 24 * time.Hour)
		if err := os.Chtimes(log, at, at); err != nil {
			t.Fatal(err)
		}
		var group storage.Group
		for _, g := range storage.Scan(context.Background(), l, time.Now(), 30).Groups {
			if g.ID == "claude-debug" {
				group = g
			}
		}
		if len(group.Candidates) != 1 {
			t.Fatalf("candidates: %+v", group.Candidates)
		}
		if _, err := manage.CleanupFile(context.Background(), group.Category, group.Candidates[0], filepath.Join(home, ".config", "skill-manager", "undo")); err != nil {
			t.Fatal(err)
		}
		config, _, err := dataDirs(home)
		if err != nil {
			t.Fatal(err)
		}
		records, _, err := manage.CleanupRecords(filepath.Join(config, "undo"))
		if err != nil || len(records) != 1 {
			t.Fatalf("backup not listed after the move: %d %v", len(records), err)
		}
		if err := manage.Restore(records[0]); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, log); !strings.HasPrefix(got, "private conversation") {
			t.Fatal("backup did not restore")
		}
	})
}
