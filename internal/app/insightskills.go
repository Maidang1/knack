package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/insight"
	"knack/internal/session"
	"knack/internal/skill"
)

// skillInsights is the skill report of the current scans, rebuilt when
// either changes or the day turns.
func (a *App) skillInsights() insight.SkillReport {
	now := time.Now()
	date := now.Format("2006-01-02")
	if s := a.insights.skills; s != nil && s.snapshot == any(a.snap) && s.sessions == a.sessions.snapshot && s.date == date {
		return s.report
	}
	agentOf := func(r skill.Root) session.Agent { return session.Agent(agentKey(a.home, r.Path)) }
	known := func(id session.Agent) bool { return skillSupport(id) == "" }
	report := insight.AnalyzeSkills(a.snap, a.sessions.snapshot, agentOf, known, now)
	a.insights.skills = &insightSkills{snapshot: a.snap, sessions: a.sessions.snapshot, date: date, report: report}
	return report
}

// configFor reads the configuration that applies in dir once per scan.
func (a *App) configFor(dir string) insight.ConfigReport {
	if a.insights.configGen != a.scanGen || a.insights.configs == nil {
		a.insights.configs = map[string]*insight.ConfigReport{}
		a.insights.configGen = a.scanGen
	}
	if r := a.insights.configs[dir]; r != nil {
		return *r
	}
	r := insight.ReadConfig(a.home, dir)
	a.insights.configs[dir] = &r
	return r
}

// skillsInScope keeps the skills of the Agent the toolbar picked.
func (a *App) skillsInScope(r insight.SkillReport) insight.SkillReport {
	f := overviewFilter(a.sessions.period, a.sessionAgentFilter(), time.Now())
	if f.Agent == "" {
		return r
	}
	var out insight.SkillReport
	roots := map[string]bool{}
	for _, root := range r.Roots {
		if root.Agent == f.Agent {
			out.Roots = append(out.Roots, root)
			roots[root.RootName] = true
		}
	}
	for _, h := range r.Skills {
		if h.Agent == f.Agent {
			out.Skills = append(out.Skills, h)
		}
	}
	for _, o := range r.Overlaps {
		if roots[o.RootName] {
			out.Overlaps = append(out.Overlaps, o)
		}
	}
	return out
}

// ---- 技能 ----

// skillGroup is one skill linked into several Agents: the same name and
// description, reported once with the Agents that load it.
type skillGroup struct {
	skill insight.SkillHealth
	roots []string
}

func groupSkills(list []insight.SkillHealth) []skillGroup {
	var out []skillGroup
	index := map[string]int{}
	for _, h := range list {
		key := h.Name + "\x00" + h.Description
		if i, ok := index[key]; ok {
			out[i].roots = append(out[i].roots, h.RootName)
			continue
		}
		index[key] = len(out)
		out = append(out, skillGroup{skill: h, roots: []string{h.RootName}})
	}
	return out
}

// rootsText names the Agents, shortening long lists.
func rootsText(roots []string) string {
	names := make([]string, len(roots))
	for i, r := range roots {
		names[i] = strings.TrimSuffix(r, " 自带")
	}
	if len(names) <= 2 {
		return strings.Join(names, "、")
	}
	return fmt.Sprintf("%s 等 %d 个 Agent", strings.Join(names[:2], "、"), len(names))
}

func (a *App) insightSkills(c *ui.Context) {
	t := c.Theme()
	if a.snap == nil {
		emptyState(c, "技能", "hourglass", t.TextMuted, "正在扫描技能目录", "", nil)
		return
	}
	r := a.skillsInScope(a.skillInsights())
	var unused, problems []insight.SkillHealth
	loaded, heaviest := 0, insight.SkillRootSummary{}
	for _, root := range r.Roots {
		if root.StandingTokens > heaviest.StandingTokens {
			heaviest = root
		}
	}
	for _, h := range r.Skills {
		loaded++
		if h.Unused() {
			unused = append(unused, h)
		}
		if len(h.Findings) > 0 && h.Findings[0].Severity > 0 {
			problems = append(problems, h)
		}
	}
	figures(c, []metric{
		{label: "已加载的技能", value: groupDigits(loaded), note: fmt.Sprintf("%d 个 Agent 目录", len(r.Roots))},
		{label: "技能列表常驻", value: tokenNumber(int64(heaviest.StandingTokens)), note: "最多的是 " + strings.TrimSuffix(heaviest.RootName, " 自带") + " · 估算"},
		{label: "90 天未调用", value: groupDigits(len(unused)), note: "只统计日志记录 Skill 调用的 Agent", warn: len(unused) > 0},
		{label: "描述有问题", value: groupDigits(len(groupSkills(problems))), note: "缺失、过长、过短或名称不符", warn: len(problems) > 0},
		{label: "描述相近", value: groupDigits(len(groupOverlaps(r.Overlaps))), note: "可能互相抢触发"},
	})
	width, _ := c.Size()
	wide := width-a.sidebarWidth() >= 1000
	main := func() {
		a.skillRootsTable(c, r)
		a.skillUnused(c, unused)
	}
	side := func() {
		a.skillProblems(c, problems)
		a.skillOverlaps(c, r.Overlaps)
		a.skillHeaviest(c, r.Skills)
	}
	if wide {
		ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(28).Children(main)
			ui.Column(c).Width(420).Shrink(0).Gap(28).Children(side)
		})
	} else {
		main()
		side()
	}
	note(c, "常驻 = 技能名称和描述在每个会话的技能列表里占用的 Token，按字符数估算。调用按日志里的技能名匹配，同名副本无法区分；没有调用记录不代表一定没用，卸载前请先确认。")
}

func (a *App) skillRootsTable(c *ui.Context, r insight.SkillReport) {
	t := c.Theme()
	roots := slices.Clone(r.Roots)
	logs := func(root insight.SkillRootSummary) int {
		if logSupport(root.Agent) == "" {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(roots, func(x, y insight.SkillRootSummary) int {
		if d := logs(y) - logs(x); d != 0 {
			return d
		}
		return y.StandingTokens - x.StandingTokens
	})
	peak := 1
	for _, root := range roots {
		peak = max(peak, root.StandingTokens)
	}
	link := ""
	if len(roots) > 12 {
		link = fmt.Sprintf("全部 %d", len(roots))
		if a.insights.allRoots {
			link = "收起"
		} else {
			roots = roots[:12]
		}
	}
	ui.Column(c).Label("各 Agent 的技能列表").Children(func() {
		sectionHead(c, "各 Agent 的技能列表 · 有会话日志的在前", link, func() { a.insights.allRoots = !a.insights.allRoots })
		widths := []float32{44, 64, 72}
		tableHead(c, "Agent 目录", []string{"技能", "常驻", "90 天未用"}, widths)
		for _, root := range roots {
			row := listRow(c, "skill-root:"+root.RootID, root.RootName)
			row.Children(func() {
				ui.Text(c, strings.TrimSuffix(root.RootName, " 自带")).FontSize(12.5).Grow(1).MinWidth(0).SingleLine()
				shareBar(c, float64(root.StandingTokens), float64(peak), token(t, "ink-2"), 60)
				cell(c, groupDigits(root.Skills), widths[0], true).TextColor(t.TextMuted)
				cell(c, tokenNumber(int64(root.StandingTokens)), widths[1], true)
				unused := "暂不支持"
				if root.UsageKnown {
					unused = countOrDash(root.Unused)
				}
				e := cell(c, unused, widths[2], true)
				if !root.UsageKnown {
					e.TextColor(token(t, "ink-3")).Tooltip(skillSupport(root.Agent))
				} else if root.Unused > 0 {
					e.TextColor(t.Warning)
				}
			})
			if row.Clicked() {
				a.page = "root:" + root.RootID
				a.state(a.page).agentTab = 0
			}
		}
	})
}

// openSkill shows a skill on its Agent's page, filtered to its name.
func (a *App) openSkill(h insight.SkillHealth) {
	a.page = "root:" + h.RootID
	a.state(a.page).agentTab = 0
	a.query = h.Name
}

func (a *App) skillRow(c *ui.Context, key string, h insight.SkillHealth, meta string, metaColor ui.Color, trailing func()) {
	t := c.Theme()
	row := listRow(c, key, h.Name)
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, h.Name).Font(monoFont).FontSize(12.5).SingleLine().Tooltip(h.Path)
			if metaColor.A == 0 {
				metaColor = token(t, "ink-3")
			}
			ui.Text(c, meta).FontSize(11).TextColor(metaColor).SingleLine().Tooltip(meta)
		})
		if trailing != nil {
			trailing()
		}
	})
	if row.Clicked() {
		a.openSkill(h)
	}
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("在技能列表中查看").Chosen() {
			a.openSkill(h)
		}
		if m.Item("在访达中显示").Chosen() {
			a.reveal(h.Path)
		}
	})
}

func (a *App) skillUnused(c *ui.Context, unused []insight.SkillHealth) {
	t := c.Theme()
	slices.SortStableFunc(unused, func(x, y insight.SkillHealth) int { return y.DescTokens - x.DescTokens })
	ui.Column(c).Label("90 天未调用").Children(func() {
		link := ""
		if n := len(groupSkills(unused)); n > 12 {
			link = fmt.Sprintf("全部 %d", n)
			if a.insights.allUnused {
				link = "收起"
			}
		}
		sectionHead(c, "90 天未调用 · 按常驻开销排序", link, func() { a.insights.allUnused = !a.insights.allUnused })
		if len(unused) == 0 {
			ui.Text(c, "已加载的技能近 90 天都有调用记录").FontSize(12).TextColor(t.TextMuted)
			return
		}
		list := groupSkills(unused)
		if !a.insights.allUnused {
			list = list[:min(12, len(list))]
		}
		for i, g := range list {
			h := g.skill
			meta := rootsText(g.roots)
			if h.LastUsed.IsZero() {
				meta += " · 从未调用"
			} else {
				meta += " · 上次 " + h.LastUsed.Local().Format("2006-01-02")
			}
			a.skillRow(c, fmt.Sprintf("unused:%d", i), h, meta, ui.Color{}, func() {
				number(c, tokenNumber(int64(h.DescTokens))).FontSize(12).Width(56).TextAlign(ui.End).Shrink(0).Tooltip("常驻 Token · 估算")
			})
		}
	})
}

func (a *App) skillProblems(c *ui.Context, problems []insight.SkillHealth) {
	t := c.Theme()
	slices.SortStableFunc(problems, func(x, y insight.SkillHealth) int { return y.Findings[0].Severity - x.Findings[0].Severity })
	groups := groupSkills(problems)
	ui.Column(c).Label("描述有问题的技能").Children(func() {
		link := ""
		if len(groups) > 10 {
			link = fmt.Sprintf("全部 %d", len(groups))
			if a.insights.allSkills {
				link = "收起"
			}
		}
		sectionHead(c, "描述有问题的技能", link, func() { a.insights.allSkills = !a.insights.allSkills })
		if len(problems) == 0 {
			ui.Text(c, "没有发现缺失或明显不合适的描述").FontSize(12).TextColor(t.TextMuted)
			return
		}
		list := groups
		if !a.insights.allSkills {
			list = list[:min(10, len(list))]
		}
		for i, g := range list {
			h := g.skill
			var texts []string
			for _, f := range h.Findings {
				if f.Severity > 0 {
					texts = append(texts, f.Text)
				}
			}
			color := t.Warning
			if h.Findings[0].Severity == 2 {
				color = t.Danger
			}
			a.skillRow(c, fmt.Sprintf("problem:%d", i), h, rootsText(g.roots)+" · "+strings.Join(texts, "；"), color, nil)
		}
	})
}

// overlapGroup is one pair of competing skills and the Agents that load
// both.
type overlapGroup struct {
	overlap insight.Overlap
	roots   []string
}

func groupOverlaps(list []insight.Overlap) []overlapGroup {
	var out []overlapGroup
	index := map[string]int{}
	for _, o := range list {
		key := o.A + "\x00" + o.B
		if i, ok := index[key]; ok {
			out[i].roots = append(out[i].roots, o.RootName)
			continue
		}
		index[key] = len(out)
		out = append(out, overlapGroup{overlap: o, roots: []string{o.RootName}})
	}
	return out
}

func (a *App) skillOverlaps(c *ui.Context, list []insight.Overlap) {
	t := c.Theme()
	overlaps := groupOverlaps(list)
	ui.Column(c).Gap(8).Label("描述相近的技能").Children(func() {
		overviewTitle(c, "描述相近的技能")
		if len(overlaps) == 0 {
			ui.Text(c, "同一 Agent 下没有描述高度重叠的技能").FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, g := range overlaps[:min(8, len(overlaps))] {
			o := g.overlap
			ui.Column(c).Gap(2).Padding(0, 0, 6, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, o.A).Font(monoFont).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
					ui.Text(c, "≈").FontSize(12).TextColor(token(t, "ink-3")).Shrink(0)
					ui.Text(c, o.B).Font(monoFont).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
					ui.Spacer(c)
					number(c, fmt.Sprintf("%.0f%%", o.Score*100)).FontSize(11.5).TextColor(t.TextMuted).Shrink(0).Tooltip("描述用词的重合度")
				})
				ui.Text(c, rootsText(g.roots)+" · 共同词："+strings.Join(o.Shared, "、")).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
			})
		}
		note(c, "描述相近时模型可能随机选一个。合并重复的技能，或在描述里写清各自的使用场景。")
	})
}

func (a *App) skillHeaviest(c *ui.Context, skills []insight.SkillHealth) {
	t := c.Theme()
	sorted := slices.Clone(skills)
	slices.SortStableFunc(sorted, func(x, y insight.SkillHealth) int { return y.DescTokens - x.DescTokens })
	list := groupSkills(sorted)
	ui.Column(c).Label("描述最长的技能").Children(func() {
		sectionHead(c, "描述最长的技能", "", nil)
		for i, g := range list[:min(8, len(list))] {
			h := g.skill
			meta := rootsText(g.roots)
			if h.UsageKnown {
				meta += fmt.Sprintf(" · 近 90 天 %d 次", h.Calls90)
			}
			a.skillRow(c, fmt.Sprintf("heavy:%d", i), h, meta, ui.Color{}, func() {
				number(c, tokenNumber(int64(h.DescTokens))).FontSize(12).Width(56).TextAlign(ui.End).Shrink(0).TextColor(t.Text)
			})
		}
	})
}

// ---- 配置 ----

func (a *App) insightConfig(c *ui.Context) {
	t := c.Theme()
	dirs := []string{"仅全局配置"}
	paths := []string{""}
	for _, p := range a.insightData().Projects {
		if p.Path != "" && len(paths) < 30 {
			dirs = append(dirs, shortPath(a.home, p.Path))
			paths = append(paths, p.Path)
		}
	}
	if a.insights.configDir != "" && !slices.Contains(paths, a.insights.configDir) {
		dirs = append(dirs, shortPath(a.home, a.insights.configDir))
		paths = append(paths, a.insights.configDir)
	}
	choice := dirs[max(0, slices.Index(paths, a.insights.configDir))]
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "在这个目录启动时").FontSize(12.5).TextColor(t.TextMuted).Shrink(0)
		if selectStyle(c, ui.Select(c, &choice, dirs)).Label("配置目录").Width(320).Changed() {
			if i := slices.Index(dirs, choice); i >= 0 {
				a.insights.configDir = paths[i]
			}
			c.Invalidate()
		}
		if a.insights.configDir != "" && outlineButton(c, "config-reveal", "在访达中显示").Clicked() {
			a.reveal(a.insights.configDir)
		}
	})
	cfg := a.configFor(a.insights.configDir)
	cfg.MatchMCPCalls(a.insightData().Friction.MCP)
	unusedMCP := 0
	for _, m := range cfg.MCP {
		if !m.Disabled && m.Calls == 0 {
			unusedMCP++
		}
	}
	instr := 0
	for _, n := range cfg.InstructionTokens {
		instr += n
	}
	allow, deny := 0, 0
	for _, p := range cfg.Permissions {
		switch p.Kind {
		case "allow":
			allow++
		case "deny":
			deny++
		}
	}
	figures(c, []metric{
		{label: "指令文件", value: tokenNumber(int64(instr)), note: fmt.Sprintf("%d 个文件 · 估算 Token", countKind(cfg.Files, "instructions"))},
		{label: "MCP 服务器", value: groupDigits(len(cfg.MCP)), note: fmt.Sprintf("%d 个在%s没有调用", unusedMCP, periodText(a.sessions.period)), warn: unusedMCP > 0},
		{label: "Hooks", value: groupDigits(len(cfg.Hooks)), note: "Claude Code 的生命周期钩子"},
		{label: "权限规则", value: groupDigits(len(cfg.Permissions)), note: fmt.Sprintf("allow %d · deny %d", allow, deny)},
	})
	for _, w := range cfg.Warnings {
		callout(c, "alert", t.Warning, w)
	}
	width, _ := c.Size()
	wide := width-a.sidebarWidth() >= 1000
	left := func() {
		a.configInstructions(c, cfg)
		a.configMCP(c, cfg)
	}
	right := func() {
		a.configHooks(c, cfg)
		a.configPermissions(c, cfg)
	}
	if wide {
		ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(28).Children(left)
			ui.Column(c).Width(400).Shrink(0).Gap(28).Children(right)
		})
	} else {
		left()
		right()
	}
	note(c, "只读取 Claude Code 与 Codex 的配置文件，不修改。MCP 只显示启动命令名或服务地址的主机名，参数、环境变量和完整 URL 可能含密钥，不显示。插件带来的 MCP 和技能不在此列。")
}

func countKind(files []insight.ConfigFile, kind string) int {
	n := 0
	for _, f := range files {
		if f.Kind == kind && f.Exists {
			n++
		}
	}
	return n
}

func (a *App) configInstructions(c *ui.Context, cfg insight.ConfigReport) {
	t := c.Theme()
	ui.Column(c).Label("指令文件").Children(func() {
		sectionHead(c, "指令文件 · 按加载顺序", "", nil)
		widths := []float32{44, 56}
		tableHead(c, "文件", []string{"范围", "Token"}, widths)
		any := false
		for i, f := range cfg.Files {
			if f.Kind != "instructions" {
				continue
			}
			any = true
			label := sessionAgentName(f.Agent) + " · " + shortPath(a.home, f.Path)
			row := listRow(c, fmt.Sprintf("instr:%d", i), label)
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, shortPath(a.home, f.Path)).Font(monoFont).FontSize(12).SingleLine().Tooltip(f.Path)
					meta := sessionAgentName(f.Agent)
					if f.ImportedBy != "" {
						meta += " · 由 " + filepath.Base(f.ImportedBy) + " 导入"
					}
					if f.Err != "" {
						meta += " · " + f.Err
					}
					ui.Text(c, meta).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
				})
				cell(c, f.Scope, widths[0], false).TextColor(t.TextMuted).TextAlign(ui.End)
				cell(c, tokenNumber(int64(f.Tokens)), widths[1], true)
			})
			if row.Clicked() {
				a.openPath(f.Path)
			}
		}
		if !any {
			ui.Text(c, "没有找到 CLAUDE.md 或 AGENTS.md").FontSize(12).TextColor(t.TextMuted).Padding(8, 0)
		}
	})
}

func (a *App) configMCP(c *ui.Context, cfg insight.ConfigReport) {
	t := c.Theme()
	ui.Column(c).Label("MCP 服务器").Children(func() {
		sectionHead(c, "MCP 服务器", "", nil)
		if len(cfg.MCP) == 0 {
			ui.Text(c, "没有配置 MCP 服务器").FontSize(12).TextColor(t.TextMuted)
			return
		}
		widths := []float32{44, 64}
		tableHead(c, "服务器", []string{"范围", "调用"}, widths)
		for _, m := range cfg.MCP {
			ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
				Label(fmt.Sprintf("%s %s，%d 次调用", sessionAgentName(m.Agent), m.Name, m.Calls)).Children(func() {
				ui.Box(c).Size(3, 26).Radius(2).Shrink(0).Background(agentColor(m.Agent, t.Dark))
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					name := ui.Text(c, m.Name).Font(monoFont).FontSize(12).SingleLine()
					if m.Disabled {
						name.TextColor(token(t, "ink-3"))
					}
					meta := sessionAgentName(m.Agent) + " · " + m.Launch
					if m.Disabled {
						meta += " · 已停用"
					}
					ui.Text(c, meta).FontSize(11).TextColor(token(t, "ink-3")).SingleLine().Tooltip(shortPath(a.home, m.Source))
				})
				cell(c, m.Scope, widths[0], false).TextColor(t.TextMuted).TextAlign(ui.End)
				calls := countOrDash(m.Calls)
				if m.Disabled {
					calls = "—"
				}
				e := cell(c, calls, widths[1], true)
				if m.Calls == 0 && !m.Disabled {
					e.TextColor(t.Warning).Tooltip("这段时间没有调用。每个 MCP 服务器的工具定义都会占用上下文。")
				}
			})
		}
		note(c, "调用为"+periodText(a.sessions.period)+"的次数，按日志里的 mcp__<服务器>__<工具> 工具名统计。没有调用的服务器仍会把工具定义带进每个会话，可考虑停用或改为按项目启用。")
	})
}

func (a *App) configHooks(c *ui.Context, cfg insight.ConfigReport) {
	t := c.Theme()
	ui.Column(c).Gap(8).Label("Hooks").Children(func() {
		overviewTitle(c, "Hooks")
		if len(cfg.Hooks) == 0 {
			ui.Text(c, "没有配置 hooks").FontSize(12).TextColor(t.TextMuted)
			return
		}
		for _, h := range cfg.Hooks {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, h.Event).FontSize(12).FontWeight(600).Shrink(0)
				if h.Matcher != "" {
					ui.Text(c, h.Matcher).Font(monoFont).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
				}
				ui.Spacer(c)
				ui.Text(c, h.Command).Font(monoFont).FontSize(11.5).SingleLine().Shrink(1).MinWidth(0).Tooltip(shortPath(a.home, h.Source))
				ui.Text(c, h.Scope).FontSize(11).TextColor(token(t, "ink-3")).Shrink(0)
			})
		}
		note(c, "只显示命令的第一个词。hooks 在每次匹配的事件上运行，慢的 hook 会拖慢每一步。")
	})
}

func (a *App) configPermissions(c *ui.Context, cfg insight.ConfigReport) {
	t := c.Theme()
	ui.Column(c).Gap(8).Label("权限规则").Children(func() {
		overviewTitle(c, "权限规则")
		if len(cfg.Permissions) == 0 {
			ui.Text(c, "没有配置权限规则").FontSize(12).TextColor(t.TextMuted)
		}
		for _, p := range cfg.Permissions[:min(24, len(cfg.Permissions))] {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				color := t.Success
				switch p.Kind {
				case "deny":
					color = t.Danger
				case "ask":
					color = t.Warning
				}
				ui.Text(c, p.Kind).Font(monoFont).FontSize(11).TextColor(color).Width(36).Shrink(0)
				ui.Text(c, p.Rule).Font(monoFont).FontSize(11.5).Grow(1).MinWidth(0).SingleLine().Tooltip(p.Rule)
				ui.Text(c, p.Scope).FontSize(11).TextColor(token(t, "ink-3")).Shrink(0).Tooltip(shortPath(a.home, p.Source))
			})
		}
		if len(cfg.Permissions) > 24 {
			note(c, fmt.Sprintf("另有 %d 条规则", len(cfg.Permissions)-24))
		}
		hints := insight.PermissionHints(a.insightData().Friction.Tools, cfg.Permissions, 3)
		var open []string
		for _, h := range hints {
			if !h.Covered {
				open = append(open, fmt.Sprintf("%s（%d 次）", h.Tool, h.Waits))
			}
		}
		if len(open) > 0 {
			callout(c, "hand", t.Accent, "这些工具常需人工确认且没有 allow 规则："+strings.Join(open[:min(5, len(open))], "、")+"。安全的用法可以加进 permissions.allow。")
		}
	})
}
