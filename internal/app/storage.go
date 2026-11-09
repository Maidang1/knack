package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"knack/internal/fsutil"
	"knack/internal/manage"
	"knack/internal/storage"
)

const storagePage = "storage"

// storageBatch bounds one run, since every file gets its own undo record.
const storageBatch = 2000

// backupExpiryDays is the age offered for bulk removal of old backups.
const backupExpiryDays = 30

var retentionDays = []int{7, 30, 90, 180}

type backupGroup struct {
	label   string
	records []manage.Record
	bytes   int64
}

type storageState struct {
	snapshot     *storage.Snapshot
	scanning     bool
	rescan       bool
	retention    int
	scanned      int // the retention choice the snapshot was taken with
	selected     map[string]bool
	backups      []manage.Record
	backupBytes  int64
	backupGroups []backupGroup
	expired      []manage.Record
	result       string
	progress     string
	showEmpty    bool
	agentFilter  int
	cancel       context.CancelFunc
}

// storageSize keeps large Agent stores readable without changing other pages.
func storageSize(n int64) string {
	if n >= 1<<40 {
		return fmt.Sprintf("%.1f TB", float64(n)/(1<<40))
	}
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return byteSize(n)
}

// groupBackups labels backups by category; older records only know their root.
func (a *App) groupBackups(groups []storage.Group, records []manage.Record) []backupGroup {
	names := map[string]string{}
	for _, g := range groups {
		names[g.ID] = g.Agent + " · " + g.Name
	}
	byLabel := map[string]*backupGroup{}
	var out []backupGroup
	for _, r := range records {
		label := names[r.Cleanup.Category]
		if label == "" {
			label = shortPath(a.home, r.Cleanup.Root)
		}
		g := byLabel[label]
		if g == nil {
			g = &backupGroup{label: label}
			byLabel[label] = g
		}
		g.records = append(g.records, r)
		g.bytes += r.Cleanup.Stored
	}
	for _, g := range byLabel {
		out = append(out, *g)
	}
	slices.SortFunc(out, func(x, y backupGroup) int { return strings.Compare(x.label, y.label) })
	return out
}

func expiredBackups(records []manage.Record, now time.Time) []manage.Record {
	cutoff := now.Add(-backupExpiryDays * 24 * time.Hour)
	var out []manage.Record
	for _, r := range records {
		if r.At.Before(cutoff) {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) storageLocations() storage.Locations {
	l := storage.DefaultLocations(a.home)
	// Environment overrides only apply to the real user's home, never fixtures.
	if home, err := os.UserHomeDir(); err == nil && fsutil.EqualPath(home, a.home) {
		if p := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(p) {
			l.Claude = filepath.Clean(p)
		}
		if p := os.Getenv("CODEX_HOME"); filepath.IsAbs(p) {
			l.Codex = filepath.Clean(p)
		}
		if p := os.Getenv("CODEX_SQLITE_HOME"); filepath.IsAbs(p) {
			l.SQLite = filepath.Clean(p)
		}
	}
	return l
}

func (a *App) startStorageScan() {
	s := &a.storage
	if s.scanning {
		// A running scan may have read state that the caller just changed.
		s.rescan = true
		return
	}
	s.scanning = true
	retention := s.retention
	days := retentionDays[retention]
	locations := a.storageLocations()
	undoRoot := a.undoRoot
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		snap := storage.Scan(ctx, locations, time.Now(), days)
		records, bytes, err := manage.CleanupRecords(undoRoot)
		if err != nil {
			snap.Warnings = append(snap.Warnings, "读取清理备份："+err.Error())
		}
		a.post(func() {
			s.scanning = false
			s.snapshot = &snap
			s.scanned = retention
			s.backups = records
			s.backupBytes = bytes
			s.backupGroups = a.groupBackups(snap.Groups, records)
			s.expired = expiredBackups(records, time.Now())
			if s.selected == nil {
				s.selected = map[string]bool{}
				for _, g := range snap.Groups {
					s.selected[g.ID] = !g.History
				}
			}
			if s.rescan {
				s.rescan = false
				a.startStorageScan()
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

func (a *App) confirmStorageCleanup() {
	if a.storage.snapshot == nil {
		return
	}
	var groups []storage.Group
	var names []string
	count := 0
	for _, g := range a.storage.snapshot.Groups {
		if a.storage.selected[g.ID] && g.Eligible > 0 {
			groups = append(groups, g)
			names = append(names, g.Agent+" · "+g.Name)
			count += g.Eligible
		}
	}
	if count == 0 {
		return
	}
	days := retentionDays[a.storage.retention]
	a.ask = ask{open: true, title: "清理所选文件？", message: fmt.Sprintf("保留最近 %d 天，处理 %d 个文件：\n%s\n先压缩备份，再移除原文件；可通过撤销或清理备份恢复。历史会话与文件快照清理会影响恢复和统计。每次最多处理 %d 个文件，可随时停止。", retentionDays[a.storage.retention], count, strings.Join(names, "\n"), storageBatch), ok: "清理", done: "已清理，可撤销", run: func() ([]manage.Record, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		a.post(func() { a.storage.cancel = cancel })
		return a.runStorageCleanup(ctx, groups, count, days)
	}}
}

// runStorageCleanup stops between files on cancellation; finished files stay undoable.
func (a *App) runStorageCleanup(ctx context.Context, groups []storage.Group, count, days int) ([]manage.Record, error) {
	s := &a.storage
	// The snapshot may be old; a session resumed since then is live again.
	active := storage.FindActiveSessions(ctx, a.storageLocations(), time.Now(), days)
	total := min(count, storageBatch)
	a.post(func() { s.result, s.progress = "", fmt.Sprintf("正在清理 %d 个文件…", total) })
	var recs []manage.Record
	var first error
	var source int64
	skipped := 0
	last := time.Now()
	for _, g := range groups {
		for _, f := range g.Candidates {
			if len(recs) >= storageBatch || ctx.Err() != nil {
				break
			}
			if active.Holds(g.Category, f.Path) {
				skipped++
				if first == nil {
					first = errors.New("文件属于仍活跃的会话")
				}
				continue
			}
			rec, err := manage.CleanupFile(ctx, g.Category, f, a.undoRoot)
			if err != nil {
				if ctx.Err() != nil {
					break
				}
				skipped++
				if first == nil {
					first = err
				}
				continue
			}
			recs = append(recs, rec)
			source += f.Size
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				done := len(recs)
				a.post(func() { s.progress = fmt.Sprintf("已清理 %d / %d 个文件…", done, total) })
			}
		}
	}
	var backed int64
	for _, r := range recs {
		for _, p := range []string{r.Payload, filepath.Join(r.Dir, "record.json")} {
			if st, e := os.Stat(p); e == nil {
				backed += st.Size()
			}
		}
	}
	result := fmt.Sprintf("已清理 %d 个文件；原文件 %s，压缩备份 %s，净减少 %s。", len(recs), storageSize(source), storageSize(backed), storageSize(max(0, source-backed)))
	if backed > source {
		result += "备份占用大于原文件，可清空备份释放空间。"
	}
	if skipped > 0 {
		result += fmt.Sprintf("跳过 %d 个：%s。", skipped, first.Error())
	}
	rest := count - len(recs) - skipped
	stop := ctx.Err()
	if rest == 0 {
		stop = nil
	}
	switch {
	case errors.Is(stop, context.DeadlineExceeded):
		result += fmt.Sprintf("已超时，%d 个文件未处理，可重新清理。", rest)
		first = errors.New("清理超时，已完成的部分可撤销")
	case stop != nil:
		result += fmt.Sprintf("已停止，%d 个文件未处理。", rest)
		first = errors.New("清理已停止，已完成的部分可撤销")
	case rest > 0:
		result += fmt.Sprintf("本批最多 %d 个文件，还有 %d 个，请继续清理。", storageBatch, rest)
	}
	a.post(func() {
		s.progress, s.cancel = "", nil
		s.result = result
		a.startStorageScan()
	})
	return recs, first
}

func (a *App) confirmDatabase(d storage.Database, checkpoint bool) {
	verb := "整理碎片"
	detail := "回收空闲页并合并 WAL。"
	if checkpoint {
		verb = "合并 WAL"
		detail = "将 WAL 中已提交的记录合并回数据库。"
	}
	a.ask = ask{open: true, title: verb + "？", message: d.Agent + " · " + d.Path + "\n" + detail + "数据库记录会保留，此维护操作没有撤销备份。请先退出对应 Agent；如果数据库被占用，将提示失败。", ok: verb, run: func() ([]manage.Record, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		freed, err := manage.MaintainDatabase(ctx, d, checkpoint)
		a.post(func() {
			if err == nil {
				a.storage.result = verb + "完成，文件大小减少 " + storageSize(freed) + "。"
			} else {
				a.storage.result = verb + "失败：" + err.Error()
			}
			a.startStorageScan()
		})
		return nil, err
	}}
}

func (a *App) confirmRestoreBackups(records []manage.Record) {
	recs := append([]manage.Record(nil), records...)
	a.ask = ask{open: true, title: "恢复清理备份？", message: fmt.Sprintf("恢复 %d 个文件。若原路径已有新文件，将保留新文件并提示冲突。", len(recs)), ok: "恢复", onUI: true, run: func() ([]manage.Record, error) { a.undo(recs); return nil, nil }}
}

func (a *App) confirmPurgeBackups(records []manage.Record) {
	recs := append([]manage.Record(nil), records...)
	var bytes int64
	for _, r := range recs {
		bytes += r.Cleanup.Stored
	}
	a.ask = ask{open: true, title: "永久清空清理备份？", message: fmt.Sprintf("永久删除 %d 个文件的压缩备份（%s），之后无法撤销或恢复这些文件。", len(recs), storageSize(bytes)), ok: "永久清空", run: func() ([]manage.Record, error) {
		err := manage.PurgeCleanupBackups(a.undoRoot, recs)
		a.post(func() {
			if err == nil {
				a.storage.result = fmt.Sprintf("已永久删除 %d 个备份，释放 %s。", len(recs), storageSize(bytes))
			}
			a.startStorageScan()
		})
		return nil, err
	}}
}
