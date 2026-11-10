package app

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"knack/internal/session"
	"knack/internal/storage"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"
)

var storageAgents = []string{"Claude Code", "Codex"}

var storageAgentIDs = map[string]session.Agent{"Claude Code": session.Claude, "Codex": session.Codex}

// One meaning, one color, on every chart of the page. What stays recedes in
// gray; data-neutral sits too close to chart-2 to stack beside it.
type storagePalette struct{ selected, idle, kept, database, wal ui.Color }

func storageColors(dark bool) storagePalette {
	return storagePalette{sessionColor("chart-1", dark), sessionColor("chart-2", dark), sessionColor("phase-wait", dark), sessionColor("phase-tool", dark), sessionColor("phase-think", dark)}
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

func storagePanel(c *ui.Context) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(16).Gap(12).Radius(10).Background(token(t, "surface")).Border(1, token(t, "line"))
}

func (a *App) storagePane(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	if s.snapshot == nil && !s.scanning {
		a.startStorageScan()
	}
	busy := s.scanning || a.mutating || a.sessions.loading
	width, _ := c.Size()
	contentW := width - a.sidebarWidth() - 64
	ui.Scroll(c.Key("storage-scroll")).Grow(1).MinHeight(0).Label("磁盘清理内容").Children(func() {
		ui.Column(c).Padding(16, 32).Gap(12).Children(func() {
			if s.result != "" {
				ui.Text(c, s.result).FontSize(13).Label("清理结果").Padding(8, 12).Radius(8).Background(t.Surface).Border(1, t.Border)
			}
			if s.snapshot == nil {
				ui.Text(c, "正在扫描磁盘占用…").FontSize(14).TextColor(t.TextMuted)
				return
			}
			for _, w := range s.snapshot.Warnings {
				ui.Text(c, w).FontSize(12).TextColor(t.Warning)
			}
			if contentW >= 1240 {
				ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
					storagePanel(c).Width(400).Shrink(0).Height(353).Children(func() { a.storageMetrics(c, false) })
					storagePanel(c).Grow(1).MinWidth(0).Height(353).Children(func() { a.storageAges(c) })
					storagePanel(c).Width(372).Shrink(0).Height(353).Children(func() { a.storageSuggestions(c, busy) })
				})
			} else {
				row := ui.Row(c).Gap(12).AlignItems(ui.Stretch)
				if contentW < 860 {
					row.Column()
				}
				row.Children(func() {
					storagePanel(c).Grow(1).MinWidth(0).MinHeight(353).Children(func() { a.storageMetrics(c, contentW < 400) })
					suggestionHeight := float32(353)
					if contentW < 860 {
						suggestionHeight = float32(61 + 65*max(1, len(storageSuggestions(s.snapshot))))
					}
					storagePanel(c).Grow(1).MinWidth(0).MinHeight(suggestionHeight).Children(func() { a.storageSuggestions(c, busy) })
				})
				storagePanel(c).MinHeight(353).Children(func() { a.storageAges(c) })
			}
			bottom := ui.Row(c).Gap(12).AlignItems(ui.Stretch)
			if contentW < 1240 {
				bottom.Column()
			}
			bottom.Children(func() {
				storagePanel(c).Grow(1).MinWidth(0).Children(func() { a.storageTable(c, busy, contentW < 640) })
				side := ui.Column(c).Gap(12)
				if contentW >= 1240 {
					side.Width(372).Shrink(0)
				}
				side.Children(func() {
					storagePanel(c).Grow(1).Children(func() { a.storageDatabases(c, busy) })
					storagePanel(c).Children(func() { a.storageBackups(c, busy) })
				})
			})
		})
	})
	if s.snapshot != nil {
		a.storageActions(c, busy)
	}
}

// storageSummary keeps the explanation when nothing can be cleaned.
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
	ui.Row(c).Gap(10).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		status := "正在扫描…"
		if !s.scanning && s.snapshot != nil {
			status = fmt.Sprintf("已扫描 · %d 分钟前", max(0, int(time.Since(s.snapshot.At).Minutes())))
		}
		ui.Text(c, status).FontSize(12).TextColor(token(c.Theme(), "ink-3"))
		if outlineButton(c, "storage-refresh", "扫描磁盘").Disabled(busy).Clicked() {
			a.startStorageScan()
		}
	})
}

func (a *App) storageMetrics(c *ui.Context, narrow bool) {
	s := &a.storage
	t := c.Theme()
	colors := storageColors(t.Dark)
	var tracked, eligible, chosen int64
	files, candidates, selected := 0, 0, 0
	for _, g := range s.snapshot.Groups {
		tracked += g.Bytes
		eligible += g.EligibleBytes
		files += g.Files
		candidates += g.Eligible
		if s.selected[g.ID] {
			chosen += g.EligibleBytes
			selected += g.Eligible
		}
	}
	share := int64(0)
	if tracked > 0 {
		share = 100 * eligible / tracked
	}
	ui.Text(c, "可回收空间").FontSize(14).FontWeight(600)
	row := ui.Row(c).Grow(1).MinHeight(220).Gap(22).AlignItems(ui.Center)
	if narrow {
		row.Wrap()
	}
	row.Children(func() {
		ring := ui.Box(c).Size(188, 188).Shrink(0).Label("可回收空间圆环").Justify(ui.Center).AlignItems(ui.Center)
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			parts := []barPart{{chosen, colors.selected}, {eligible - chosen, colors.idle}, {tracked - eligible, colors.kept}}
			var stops []ui.GradientStop
			var weights [3]float32
			sum := float32(0)
			for i, part := range parts {
				if part.value > 0 {
					weights[i] = max(float32(float64(part.value)/float64(max(tracked, 1))), float32(4.0/360))
					sum += weights[i]
				}
			}
			at := float32(0)
			for i, part := range parts {
				if weights[i] == 0 {
					continue
				}
				end := at + weights[i]/sum
				gap := min(float32(2.0/360), (end-at)/2)
				stops = append(stops, ui.GradientStop{Color: part.color, At: at}, ui.GradientStop{Color: part.color, At: end - gap}, ui.GradientStop{Color: token(t, "surface"), At: end - gap}, ui.GradientStop{Color: token(t, "surface"), At: end})
				at = end
			}
			if len(stops) > 0 {
				p.FillConicGradient(r, ui.ConicGradient{Stops: stops}, 94)
			} else {
				p.Fill(r, colors.kept, 94)
			}
			inset := r.W * .11
			p.Fill(ui.Rect{X: r.X + inset, Y: r.Y + inset, W: r.W - 2*inset, H: r.H - 2*inset}, token(t, "surface"), 94)
		})
		ring.Children(func() {
			ui.Column(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "可清理").FontSize(12).TextColor(t.TextMuted)
				number(c, storageSize(eligible)).FontSize(26).FontWeight(600)
				ui.Text(c, fmt.Sprintf("占文件 %d%%", share)).FontSize(12).TextColor(t.TextMuted)
			})
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(16).Children(func() {
			if eligible == 0 {
				title, note := storageSummary(s.snapshot)
				ui.Text(c, title).FontSize(13).FontWeight(600)
				ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
				return
			}
			for _, v := range []struct {
				label string
				bytes int64
				count int
				color ui.Color
			}{{"已选", chosen, selected, colors.selected}, {"待选", eligible - chosen, candidates - selected, colors.idle}, {"保留期内", tracked - eligible, files - candidates, colors.kept}} {
				ui.Column(c).Gap(3).Children(func() {
					legendItem(c, v.color, v.label)
					number(c, storageSize(v.bytes)).FontSize(16).FontWeight(600)
					ui.Text(c, fmt.Sprintf("%d 个文件", v.count)).FontSize(12).TextColor(t.TextMuted)
				})
			}
		})
	})
	ui.Column(c).Gap(6).Padding(12, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Wrap().Gap(6).Children(func() {
			ui.Text(c, "全部清理后的文件占用").Grow(1).FontSize(11.5).TextColor(t.TextMuted)
			number(c, storageSize(tracked)+" → "+storageSize(tracked-eligible)).FontSize(12).FontWeight(600)
		})
		storageBar(c, 6, tracked, barPart{tracked - eligible, colors.kept})
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

// storageReleasable is exact only for the current scanned retention choice;
// other choices estimate release using the snapshot's protected age bands.
func storageReleasable(groups []storage.Group, retention, scanned int) int64 {
	var total int64
	for _, g := range groups {
		if retention == scanned {
			total += g.EligibleBytes
			continue
		}
		for i, bytes := range g.Ages {
			if i > retention {
				total += max(bytes-g.ProtectedAges[i], 0)
			}
		}
	}
	return total
}

// storageAges shows how much each retention choice would release: the bands
// older than the current choice take the colors of what can be cleaned.
func (a *App) storageAges(c *ui.Context) {
	s := &a.storage
	t := c.Theme()
	colors := storageColors(t.Dark)
	const plot = 174
	bands := storageAgeBands(s.snapshot.Groups, s.selected, s.scanned)
	var peak int64
	for _, band := range bands {
		peak = max(peak, band[0]+band[1]+band[2])
	}
	ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "文件年龄").FontSize(14).FontWeight(600).Grow(1)
		for _, v := range []struct {
			label string
			color ui.Color
		}{{"已选", colors.selected}, {"待选", colors.idle}, {"保留期内", colors.kept}} {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(2).Background(v.color)
				ui.Text(c, v.label).FontSize(11).TextColor(t.TextMuted)
			})
		}
		withTip(c, ui.Icon(c, glyph("info")).FontSize(13).TextColor(t.TextMuted), "按文件最后修改时间筛选，大小为文件逻辑大小。建议先退出对应 Agent；执行时会再次检查文件是否变化。")
	})
	ui.Column(c).Gap(0).Grow(1).Children(func() {
		ui.Row(c).Gap(6).Grow(1).MinHeight(plot + 22).AlignItems(ui.End).Label("按文件年龄的占用图表").Children(func() {
			for i, band := range bands {
				total := band[0] + band[1] + band[2]
				withTip(c, ui.Column(c.Key(storageAgeLabels[i])).Grow(1).Basis(0).MinWidth(0).Gap(4).AlignItems(ui.Center), storageAgeLabels[i]+" · "+storageSize(total)).Children(func() {
					number(c, storageSize(total)).FontSize(11.5).TextColor(t.TextMuted)
					h := float32(2)
					if peak > 0 && total > 0 {
						h = max(float32(float64(total)/float64(peak))*plot, 2)
					}
					ui.Box(c).Height(h).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
						w := min(r.W, float32(60))
						y := r.Y + r.H
						if total == 0 {
							p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: r.Y, W: w, H: 2}, t.Border, 1)
							return
						}
						for _, part := range []barPart{{band[2], colors.kept}, {band[1], colors.idle}, {band[0], colors.selected}} {
							if part.value <= 0 {
								continue
							}
							segment := float32(float64(part.value) / float64(total) * float64(r.H))
							y -= segment
							p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: y + 1, W: w, H: max(segment-1, 1)}, part.color, 2)
						}
					})
				})
				if i == s.scanned {
					ui.Column(c).Width(84).Shrink(0).AlignSelf(ui.Stretch).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "保留线").FontSize(11).FontWeight(600).Padding(3, 7).Radius(5).Background(token(t, "accent-bg")).Border(1, token(t, "accent")).TextColor(t.Text)
						ui.Box(c).Width(1.5).Grow(1).Background(token(t, "accent"))
					})
				}
			}
		})
		ui.Row(c).Gap(6).Padding(7, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			for i, label := range storageAgeLabels {
				ui.Text(c, label).Grow(1).Basis(0).MinWidth(0).TextAlign(ui.Center).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
				if i == s.scanned {
					ui.Text(c, fmt.Sprintf("%d 天", retentionDays[s.scanned])).Width(84).Shrink(0).TextAlign(ui.Center).FontSize(11).FontWeight(600).TextColor(t.Text)
				}
			}
		})
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(84).Shrink(0).Children(func() {
			ui.Text(c, "保留最近").FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "可释放").FontSize(11).TextColor(t.TextMuted)
		})
		tabs := ui.TabsBase(c.Key("storage-retention"), &s.retention, len(retentionDays))
		tabs.List.Grow(1).MinWidth(0).Gap(8).Label("清理保留时间").Disabled(s.scanning || a.mutating || a.sessions.loading).Children(func() {
			for i, days := range retentionDays {
				value := storageSize(storageReleasable(s.snapshot.Groups, i, s.scanned))
				if i != s.scanned {
					value = "约 " + value
				}
				// Lobe UI's selectable cards: a filled card, the chosen one ringed.
				tk := lobe.TokensOf(c)
				tab := tabs.Tab(i).Grow(1).Basis(0).MinWidth(0).Height(48).Column().Justify(ui.Center).Padding(5, 10).
					Radius(tk.BorderRadius).Background(tk.FillQuaternary).Border(1, tk.BorderSecondary).Cursor(ui.CursorPointer).
					Label(fmt.Sprintf("保留 %d 天，可释放 %s", days, value))
				switch {
				case i == s.retention:
					tab.Background(tk.BgContainer).Border(1, tk.Text)
				case tab.Hovered():
					tab.Background(tk.FillTertiary)
				}
				tab.Children(func() {
					ui.Text(c, fmt.Sprintf("%d 天", days)).FontSize(12).SingleLine()
					number(c, value).FontSize(12).FontWeight(600).SingleLine()
				})
			}
		})
		if tabs.List.Changed() {
			a.startStorageScan()
		}
	})
}

type storageSuggestion struct {
	groups                  []string
	title, note, icon, tone string
	bytes                   int64
	files                   int
	database                *storage.Database
}

func storageSuggestions(snap *storage.Snapshot) []storageSuggestion {
	var out []storageSuggestion
	safe := storageSuggestion{title: "缓存与日志", icon: "shield", tone: "ok"}
	var history []storage.Group
	for _, g := range snap.Groups {
		if g.Eligible <= 0 {
			continue
		}
		if g.History {
			history = append(history, g)
		} else {
			safe.groups = append(safe.groups, g.ID)
			safe.bytes += g.EligibleBytes
			safe.files += g.Eligible
		}
	}
	if len(safe.groups) > 0 {
		safe.note = fmt.Sprintf("可重新生成 · %d 个文件", safe.files)
		out = append(out, safe)
	}
	slices.SortStableFunc(history, func(a, b storage.Group) int { return cmp.Compare(b.EligibleBytes, a.EligibleBytes) })
	for _, g := range history[:min(2, len(history))] {
		out = append(out, storageSuggestion{groups: []string{g.ID}, title: g.Agent + " " + g.Name, note: fmt.Sprintf("影响历史恢复 · %d 个文件", g.Eligible), icon: "alert", tone: "warn", bytes: g.EligibleBytes, files: g.Eligible})
	}
	var best *storage.Database
	for i := range snap.Databases {
		d := &snap.Databases[i]
		if d.Error == "" && d.WALBytes+d.FreeBytes > 0 && (best == nil || d.WALBytes+d.FreeBytes > best.WALBytes+best.FreeBytes) {
			best = d
		}
	}
	if best != nil {
		out = append(out, storageSuggestion{title: best.Agent + " 数据库", note: "合并 WAL 或回收空闲页 " + storageSize(best.FreeBytes), icon: "database", tone: "phase-tool", database: best})
	}
	return out
}

var storageGlyphs = map[string]*ui.SVG{
	"shield":   ui.MustParseSVG([]byte(svgHead + `<path d="M12 3 4 6v6c0 4 4 7 8 9 4-2 8-5 8-9V6z"/><path d="m8 12 3 3 5-6"/></svg>`)),
	"alert":    ui.MustParseSVG([]byte(svgHead + `<path d="m12 3 10 18H2z"/><path d="M12 9v5m0 3v.1"/></svg>`)),
	"database": ui.MustParseSVG([]byte(svgHead + `<ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 4 16 4 16 0V5M4 12c0 4 16 4 16 0"/></svg>`)),
	"backup":   ui.MustParseSVG([]byte(svgHead + `<path d="M4 8h16v13H4zM3 3h18v5H3zM12 18V11m-3 3 3-3 3 3"/></svg>`)),
}

func (a *App) storageSuggestions(c *ui.Context, busy bool) {
	s := &a.storage
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "建议操作").Grow(1).FontSize(14).FontWeight(600)
		ui.Text(c, "先安全，后确认").FontSize(11).TextColor(token(t, "ink-3"))
	})
	suggestions := storageSuggestions(s.snapshot)
	if len(suggestions) == 0 {
		ui.Text(c, "暂无建议").FontSize(13).TextColor(t.TextMuted)
		return
	}
	ui.Column(c).Grow(1).Children(func() {
		for i, item := range suggestions {
			row := ui.Row(c.Key(fmt.Sprintf("suggestion:%d", i))).Grow(1).MinHeight(65).Gap(10).AlignItems(ui.Center)
			if i > 0 {
				row.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			row.Children(func() {
				var tone ui.Color
				if item.database != nil {
					tone = sessionColor(item.tone, t.Dark)
				} else {
					tone = token(t, item.tone)
				}
				ui.Icon(c, storageGlyphs[item.icon]).FontSize(16).TextColor(tone).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).Gap(5).Children(func() {
						withTip(c, ui.Text(c, item.title).Grow(1).MinWidth(0).FontSize(13).SingleLine(), item.title)
						value := storageSize(item.bytes)
						if item.database != nil {
							value = "WAL " + storageSize(item.database.WALBytes)
						}
						number(c, value).FontSize(12).FontWeight(600).SingleLine()
					})
					withTip(c, ui.Text(c, item.note).FontSize(11).TextColor(t.TextMuted).SingleLine(), item.note)
				})
				selected := len(item.groups) > 0
				for _, id := range item.groups {
					selected = selected && s.selected[id]
				}
				label := "加入清理"
				if selected {
					label = "✓ 已选"
				}
				if item.database != nil {
					label = "维护…"
				}
				button := outlineButton(c, fmt.Sprintf("suggestion-action:%d", i), label).Width(72).Padding(0).Disabled(busy).Label(item.title + "：" + label)
				if selected {
					button.Background(token(t, "selected")).TextColor(token(t, "accent-ink"))
				}
				if button.Clicked() {
					if item.database != nil {
						a.confirmDatabase(*item.database, false)
					} else {
						for _, id := range item.groups {
							s.selected[id] = !selected
						}
					}
				}
			})
		}
	})
}

func storageAgentDot(c *ui.Context, agent string) {
	ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(agentColor(storageAgentIDs[agent], c.Theme().Dark))
}

// storageRows ranks an Agent's categories by what can be released.
func storageRows(groups []storage.Group, agent string, showEmpty bool) []storage.Group {
	var rows []storage.Group
	for _, g := range groups {
		if (agent == "" || g.Agent == agent) && (showEmpty || g.Files > 0) {
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
	agents := []string{"", "Claude Code", "Codex"}
	emptyCounts := map[string]int{}
	totals := map[string]int64{}
	for _, g := range s.snapshot.Groups {
		peak = max(peak, g.Bytes)
		totals[""] += g.Bytes
		totals[g.Agent] += g.Bytes
		if g.Files == 0 {
			emptyCounts[""]++
			emptyCounts[g.Agent]++
		}
	}
	ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "清理项").FontSize(14).FontWeight(600)
		tabs := ui.TabsBase(c.Key("storage-agent"), &s.agentFilter, len(agents))
		segmentTrack(c, tabs.List).Label("清理项 Agent").Wrap().Children(func() {
			for i, agent := range agents {
				name := agent
				if name == "" {
					name = "全部"
				}
				tab := segmentTab(c, tabs.Tab(i), s.agentFilter == i).Label(name + " 文件占用 " + storageSize(totals[agent]))
				tab.Children(func() {
					if agent != "" {
						storageAgentDot(c, agent)
					}
					ui.Text(c, name).SingleLine()
					number(c, storageSize(totals[agent])).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
				})
			}
		})
		ui.Box(c).Grow(1)
		empty := emptyCounts[agents[s.agentFilter]]
		if empty > 0 {
			label := fmt.Sprintf("显示 %d 个空类别", empty)
			if s.showEmpty {
				label = "隐藏空类别"
			}
			if linkButton(c, "storage-empty", label).Clicked() {
				s.showEmpty = !s.showEmpty
			}
		}
	})
	ui.Column(c).Gap(0).Children(func() {
		ui.Row(c).Height(26).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, "类别").Grow(1).MinWidth(0).FontSize(11.5).TextColor(muted).Margin(0, 0, 0, 28)
			ui.Text(c, "Agent").Width(104).Shrink(0).FontSize(11.5).TextColor(muted)
			if !compact {
				ui.Text(c, "占比").Width(storageBarWidth).Shrink(0).FontSize(11.5).TextColor(muted)
			}
			for _, title := range storageColumns(compact) {
				w := float32(storageColumn)
				if title == "文件" {
					w = 76
				}
				ui.Text(c, title).Width(w).Shrink(0).TextAlign(ui.End).FontSize(11.5).TextColor(muted)
			}
			ui.Box(c).Width(storageOpenWidth).Shrink(0)
		})
		for _, g := range storageRows(s.snapshot.Groups, agents[s.agentFilter], s.showEmpty) {
			a.storageGroup(c, g, peak, busy, compact)
		}
	})
}

const (
	storageBarWidth  = 150
	storageColumn    = 84
	storageOpenWidth = 26
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
	ui.Row(c.Key(g.ID)).MinHeight(38).Padding(6, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		// Nothing to clean must not read as chosen, whatever is remembered.
		selected := s.selected[g.ID] && g.Eligible > 0
		if choice.Checkbox(c, &selected, "", choice.CheckboxProps{}).Label("选择 " + g.Agent + " " + g.Name).Disabled(busy || g.Eligible == 0).Changed() {
			s.selected[g.ID] = selected
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
				withTip(c, ui.Text(c, g.Name).FontSize(13).FontWeight(500).SingleLine(), g.Description+"\n"+g.Root)
				if g.History {
					ui.Text(c, "影响历史恢复").FontSize(11).TextColor(token(t, "warn-ink")).SingleLine()
				}
				if g.Protected > 0 {
					withTip(c, ui.Text(c, fmt.Sprintf("%d 个受保护", g.Protected)).FontSize(11).TextColor(muted).SingleLine(),
						fmt.Sprintf("%d 个文件（%s）属于保留期内仍活跃的会话，不会清理", g.Protected, storageSize(g.ProtectedBytes)))
				}
			})
		})
		ui.Row(c).Width(104).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			storageAgentDot(c, g.Agent)
			ui.Text(c, g.Agent).FontSize(12).TextColor(t.TextMuted).SingleLine()
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
		value := number(c, storageSize(g.EligibleBytes)).Width(storageColumn).Shrink(0).TextAlign(ui.End).FontSize(13).FontWeight(600)
		if g.Eligible == 0 {
			value.TextColor(t.TextMuted)
		}
		if !compact {
			withTip(c, number(c, fmt.Sprintf("%d / %d", g.Eligible, g.Files)).Width(76).Shrink(0).TextAlign(ui.End).FontSize(13).TextColor(t.TextMuted), "可清理 / 全部文件")
		}
		open := action.ActionIcon(c.Key("open:"+g.ID), glyph("reveal"), action.ActionIconProps{Title: g.Root, BlockSize: 26, GlyphSize: 14}).
			Width(storageOpenWidth).Label("查看 " + g.Agent + " " + g.Name).Disabled(g.Files == 0)
		if open.Clicked() {
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
			withTip(c, ui.Text(c, note).FontSize(12).TextColor(token(t, "ink-3")).SingleLine(), note)
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
	colors := storageColors(t.Dark)
	var total int64
	for _, d := range a.storage.snapshot.Databases {
		total += d.Bytes + d.WALBytes
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "数据库维护").Grow(1).FontSize(14).FontWeight(600)
		number(c, storageSize(total)).FontSize(13).FontWeight(600)
	})
	for _, agent := range storageAgents {
		found := false
		for _, d := range a.storage.snapshot.Databases {
			if d.Agent != agent {
				continue
			}
			found = true
			ui.Column(c.Key(d.Path)).Gap(10).Padding(4, 0, 10, 0).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					storageAgentDot(c, agent)
					ui.Text(c, agent+" 数据库").FontSize(13).SingleLine()
					withTip(c, ui.Text(c, filepath.Base(d.Path)).Grow(1).MinWidth(0).FontSize(12).TextColor(t.TextMuted).SingleLine(), d.Path)
				})
				storageBar(c, 8, d.Bytes+d.WALBytes, barPart{max(d.Bytes-d.FreeBytes, 0), colors.database}, barPart{d.FreeBytes, colors.kept}, barPart{d.WALBytes, colors.wal})
				ui.Row(c).Gap(8).Children(func() {
					for _, v := range []struct {
						label string
						bytes int64
						color ui.Color
					}{{"有效数据", max(d.Bytes-d.FreeBytes, 0), colors.database}, {"空闲页", d.FreeBytes, colors.kept}, {"WAL", d.WALBytes, colors.wal}} {
						ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(4).Children(func() { legendItem(c, v.color, v.label); number(c, storageSize(v.bytes)).FontSize(15).FontWeight(600) })
					}
				})
				if d.Error != "" {
					ui.Text(c, d.Error).FontSize(12).TextColor(t.Danger)
				}
				ui.Row(c).Wrap().Gap(6).Children(func() {
					if outlineButton(c, "vacuum:"+d.Path, "整理碎片…").Grow(1).Disabled(busy || d.Error != "").Clicked() {
						a.confirmDatabase(d, false)
					}
					if outlineButton(c, "checkpoint:"+d.Path, "合并 WAL…").Grow(1).Disabled(busy || d.Error != "").Clicked() {
						a.confirmDatabase(d, true)
					}
					if withTip(c, outlineButton(c, "reveal:"+d.Path, "查看"), d.Path).Clicked() {
						a.reveal(d.Path)
					}
				})
			})
		}
		if !found {
			ui.Row(c).Gap(8).Padding(4, 0).AlignItems(ui.Start).Children(func() {
				storageAgentDot(c, agent)
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Text(c, agent+" 数据库").FontSize(13)
					message := "没有发现本地 SQLite 数据库。"
					if agent == "Claude Code" {
						message = "未发现 SQLite 数据库。Claude Code 的会话通常以 JSONL 保存，可在上方清理历史文件。"
					}
					ui.Text(c, message).FontSize(12).TextColor(token(t, "ink-3"))
				})
			})
		}
	}
	ui.Box(c).Grow(1)
	withTip(c, ui.Row(c).Gap(6).AlignItems(ui.Center), "整理可能需要约数据库大小两倍的临时空间。").Children(func() {
		ui.Icon(c, storageGlyphs["alert"]).FontSize(13).TextColor(token(t, "warn-ink"))
		ui.Text(c, "先退出对应 Agent；维护没有撤销备份。").Grow(1).MinWidth(0).FontSize(11).TextColor(token(t, "warn-ink"))
	})
}

func (a *App) storageBackups(c *ui.Context, busy bool) {
	s := &a.storage
	t := c.Theme()
	empty := len(s.backups) == 0
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "清理备份").FontSize(14).FontWeight(600).Grow(1)
		if !empty {
			if linkButton(c, "backup-directory", "查看目录 →").Clicked() {
				a.reveal(a.undoRoot)
			}
		}
	})
	ui.Row(c).Gap(12).Padding(12, 0).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(36, 36).Radius(7).Background(token(t, "ok-bg")).AlignItems(ui.Center).Justify(ui.Center).Children(func() { ui.Icon(c, storageGlyphs["backup"]).FontSize(20).TextColor(token(t, "ok")) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				number(c, storageSize(s.backupBytes)).FontSize(20).FontWeight(600)
				ui.Text(c, fmt.Sprintf("%d 个文件", len(s.backups))).FontSize(12).TextColor(t.TextMuted)
			})
			note := "清理前自动压缩备份，可按类别恢复。"
			if empty {
				note = "还没有备份；每次清理前自动压缩备份"
			}
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		})
	})
	for _, g := range s.backupGroups {
		a.storageBackupGroup(c, g, busy)
	}
	ui.Row(c).Gap(6).Children(func() {
		if outlineButton(c, "storage-restore", "恢复全部…").Grow(1).Basis(0).Label("恢复全部清理备份").Disabled(busy || empty).Clicked() {
			a.confirmRestoreBackups(s.backups)
		}
		if outlineButton(c, "storage-purge", "清空全部…").Grow(1).Basis(0).Label("清空全部清理备份").TextColor(t.Danger).Disabled(busy || empty).Clicked() {
			a.confirmPurgeBackups(s.backups)
		}
	})
	if len(s.expired) > 0 && outlineButton(c, "storage-expire", fmt.Sprintf("清除 %d 天前…", backupExpiryDays)).Label(fmt.Sprintf("清除 %d 天前的备份", backupExpiryDays)).TextColor(t.Danger).Disabled(busy).Clicked() {
		a.confirmPurgeBackups(s.expired)
	}
}

func (a *App) storageBackupGroup(c *ui.Context, g backupGroup, busy bool) {
	t := c.Theme()
	ui.Row(c.Key(g.label)).Padding(8, 0).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			withTip(c, ui.Text(c, g.label).FontSize(13).SingleLine(), g.label)
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
