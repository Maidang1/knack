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
	"knack/internal/manage"
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
				for _, text := range []string{"已超过保留期，可以清理", "保留最近", "30 天", "Claude Code", "文件年龄", "Agent 占用", "清理项", "调试日志"} {
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
	a.confirmPurgeBackups(a.storage.backups)
	tt.Frame()
	if !tt.HasText("永久清空清理备份？") {
		t.Fatal("missing permanent confirmation")
	}
	click(t, tt, "永久清空")
	if len(a.storage.backups) != 0 {
		t.Fatal("backup not purged")
	}
}

func storageSecondFile(t *testing.T, a *App) string {
	t.Helper()
	path := filepath.Join(a.home, ".claude", "cache", "old.bin")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("cache data\n", 1000)), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}

func storageSelectedGroups(a *App) ([]storage.Group, int) {
	var groups []storage.Group
	count := 0
	for _, g := range a.storage.snapshot.Groups {
		if a.storage.selected[g.ID] && g.Eligible > 0 {
			groups = append(groups, g)
			count += g.Eligible
		}
	}
	return groups, count
}

func TestStorageCleanupReportsPartialRuns(t *testing.T) {
	t.Run("skipped file", func(t *testing.T) {
		a, path := storageFixture(t)
		other := storageSecondFile(t, a)
		a.startStorageScan()
		groups, count := storageSelectedGroups(a)
		if err := os.WriteFile(path, []byte("rewritten"), 0600); err != nil {
			t.Fatal(err)
		}
		recs, err := a.runStorageCleanup(context.Background(), groups, count, 30)
		if err == nil || len(recs) != 1 {
			t.Fatalf("recs=%d err=%v", len(recs), err)
		}
		if !strings.Contains(a.storage.result, "跳过 1 个") {
			t.Fatal(a.storage.result)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("changed file removed", err)
		}
		if _, err := os.Stat(other); !os.IsNotExist(err) {
			t.Fatal("remaining file was not cleaned")
		}
	})
	t.Run("stopped", func(t *testing.T) {
		a, path := storageFixture(t)
		a.startStorageScan()
		groups, count := storageSelectedGroups(a)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		recs, err := a.runStorageCleanup(ctx, groups, count, 30)
		if err == nil || len(recs) != 0 || !strings.Contains(a.storage.result, "已停止，1 个文件未处理") {
			t.Fatalf("recs=%d err=%v result=%s", len(recs), err, a.storage.result)
		}
		if a.storage.progress != "" || a.storage.cancel != nil {
			t.Fatal("progress not cleared")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("stopped run removed file", err)
		}
	})
}

func TestStorageCleanupRechecksActiveSessions(t *testing.T) {
	a, _ := storageFixture(t)
	id := "33333333-resumed"
	project := filepath.Join(a.home, ".claude", "projects", "-work-demo")
	transcript := filepath.Join(project, id+".jsonl")
	attachment := filepath.Join(a.home, ".claude", "file-history", id, "abc@v1")
	at := time.Now().Add(-60 * 24 * time.Hour)
	for _, path := range []string{transcript, attachment} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	a.startStorageScan()
	for _, g := range a.storage.snapshot.Groups {
		if g.ID != "claude-history" {
			continue
		}
		if g.Eligible != 1 {
			t.Fatalf("attachment not offered: %+v", g)
		}
		// The session resumes after the scan, leaving the attachment unchanged.
		if err := os.Chtimes(transcript, time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
		recs, err := a.runStorageCleanup(context.Background(), []storage.Group{g}, 1, 30)
		if err == nil || len(recs) != 0 || !strings.Contains(a.storage.result, "仍活跃的会话") {
			t.Fatalf("recs=%d err=%v result=%s", len(recs), err, a.storage.result)
		}
	}
	if _, err := os.Stat(attachment); err != nil {
		t.Fatal("live session attachment removed", err)
	}
}

func TestStorageScanRequestDuringScan(t *testing.T) {
	a, _ := storageFixture(t)
	a.storage.scanning = true
	a.startStorageScan()
	if !a.storage.rescan {
		t.Fatal("request during scan was dropped")
	}
	a.storage.scanning = false
	a.startStorageScan()
	if a.storage.rescan || a.storage.scanning || a.storage.snapshot == nil {
		t.Fatalf("queued scan did not run: %+v", a.storage)
	}
}

func TestStorageTableHidesEmptyCategories(t *testing.T) {
	a, _ := storageFixture(t)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.Frame()
	if tt.HasText("Hook 调试日志") {
		t.Fatal("empty category shown by default")
	}
	for i := 0; i < 8 && !tt.HasText("隐藏空类别"); i++ {
		if _, ok := tt.Find(storageEmptyLink(a)); ok {
			click(t, tt, storageEmptyLink(a))
		}
		tt.Frame()
	}
	if !tt.HasText("Hook 调试日志") {
		t.Fatal("empty categories not revealed", tt.Texts())
	}
}

func storageEmptyLink(a *App) string {
	empty := 0
	for _, g := range a.storage.snapshot.Groups {
		if g.Files == 0 {
			empty++
		}
	}
	return fmt.Sprintf("显示 %d 个空类别", empty)
}

func TestStorageRowsRankByReleasable(t *testing.T) {
	groups := []storage.Group{
		{Category: storage.Category{ID: "small", Agent: "A"}, Files: 1, EligibleBytes: 1},
		{Category: storage.Category{ID: "big", Agent: "A"}, Files: 1, EligibleBytes: 9},
		{Category: storage.Category{ID: "none", Agent: "A"}},
		{Category: storage.Category{ID: "other", Agent: "B"}, Files: 1},
	}
	rows := storageRows(groups, "A", false)
	if len(rows) != 2 || rows[0].ID != "big" {
		t.Fatalf("rows=%+v", rows)
	}
	if rows := storageRows(groups, "A", true); len(rows) != 3 {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestStorageAgeBandsKeepProtectedFiles(t *testing.T) {
	g := storage.Group{Category: storage.Category{ID: "c"}}
	g.Ages = [storage.AgeBands]int64{10, 20, 30, 0, 0}
	g.ProtectedAges = [storage.AgeBands]int64{0, 0, 12, 0, 0}
	bands := storageAgeBands([]storage.Group{g}, map[string]bool{"c": true}, 1)
	if bands[1] != [3]int64{0, 0, 20} {
		t.Fatalf("within retention: %v", bands[1])
	}
	if bands[2] != [3]int64{18, 0, 12} {
		t.Fatalf("protected counted as releasable: %v", bands[2])
	}
	if bands := storageAgeBands([]storage.Group{g}, nil, 1); bands[2] != [3]int64{0, 18, 12} {
		t.Fatalf("unselected: %v", bands[2])
	}
}

func TestStorageSummaryExplainsNothingToClean(t *testing.T) {
	protected := &storage.Snapshot{Groups: []storage.Group{{ProtectedBytes: 2048}}}
	if title, note := storageSummary(protected); title != "暂时没有可清理的文件" || !strings.Contains(note, "活跃的会话") {
		t.Fatal(title, note)
	}
	partial := &storage.Snapshot{Warnings: []string{"扫描超时"}}
	if title, note := storageSummary(partial); title != "暂时没有可清理的文件" || !strings.Contains(note, "不完整") {
		t.Fatal(title, note)
	}
	if title, _ := storageSummary(&storage.Snapshot{}); title != "没有超过保留期的文件" {
		t.Fatal(title)
	}
}

func TestStorageBackupGroups(t *testing.T) {
	a, _ := storageFixture(t)
	cachePath := storageSecondFile(t, a)
	tt := ui.NewTester(a.View, 1180, 760)
	a.confirmStorageCleanup()
	tt.Frame()
	click(t, tt, "清理")
	groups := a.storage.backupGroups
	if len(groups) != 2 || groups[0].label != "Claude Code · 缓存" || groups[1].label != "Claude Code · 调试日志" || groups[0].bytes == 0 {
		t.Fatalf("groups=%+v", groups)
	}
	// Removing one category's backups must leave the other restorable.
	a.confirmPurgeBackups(groups[1].records)
	tt.Frame()
	click(t, tt, "永久清空")
	if len(a.storage.backups) != 1 || !strings.Contains(a.storage.result, "已永久删除 1 个备份") {
		t.Fatalf("backups=%d result=%s", len(a.storage.backups), a.storage.result)
	}
	a.undo(a.storage.backupGroups[0].records)
	tt.Frame()
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal("kept category not restored", err)
	}
	if !strings.Contains(a.storage.result, "已恢复 1 个文件") {
		t.Fatal(a.storage.result)
	}
}

func TestExpiredBackups(t *testing.T) {
	now := time.Now()
	old := manage.Record{ID: "old", At: now.Add(-(backupExpiryDays + 1) * 24 * time.Hour)}
	fresh := manage.Record{ID: "fresh", At: now.Add(-(backupExpiryDays - 1) * 24 * time.Hour)}
	got := expiredBackups([]manage.Record{old, fresh}, now)
	if len(got) != 1 || got[0].ID != "old" {
		t.Fatalf("%+v", got)
	}
}
