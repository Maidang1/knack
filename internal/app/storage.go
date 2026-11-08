package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/fsutil"
	"knack/internal/manage"
	"knack/internal/storage"
)

const storagePage = "storage"

var retentionDays = []int{7, 30, 90, 180}

type storageState struct {
	snapshot    *storage.Snapshot
	scanning    bool
	retention   int
	selected    map[string]bool
	backups     []manage.Record
	backupBytes int64
	result      string
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
		return
	}
	s.scanning = true
	days := retentionDays[s.retention]
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
			s.backups = records
			s.backupBytes = bytes
			if s.selected == nil {
				s.selected = map[string]bool{}
				for _, g := range snap.Groups {
					s.selected[g.ID] = !g.History
				}
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

func (a *App) storagePane(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	if s.snapshot == nil && !s.scanning {
		a.startStorageScan()
	}
	busy := s.scanning || a.mutating || a.sessions.loading
	ui.Scroll(c.Key("storage-scroll")).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Padding(24, 32).Gap(20).Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "给 Agent 腾出空间").FontSize(22).FontWeight(600)
				ui.Text(c, "扫描 Claude Code 与 Codex 的缓存、历史记录和本地数据库。").FontSize(13).TextColor(t.TextMuted)
			})
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "保留最近").FontSize(13)
				ui.Box(c).Disabled(busy).Children(func() {
					if segmented(c, "清理保留时间", &s.retention, "7 天", "30 天", "90 天", "180 天") {
						a.startStorageScan()
					}
				})
				ui.Spacer(c)
				if outlineButton(c, "storage-refresh", "扫描磁盘").Disabled(busy).Clicked() {
					a.startStorageScan()
				}
			})
			if s.scanning {
				ui.Text(c, "正在扫描磁盘占用…").FontSize(13).TextColor(t.TextMuted)
			}
			if s.result != "" {
				ui.Text(c, s.result).FontSize(13).Label("清理结果")
			}
			if s.snapshot == nil {
				return
			}
			for _, w := range s.snapshot.Warnings {
				ui.Text(c, w).FontSize(12).TextColor(t.Danger)
			}
			var tracked, eligible int64
			for _, g := range s.snapshot.Groups {
				tracked += g.Bytes
				eligible += g.EligibleBytes
			}
			var databaseBytes int64
			for _, d := range s.snapshot.Databases {
				databaseBytes += d.Bytes + d.WALBytes
			}
			ui.Row(c).Wrap().Gap(20).Children(func() {
				for _, metric := range []struct{ label, value string }{
					{"文件占用", storageSize(tracked)},
					{"超过保留期", storageSize(eligible)},
					{"数据库与 WAL", storageSize(databaseBytes)},
				} {
					ui.Column(c).Grow(1).MinWidth(140).Padding(14).Gap(6).Background(t.Surface).Border(1, t.Border).Radius(10).Children(func() {
						ui.Text(c, metric.label).FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, metric.value).FontSize(22).FontWeight(600).FontFeatures("tnum")
					})
				}
			})
			ui.Text(c, "按文件最后修改时间筛选。建议先退出对应 Agent；执行时会再次检查文件是否变化。").FontSize(12).TextColor(t.TextMuted)
			for _, agent := range []string{"Claude Code", "Codex"} {
				ui.Column(c).Gap(8).Children(func() {
					ui.Text(c, agent).FontSize(15).FontWeight(600)
					ui.Column(c).Background(t.Surface).Border(1, t.Border).Radius(10).Clip().Children(func() {
						for _, g := range s.snapshot.Groups {
							if g.Agent == agent {
								a.storageGroup(c, g, busy)
							}
						}
					})
				})
			}
			count, bytes := 0, int64(0)
			for _, g := range s.snapshot.Groups {
				if s.selected[g.ID] {
					count += g.Eligible
					bytes += g.EligibleBytes
				}
			}
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("已选 %d 个文件 · %s", count, storageSize(bytes))).FontSize(13).Grow(1)
				if outlineButton(c, "storage-safe", "选择缓存与日志").Disabled(busy).Clicked() {
					for _, g := range s.snapshot.Groups {
						s.selected[g.ID] = !g.History
					}
				}
				if primaryButton(c, "storage-clean", "", "清理所选…").Disabled(busy || count == 0).Clicked() {
					a.confirmStorageCleanup()
				}
			})
			ui.Text(c, "清理前会压缩备份，可撤销；释放空间取决于压缩效果。清空备份后将无法恢复。").FontSize(12).TextColor(t.TextMuted)
			a.storageDatabases(c, busy)
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "清理备份").FontSize(15).FontWeight(600)
				ui.Text(c, fmt.Sprintf("%d 个文件 · %s", len(s.backups), storageSize(s.backupBytes))).FontSize(13)
				ui.Row(c).Wrap().Gap(8).Children(func() {
					if outlineButton(c, "storage-restore", "恢复全部清理备份…").Disabled(busy || len(s.backups) == 0).Clicked() {
						recs := append([]manage.Record(nil), s.backups...)
						a.ask = ask{open: true, title: "恢复清理备份？", message: fmt.Sprintf("恢复 %d 个文件。若原路径已有新文件，将保留新文件并提示冲突。", len(recs)), ok: "恢复", onUI: true, run: func() ([]manage.Record, error) { a.undo(recs); return nil, nil }}
					}
					if outlineButton(c, "storage-purge", "清空清理备份…").TextColor(t.Danger).Disabled(busy || len(s.backups) == 0).Clicked() {
						a.confirmPurgeStorage()
					}
					if outlineButton(c, "storage-reveal-backup", "查看备份目录").Clicked() {
						a.reveal(a.undoRoot)
					}
				})
			})
		})
	})
}

func (a *App) storageGroup(c *ui.Context, g storage.Group, busy bool) {
	s := &a.storage
	t := c.Theme()
	ui.Row(c.Key(g.ID)).Padding(12, 14).Gap(12).AlignItems(ui.Center).Children(func() {
		selected := s.selected[g.ID]
		if ui.Checkbox(c, &selected, "").Label("选择 " + g.Agent + " " + g.Name).Disabled(busy || g.Eligible == 0).Changed() {
			s.selected[g.ID] = selected
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Wrap().Gap(8).Children(func() {
				ui.Text(c, g.Name).FontSize(13).FontWeight(500)
				if g.History {
					ui.Text(c, "影响历史恢复").FontSize(11).TextColor(token(t, "warn-ink"))
				}
			})
			ui.Text(c, g.Description).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, shortPath(a.home, g.Root)).FontSize(11).TextColor(t.TextMuted).MaxLines(1).Tooltip(g.Root)
			ui.Text(c, fmt.Sprintf("占用 %s · %d 个文件；可清理 %s · %d 个文件", storageSize(g.Bytes), g.Files, storageSize(g.EligibleBytes), g.Eligible)).FontSize(12).TextColor(t.TextMuted)
		})
		if outlineButton(c, "open:"+g.ID, "查看").Clicked() {
			a.reveal(g.Root)
		}
	})
}

func (a *App) storageDatabases(c *ui.Context, busy bool) {
	t := c.Theme()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "数据库维护").FontSize(15).FontWeight(600)
		ui.Text(c, "整理回收空闲页，合并 WAL 保留数据库记录。请先退出对应 Agent；整理可能需要约数据库大小两倍的临时空间。").FontSize(12).TextColor(t.TextMuted)
		for _, agent := range []string{"Claude Code", "Codex"} {
			ui.Text(c, agent+" 数据库").FontSize(13).FontWeight(600).Margin(4, 0, 0, 0)
			found := false
			for _, d := range a.storage.snapshot.Databases {
				if d.Agent != agent {
					continue
				}
				found = true
				ui.Column(c.Key(d.Path)).Padding(14).Gap(8).Background(t.Surface).Border(1, t.Border).Radius(10).Children(func() {
					ui.Text(c, filepath.Base(d.Path)).FontSize(13).FontWeight(600)
					ui.Text(c, shortPath(a.home, d.Path)).FontSize(11).TextColor(t.TextMuted).MaxLines(1).Tooltip(d.Path)
					ui.Text(c, fmt.Sprintf("数据库 %s · 空闲页 %s · WAL %s", storageSize(d.Bytes), storageSize(d.FreeBytes), storageSize(d.WALBytes))).FontSize(12).TextColor(t.TextMuted)
					if d.Error != "" {
						ui.Text(c, d.Error).FontSize(12).TextColor(t.Danger)
					}
					ui.Row(c).Wrap().Gap(8).Children(func() {
						if outlineButton(c, "vacuum:"+d.Path, "整理碎片…").Disabled(busy || d.Error != "").Clicked() {
							a.confirmDatabase(d, false)
						}
						if outlineButton(c, "checkpoint:"+d.Path, "合并 WAL…").Disabled(busy || d.Error != "").Clicked() {
							a.confirmDatabase(d, true)
						}
						if outlineButton(c, "reveal:"+d.Path, "查看文件").Clicked() {
							a.reveal(d.Path)
						}
					})
				})
			}
			if !found {
				message := "没有发现本地 SQLite 数据库。"
				if agent == "Claude Code" {
					message = "未发现 SQLite 数据库。Claude Code 的会话通常以 JSONL 保存，可在上方清理历史文件。"
				}
				ui.Text(c, message).FontSize(13).TextColor(t.TextMuted)
			}
		}
	})
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
	a.ask = ask{open: true, title: "清理所选文件？", message: fmt.Sprintf("保留最近 %d 天，处理 %d 个文件：\n%s\n先压缩备份，再移除原文件；可通过撤销或清理备份恢复。历史会话与文件快照清理会影响恢复和统计。每次最多处理 2000 个文件。", retentionDays[a.storage.retention], count, strings.Join(names, "\n")), ok: "清理", done: "已清理，可撤销", run: func() ([]manage.Record, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		var recs []manage.Record
		var first error
		var source int64
		for _, g := range groups {
			for _, f := range g.Candidates {
				if len(recs) >= 2000 {
					break
				}
				rec, err := manage.CleanupFile(ctx, g.Category, f, a.undoRoot)
				if err != nil {
					if first == nil {
						first = err
					}
					continue
				}
				recs = append(recs, rec)
				source += f.Size
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
		if count > 2000 {
			result += "本批最多 2000 个文件，请继续清理剩余文件。"
		}
		a.post(func() { a.storage.result = result; a.startStorageScan() })
		return recs, first
	}}
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

func (a *App) confirmPurgeStorage() {
	recs := append([]manage.Record(nil), a.storage.backups...)
	a.ask = ask{open: true, title: "永久清空清理备份？", message: fmt.Sprintf("永久删除 %d 个文件的压缩备份（%s），之后无法撤销或恢复这些文件。", len(recs), storageSize(a.storage.backupBytes)), ok: "永久清空", run: func() ([]manage.Record, error) {
		err := manage.PurgeCleanupBackups(a.undoRoot, recs)
		a.post(func() {
			if err == nil {
				a.storage.result = "已清空清理备份。"
			}
			a.startStorageScan()
		})
		return nil, err
	}}
}
