package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/skill"
)

// The home page is flat on the window background: who and what the period
// amounted to, a row of figures between hairlines, then a wide column of how
// the Agents were used beside a narrow one of what waits and what was recent.
// The two columns stack independently, so neither leaves a gap; narrow panes
// merge them into one.
const (
	homeSide = 342
	homeGap  = 40
)

func (a *App) overviewPane(c *ui.Context) {
	t := c.Theme()
	d := a.overviewData()
	p := a.profileData()
	width, _ := c.Size()
	contentW := width - a.sidebarWidth() - 2*32
	wide := contentW >= 860
	heatWidth := contentW
	if wide {
		heatWidth -= homeSide + homeGap
	}
	color := token(t, "ink-2")
	if agent := overviewFilter(d.period, d.agent, time.Now()).Agent; agent != "" {
		color = agentColor(agent, t.Dark)
	}
	block := func(body func()) func() {
		return func() { ui.Column(c).Children(body) }
	}
	heat := func() { a.profileHeatmap(c, p, heatWidth) }
	mix := func() { ui.Column(c).Gap(22).Children(func() { a.profileMix(c, d) }) }
	agents := block(func() { a.agentUsage(c, d) })
	models := block(func() { a.sessionModelSpeeds(c, &sessionDetailData{models: p.models}, color) })
	inbox := block(func() { a.overviewIssues(c, d) })
	recent := block(func() { a.recentSessions(c, d) })
	rhythm := func() { a.profileRhythm(c, p) }
	ranking := func() { a.profileRanking(c, p) }
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).Label("总览内容").Children(func() {
		ui.Column(c).Padding(26, 32, 24, 32).Gap(24).Children(func() {
			if a.sessions.err != "" {
				ui.Text(c, a.sessions.err).FontSize(13).TextColor(t.Warning)
			}
			a.partialBanner(c)
			a.homeHero(c, d, p)
			a.metricStrip(c, d, contentW < 640)
			stack := func(e ui.Element, sections ...func()) {
				e.Gap(30).Children(func() {
					for _, section := range sections {
						section()
					}
				})
			}
			if wide {
				ui.Row(c).Gap(homeGap).AlignItems(ui.Start).Children(func() {
					stack(ui.Column(c).Grow(1).MinWidth(0), heat, mix, agents, models)
					stack(ui.Column(c).Width(homeSide).Shrink(0), inbox, recent, rhythm, ranking)
				})
			} else {
				stack(ui.Column(c), heat, inbox, mix, agents, recent, models, rhythm, ranking)
			}
			a.overviewNotes(c, d)
		})
	})
}

// homeHero greets the person with what the period amounted to, then what
// needs them, then what is true of all their history.
func (a *App) homeHero(c *ui.Context, d *overviewData, p *profileData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	span := []string{"今天", "过去 7 天", "过去 30 天", "全部记录中"}[max(0, min(d.period, 3))]
	active := 0
	for _, ag := range d.agents {
		if ag.summary.Sessions > 0 {
			active++
		}
	}
	title := fmt.Sprintf("%s，%s %d 个 Agent 跑了 %d 个会话", p.name, span, active, d.summary.Sessions)
	if d.summary.Sessions == 0 {
		title = p.name + "，" + span + "没有本地会话"
	}
	var habits []string
	if total := d.summary.Tokens.Total(); total > 0 && len(d.agents) > 1 && d.agents[0].summary.Tokens.Total() > 0 {
		top := d.agents[0]
		habits = append(habits, fmt.Sprintf("%s 占用 %.0f%% Token", sessionAgentName(top.id), 100*float64(top.summary.Tokens.Total())/float64(total)))
	}
	habits = append(habits, homeHabits(d.summary, p.tools)...)
	var waiting []string
	if n := a.countKind(skill.IssueBroken); n > 0 {
		waiting = append(waiting, fmt.Sprintf("%d 个技能断链", n))
	}
	if n := len(d.anomalies); n > 0 {
		waiting = append(waiting, fmt.Sprintf("%d 个会话有耗时异常", n))
	}
	settled := "没有需要处理的事"
	if len(waiting) > 0 {
		settled = "有 " + strings.Join(waiting, "、") + "等你处理"
	}
	line := settled + "。"
	if len(habits) > 0 {
		line = strings.Join(habits, "，") + "；" + line
	}
	lifetime := []string{fmt.Sprintf("累计 %s 个会话", groupDigits(p.sessions)), fmt.Sprintf("%s 个技能", groupDigits(len(a.libraryEntries())))}
	if !p.first.IsZero() {
		lifetime = append(lifetime, "首次记录 "+p.first.Local().Format("2006/01/02"))
	}
	ui.Row(c).Gap(18).AlignItems(ui.Center).Label("个人资料").Children(func() {
		ui.Box(c).Size(68, 68).Shrink(0).Radius(34).Background(t.Accent).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Text(c, strings.ToUpper(string([]rune(p.name)[:1]))).FontSize(28).FontWeight(600).TextColor(t.AccentText)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, title).FontSize(22).FontWeight(600).MaxLines(2)
			ui.Text(c, line).FontSize(13.5).TextColor(t.TextMuted).MaxLines(2)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Shrink(0).Gap(4).Padding(2, 7).Radius(5).Background(t.SurfaceHover).AlignItems(ui.Center).
					Tooltip("统计只读取本机日志，不联网").Children(func() {
					ui.Icon(c, glyph("lock")).FontSize(11).TextColor(muted)
					ui.Text(c, "仅存于本机").FontSize(11).TextColor(t.TextMuted).SingleLine()
				})
				ui.Text(c, strings.Join(lifetime, " · ")).FontSize(12).TextColor(muted).SingleLine().Shrink(1).MinWidth(0)
			})
		})
	})
}

func (a *App) overviewNotes(c *ui.Context, d *overviewData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	var notes []string
	if d.summary.Incomplete {
		notes = append(notes, "部分日志或用量暂不支持，Token 为已识别部分")
	}
	notes = append([]string{"全部数据只来自本机日志，不会上传"}, notes...)
	notes = append(notes, "速率与时长均为估算", "缓存命中 = 缓存读取 / 全部输入 Token")
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, strings.Join(notes, " · ")).FontSize(11.5).TextColor(muted).MaxLines(2)
		if n := len(a.sessions.agentProblems); n > 0 {
			ui.Text(c, fmt.Sprintf("自定义 Agent 配置有 %d 个问题，相关 Agent 未加载，悬停查看", n)).FontSize(11.5).TextColor(t.Warning).Tooltip(strings.Join(a.sessions.agentProblems, "\n"))
		}
		snap := a.sessions.snapshot
		if snap == nil {
			return
		}
		if snap.Partial && a.sessions.loading {
			ui.Text(c, "正在读取剩余日志，数字会随读取进度更新").FontSize(11.5).TextColor(muted)
		}
		for _, source := range snap.Sources {
			if source.Files > 0 && !source.Supported {
				ui.Text(c, sessionAgentName(source.Agent)+"："+source.Reason).FontSize(11.5).TextColor(muted)
			}
		}
		// Per-file details are diagnostics: summarize them, keep the list
		// one hover away instead of printing raw file names in the page.
		if n := len(snap.Warnings); n > 0 {
			ui.Text(c, fmt.Sprintf("%d 条读取提示（跳过的无效或超长记录等），悬停查看", n)).FontSize(11.5).TextColor(muted).Tooltip(strings.Join(snap.Warnings[:min(12, n)], "\n"))
		}
	})
}

type metric struct {
	label, value, note string
	noteColor          ui.Color
	warn               bool
}

func (a *App) metrics(c *ui.Context, d *overviewData) []metric {
	t := c.Theme()
	muted := token(t, "ink-3")
	s := d.summary
	m := []metric{
		{label: "会话", value: groupDigits(s.Sessions), note: "全部本地会话", noteColor: muted},
		{label: "Token", value: tokenNumber(s.Tokens.Total()), note: fmt.Sprintf("输入 %s · 输出 %s", tokenNumber(s.Tokens.InputTotal()), tokenNumber(s.Tokens.Output)), noteColor: muted},
		{label: "活跃时长", value: "—", note: "没有可识别的轮次", noteColor: muted},
		{label: "估算费用", value: "—", note: "没有记录", noteColor: muted},
		{label: "耗时异常", value: fmt.Sprint(len(d.anomalies)), note: fmt.Sprintf("共 %d 处 · 慢步骤 / 重试 / 中断", len(s.Anomalies)), noteColor: muted, warn: len(d.anomalies) > 0},
	}
	if d.period < 3 {
		span := []string{"昨天", "前 7 天", "前 30 天"}[d.period]
		delta := s.Sessions - d.previous.Sessions
		switch {
		case d.previous.Sessions == 0:
			m[0].note = span + "没有会话"
		case delta == 0:
			m[0].note = "与" + span + "持平"
		default:
			m[0].note = fmt.Sprintf("较%s %+d", span, delta)
			if delta > 0 {
				m[0].noteColor = t.Success
			}
		}
	}
	if s.CacheHitKnown {
		m[1].note = fmt.Sprintf("缓存命中 %.0f%%", 100*s.CacheHit)
	}
	if s.Requests > 0 && s.UnmeteredRequests == s.Requests && s.Tokens.Total() == 0 {
		m[1].value, m[1].note = "暂不支持", "日志没有受支持的用量字段"
	}
	if timing := a.usageTimingFor("", d); timing.turns > 0 {
		m[2].value = "≈ " + session.FormatDuration(timing.phases.total)
		m[2].note = fmt.Sprintf("%d 轮 · 平均每轮 ≈ %s", timing.turns, session.FormatDuration(timing.phases.total/time.Duration(timing.turns)))
	}
	if cost := a.insightData().Cost; cost.Total > 0 || cost.UnpricedTokens.Total() > 0 {
		m[3].value, m[3].note = dollars(cost.Total), "按官方 API 价格估算"
		if n := cost.UnpricedTokens.Total(); n > 0 {
			m[3].note = tokenNumber(n) + " Token 未定价"
		}
	}
	if m[4].warn {
		m[4].noteColor = t.Warning
	}
	return m
}

// metricStrip is a row of figures between hairlines; narrow panes wrap it
// into rows of three.
func (a *App) metricStrip(c *ui.Context, d *overviewData, narrow bool) {
	t := c.Theme()
	m := a.metrics(c, d)
	rows := [][]int{{0, 1, 2, 3, 4}}
	if narrow {
		rows = [][]int{{0, 1, 2}, {3, 4}}
	}
	ui.Column(c).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
		for r, row := range rows {
			line := ui.Row(c).Padding(12, 0)
			if r > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			line.Children(func() {
				for j, i := range row {
					a.metricCell(c, d, m[i], i, j)
				}
				if narrow {
					for range 3 - len(row) {
						ui.Box(c).Grow(1).Basis(0)
					}
				}
			})
		}
	})
}

func (a *App) metricCell(c *ui.Context, d *overviewData, m metric, i, j int) {
	t := c.Theme()
	var box ui.Element
	if i == 4 {
		box = ui.ButtonBase(c.Key(m.label)).Column().Label("查看耗时异常会话").Disabled(len(d.anomalies) == 0).AlignItems(ui.Stretch).Justify(ui.Start)
	} else {
		box = ui.Column(c.Key(m.label))
	}
	box.Grow(1).Basis(0).MinWidth(0).Gap(4).Padding(0, 20)
	if j == 0 {
		box.Padding(0, 20, 0, 0)
	} else {
		box.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	}
	box.Children(func() {
		ui.Text(c, m.label).FontSize(13).TextColor(t.TextMuted).SingleLine()
		v := number(c, m.value).FontSize(24).FontWeight(600)
		if m.warn {
			v.TextColor(t.Warning)
		}
		ui.Text(c, m.note).FontSize(12).LineHeight(1.4).TextColor(m.noteColor).MaxLines(2).Tooltip(m.note)
	})
	if i == 4 && box.Clicked() {
		a.openSessionAnomalies(d)
	}
}

// listRow is a borderless, full-width row button over a hairline, the dense
// list style of the home page.
func listRow(c *ui.Context, key any, label string) ui.Element {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(label).Padding(8, 0).Gap(10).AlignItems(ui.Center).Justify(ui.Start).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	if b.Hovered() {
		b.Background(fade(t.SurfaceHover, 120))
	}
	return b
}

type inboxItem struct {
	key, title, note, action, icon, tint string
	color                                ui.Color
	open                                 func()
}

// overviewIssues lists what waits for the person, each with the one action
// that settles it; the full queue is on the 待处理 page.
func (a *App) overviewIssues(c *ui.Context, d *overviewData) {
	t := c.Theme()
	toIssues := func(kind skill.IssueKind) func() {
		return func() {
			a.page, a.query, a.filter = "issues", "", 0
			for i, f := range issueFilters {
				if f.id == string(kind) {
					a.filter = i
				}
			}
		}
	}
	var items []inboxItem
	add := func(kind skill.IssueKind, title, note, action, icon, tint string, color ui.Color) {
		if n := a.countKind(kind); n > 0 {
			items = append(items, inboxItem{string(kind), fmt.Sprintf("%d %s", n, title), note, action, icon, tint, color, toIssues(kind)})
		}
	}
	add(skill.IssueBroken, "个技能断链", "链接指向的目录已不存在", "修复", "unlink", "danger-bg", t.Danger)
	add(skill.IssueStale, "组同名副本存在差异", "副本与共享池内容不一致", "比较", "copy", "warn-bg", t.Warning)
	add(skill.IssueOrphan, "个共享技能未挂出", "没有任何 Agent 在使用", "挂载", "link", "hover", t.TextMuted)
	add(skill.IssueEmpty, "个技能目录为空", "目录里没有 SKILL.md", "查看", "empty", "hover", t.TextMuted)
	if n := len(d.anomalies); n > 0 {
		items = append(items, inboxItem{"anomalies", fmt.Sprintf("%d 个会话有耗时异常", n), "慢步骤、重试和中断，在会话中逐轮查看", "查看", "alert", "warn-bg", t.Warning, func() { a.openSessionAnomalies(d) }})
	}
	link := ""
	if a.snap != nil && len(a.snap.Issues) > 0 {
		link = fmt.Sprintf("全部 %d 项 →", len(a.snap.Issues))
	}
	sectionHead(c, "待处理", link, func() { a.page, a.query, a.filter = "issues", "", 0 })
	if len(items) == 0 {
		ui.Text(c, "目前没有需要处理的事").FontSize(13).TextColor(t.TextMuted).Padding(11, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
		return
	}
	for _, it := range items {
		ui.Row(c.Key("inbox:"+it.key)).Gap(12).Padding(11, 0).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			ui.Box(c).Size(28, 28).Radius(7).Shrink(0).Center().Background(token(t, it.tint)).Children(func() {
				ui.Icon(c, glyph(it.icon)).FontSize(15).TextColor(it.color)
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, it.title).FontSize(13).SingleLine()
				ui.Text(c, it.note).FontSize(12).TextColor(token(t, "ink-3")).SingleLine().Tooltip(it.note)
			})
			if outlineButton(c, "act:"+it.key, it.action).Label(it.action + "：" + it.title).Clicked() {
				it.open()
			}
		})
	}
}

func (a *App) recentSessions(c *ui.Context, d *overviewData) {
	t := c.Theme()
	sectionHead(c, "最近会话", "全部 →", func() { a.page = "sessions" })
	if len(d.recent) == 0 {
		ui.Text(c, "当前范围没有本地会话").FontSize(13).TextColor(t.TextMuted).Padding(8, 0)
		return
	}
	for _, s := range d.recent[:min(5, len(d.recent))] {
		row := listRow(c, s.Path, recentRowLabel(s))
		row.Children(func() {
			ui.Box(c).Size(3, 30).Radius(2).Shrink(0).Background(agentColor(s.Agent, t.Dark))
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				ui.Text(c, recentLabel(s)).FontSize(13).SingleLine().Tooltip(recentLabel(s))
				ui.Text(c, recentMeta(s)).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
			})
			if d.anomalies[sessionID(s)] {
				ui.Icon(c, glyph("issues")).FontSize(15).TextColor(t.Warning).Label("有耗时异常").Shrink(0)
			}
		})
		if row.Clicked() {
			a.openSession(sessionID(s))
		}
	}
}

// recentMeta is a session's place, length and size on one line.
func recentMeta(s session.Session) string {
	var parts []string
	if s.CWD != "" {
		parts = append(parts, filepath.Base(s.CWD))
	} else {
		parts = append(parts, sessionAgentName(s.Agent))
	}
	if !s.Start.IsZero() && s.End.Sub(s.Start) >= time.Second {
		parts = append(parts, session.FormatDuration(s.End.Sub(s.Start)))
	}
	parts = append(parts, fmt.Sprintf("%d 轮", len(s.Turns)))
	children := 0
	for _, t := range s.Turns {
		children += len(t.Subagents)
	}
	if children > 0 {
		parts = append(parts, fmt.Sprintf("%d 子 Agent", children))
	}
	parts = append(parts, s.End.Local().Format("01/02 15:04"))
	return strings.Join(parts, " · ")
}
