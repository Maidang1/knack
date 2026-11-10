package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/field"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

	"knack/internal/session"
	"knack/internal/skill"
)

const effectivePage = "effective"

// effectiveState is the choice behind the effective view and the view
// computed from it. The view is rebuilt when the scan, the agent or the
// working directory changes; it reads only the project folder.
type effectiveState struct {
	agent   string // root ID; "" picks the first agent
	workDir string
	filter  int    // 0 every skill, 1 those with a shadowed copy, 2 those in one layer
	chosen  string // the skill whose lookup is shown

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
	muted := token(t, "ink-3")
	picker := func(b ui.Element, label, tip string) ui.Element {
		return withTip(c, action.StyleButton(c, b, "", action.ButtonProps{Size: lobe.Small}).
			Height(28).Gap(6).Label(label), tip).Shrink(0)
	}
	if agent, ok := a.effAgentRoot(); ok {
		a.dropdown(c, "eff-agent", func(m *menu) {
			for _, r := range a.roots(skill.RoleAgent) {
				if m.Item(r.Name).Checked(r.ID == agent.ID).Chosen() {
					a.eff.agent, a.eff.chosen = r.ID, ""
				}
			}
		}, func(b ui.Element) {
			picker(b, "Agent："+agent.Name, "选择 Agent").Children(func() {
				ui.Text(c, "Agent").FontSize(12).TextColor(muted)
				ui.Box(c).Size(7, 7).Radius(3.5).Background(agentColor(session.Agent(agentKey(a.home, agent.Path)), t.Dark)).Shrink(0)
				ui.Text(c, agent.Name).FontSize(13).FontWeight(500).SingleLine()
				ui.Icon(c, glyph("chevron-down")).FontSize(13).TextColor(muted)
			})
		})
	}
	label := "选择工作目录…"
	name, tip := label, "选择 Agent 启动时所在的工作目录"
	if a.eff.workDir != "" {
		label = shortPath(a.home, a.eff.workDir)
		name, tip = label, a.eff.workDir
	}
	dir := picker(ui.ButtonBase(c.Key("eff-dir")), "工作目录："+label, tip).Shrink(1).MinWidth(0).MaxWidth(180)
	dir.Children(func() {
		ui.Text(c, "工作目录").FontSize(12).TextColor(muted).Shrink(0)
		ui.Text(c, name).Font(monoFont).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
	})
	if dir.Clicked() {
		a.beginPickWorkDir()
		c.Invalidate()
	}
}

// effectiveFilters are the chips over the matrix.
var effectiveFilters = []string{"全部", "存在覆盖", "仅一层"}

// effectivePane is the page: what the Agent loads started in the folder,
// every layer side by side, and the lookup for the chosen skill.
func (a *App) effectivePane(c *ui.Context) {
	v, errText := a.effective()
	if v == nil {
		msg := "还没有扫描结果。"
		switch {
		case errText != "":
			msg = errText
		case a.snap != nil:
			msg = "没有找到 Agent 的 skill 目录。"
		case a.scanning:
			msg = "正在扫描。"
		}
		emptyState(c, "生效视图", "empty", "", msg, nil)
		return
	}
	skills := a.effectiveRows(v)
	chosen, ok := effectiveChosen(skills, a.eff.chosen)
	ui.Scroll(c).Fill().Label("生效视图内容").Children(func() {
		ui.Column(c).Padding(26, 32, 36, 32).Gap(18).Children(func() {
			a.effectiveHeader(c, v)
			a.matrixPane(c, v, skills, chosen)
			if ok {
				a.effectiveDetail(c, v, chosen)
			}
		})
	})
}

// effectiveChosen is the chosen skill if the filter keeps it, else the
// first one shown.
func effectiveChosen(skills []skill.Effective, name string) (skill.Effective, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	if len(skills) == 0 {
		return skill.Effective{}, false
	}
	return skills[0], true
}

func (a *App) effectiveHeader(c *ui.Context, v *skill.EffectiveView) {
	t := c.Theme()
	where := "其他目录"
	if v.WorkDir != "" {
		where = filepath.Base(v.WorkDir)
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Textf(c, "在 %s 里启动 %s 时，会加载 %d 个技能", where, v.Agent.Name, len(v.Skills)).FontSize(20).FontWeight(600).MaxLines(2)
		note := "同名技能按「项目 → 全局 → 共享池 → 内置」取第一个"
		if n := v.Overridden(); n > 0 {
			note += fmt.Sprintf("；%d 个技能被更高层覆盖", n)
		}
		ui.Text(c, note+"。实际加载以 Agent 自身规则为准。").FontSize(13).TextColor(t.TextMuted).MaxLines(2)
	})
	counts := []int{len(v.Skills), v.Overridden(), len(v.Skills) - v.Overridden()}
	ui.Row(c).Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
		chipTabs(c, "生效筛选", &a.eff.filter, effectiveFilters, counts)
		ui.Spacer(c)
		searchField(c, &a.query, "搜索", 220, field.SearchBarProps{Placeholder: "搜索技能"})
	})
}

// effectiveRows are the skills the filter and the search leave.
func (a *App) effectiveRows(v *skill.EffectiveView) []skill.Effective {
	var out []skill.Effective
	for _, s := range v.Skills {
		switch {
		case a.eff.filter == 1 && len(s.Sources) < 2, a.eff.filter == 2 && len(s.Sources) > 1:
			continue
		}
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
			return "./" + rel
		}
	}
	return shortPath(a.home, path)
}

func layerGlyph(l skill.Layer) string {
	switch l {
	case skill.LayerProject:
		return "folder"
	case skill.LayerGlobal:
		return "user"
	case skill.LayerPool:
		return "package"
	}
	return "lock"
}

// layerWhere is where a layer looks, or why it does not.
func (a *App) layerWhere(v *skill.EffectiveView, l skill.LayerInfo) string {
	switch {
	case l.Path != "":
		return a.layerPath(v, l.Path)
	case l.Layer == skill.LayerProject:
		return "未选择工作目录"
	case l.Layer == skill.LayerBuiltin:
		return "没有内置 skill"
	}
	return "—"
}

// effectiveNote says in a few words how a skill's copies stack up.
func effectiveNote(s skill.Effective) string {
	shadowed := s.Shadowed()
	if len(shadowed) == 0 {
		if s.Description != "" {
			return oneLine(s.Description)
		}
		return "只在" + s.Winner().Layer.Label() + "一层"
	}
	names := make([]string, len(shadowed))
	for i, sh := range shadowed {
		names[i] = sh.Layer.Label()
	}
	return s.Winner().Layer.Label() + "版本覆盖" + strings.Join(names, "、")
}

const (
	effNameGrow  = 1.5
	effLayerGrow = 1
)

// matrixPane draws every layer side by side: which copy each skill loads
// and which it shadows. A row chooses the skill whose lookup is shown.
func (a *App) matrixPane(c *ui.Context, v *skill.EffectiveView, skills []skill.Effective, chosen skill.Effective) {
	t := c.Theme()
	muted := token(t, "ink-3")
	var missing []string
	for _, m := range v.Missing {
		if matchQuery(a.query, m) {
			missing = append(missing, m)
		}
	}
	ui.Column(c).Children(func() {
		ui.Row(c).Padding(0, 0, 10, 0).Gap(12).AlignItems(ui.End).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, "技能").FontSize(12).TextColor(muted).Grow(effNameGrow).Basis(0).MinWidth(0).Padding(0, 0, 0, 12)
			for i, l := range v.Layers {
				ui.Column(c).Grow(effLayerGrow).Basis(0).MinWidth(0).Gap(3).Children(func() {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						number(c, fmt.Sprint(i+1)).FontSize(11).TextColor(muted)
						ui.Icon(c, glyph(layerGlyph(l.Layer))).FontSize(13).TextColor(t.TextMuted)
						ui.Text(c, l.Layer.Label()).FontSize(13).FontWeight(600).SingleLine()
					})
					where := withTip(c, ui.Text(c, a.layerWhere(v, l)).FontSize(11).TextColor(muted).SingleLine(), l.Path)
					if l.Path != "" {
						where.Font(monoFont)
					}
				})
			}
		})
		ui.Column(c).FocusGroup(ui.Vertical).Label("生效矩阵").Children(func() {
			for _, s := range skills {
				a.matrixRow(c, v, s, s.Name == chosen.Name)
			}
		})
		for _, m := range missing {
			ui.Row(c).Padding(10, 0, 10, 12).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Column(c).Grow(effNameGrow).Basis(0).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, m).FontSize(13).TextColor(t.TextMuted).SingleLine()
					ui.Text(c, "被其他技能依赖，但没有一层有它").FontSize(11.5).TextColor(muted).SingleLine()
				})
				ui.Text(c, "未安装").FontSize(13).TextColor(t.Danger).Grow(effLayerGrow * float32(len(v.Layers))).Basis(0)
			})
		}
		if len(skills) == 0 && len(missing) == 0 {
			msg := "这里是空的。"
			if strings.TrimSpace(a.query) != "" {
				msg = "没有匹配的 skill。"
			}
			ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted).Padding(24, 12)
		}
	})
	note := "同名只取最靠前的一层，其余版本仍在磁盘上。"
	switch a.eff.filter {
	case 1:
		note = fmt.Sprintf("其余 %d 个技能只存在于一层，切换到「全部」查看。", len(v.Skills)-v.Overridden())
	case 2:
		note = fmt.Sprintf("另有 %d 个技能存在覆盖，切换到「存在覆盖」查看。", v.Overridden())
	}
	ui.Text(c, note).FontSize(12).TextColor(muted).Margin(-8, 0, 0, 0)
}

func (a *App) matrixRow(c *ui.Context, v *skill.EffectiveView, s skill.Effective, chosen bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	w := s.Winner()
	b := ui.ButtonBase(c.Key("m-"+s.Name)).Label(s.Name).Padding(10, 0).Gap(12).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	switch {
	case chosen:
		b.Background(token(t, "selected"))
	case b.Hovered():
		b.Background(t.SurfaceHover.Alpha(0.5))
	}
	if b.Focused() && a.eff.chosen != s.Name {
		a.eff.chosen = s.Name
		c.Invalidate()
	}
	if b.Clicked() {
		a.eff.chosen = s.Name
		c.Invalidate()
	}
	b.Children(func() {
		ui.Column(c).Grow(effNameGrow).Basis(0).MinWidth(0).Gap(3).Padding(0, 0, 0, 12).Children(func() {
			withTip(c, ui.Text(c, s.Name).FontSize(13).FontWeight(500).SingleLine(), s.Name)
			ui.Text(c, effectiveNote(s)).FontSize(11.5).TextColor(muted).SingleLine()
		})
		for _, l := range v.Layers {
			ui.Row(c).Grow(effLayerGrow).Basis(0).MinWidth(0).AlignItems(ui.Center).Children(func() {
				_, has := s.In(l.Layer)
				switch {
				case has && l.Layer == w.Layer:
					// A table cell says its state in words, as Finder's
					// columns do: the layer that wins is ink with a tick,
					// the ones it hides are gray.
					ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Label(l.Layer.Label() + "：生效").Children(func() {
						ui.Icon(c, glyph("tick")).FontSize(13).TextColor(t.Text)
						ui.Text(c, "生效").FontSize(13).FontWeight(600)
					})
				case has:
					ui.Text(c, "被覆盖").FontSize(13).TextColor(muted)
				default:
					ui.Text(c, "—").FontSize(13).TextColor(muted)
				}
			})
		}
	})
}

// effectiveDetail is the lookup for one skill: each layer in order, which
// copy it found and what that meant, then what the shadowed copies cost.
func (a *App) effectiveDetail(c *ui.Context, v *skill.EffectiveView, s skill.Effective) {
	t := c.Theme()
	muted := token(t, "ink-3")
	w := s.Winner()
	width, _ := c.Size()
	narrow := width < 760
	ui.Column(c).Padding(18, 20).Gap(14).Radius(10).Background(t.Background).Border(1, t.Border).Label(s.Name + " 的查找路径").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, s.Name+" 的查找路径").FontSize(15).FontWeight(600).MinWidth(0).SingleLine()
		})
		ui.Row(c).Gap(6).AlignItems(ui.Stretch).Wrap().Children(func() {
			for i, l := range v.Layers {
				if i > 0 && !narrow {
					ui.Icon(c, glyph("chevron-right")).FontSize(13).TextColor(muted).AlignSelf(ui.Center).Shrink(0)
				}
				src, has := s.In(l.Layer)
				a.lookupCard(c, v, i+1, l, src, has, has && src.Layer == w.Layer, narrow)
			}
		})
		if shadowed := s.Shadowed(); len(shadowed) > 0 {
			ui.Column(c).Gap(12).Padding(14, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Textf(c, "%d 个同名副本仍占用磁盘", len(shadowed)).FontSize(13).FontWeight(600)
				for _, sh := range shadowed {
					a.shadowedCopy(c, v, w.Entry, sh)
				}
			})
		}
	})
	ui.Text(c, "换个工作目录，生效结果会重新计算。").FontSize(11.5).TextColor(muted)
}

// lookupCard is one step of the lookup: its place in the order, where it
// looked, and what it found.
func (a *App) lookupCard(c *ui.Context, v *skill.EffectiveView, n int, l skill.LayerInfo, src skill.Source, has, won, narrow bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	path, status, icon := a.layerWhere(v, l), "不存在", "dashed"
	statusColor, iconColor := muted, muted
	switch {
	case won:
		path, status, icon = a.layerPath(v, src.Entry.Path), "加载这一份", "check"
		statusColor, iconColor = t.Success, t.Success
	case has:
		path, status, icon = a.layerPath(v, src.Entry.Path), "同名，跳过", "skip"
		statusColor, iconColor = t.TextMuted, t.TextMuted
	case l.Path != "":
		path = "—"
	}
	box := ui.Column(c).Grow(1).Basis(0).MinWidth(140).Padding(12, 14).Gap(6).Radius(8).Background(t.Surface).
		Label(fmt.Sprintf("%d %s：%s", n, l.Layer.Label(), status))
	if narrow {
		box.Basis(160)
	}
	if won {
		box.Border(1.5, t.Accent)
	} else {
		box.Border(1, t.Border)
	}
	if !has {
		box.Opacity(0.7)
	}
	box.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			number(c, fmt.Sprint(n)).FontSize(11).TextColor(muted)
			ui.Text(c, l.Layer.Label()).FontSize(13).FontWeight(600).Grow(1).SingleLine()
			ui.Icon(c, glyph(icon)).FontSize(15).TextColor(iconColor).Shrink(0)
		})
		where := withTip(c, ui.Text(c, path).FontSize(11.5).TextColor(t.TextMuted).SingleLine(), path)
		if has || l.Path != "" {
			where.Font(monoFont)
		}
		ui.Text(c, status).FontSize(12).TextColor(statusColor).SingleLine()
	})
}

// shadowedCopy is a copy that is on disk but not loaded. It can be compared
// and turned into a link; it can not be removed from here — deleting
// stays in the problems page.
func (a *App) shadowedCopy(c *ui.Context, v *skill.EffectiveView, winner skill.Entry, sh skill.Source) {
	t := c.Theme()
	e := sh.Entry
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, sh.Layer.Label()).FontSize(13).FontWeight(500).Shrink(0)
			ui.Text(c, a.layerPath(v, e.Path)).Font(monoFont).FontSize(11.5).TextColor(t.TextMuted).SingleLine().MinWidth(0)
		})
		if note := skill.AgeNote(winner, e); note != "" {
			ui.Text(c, note).FontSize(13).TextColor(t.TextMuted)
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
