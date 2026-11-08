package app

import (
	"context"
	"database/sql"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/storage"
)

func storageFixture(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	var err error
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".claude", "debug", "old.log")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("local log data\n", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	a := New(home)
	a.page = storagePage
	return a, path
}

func TestStorageDatabases(t *testing.T) {
	for _, withDatabase := range []bool{false, true} {
		t.Run(fmt.Sprintf("found=%t", withDatabase), func(t *testing.T) {
			a, _ := storageFixture(t)
			path := filepath.Join(a.home, ".claude", "state", "index.sqlite3")
			if withDatabase {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				db, err := sql.Open("sqlite", filepath.ToSlash(path))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE kept(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO kept VALUES(1,'preserved')"); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			a.startStorageScan()
			tt := ui.NewTester(func(c *ui.Context) { a.storageDatabases(c, false); a.dialogs(c) }, 880, 760)
			for _, label := range []string{"数据库维护", "Claude Code 数据库", "Codex 数据库"} {
				if !tt.HasText(label) {
					t.Fatal("missing " + label)
				}
			}
			if !withDatabase {
				if !tt.HasText("未发现 SQLite 数据库。Claude Code 的会话通常以 JSONL 保存，可在上方清理历史文件。") || tt.HasText("整理碎片…") {
					t.Fatal("incorrect empty database state", tt.Texts())
				}
				return
			}
			if !tt.HasText("index.sqlite3") {
				t.Fatal("missing discovered Claude database")
			}
			for _, action := range []string{"整理碎片…", "合并 WAL…"} {
				click(t, tt, action)
				if !a.ask.open || !strings.Contains(a.ask.message, "Claude Code") || strings.Contains(a.ask.message, "退出 Codex") {
					t.Fatal("wrong database confirmation", a.ask.message)
				}
				click(t, tt, a.ask.ok)
				if !strings.Contains(a.storage.result, "完成") {
					t.Fatal(a.storage.result)
				}
			}
			db, err := storage.OpenDatabase(filepath.Dir(path), path, false)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var value string
			if err := db.QueryRowContext(context.Background(), "SELECT value FROM kept WHERE id=1").Scan(&value); err != nil || value != "preserved" {
				t.Fatalf("Claude database record changed: %s, %v", value, err)
			}
		})
	}
}

func TestStoragePane(t *testing.T) {
	for _, width := range []int{1180, 880} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%t", width, dark), func(t *testing.T) {
				a, _ := storageFixture(t)
				tt := ui.NewTester(a.View, width, 760)
				tt.SetDark(dark)
				tt.Frame()
				for _, text := range []string{"给 Agent 腾出空间", "保留最近", "30 天", "Claude Code", "调试日志"} {
					if !tt.HasText(text) {
						t.Fatalf("missing %s: %v", text, tt.Texts())
					}
				}
				if !a.storage.selected["claude-debug"] || a.storage.selected["claude-projects"] || a.storage.selected["claude-history"] {
					t.Fatal("unsafe defaults")
				}
				if output := os.Getenv("KNACK_STORAGE_PREVIEW"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, fmt.Sprintf("storage-%d-dark-%t.png", width, dark)))
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
					f.Close()
				}
				// The action remains reachable by scrolling, including the narrow rail.
				for i := 0; i < 12; i++ {
					if r, ok := tt.Find("清理所选…"); ok && r.Y > 45 && r.Y+r.H < 760 {
						break
					}
					tt.Scroll(float32(width-100), 600, 0, 400)
				}
				r, ok := tt.Find("清理所选…")
				if !ok || r.Y < 45 || r.Y+r.H > 760 {
					t.Fatalf("unreachable cleanup: %+v", r)
				}
				click(t, tt, "清理所选…")
				if !a.ask.open || !tt.HasText("清理所选文件？") {
					t.Fatal("missing confirmation")
				}
				// All confirmation text and both actions must fit on the screen.
				for _, label := range []string{"清理", "取消"} {
					r, ok := tt.Find(label)
					if !ok || r.Y < 0 || r.Y+r.H > 760 {
						t.Fatalf("unreachable %s: %+v", label, r)
					}
				}
				click(t, tt, "取消")
				if a.ask.open {
					t.Fatal("cancel did not close")
				}
			})
		}
	}
}

func TestStorageCleanupAndRestore(t *testing.T) {
	a, path := storageFixture(t)
	tt := ui.NewTester(a.View, 1180, 760)
	a.confirmStorageCleanup()
	tt.Frame()
	click(t, tt, "清理")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file was not cleaned")
	}
	if len(a.storage.backups) != 1 {
		t.Fatal("missing backup", len(a.storage.backups))
	}
	if !strings.Contains(a.storage.result, "已清理 1 个文件") {
		t.Fatal(a.storage.result)
	}
	a.undo(a.storage.backups)
	tt.Frame()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("undo failed", err)
	}
	if len(a.storage.backups) != 0 {
		t.Fatal("backup list stale")
	}
	a.confirmStorageCleanup()
	tt.Frame()
	click(t, tt, "清理")
	a.confirmPurgeStorage()
	tt.Frame()
	if !tt.HasText("永久清空清理备份？") {
		t.Fatal("missing permanent confirmation")
	}
	click(t, tt, "永久清空")
	if len(a.storage.backups) != 0 {
		t.Fatal("backup not purged")
	}
}
