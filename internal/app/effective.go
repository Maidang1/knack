package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"skill-manager/internal/skill"
)

const effectivePage = "effective"

// effectiveState is the choice behind the effective view and the view
// computed from it. The view is rebuilt when the scan, the agent or the
// working directory changes; it reads only the project folder.
type effectiveState struct {
	agent   string // root ID; "" picks the first agent
	workDir string
	matrix  int // 0 what loads, 1 every layer side by side

	snap  *skill.Snapshot
	key   string
	view  *skill.EffectiveView
	err   string
	ready bool
}

// SetWorkDir sets the folder the effective view starts in. A folder that
// says nothing about a project (the filesystem root, home) is ignored.
func (a *App) SetWorkDir(dir string) {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "/" || dir == a.home {
		return
	}
	a.eff.workDir = dir
}

// effAgentRoot is the agent whose view is shown.
func (a *App) effAgentRoot() (skill.Root, bool) {
	agents := a.roots(skill.RoleAgent)
	if len(agents) == 0 {
		return skill.Root{}, false
	}
	for _, r := range agents {
		if r.ID == a.eff.agent {
			return r, true
		}
	}
	for _, r := range agents {
		if r.ID == "claude" {
			return r, true
		}
	}
	return agents[0], true
}

// effective returns the view for the chosen agent and folder.
func (a *App) effective() (*skill.EffectiveView, string) {
	agent, ok := a.effAgentRoot()
	if !ok || a.snap == nil {
		return nil, ""
	}
	e := &a.eff
	key := agent.ID + "|" + e.workDir
	if e.ready && e.snap == a.snap && e.key == key {
		return e.view, e.err
	}
	e.snap, e.key, e.ready = a.snap, key, true
	e.view, e.err = nil, ""
	v, err := skill.BuildEffective(a.snap, a.home, agent, e.workDir)
	if err != nil {
		e.err = err.Error()
		return nil, e.err
	}
	e.view = v
	return v, ""
}

func (a *App) beginPickWorkDir() {
	run := func() {
		path, err := a.chooseDirWith("选择工作目录", "选择 Agent 启动时所在的项目目录")
		a.post(func() {
			if err != nil {
				a.notice = err.Error()
				return
			}
			if strings.TrimSpace(path) != "" {
				a.eff.workDir = filepath.Clean(path)
			}
		})
	}
	if a.win == nil {
		run()
		return
	}
	go run()
}

// effectiveToolbar is the part of the title bar only this page has: which
// agent, and which folder it is started in.
func (a *App) effectiveToolbar(c *ui.Context) {
	t := c.Theme()
	if agent, ok := a.effAgentRoot(); ok {
		ui.PopoverMenuButton(c, agent.Name, func(m *ui.Menu) {
			for _, r := range a.roots(skill.RoleAgent) {
				if m.Item(r.Name).Checked(r.ID == agent.ID).Chosen() {
					a.eff.agent = r.ID
				}
			}
		}).Label("Agent："+agent.Name).Tooltip("选择 Agent").Height(toolbarControlHeight).
			Padding(0, 8).Shrink(0)
	}
	label := "选择工作目录…"
	name, tip := label, "选择 Agent 启动时所在的工作目录"
	if a.eff.workDir != "" {
		label = shortPath(a.home, a.eff.workDir)
		name, tip = filepath.Base(a.eff.workDir), a.eff.workDir
	}
	dir := ui.Button(c, "").Shrink(1).MinWidth(0).MaxWidth(180).Height(toolbarControlHeight).
		Padding(0, 8).Label("工作目录：" + label).Tooltip(tip)
	dir.Children(func() {
		ui.Icon(c, glyph("folder")).FontSize(14).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, name).SingleLine().MinWidth(0).Shrink(1)
	})
	if dir.Clicked() {
		a.beginPickWorkDir()
		c.Invalidate()
	}
}

// effectivePane is the page: the list of what loads and why, or the matrix
// of every layer.
func (a *App) effectivePane(c *ui.Context) {
	t := c.Theme()
	v, errText := a.effective()
	width, _ := c.Size()
	a.listW = min(a.listW, max(180, width-a.sidebarWidth()-320-2))
	// Keep the tab list in the same place in the view tree so switching
	// between list and matrix preserves keyboard focus.
	a.effectiveHeader(c, v)
	if v == nil {
		ui.Box(c).MaxWidth(420).Children(func() { a.listSearch(c) })
		ui.Column(c).Grow(1).Gap(6).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Icon(c, glyph("empty")).FontSize(28).TextColor(t.TextMuted)
			msg := "还没有扫描结果。"
			switch {
			case errText != "":
				msg = errText
			case a.snap != nil:
				msg = "没有找到 Agent 的 skill 目录。"
			case a.scanning:
				msg = "正在扫描。"
			}
			ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
		})
		return
	}
	if a.eff.matrix == 1 {
		ui.Box(c).MaxWidth(420).Children(func() { a.listSearch(c) })
		a.matrixPane(c, v)
		return
	}
	ps := a.state(effectivePage)
	skills := a.effectiveRows(v)
	clamp(&ps.sel, len(skills))
	ui.Split(c, &a.listW, func() {
		ui.Column(c).Fill().Children(func() {
			a.listSearch(c)
			ps.list.Key = func(i int) any {
				if i < 0 || i >= len(skills) {
					return i
				}
				return skills[i].Name
			}
			ps.list.Label = func(i int) string {
				if i < 0 || i >= len(skills) {
					return ""
				}
				return skills[i].Name
			}
			ps.list.Selected = &ps.sel
			list := ui.List(c, &ps.list, len(skills), func(i int) {
				s := skills[i]
				w := s.Winner()
				a.paintRow(c, row{
					key:      s.Name,
					name:     s.Name,
					badge:    w.Layer.Label(),
					glyph:    relationGlyph(w.Entry.Relation),
					subtitle: a.layerPath(v, w.Entry.Path),
				}, i == ps.sel)
			}).Grow(1).Padding(4, 8).Gap(2).Children(func() {
				if len(skills) == 0 {
					ui.Column(c).Padding(40, 12).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, glyph("empty")).FontSize(26).TextColor(t.TextMuted)
						msg := "这里是空的。"
						if strings.TrimSpace(a.query) != "" {
							msg = "没有匹配的 skill。"
						}
						ui.Text(c, msg).FontSize(12).TextColor(t.TextMuted)
					})
				}
			})
			if list.Changed() {
				c.Invalidate()
			}
		})
	}, func() {
		ui.Scroll(c).Fill().Children(func() {
			if ps.sel < 0 || ps.sel >= len(skills) {
				ui.Column(c).Padding(48, 24).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph("nested")).FontSize(28).TextColor(t.TextMuted)
					ui.Text(c, "在左边选择一个 skill。").FontSize(12).TextColor(t.TextMuted)
				})
				return
			}
			a.effectiveDetail(c, v, skills[ps.sel])
		})
	}).Grow(1)
}

func (a *App) effectiveHeader(c *ui.Context, v *skill.EffectiveView) {
	label := "生效"
	if v != nil {
		label = fmt.Sprintf("%d 项生效", len(v.Skills))
	}
	ui.Row(c).Width(a.listW).Shrink(0).Padding(6, 16, 4, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		viewTabs(c, &a.eff.matrix, label, "全貌")
		ui.Box(c).Grow(1)
		if v != nil && v.Overridden() > 0 {
			ui.Textf(c, "%d 项被覆盖", v.Overridden()).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine()
		}
	})
}

// effectiveRows are the skills the search leaves.
func (a *App) effectiveRows(v *skill.EffectiveView) []skill.Effective {
	var out []skill.Effective
	for _, s := range v.Skills {
		if matchQuery(a.query, s.Name, s.Description) {
			out = append(out, s)
		}
	}
	return out
}

// layerPath is a folder shown short: inside the working directory it is
// relative to it, otherwise ~-relative.
func (a *App) layerPath(v *skill.EffectiveView, path string) string {
	if v.WorkDir != "" {
		if rel, err := filepath.Rel(v.WorkDir, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return shortPath(a.home, path)
}

func (a *App) effectiveDetail(c *ui.Context, v *skill.EffectiveView, s skill.Effective) {
	t := c.Theme()
	w := s.Winner()
	ui.Column(c).Padding(24, 28).Gap(14).MaxWidth(680).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			tile(c, lookOf(a.home, skill.Root{ID: w.Entry.RootID, Name: w.Entry.RootName, Path: w.Entry.RootPath, Role: w.Entry.RootRole}), 28)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, s.Name).FontSize(17).FontWeight(600).SingleLine()
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "生效").FontSize(12).TextColor(t.Accent)
					ui.Text(c, w.Layer.Label()+"级").FontSize(12).TextColor(t.TextMuted)
					if n := len(s.Shadowed()); n > 0 {
						ui.Textf(c, "覆盖 %d 个同名", n).FontSize(12).TextColor(t.TextMuted)
					}
				})
			})
		})
		if s.Description != "" {
			ui.Text(c, s.Description).FontSize(13).LineHeight(1.5).MaxLines(6)
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "为什么是这一个").FontSize(13).FontWeight(600)
			ui.Box(c).Grow(1)
			ui.Text(c, "项目 → 全局 → 共享池 → 内置").FontSize(12).TextColor(t.TextMuted)
		})
		ui.Column(c).Gap(2).Children(func() {
			for i, l := range v.Layers {
				src, has := s.In(l.Layer)
				a.ladderRow(c, v, i+1, l, src, has, has && src.Layer == w.Layer)
			}
		})

		if shadowed := s.Shadowed(); len(shadowed) > 0 {
			ui.Textf(c, "%d 个同名副本仍占用磁盘", len(shadowed)).FontSize(13).FontWeight(600)
			ui.Column(c).Gap(12).Children(func() {
				for _, sh := range shadowed {
					a.shadowedCopy(c, v, w.Entry, sh)
				}
			})
		}
		ui.Text(c, "换个工作目录，生效结果会重新计算。").FontSize(11).TextColor(t.TextMuted)
	})
}

// ladderRow is one step of the lookup: its place in the order, where it
// looks, and whether it won.
func (a *App) ladderRow(c *ui.Context, v *skill.EffectiveView, n int, l skill.LayerInfo, src skill.Source, has, won bool) {
	t := c.Theme()
	row := ui.Row(c).Padding(7, 10).Gap(10).AlignItems(ui.Center).Radius(6)
	if won {
		row.Background(fade(t.Accent, 26))
	}
	row.Children(func() {
		chip := ui.Box(c).Size(18, 18).Shrink(0).Radius(9).AlignItems(ui.Center).Justify(ui.Center)
		if won {
			chip.Background(t.Accent)
		} else {
			chip.Background(t.Text.Alpha(0.08))
		}
		chip.Children(func() {
			txt := ui.Text(c, fmt.Sprint(n)).FontSize(11).FontWeight(600)
			if won {
				txt.TextColor(t.AccentText)
			} else {
				txt.TextColor(t.TextMuted)
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			title := ui.Text(c, l.Layer.Label()).FontSize(13).SingleLine()
			if won {
				title.FontWeight(600)
			}
			path := "没有这个 skill"
			switch {
			case has:
				path = a.layerPath(v, src.Entry.Path)
			case l.Path == "" && l.Layer == skill.LayerProject:
				path = "未选择工作目录，或这个 Agent 没有项目级目录"
			case l.Path == "" && l.Layer == skill.LayerBuiltin:
				path = "这个 Agent 没有内置 skill"
			}
			ui.Text(c, path).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		switch {
		case won:
			ui.Text(c, "生效").FontSize(12).TextColor(t.Accent).Shrink(0)
		case has:
			ui.Text(c, "被覆盖").FontSize(12).TextColor(t.TextMuted).Shrink(0)
		default:
			ui.Text(c, "—").FontSize(12).TextColor(t.TextMuted).Shrink(0)
		}
	})
}

// shadowedCopy is a copy that is on disk but not loaded. It can be compared
// and turned into a link; it can not be removed from here — deleting
// stays in the problems page.
func (a *App) shadowedCopy(c *ui.Context, v *skill.EffectiveView, winner skill.Entry, sh skill.Source) {
	t := c.Theme()
	e := sh.Entry
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, sh.Layer.Label()).FontSize(13)
		ui.Text(c, a.layerPath(v, e.Path)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		if note := skill.AgeNote(winner, e); note != "" {
			ui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		}
		readOnly := e.System || e.RootRole == skill.RoleBuiltin
		if readOnly {
			return
		}
		fixes := a.fixesFor(e)
		if len(fixes) == 0 && e.Relation != skill.RelCopy {
			return
		}
		ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			for _, f := range fixes {
				a.fixButton(c, f, false)
			}
		})
		a.compareButton(c, e)
	})
}

// matrixPane draws every layer side by side: who wins, who is shadowed,
// and who is missing everywhere.
func (a *App) matrixPane(c *ui.Context, v *skill.EffectiveView) {
	t := c.Theme()
	skills := a.effectiveRows(v)
	var missing []string
	for _, m := range v.Missing {
		if matchQuery(a.query, m) {
			missing = append(missing, m)
		}
	}
	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(16, 20).Gap(14).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				for i, l := range v.Layers {
					ui.Row(c).Grow(1).Basis(0).Padding(8, 12).Gap(8).AlignItems(ui.Center).Radius(8).
						Background(t.Text.Alpha(0.04)).Children(func() {
						chip := ui.Box(c).Size(18, 18).Shrink(0).Radius(9).AlignItems(ui.Center).Justify(ui.Center).
							Background(t.Text.Alpha(0.08))
						if i == 0 {
							chip.Background(t.Accent)
						}
						chip.Children(func() {
							txt := ui.Text(c, fmt.Sprint(i+1)).FontSize(11).FontWeight(600).TextColor(t.TextMuted)
							if i == 0 {
								txt.TextColor(t.AccentText)
							}
						})
						ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
							ui.Row(c).Gap(6).Children(func() {
								ui.Text(c, l.Layer.Label()).FontSize(13).FontWeight(600).SingleLine()
								ui.Box(c).Grow(1)
								ui.Textf(c, "%d 项", l.Count).FontSize(11).TextColor(t.TextMuted)
							})
							p := l.Path
							if p == "" {
								p = "—"
							} else {
								p = a.layerPath(v, p)
							}
							ui.Text(c, p).FontSize(11).TextColor(t.TextMuted).SingleLine()
						})
					})
				}
			})
			box := ui.Column(c)
			box.Dividers(1, t.Border)
			box.Children(func() {
				ui.Row(c).Padding(6, 10).Gap(8).Children(func() {
					head := func(s string, grow float32) {
						ui.Text(c, s).FontSize(11).TextColor(t.TextMuted).Grow(grow).Basis(0).SingleLine()
					}
					head("Skill", 2.4)
					for _, l := range v.Layers {
						head(l.Layer.Label(), 1)
					}
					head("生效来源", 1.2)
				})
				for _, s := range skills {
					a.matrixRow(c, v, s)
				}
				for _, m := range missing {
					ui.Row(c).Padding(7, 10).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, m).FontSize(13).TextColor(t.TextMuted).Grow(2.4).Basis(0).SingleLine()
						for range v.Layers {
							ui.Text(c, "—").FontSize(12).TextColor(t.TextMuted).Grow(1).Basis(0)
						}
						ui.Text(c, "未安装").FontSize(12).TextColor(t.Danger).Grow(1.2).Basis(0)
					})
				}
			})
			ui.Text(c, "同名只取最靠前的一层，其余版本仍在磁盘上。").FontSize(11).TextColor(t.TextMuted)
		})
	})
}

func (a *App) matrixRow(c *ui.Context, v *skill.EffectiveView, s skill.Effective) {
	t := c.Theme()
	w := s.Winner()
	ui.Row(c.Key("m-"+s.Name)).Padding(7, 10).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(2.4).Basis(0).MinWidth(0).Children(func() {
			ui.Text(c, s.Name).FontSize(13).SingleLine()
			if n := len(s.Shadowed()); n > 0 {
				ui.Textf(c, "%d 个副本", n).FontSize(11).TextColor(t.TextMuted).Shrink(0)
			}
		})
		for _, l := range v.Layers {
			cell := ui.Row(c).Gap(5).AlignItems(ui.Center).Grow(1).Basis(0)
			_, has := s.In(l.Layer)
			cell.Children(func() {
				switch {
				case has && l.Layer == w.Layer:
					ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(t.Accent)
					ui.Text(c, "生效").FontSize(12).FontWeight(600).TextColor(t.Accent)
				case has:
					ui.Box(c).Size(6, 6).Shrink(0).Radius(3).Background(t.TextMuted)
					ui.Text(c, "被覆盖").FontSize(12).TextColor(t.TextMuted)
				default:
					ui.Text(c, "—").FontSize(12).TextColor(t.TextMuted)
				}
			})
		}
		ui.Text(c, w.Layer.Label()).FontSize(12).TextColor(t.TextMuted).Grow(1.2).Basis(0)
	})
}
