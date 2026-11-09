package manage

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

	"knack/internal/storage"
)

func cleanupFixture(t *testing.T) (storage.Group, string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := storage.DefaultLocations(home)
	p := filepath.Join(l.Claude, "debug", "old.log")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("private conversation\n", 2000)), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	snap := storage.Scan(context.Background(), l, time.Now(), 30)
	for _, g := range snap.Groups {
		if g.ID == "claude-debug" {
			return g, p, filepath.Join(home, "undo")
		}
	}
	t.Fatal("no group")
	return storage.Group{}, "", ""
}

func TestCleanupFile(t *testing.T) {
	t.Run("compressed persisted undo", func(t *testing.T) {
		g, p, undo := cleanupFixture(t)
		original, _ := os.ReadFile(p)
		rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("original remains")
		}
		records, n, err := CleanupRecords(undo)
		if err != nil || len(records) != 1 || n >= int64(len(original)) {
			t.Fatalf("records=%d bytes=%d err=%v", len(records), n, err)
		}
		for _, path := range []string{rec.Dir, rec.Payload, filepath.Join(rec.Dir, "record.json")} {
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
				t.Fatalf("non-private %s: %v", path, st.Mode())
			}
		}
		if err := Restore(records[0]); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(original) {
			t.Fatal("restore changed content", err)
		}
		st, _ := os.Stat(p)
		if !st.ModTime().Equal(g.Candidates[0].Modified) {
			t.Fatal("mtime not restored")
		}
		if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
			t.Fatal("record not removed")
		}
	})
	t.Run("stale scan", func(t *testing.T) {
		g, p, undo := cleanupFixture(t)
		if err := os.WriteFile(p, []byte("new data"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo); err == nil {
			t.Fatal("accepted stale file")
		}
		got, _ := os.ReadFile(p)
		if string(got) != "new data" {
			t.Fatal("changed file removed")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		g, p, undo := cleanupFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := CleanupFile(ctx, g.Category, g.Candidates[0], undo); err == nil {
			t.Fatal("ignored cancellation")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("replaced root", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege required")
		}
		g, p, undo := cleanupFixture(t)
		if err := os.Rename(g.Root, g.Root+"-saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(g.Root+"-saved", g.Root); err != nil {
			t.Fatal(err)
		}
		if _, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo); err == nil {
			t.Fatal("followed replaced root")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRestoreCleanup(t *testing.T) {
	t.Run("new file is preserved", func(t *testing.T) {
		g, p, undo := cleanupFixture(t)
		rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("new"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Restore(rec); err == nil {
			t.Fatal("overwrote new file")
		}
		got, _ := os.ReadFile(p)
		if string(got) != "new" {
			t.Fatal("new file changed")
		}
		if _, err := os.Stat(rec.Payload); err != nil {
			t.Fatal("backup lost")
		}
	})
	t.Run("corrupt backup", func(t *testing.T) {
		g, p, undo := cleanupFixture(t)
		rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rec.Payload, []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Restore(rec); err == nil {
			t.Fatal("accepted corrupt archive")
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("partial restored file remains")
		}
	})
}

func nestedCleanupFixture(t *testing.T) (storage.Group, string, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := storage.DefaultLocations(home)
	p := filepath.Join(l.Codex, "sessions", "2026", "01", "02", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Repeat("session line\n", 500)), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	for _, g := range storage.Scan(context.Background(), l, time.Now(), 30).Groups {
		if g.ID == "codex-sessions" {
			return g, p, filepath.Join(home, "undo")
		}
	}
	t.Fatal("no group")
	return storage.Group{}, "", ""
}

func TestCleanupEmptyDirectories(t *testing.T) {
	t.Run("pruned then recreated on restore", func(t *testing.T) {
		g, p, undo := nestedCleanupFixture(t)
		original, _ := os.ReadFile(p)
		rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(g.Root, "2026")); !os.IsNotExist(err) {
			t.Fatal("empty parents remain", err)
		}
		if _, err := os.Stat(g.Root); err != nil {
			t.Fatal("category root removed", err)
		}
		if err := Restore(rec); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(p)
		if err != nil || string(got) != string(original) {
			t.Fatal("restore changed content", err)
		}
	})
	t.Run("sibling keeps directory", func(t *testing.T) {
		g, p, undo := nestedCleanupFixture(t)
		sibling := filepath.Join(filepath.Dir(p), "keep.txt")
		if err := os.WriteFile(sibling, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(sibling); err != nil {
			t.Fatal("sibling removed", err)
		}
	})
	t.Run("restore refuses linked parent", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink privilege required")
		}
		g, _, undo := nestedCleanupFixture(t)
		rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(filepath.Dir(g.Root), "outside")
		if err := os.MkdirAll(outside, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(g.Root, "2026")); err != nil {
			t.Fatal(err)
		}
		if err := Restore(rec); err == nil {
			t.Fatal("restored through link")
		}
		entries, _ := os.ReadDir(outside)
		if len(entries) != 0 {
			t.Fatal("wrote outside the category", entries)
		}
		if _, err := os.Stat(rec.Payload); err != nil {
			t.Fatal("backup lost", err)
		}
	})
}

func TestEnsureCleanupDirNeverWritesThroughLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privilege required")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "codex")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(link, "sessions")
	if err := ensureCleanupDir(root, filepath.Join(root, "2026")); err == nil {
		t.Fatal("accepted a linked ancestor of the category root")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("created directories through the link", entries)
	}
}

func TestRestoreFailureLeavesNoNewDirectories(t *testing.T) {
	g, _, undo := nestedCleanupFixture(t)
	rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec.Payload, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err == nil {
		t.Fatal("accepted corrupt archive")
	}
	if _, err := os.Stat(filepath.Join(g.Root, "2026")); !os.IsNotExist(err) {
		t.Fatal("failed restore left directories", err)
	}
}

func TestPurgeCleanupBackups(t *testing.T) {
	g, _, undo := cleanupFixture(t)
	rec, err := CleanupFile(context.Background(), g.Category, g.Candidates[0], undo)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(undo, "skill-undo")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "record.json"), []byte(`{"op":"remove-dir"}`), 0600); err != nil {
		t.Fatal(err)
	}
	records, _, err := CleanupRecords(undo)
	if err != nil || len(records) != 1 {
		t.Fatal(records, err)
	}
	if err := PurgeCleanupBackups(undo, []Record{{ID: "../outside", Op: opCleanup}}); err == nil {
		t.Fatal("accepted escape")
	}
	if err := PurgeCleanupBackups(undo, records); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Fatal("backup remains")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("skill undo removed", err)
	}
}

func TestMaintainDatabase(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		name := "vacuum"
		if checkpoint {
			name = "checkpoint"
		}
		t.Run(name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "state.sqlite")
			uriPath := filepath.ToSlash(path)
			if !strings.HasPrefix(uriPath, "/") {
				uriPath = "/" + uriPath
			}
			u := url.URL{Scheme: "file", Path: uriPath}
			db, err := sql.Open("sqlite", u.String())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, err = db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE data(id INTEGER PRIMARY KEY, value BLOB); WITH RECURSIVE n(x) AS(VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<256) INSERT INTO data SELECT x,zeroblob(8192) FROM n; DELETE FROM data WHERE id>16;")
			if err != nil {
				t.Fatal(err)
			}
			before := storage.InspectDatabase(context.Background(), home, path)
			if before.Error != "" || before.FreeBytes == 0 {
				t.Fatalf("%+v", before)
			}
			_, err = MaintainDatabase(context.Background(), before, checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM data").Scan(&count); err != nil || count != 16 {
				t.Fatalf("rows=%d err=%v", count, err)
			}
			after := storage.InspectDatabase(context.Background(), home, path)
			if !checkpoint && after.FreeBytes != 0 {
				t.Fatal("vacuum did not reclaim pages", after)
			}
			if after.WALBytes != 0 {
				t.Fatal("WAL not truncated", after.WALBytes)
			}
			if checkpoint && after.FreeBytes != before.FreeBytes {
				t.Fatal("checkpoint unexpectedly vacuumed")
			}
		})
	}
	t.Run("busy writer", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "logs.sqlite")
		db, err := sql.Open("sqlite", filepath.ToSlash(path))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE t(id); INSERT INTO t VALUES(1)"); err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.Exec("INSERT INTO t VALUES(2)"); err != nil {
			t.Fatal(err)
		}
		_, err = MaintainDatabase(context.Background(), storage.Database{Root: root, Path: path}, false)
		if err == nil {
			t.Fatal("maintenance succeeded with writer lock")
		}
	})
}
