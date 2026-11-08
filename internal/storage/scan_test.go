package storage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func putFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("local data"), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	t.Run("Claude and Codex database ownership and cleanup exclusion", func(t *testing.T) {
		l := DefaultLocations(t.TempDir())
		paths := []string{
			filepath.Join(l.Claude, "root.db"),
			filepath.Join(l.Claude, "state", "state.sqlite3"),
			filepath.Join(l.Claude, "sqlite", "data.sqlite"),
			filepath.Join(l.Claude, "cache", "cache.DB"),
			filepath.Join(l.Claude, "daemon", "daemon.db"),
			filepath.Join(l.Claude, "sessions", "sessions.sqlite"),
			filepath.Join(l.Codex, "state.sqlite"),
		}
		for _, path := range paths {
			db := createDatabase(t, path)
			db.Close()
			at := time.Now().Add(-60 * 24 * time.Hour)
			if err := os.Chtimes(path, at, at); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"orphan.DB-wal", "orphan.DB-shm", "orphan.DB-journal", "unknown-wal"} {
			putFile(t, filepath.Join(l.Claude, "cache", name), 60*24*time.Hour)
		}
		// A plugin's own DB must not be labelled as Claude Code's database.
		plugin := createDatabase(t, filepath.Join(l.Claude, "plugins", "third-party.db"))
		plugin.Close()
		snap := Scan(context.Background(), l, time.Now(), 30)
		if len(snap.Databases) != len(paths) {
			t.Fatalf("found %d databases, want %d", len(snap.Databases), len(paths))
		}
		claude, codex := 0, 0
		for _, d := range snap.Databases {
			if d.Error != "" {
				t.Fatalf("could not inspect database: %+v", d)
			}
			if d.Agent == "Claude Code" {
				claude++
			} else if d.Agent == "Codex" {
				codex++
			} else {
				t.Fatalf("unknown owner: %+v", d)
			}
		}
		if claude != 6 || codex != 1 {
			t.Fatalf("Claude=%d Codex=%d", claude, codex)
		}
		for _, g := range snap.Groups {
			if g.ID == "claude-cache" && (g.Files != 0 || g.Eligible != 0 || len(g.Candidates) != 0) {
				t.Fatalf("database or sidecar entered cleanup: %+v", g)
			}
		}
	})
	t.Run("age filtering and protected data", func(t *testing.T) {
		l := DefaultLocations(t.TempDir())
		now := time.Now()
		putFile(t, filepath.Join(l.Claude, "debug", "old.log"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "debug", "new.log"), time.Hour)
		putFile(t, filepath.Join(l.Claude, "projects", "project", "session.jsonl"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "projects", "project", "memory", "MEMORY.md"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "projects", "project", "memory", "notes.jsonl"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "projects", "project", "session", "tool-results", "image.png"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "projects", "project", "session", "tool-results", "output.jsonl"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Claude, "settings.json"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Codex, "auth.json"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Codex, "tmp", "arg0", "codex"), 40*24*time.Hour)
		putFile(t, filepath.Join(l.Codex, "archived_sessions", "old.jsonl.zst"), 40*24*time.Hour)
		snap := Scan(context.Background(), l, now, 30)
		if len(snap.Warnings) != 0 {
			t.Fatal(snap.Warnings)
		}
		var files, eligible int
		for _, g := range snap.Groups {
			files += g.Files
			eligible += g.Eligible
			for _, f := range g.Candidates {
				if err := f.Unchanged(g.Root); err != nil {
					t.Fatal(err)
				}
			}
		}
		if files != 6 || eligible != 5 {
			t.Fatalf("files=%d eligible=%d", files, eligible)
		}
		snap = Scan(context.Background(), l, now, 90)
		for _, g := range snap.Groups {
			if g.Eligible != 0 {
				t.Fatal("retention ignored")
			}
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		l := DefaultLocations(t.TempDir())
		putFile(t, filepath.Join(l.Claude, "cache", "x"), time.Hour)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		snap := Scan(ctx, l, time.Now(), 1)
		if len(snap.Warnings) == 0 {
			t.Fatal("missing cancellation warning")
		}
	})
	t.Run("symlinked subtree and root", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege required")
		}
		l := DefaultLocations(t.TempDir())
		outside := t.TempDir()
		putFile(t, filepath.Join(outside, "old.log"), 100*24*time.Hour)
		if err := os.MkdirAll(l.Claude, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(l.Claude, "debug")); err != nil {
			t.Fatal(err)
		}
		putFile(t, filepath.Join(l.Claude, "cache", "keep"), time.Hour)
		if err := os.Symlink(outside, filepath.Join(l.Claude, "cache", "external")); err != nil {
			t.Fatal(err)
		}
		snap := Scan(context.Background(), l, time.Now(), 30)
		if len(snap.Warnings) == 0 {
			t.Fatal("missing link warning")
		}
		for _, g := range snap.Groups {
			if g.Eligible != 0 {
				t.Fatal("followed symlink")
			}
		}
	})
}

func TestFileUnchanged(t *testing.T) {
	l := DefaultLocations(t.TempDir())
	p := filepath.Join(l.Claude, "cache", "x")
	putFile(t, p, 100*24*time.Hour)
	s := Scan(context.Background(), l, time.Now(), 30)
	var group Group
	for _, g := range s.Groups {
		if g.ID == "claude-cache" {
			group = g
		}
	}
	f := group.Candidates[0]
	if err := f.Unchanged(group.Root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.Unchanged(group.Root); err == nil {
		t.Fatal("accepted changed file")
	}
	if _, err := ValidatePath(group.Root, filepath.Dir(group.Root)); err == nil {
		t.Fatal("accepted escape")
	}
}

func createDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE preserved(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO preserved VALUES(1,'keep')"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInspectDatabase(t *testing.T) {
	l := DefaultLocations(t.TempDir())
	path := filepath.Join(l.Codex, "sqlite", "state #1.sqlite")
	db := createDatabase(t, path)
	db.Close()
	d := InspectDatabase(context.Background(), filepath.Dir(path), path)
	if d.Error != "" || d.Bytes == 0 || d.Pages == 0 {
		t.Fatalf("%+v", d)
	}
	s := Scan(context.Background(), l, time.Now(), 30)
	if len(s.Databases) != 1 || s.Databases[0].Error != "" {
		t.Fatalf("%+v", s.Databases)
	}
	putFile(t, filepath.Join(l.Codex, "broken.sqlite"), 0)
	d = InspectDatabase(context.Background(), l.Codex, filepath.Join(l.Codex, "broken.sqlite"))
	if !strings.Contains(d.Error, "SQLite") {
		t.Fatalf("%+v", d)
	}
}

func TestOpenDatabase(t *testing.T) {
	l := DefaultLocations(t.TempDir())
	path := filepath.Join(l.Codex, "state.sqlite")
	if _, err := OpenDatabase(l.Codex, path, true); err == nil {
		t.Fatal("accepted missing DB")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created DB during open")
	}
	db := createDatabase(t, path)
	db.Close()
	ro, err := OpenDatabase(l.Codex, path, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Exec("DELETE FROM preserved"); err == nil {
		t.Fatal("read-only connection wrote")
	}
	ro.Close()
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "wal")
		putFile(t, outside, 0)
		if err := os.Symlink(outside, path+"-wal"); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenDatabase(l.Codex, path, true); err == nil {
			t.Fatal("accepted linked WAL")
		}
	}
}
