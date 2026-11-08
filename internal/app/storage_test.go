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

// Use real files and a real SQLite database so previews exercise every panel.
func storageDashboardFixture(t *testing.T, a *App) {
	t.Helper()
	for _, f := range []struct {
		path       string
		size, days int
	}{
		{".claude/cache/recent.bin", 8000, 3},
		{".claude/shell-snapshots/recent.sh", 4000, 3},
		{".claude/file-history/recent/backup", 6000, 3},
		{".claude/transcripts/old.jsonl", 77000, 120},
		{".codex/log/old.log", 46000, 120},
		{".codex/log/recent.log", 4000, 3},
		{".codex/cache/old.bin", 5000, 60},
		{".codex/cache/recent.bin", 3000, 3},
		{".codex/shell_snapshots/old.sh", 1000, 60},
		{".codex/sessions/old.jsonl", 324000, 200},
		{".codex/archived_sessions/old.jsonl", 3000, 120},
	} {
		path := filepath.Join(a.home, f.path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Repeat("x", f.size)), 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-time.Duration(f.days) * 24 * time.Hour)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(a.home, ".codex", "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE data(value BLOB); INSERT INTO data VALUES(zeroblob(100000)); INSERT INTO data VALUES(zeroblob(100000)); DELETE FROM data WHERE rowid=1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStoragePane(t *testing.T) {
	for _, size := range [][2]int{{1730, 1000}, {1180, 760}, {880, 760}, {700, 760}} {
		width, height := size[0], size[1]
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%t", width, dark), func(t *testing.T) {
				a, _ := storageFixture(t)
				storageDashboardFixture(t, a)
				tt := ui.NewTester(a.View, width, height)
				tt.SetDark(dark)
				tt.Frame()
				for _, text := range []string{"可回收空间", "建议操作", "保留线", "数据库维护", "清理备份", "保留最近", "30 天", "Claude Code", "文件年龄", "清理项", "调试日志"} {
					if !tt.HasText(text) {
						t.Fatalf("missing %s: %v", text, tt.Texts())
					}
				}
				if !a.storage.selected["claude-debug"] || a.storage.selected["claude-projects"] || a.storage.selected["claude-history"] {
					t.Fatal("unsafe defaults")
				}
				title, _ := tt.Find("可回收空间")
				ring, _ := tt.Find("可回收空间圆环")
				after, _ := tt.Find("全部清理后的文件占用")
				if ring.Y < title.Y+title.H || after.Y < ring.Y+ring.H {
					t.Fatalf("overlapping summary: title=%+v ring=%+v after=%+v", title, ring, after)
				}
				savePreview := func(suffix string) {
					t.Helper()
					output := os.Getenv("KNACK_STORAGE_PREVIEW")
					if output == "" {
						return
					}
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, fmt.Sprintf("storage-%d-dark-%t%s.png", width, dark, suffix)))
					if err != nil {
						t.Fatal(err)
					}
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
					if err := f.Close(); err != nil {
						t.Fatal(err)
					}
				}
				savePreview("")
				if width == 1730 {
					backup, _ := tt.Find("恢复全部…")
					if backup.Y+backup.H > float32(height)-60 {
						t.Fatalf("wide dashboard does not fit: %+v", backup)
					}
				} else {
					for i, label := range []string{"文件年龄", "清理项", "数据库维护", "清理备份"} {
						limit := float32(500)
						if label == "清理备份" {
							limit = 650
						}
						for attempts := 0; attempts < 20; attempts++ {
							r, ok := tt.Find(label)
							if ok && r.Y >= 44 && r.Y < limit {
								break
							}
							tt.Scroll(float32(width-100), 600, 0, 200)
						}
						r, ok := tt.Find(label)
						if !ok || r.X < 0 || r.X+r.W > float32(width) || r.Y < 44 || r.Y >= limit {
							t.Fatalf("unreachable %s: %+v", label, r)
						}
						savePreview(fmt.Sprintf("-section-%d", i))
					}
				}
				// The action remains reachable by scrolling, including the narrow rail.
				for i := 0; i < 12; i++ {
					if r, ok := tt.Find("清理所选…"); ok && r.Y > 45 && r.Y+r.H < float32(height) {
						break
					}
					tt.Scroll(float32(width-100), 600, 0, 400)
				}
				r, ok := tt.Find("清理所选…")
				if !ok || r.Y < 45 || r.Y+r.H > float32(height) {
					t.Fatalf("unreachable cleanup: %+v", r)
				}
				click(t, tt, "清理所选…")
				if !a.ask.open || !tt.HasText("清理所选文件？") {
					t.Fatal("missing confirmation")
				}
				// All confirmation text and both actions must fit on the screen.
				for _, label := range []string{"清理", "取消"} {
					r, ok := tt.Find(label)
					if !ok || r.Y < 0 || r.Y+r.H > float32(height) {
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
		if r, ok := tt.Find(storageEmptyLink(a)); ok && r.Y > 44 && r.Y+r.H < 700 {
			click(t, tt, storageEmptyLink(a))
		} else {
			tt.Scroll(1050, 600, 0, 300)
		}
		tt.Frame()
	}
	if !tt.HasText("Hook 调试日志") {
		t.Fatal("empty categories not revealed", tt.Texts())
	}
}

func TestStorageTableEmptyCategoriesByAgent(t *testing.T) {
	for _, codexEmpty := range []bool{false, true} {
		t.Run(fmt.Sprintf("codexEmpty=%t", codexEmpty), func(t *testing.T) {
			groups := []storage.Group{
				{Category: storage.Category{ID: "claude-used", Agent: "Claude Code"}, Files: 1, Bytes: 10},
				{Category: storage.Category{ID: "claude-empty-1", Agent: "Claude Code"}},
				{Category: storage.Category{ID: "claude-empty-2", Agent: "Claude Code"}},
				{Category: storage.Category{ID: "codex-used", Agent: "Codex"}, Files: 1, Bytes: 20},
			}
			if codexEmpty {
				groups = append(groups, storage.Group{Category: storage.Category{ID: "codex-empty", Agent: "Codex"}})
			}
			a := &App{storage: storageState{snapshot: &storage.Snapshot{Groups: groups}}}
			tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Gap(12).Children(func() { a.storageTable(c, false, false) }) }, 1180, 760)
			for _, agent := range []string{"Claude Code", "Codex", ""} {
				count := 0
				var bytes int64
				for _, g := range groups {
					if agent == "" || g.Agent == agent {
						bytes += g.Bytes
						if g.Files == 0 {
							count++
						}
					}
				}
				name := agent
				if name == "" {
					name = "全部"
				}
				click(t, tt, name+" 文件占用 "+storageSize(bytes))
				if count > 0 {
					label := fmt.Sprintf("显示 %d 个空类别", count)
					if _, ok := tt.Find(label); !ok {
						t.Fatalf("agent=%q: missing %q: %v", agent, label, tt.Texts())
					}
					click(t, tt, label)
					if !tt.HasText("隐藏空类别") {
						t.Fatalf("agent=%q: missing hide link", agent)
					}
					click(t, tt, "隐藏空类别")
				} else {
					for _, text := range tt.Texts() {
						if strings.HasPrefix(text, "显示 ") && strings.HasSuffix(text, " 个空类别") || text == "隐藏空类别" {
							t.Fatalf("agent=%q: unexpected link %q", agent, text)
						}
					}
				}
			}
		})
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

func TestStorageReleasable(t *testing.T) {
	groups := []storage.Group{
		{Ages: [storage.AgeBands]int64{10, 20, 30, 40, 50}, ProtectedAges: [storage.AgeBands]int64{0, 0, 12, 5, 3}, EligibleBytes: 100},
		{Ages: [storage.AgeBands]int64{0, 0, 10, 0, 20}, ProtectedAges: [storage.AgeBands]int64{0, 0, 4, 0, 2}, EligibleBytes: 24},
	}
	for _, tc := range []struct {
		retention int
		want      int64
	}{{0, 144}, {1, 124}, {2, 100}, {3, 65}} {
		if got := storageReleasable(groups, tc.retention, 1); got != tc.want {
			t.Errorf("retention=%d: got %d, want %d", tc.retention, got, tc.want)
		}
	}
	if got := storageReleasable(nil, 0, 0); got != 0 {
		t.Fatal(got)
	}
}

func TestStorageSuggestions(t *testing.T) {
	groups := []storage.Group{
		{Category: storage.Category{ID: "cache", Name: "缓存", Agent: "Codex"}, Eligible: 2, EligibleBytes: 40},
		{Category: storage.Category{ID: "log", Name: "日志", Agent: "Claude Code"}, Eligible: 3, EligibleBytes: 60},
		{Category: storage.Category{ID: "protected", History: true}, Bytes: 9999, Files: 9},
		{Category: storage.Category{ID: "small", History: true}, Eligible: 1, EligibleBytes: 10},
		{Category: storage.Category{ID: "large", History: true}, Eligible: 5, EligibleBytes: 300},
		{Category: storage.Category{ID: "middle", History: true}, Eligible: 2, EligibleBytes: 100},
	}
	snap := &storage.Snapshot{Groups: groups}
	got := storageSuggestions(snap)
	if len(got) != 3 || got[0].bytes != 100 || got[0].files != 5 || strings.Join(got[0].groups, ",") != "cache,log" || got[1].groups[0] != "large" || got[2].groups[0] != "middle" {
		t.Fatalf("suggestions=%+v", got)
	}
	for _, item := range got {
		if item.database != nil {
			t.Fatal("unexpected database")
		}
	}
	snap.Databases = []storage.Database{
		{Path: "empty", Bytes: 1000},
		{Path: "broken", WALBytes: 9999, Error: "busy"},
		{Path: "wal", WALBytes: 200},
		{Path: "free", WALBytes: 50, FreeBytes: 300},
	}
	got = storageSuggestions(snap)
	if len(got) != 4 || got[3].database == nil || got[3].database.Path != "free" {
		t.Fatalf("database suggestion=%+v", got)
	}
	if got := storageSuggestions(&storage.Snapshot{}); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestStorageRowsAllAgents(t *testing.T) {
	groups := []storage.Group{
		{Category: storage.Category{ID: "a", Agent: "Claude Code"}, Files: 1, EligibleBytes: 10, Bytes: 20},
		{Category: storage.Category{ID: "b", Agent: "Codex"}, Files: 1, EligibleBytes: 30, Bytes: 30},
		{Category: storage.Category{ID: "c", Agent: "Codex"}, Files: 1, EligibleBytes: 10, Bytes: 40},
		{Category: storage.Category{ID: "empty", Agent: "Claude Code"}},
	}
	rows := storageRows(groups, "", false)
	if len(rows) != 3 || rows[0].ID != "b" || rows[1].ID != "c" || rows[2].ID != "a" {
		t.Fatal(rows)
	}
	if len(storageRows(groups, "", true)) != 4 || groups[0].ID != "a" {
		t.Fatal("empty filtering or input order changed")
	}
}

func TestStorageDashboardControls(t *testing.T) {
	a, _ := storageFixture(t)
	storageDashboardFixture(t, a)
	tt := ui.NewTester(a.View, 1730, 1000)
	click(t, tt, "缓存与日志：✓ 已选")
	if a.storage.selected["claude-debug"] || a.storage.selected["codex-log"] {
		t.Fatal("safe suggestion did not deselect all")
	}
	click(t, tt, "缓存与日志：加入清理")
	if !a.storage.selected["claude-debug"] || !a.storage.selected["codex-log"] || a.storage.selected["codex-sessions"] {
		t.Fatal("safe suggestion selected history or missed safe files")
	}
	click(t, tt, "Codex 历史会话：加入清理")
	if !a.storage.selected["codex-sessions"] {
		t.Fatal("history suggestion did not select")
	}
	click(t, tt, "Codex 数据库：维护…")
	if !a.ask.open || !strings.Contains(a.ask.message, "Codex") {
		t.Fatal("missing database maintenance confirmation")
	}
	click(t, tt, "取消")
	a.mutating = true
	tt.Frame()
	click(t, tt, "Codex 历史会话：✓ 已选")
	if !a.storage.selected["codex-sessions"] {
		t.Fatal("busy suggestion changed selection")
	}
	a.mutating = false
	tt.Frame()
	// The retention tab must rescan with the selected choice.
	var current string
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "保留 180 天，可释放") {
			current = text
			break
		}
	}
	click(t, tt, current)
	if a.storage.retention != 3 || a.storage.scanned != 3 {
		t.Fatalf("retention did not rescan: %+v", a.storage)
	}
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Codex 文件占用 ") {
			click(t, tt, text)
			break
		}
	}
	if a.storage.agentFilter != 2 || tt.HasText("调试日志") {
		t.Fatal("Agent filter did not filter rows")
	}
}
