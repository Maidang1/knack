package app

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/storage"
)

// The page follows the home page: a headline sentence, a row of figures
// between hairlines, then a wide block beside a 356-wide column. The cleanup
// action stays below the scroll, in reach of every checkbox.

var storageAgents = []string{"Claude Code", "Codex"}

var storageAgentIDs = map[string]session.Agent{"Claude Code": session.Claude, "Codex": session.Codex}

// One meaning, one color, on every chart of the page. What stays recedes in
// gray; data-neutral sits too close to chart-2 to stack beside it.
type storagePalette struct{ selected, idle, kept, database ui.Color }

func storageColors(dark bool) storagePalette {
	return storagePalette{sessionColor("chart-1", dark), sessionColor("chart-2", dark), sessionColor("phase-wait", dark), sessionColor("phase-tool", dark)}
}

type barPart struct {
	value int64
	color ui.Color
}

// storageBar is a horizontal stack on a shared scale, over a quiet track.
func storageBar(c *ui.Context, height float32, scale int64, parts ...barPart) ui.Element {
	track := c.Theme().SurfaceHover
	bar := ui.Box(c).Height(height).Radius(2)
	bar.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, track, 2)
		if scale <= 0 {
			return
		}
		x := r.X
		for _, part := range parts {
			w := float32(float64(part.value) / float64(scale) * float64(r.W))
			if part.value <= 0 {
				continue
			}
			// A two-point seam separates the segments.
			p.Fill(ui.Rect{X: x, Y: r.Y, W: max(w-2, 1), H: r.H}, part.color, 2)
			x += w
		}
	})
	return bar
}

func (a *App) storagePane(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	if s.snapshot == nil && !s.scanning {
		a.startStorageScan()
	}
	busy := s.scanning || a.mutating || a.sessions.loading
	width, _ := c.Size()
	contentW := width - a.sidebarWidth() - 2*32
	ui.Scroll(c.Key("storage-scroll")).Grow(1).MinHeight(0).Label("磁盘清理内容").Children(func() {
		ui.Column(c).Padding(24, 32, 28, 32).Gap(22).Children(func() {
			a.storageHeadline(c, busy)
			if s.result != "" {
				ui.Text(c, s.result).FontSize(13).Label("清理结果").Padding(8, 12).Radius(8).Background(t.Surface).Border(1, t.Border)
			}
			if s.snapshot == nil {
				return
			}
			for _, w := range s.snapshot.Warnings {
				ui.Text(c, w).FontSize(12).TextColor(t.Warning)
			}
			a.storageMetrics(c, contentW < 640)
			blocks := func(wide, side func()) {
				if contentW < 860 {
					ui.Column(c).Gap(22).Children(func() {
						ui.Column(c).Children(wide)
						ui.Column(c).Children(side)
					})
					return
				}
				ui.Row(c).Gap(homeGap).AlignItems(ui.Start).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(wide)
					ui.Column(c).Width(homeSide).Shrink(0).Children(side)
				})
			}
			blocks(func() { a.storageAges(c) }, func() { a.storageAgentUsage(c) })
			ui.Column(c).Children(func() { a.storageTable(c, busy, contentW < 640) })
			blocks(func() { a.storageDatabases(c, busy) }, func() { a.storageBackups(c, busy) })
			ui.Text(c, "按文件最后修改时间筛选，大小为文件逻辑大小。建议先退出对应 Agent；执行时会再次检查文件是否变化。").FontSize(11.5).TextColor(token(t, "ink-3"))
		})
	})
	if s.snapshot != nil {
		a.storageActions(c, busy)
	}
}

// storageSummary says how much is over retention and where most of it sits.
func storageSummary(snap *storage.Snapshot) (string, string) {
	var eligible, protected int64
	largest := -1
	for i, g := range snap.Groups {
		eligible += g.EligibleBytes
		protected += g.ProtectedBytes
		if g.EligibleBytes > 0 && (largest < 0 || g.EligibleBytes > snap.Groups[largest].EligibleBytes) {
			largest = i
		}
	}
	if largest < 0 {
		switch {
		case len(snap.Warnings) > 0:
			return "暂时没有可清理的文件", "扫描结果不完整，请查看下方提示后重新扫描。"
		case protected > 0:
			return "暂时没有可清理的文件", fmt.Sprintf("%s 的旧文件属于仍活跃的会话，已自动保护。", storageSize(protected))
		}
		return "没有超过保留期的文件", "保留期内的文件不会被清理；可以缩短保留时间，或维护下方的数据库。"
	}
	g := snap.Groups[largest]
	return storageSize(eligible) + " 已超过保留期，可以清理",
		fmt.Sprintf("最大的一项是 %s %s（%s）。默认只选缓存与日志，历史内容需手动勾选。", g.Agent, g.Name, storageSize(g.EligibleBytes))
}

func (a *App) storageHeadline(c *ui.Context, busy bool) {
	s := &a.storage
	t := c.Theme()
	title, note := "正在扫描磁盘占用…", "扫描 Claude Code 与 Codex 的缓存、历史记录和本地数据库。"
	if s.snapshot != nil {
		title, note = storageSummary(s.snapshot)
	}
	ui.Row(c).Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Basis(320).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(600).MaxLines(2)
			ui.Text(c, note).FontSize(14).TextColor(t.TextMuted).MaxLines(2)
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if s.scanning && s.snapshot != nil {
				ui.Text(c, "正在扫描…").FontSize(13).TextColor(t.TextMuted).SingleLine()
			}
			ui.Text(c, "保留最近").FontSize(13).TextColor(t.TextMuted).SingleLine()
			ui.Box(c).Disabled(busy).Children(func() {
				if segmented(c, "清理保留时间", &s.retention, "7 天", "30 天", "90 天", "180 天") {
					a.startStorageScan()
				}
			})
			if outlineButton(c, "storage-refresh", "扫描磁盘").Disabled(busy).Clicked() {
				a.startStorageScan()
			}
		})
	})
}

func (a *App) storageMetrics(c *ui.Context, narrow bool) {
	s := &a.storage
	t := c.Theme()
	var tracked, eligible, databases, wal, free int64
	files, candidates := 0, 0
	for _, g := range s.snapshot.Groups {
		tracked += g.Bytes
		eligible += g.EligibleBytes
		files += g.Files
		candidates += g.Eligible
	}
	for _, d := range s.snapshot.Databases {
		databases += d.Bytes + d.WALBytes
		wal += d.WALBytes
		free += d.FreeBytes
	}
	share := 0
	if tracked > 0 {
		share = int(100 * eligible / tracked)
	}
	cells := []struct{ label, value, note string }{
		{"文件占用", storageSize(tracked), fmt.Sprintf("%d 个文件", files)},
		{"超过保留期", storageSize(eligible), fmt.Sprintf("%d 个文件 · 占 %d%%", candidates, share)},
		{"数据库与 WAL", storageSize(databases), fmt.Sprintf("空闲页 %s · WAL %s", storageSize(free), storageSize(wal))},
		{"清理备份", storageSize(s.backupBytes), fmt.Sprintf("%d 个文件 · 清空后释放", len(s.backups))},
	}
	rows := [][]int{{0, 1, 2, 3}}
	if narrow {
		rows = [][]int{{0, 1}, {2, 3}}
	}
	ui.Column(c).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
		for r, row := range rows {
			line := ui.Row(c).Padding(12, 0)
			if r > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			line.Children(func() {
				for j, i := range row {
					cell := ui.Column(c.Key(cells[i].label)).Grow(1).Basis(0).MinWidth(0).Gap(4).Padding(0, 20)
					if j == 0 {
						cell.Padding(0, 20, 0, 0)
					} else {
						cell.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
					}
					cell.Children(func() {
						ui.Text(c, cells[i].label).FontSize(13).TextColor(t.TextMuted).SingleLine()
						number(c, cells[i].value).FontSize(24).FontWeight(600)
						ui.Text(c, cells[i].note).FontSize(12).TextColor(token(t, "ink-3")).SingleLine().Tooltip(cells[i].note)
					})
				}
			})
		}
	})
}

var storageAgeLabels = []string{"7 天内", "7–30 天", "30–90 天", "90–180 天", "180 天以上"}

// storageAgeBands splits each band into selected, idle and kept bytes. Bands
// past retention still keep what active sessions protect.
func storageAgeBands(groups []storage.Group, selected map[string]bool, retention int) [][3]int64 {
	bands := make([][3]int64, storage.AgeBands)
	for _, g := range groups {
		for i, bytes := range g.Ages {
			releasable := bytes - g.ProtectedAges[i]
			if i <= retention {
				releasable = 0
			}
			bands[i][2] += bytes - releasable
			if selected[g.ID] {
				bands[i][0] += releasable
			} else {
				bands[i][1] += releasable
			}
		}
	}
	return bands
}

// storageAges shows how much each retention choice would release: the bands
// older than the current choice take the colors of what can be cleaned.
func (a *App) storageAges(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	colors := storageColors(t.Dark)
	muted := token(t, "ink-3")
	const plot = 132
	bands := storageAgeBands(s.snapshot.Groups, s.selected, s.scanned)
	var peak int64
	for _, band := range bands {
		peak = max(peak, band[0]+band[1]+band[2])
	}
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "文件年龄").FontSize(15).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			legendItem(c, colors.selected, "已选")
			legendItem(c, colors.idle, "可清理")
			legendItem(c, colors.kept, "保留期内")
		})
		ui.Row(c).Height(plot + 22).Gap(12).AlignItems(ui.End).Label("按文件年龄的占用图表").Children(func() {
			for i, band := range bands {
				total := band[0] + band[1] + band[2]
				description := storageAgeLabels[i] + " · " + storageSize(total)
				ui.Column(c.Key(storageAgeLabels[i])).Grow(1).Basis(0).MinWidth(0).Gap(4).AlignItems(ui.Center).Label(description).Tooltip(description).Children(func() {
					number(c, storageSize(total)).FontSize(11.5).TextColor(t.TextMuted)
					h := float32(0)
					if peak > 0 && total > 0 {
						h = max(float32(float64(total)/float64(peak))*plot, 2)
					}
					bar := ui.Box(c).Height(h).AlignSelf(ui.Stretch)
					values := []barPart{{band[2], colors.kept}, {band[1], colors.idle}, {band[0], colors.selected}}
					bar.Draw(func(p *ui.Painter, r ui.Rect) {
						w := min(r.W, float32(56))
						y := r.Y + r.H
						for _, part := range values {
							if part.value <= 0 {
								continue
							}
							segment := float32(float64(part.value) / float64(total) * float64(r.H))
							y -= segment
							p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: y + 1, W: w, H: max(segment-1, 1)}, part.color, 2)
						}
					})
				})
			}
		})
		ui.Column(c).Gap(5).Children(func() {
			ui.Box(c).Height(1).Background(t.Border)
			ui.Row(c).Gap(12).Children(func() {
				for i, label := range storageAgeLabels {
					text := ui.Text(c, label).FontSize(11).TextColor(muted).Grow(1).Basis(0).MinWidth(0).TextAlign(ui.Center).SingleLine()
					if i == s.scanned {
						text.TextColor(t.Text).FontWeight(600).Tooltip("保留期的边界")
					}
				}
			})
		})
	})
}

func (a *App) storageAgentUsage(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	colors := storageColors(t.Dark)
	type usage struct{ selected, idle, kept, database int64 }
	totals := map[string]*usage{}
	for _, agent := range storageAgents {
		totals[agent] = &usage{}
	}
	for _, g := range s.snapshot.Groups {
		u := totals[g.Agent]
		if u == nil {
			continue
		}
		if s.selected[g.ID] {
			u.selected += g.EligibleBytes
		} else {
			u.idle += g.EligibleBytes
		}
		u.kept += g.Bytes - g.EligibleBytes
	}
	for _, d := range s.snapshot.Databases {
		if u := totals[d.Agent]; u != nil {
			u.database += d.Bytes + d.WALBytes
		}
	}
	var peak int64
	for _, u := range totals {
		peak = max(peak, u.selected+u.idle+u.kept+u.database)
	}
	ui.Column(c).Children(func() {
		ui.Row(c).Gap(16).Padding(0, 0, 14, 0).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Agent 占用").FontSize(15).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			legendItem(c, colors.database, "数据库")
		})
		ui.Column(c).Gap(18).Children(func() {
			for _, agent := range storageAgents {
				u := totals[agent]
				total := u.selected + u.idle + u.kept + u.database
				ui.Column(c.Key(agent)).Gap(7).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						storageAgentDot(c, agent)
						ui.Text(c, agent).FontSize(13).Grow(1).MinWidth(0).SingleLine()
						number(c, storageSize(total)).FontSize(13).FontWeight(600)
					})
					storageBar(c, 8, peak, barPart{u.selected, colors.selected}, barPart{u.idle, colors.idle}, barPart{u.kept, colors.kept}, barPart{u.database, colors.database}).Label(agent + " 占用")
					ui.Text(c, fmt.Sprintf("可清理 %s · 保留 %s · 数据库 %s", storageSize(u.selected+u.idle), storageSize(u.kept), storageSize(u.database))).FontSize(12).TextColor(token(t, "ink-3")).SingleLine()
				})
			}
		})
	})
}

func storageAgentDot(c *ui.Context, agent string) {
	ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(agentColor(storageAgentIDs[agent], c.Theme().Dark))
}

// storageRows ranks an Agent's categories by what can be released.
func storageRows(groups []storage.Group, agent string, showEmpty bool) []storage.Group {
	var rows []storage.Group
	for _, g := range groups {
		if g.Agent == agent && (showEmpty || g.Files > 0) {
			rows = append(rows, g)
		}
	}
	slices.SortStableFunc(rows, func(x, y storage.Group) int {
		return cmp.Or(cmp.Compare(y.EligibleBytes, x.EligibleBytes), cmp.Compare(y.Bytes, x.Bytes))
	})
	return rows
}

func (a *App) storageTable(c *ui.Context, busy, compact bool) {
	s := &a.storage
	t := c.Theme()
	muted := token(t, "ink-3")
	var peak int64
	empty := 0
	for _, g := range s.snapshot.Groups {
		peak = max(peak, g.Bytes)
		if g.Files == 0 {
			empty++
		}
	}
	link := ""
	if empty > 0 {
		link = fmt.Sprintf("显示 %d 个空类别", empty)
		if s.showEmpty {
			link = "隐藏空类别"
		}
	}
	sectionHead(c, "清理项", link, func() { s.showEmpty = !s.showEmpty })
	ui.Row(c).Height(26).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, "类别").Grow(1).MinWidth(0).FontSize(11.5).TextColor(muted).Margin(0, 0, 0, 30)
		if !compact {
			ui.Text(c, "占比").Width(storageBarWidth).Shrink(0).FontSize(11.5).TextColor(muted)
		}
		for _, title := range storageColumns(compact) {
			ui.Text(c, title).Width(storageColumn).Shrink(0).TextAlign(ui.End).FontSize(11.5).TextColor(muted)
		}
		ui.Box(c).Width(storageOpenWidth).Shrink(0)
	})
	for _, agent := range storageAgents {
		rows := storageRows(s.snapshot.Groups, agent, s.showEmpty)
		if len(rows) == 0 {
			continue
		}
		var bytes, eligible int64
		for _, g := range rows {
			bytes += g.Bytes
			eligible += g.EligibleBytes
		}
		ui.Row(c.Key("agent:"+agent)).Height(34).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			storageAgentDot(c, agent)
			ui.Text(c, agent).FontSize(13).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			number(c, fmt.Sprintf("占用 %s · 可清理 %s", storageSize(bytes), storageSize(eligible))).FontSize(12).TextColor(muted)
		})
		for _, g := range rows {
			a.storageGroup(c, g, peak, busy, compact)
		}
	}
}

const (
	storageBarWidth  = 120
	storageColumn    = 72
	storageOpenWidth = 44
)

func storageColumns(compact bool) []string {
	if compact {
		return []string{"可清理"}
	}
	return []string{"占用", "可清理", "文件"}
}

func (a *App) storageGroup(c *ui.Context, g storage.Group, peak int64, busy, compact bool) {
	s := &a.storage
	t := c.Theme()
	colors := storageColors(t.Dark)
	muted := token(t, "ink-3")
	ui.Row(c.Key(g.ID)).Padding(8, 0).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		// Nothing to clean must not read as chosen, whatever is remembered.
		selected := s.selected[g.ID] && g.Eligible > 0
		if ui.Checkbox(c, &selected, "").Label("选择 " + g.Agent + " " + g.Name).Disabled(busy || g.Eligible == 0).Changed() {
			s.selected[g.ID] = selected
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, g.Name).FontSize(13).FontWeight(500).SingleLine()
				if g.History {
					ui.Text(c, "影响历史恢复").FontSize(11).TextColor(token(t, "warn-ink")).SingleLine()
				}
				if g.Protected > 0 {
					ui.Text(c, fmt.Sprintf("%d 个受保护", g.Protected)).FontSize(11).TextColor(muted).SingleLine().
						Tooltip(fmt.Sprintf("%d 个文件（%s）属于保留期内仍活跃的会话，不会清理", g.Protected, storageSize(g.ProtectedBytes)))
				}
			})
			ui.Text(c, g.Description).FontSize(12).TextColor(muted).SingleLine().Tooltip(g.Description + "\n" + g.Root)
		})
		part := colors.idle
		if s.selected[g.ID] {
			part = colors.selected
		}
		if !compact {
			ui.Column(c).Width(storageBarWidth).Shrink(0).Children(func() {
				storageBar(c, 6, peak, barPart{g.EligibleBytes, part}, barPart{g.Bytes - g.EligibleBytes, colors.kept}).Label(g.Name + " 占比")
			})
			number(c, storageSize(g.Bytes)).Width(storageColumn).Shrink(0).TextAlign(ui.End).FontSize(13).TextColor(t.TextMuted)
		}
		number(c, storageSize(g.EligibleBytes)).Width(storageColumn).Shrink(0).TextAlign(ui.End).FontSize(13).FontWeight(600)
		if !compact {
			number(c, fmt.Sprintf("%d / %d", g.Eligible, g.Files)).Width(storageColumn).Shrink(0).TextAlign(ui.End).FontSize(13).TextColor(t.TextMuted).Tooltip("可清理 / 全部文件")
		}
		if outlineButton(c, "open:"+g.ID, "查看").Width(storageOpenWidth).Padding(0).Tooltip(g.Root).Disabled(g.Files == 0).Clicked() {
			a.reveal(g.Root)
		}
	})
}

// storageActions stays below the scroll: the choice is made in a long table.
func (a *App) storageActions(c *ui.Context, busy bool) {
	s := &a.storage
	t := c.Theme()
	count, bytes := 0, int64(0)
	for _, g := range s.snapshot.Groups {
		if s.selected[g.ID] {
			count += g.Eligible
			bytes += g.EligibleBytes
		}
	}
	ui.Row(c).Padding(10, 32).Gap(10).AlignItems(ui.Center).Shrink(0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			if s.progress != "" {
				ui.Text(c, s.progress).FontSize(13).FontWeight(600).SingleLine().Label("清理进度")
				return
			}
			number(c, fmt.Sprintf("已选 %d 个文件 · %s", count, storageSize(bytes))).FontSize(13).FontWeight(600)
			note := "清理前会压缩备份，可撤销；释放空间取决于压缩效果。清空备份后将无法恢复。"
			if count > storageBatch {
				note = fmt.Sprintf("每次最多处理 %d 个文件，其余可继续清理。%s", storageBatch, note)
			}
			ui.Text(c, note).FontSize(12).TextColor(token(t, "ink-3")).SingleLine().Tooltip(note)
		})
		if s.cancel != nil && outlineButton(c, "storage-stop", "停止").Clicked() {
			s.cancel()
		}
		if outlineButton(c, "storage-safe", "选择缓存与日志").Disabled(busy).Clicked() {
			for _, g := range s.snapshot.Groups {
				s.selected[g.ID] = !g.History
			}
		}
		if primaryButton(c, "storage-clean", "", "清理所选…").Disabled(busy || count == 0).Clicked() {
			a.confirmStorageCleanup()
		}
	})
}

func (a *App) storageDatabases(c *ui.Context, busy bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	sectionHead(c, "数据库维护", "", nil)
	ui.Column(c).Gap(10).Children(func() {
		ui.Text(c, "整理回收空闲页，合并 WAL 保留数据库记录。请先退出对应 Agent；整理可能需要约数据库大小两倍的临时空间。").FontSize(12).TextColor(t.TextMuted)
		for _, agent := range storageAgents {
			ui.Column(c).Children(func() {
				ui.Row(c).Height(30).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					storageAgentDot(c, agent)
					ui.Text(c, agent+" 数据库").FontSize(13).FontWeight(600).SingleLine()
				})
				found := false
				for _, d := range a.storage.snapshot.Databases {
					if d.Agent != agent {
						continue
					}
					found = true
					ui.Row(c.Key(d.Path)).Wrap().Padding(8, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
						ui.Column(c).Grow(1).Basis(180).MinWidth(0).Gap(2).Children(func() {
							ui.Text(c, filepath.Base(d.Path)).FontSize(13).FontWeight(500).SingleLine().Tooltip(d.Path)
							number(c, fmt.Sprintf("%s · 空闲页 %s · WAL %s", storageSize(d.Bytes), storageSize(d.FreeBytes), storageSize(d.WALBytes))).FontSize(12).TextColor(muted)
							if d.Error != "" {
								ui.Text(c, d.Error).FontSize(12).TextColor(t.Danger)
							}
						})
						ui.Row(c).Gap(6).Children(func() {
							if outlineButton(c, "vacuum:"+d.Path, "整理碎片…").Disabled(busy || d.Error != "").Clicked() {
								a.confirmDatabase(d, false)
							}
							if outlineButton(c, "checkpoint:"+d.Path, "合并 WAL…").Disabled(busy || d.Error != "").Clicked() {
								a.confirmDatabase(d, true)
							}
							if outlineButton(c, "reveal:"+d.Path, "查看").Tooltip(d.Path).Clicked() {
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
					ui.Text(c, message).FontSize(12).TextColor(muted).Padding(8, 0)
				}
			})
		}
	})
}

func (a *App) storageBackups(c *ui.Context, busy bool) {
	s := &a.storage
	t := c.Theme()
	if len(s.backups) == 0 {
		sectionHead(c, "清理备份", "", nil)
		ui.Text(c, "还没有清理备份").FontSize(13).TextColor(t.TextMuted).Padding(11, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
		return
	}
	sectionHead(c, "清理备份", "查看目录 →", func() { a.reveal(a.undoRoot) })
	for _, g := range s.backupGroups {
		a.storageBackupGroup(c, g, busy)
	}
	ui.Row(c).Wrap().Gap(6).Padding(10, 0, 0, 0).Children(func() {
		if outlineButton(c, "storage-restore", "恢复全部…").Label("恢复全部清理备份").Disabled(busy).Clicked() {
			a.confirmRestoreBackups(s.backups)
		}
		if outlineButton(c, "storage-purge", "清空全部…").Label("清空全部清理备份").TextColor(t.Danger).Disabled(busy).Clicked() {
			a.confirmPurgeBackups(s.backups)
		}
		if outlineButton(c, "storage-expire", fmt.Sprintf("清除 %d 天前…", backupExpiryDays)).Label(fmt.Sprintf("清除 %d 天前的备份", backupExpiryDays)).TextColor(t.Danger).Disabled(busy || len(s.expired) == 0).Clicked() {
			a.confirmPurgeBackups(s.expired)
		}
	})
}

func (a *App) storageBackupGroup(c *ui.Context, g backupGroup, busy bool) {
	t := c.Theme()
	ui.Row(c.Key(g.label)).Padding(8, 0).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, g.label).FontSize(13).SingleLine().Tooltip(g.label)
			number(c, fmt.Sprintf("%d 个文件 · %s", len(g.records), storageSize(g.bytes))).FontSize(12).TextColor(token(t, "ink-3"))
		})
		if outlineButton(c, "restore:"+g.label, "恢复…").Disabled(busy).Clicked() {
			a.confirmRestoreBackups(g.records)
		}
		if outlineButton(c, "purge:"+g.label, "清空…").TextColor(t.Danger).Disabled(busy).Clicked() {
			a.confirmPurgeBackups(g.records)
		}
	})
}
