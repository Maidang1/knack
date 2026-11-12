package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"knack/internal/session"
	"knack/internal/skill"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/display"
)

// The home page answers three things and leaves the rest to 用量: what the
// period amounted to (a greeting and four figures), when the person worked
// (the year of days) and which Agents did it, beside what waits for them and
// what ran last. The two columns stack independently, so neither leaves a
// gap; narrow panes merge them into one.
const (
	homeSide = 342
	homeGap  = 48
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
	block := func(body func()) func() {
		return func() { ui.Column(c).Children(body) }
	}
	heat := func() { a.profileHeatmap(c, p, heatWidth) }
	agents := block(func() { a.agentUsage(c, d) })
	inbox := block(func() { a.overviewIssues(c, d) })
	recent := block(func() { a.recentSessions(c, d) })
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).Label("总览内容").Children(func() {
		ui.Column(c).Padding(28, 32, 28, 32).Gap(28).Children(func() {
			if a.sessions.err != "" {
				ui.Text(c, a.sessions.err).FontSize(13).TextColor(t.Warning)
			}
			a.partialBanner(c)
			a.homeHero(c, d, p)
			a.metricStrip(c, d, contentW < 640)
			stack := func(e ui.Element, sections ...func()) {
				e.Gap(36).Children(func() {
					for _, section := range sections {
						section()
					}
				})
			}
			if wide {
				ui.Row(c).Gap(homeGap).AlignItems(ui.Start).Padding(8, 0, 0, 0).Children(func() {
					stack(ui.Column(c).Grow(1).MinWidth(0), heat, agents)
					stack(ui.Column(c).Width(homeSide).Shrink(0), inbox, recent)
				})
			} else {
				stack(ui.Column(c), heat, inbox, agents, recent)
			}
			a.overviewNotes(c, d)
		})
	})
}

// homeHero greets the person with what the period amounted to, and what is
// true of all their history under it. What needs them is the 待处理 list's
// to say, the breakdowns the 用量 page's.
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
	lifetime := []string{fmt.Sprintf("累计 %s 个会话", groupDigits(p.sessions)), fmt.Sprintf("%s 个技能", groupDigits(len(a.libraryEntries())))}
	if !p.first.IsZero() {
		lifetime = append(lifetime, "首次记录 "+p.first.Local().Format("2006/01/02"))
	}
	ui.Row(c).Gap(14).AlignItems(ui.Center).Label("个人资料").Children(func() {
		display.Avatar(c, p.name, display.AvatarProps{Size: 44, Shape: display.AvatarCircle, Fallback: display.AvatarText}).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(20).FontWeight(600).MaxLines(2)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				withTip(c, ui.Row(c).Shrink(0).Gap(4).AlignItems(ui.Center), "统计只读取本机日志，不联网").Children(func() {
					ui.Icon(c, glyph("lock")).FontSize(11).TextColor(muted)
					ui.Text(c, "仅存于本机").FontSize(12).TextColor(muted).SingleLine()
				})
				ui.Text(c, "· "+strings.Join(lifetime, " · ")).FontSize(12).TextColor(muted).SingleLine().Shrink(1).MinWidth(0)
			})
		})
		if !a.hidden["sessions"] {
			overviewLink(c, "用量明细 →", func() { a.page = usagePage })
		}
	})
}

func (a *App) overviewNotes(c *ui.Context, d *overviewData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	var notes []string
	if d.summary.Incomplete {
		notes = append(notes, "部分日志或用量暂不支持，Token 为已识别部分")
	}
	notes = append(notes, "活跃时长与费用均为估算")
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, strings.Join(notes, " · ")).FontSize(11.5).TextColor(muted).MaxLines(2)
		if n := len(a.sessions.agentProblems); n > 0 {
			withTip(c, ui.Text(c, fmt.Sprintf("自定义 Agent 配置有 %d 个问题，相关 Agent 未加载，悬停查看", n)).FontSize(11.5).TextColor(t.Warning), strings.Join(a.sessions.agentProblems, "\n"))
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
			withTip(c, ui.Text(c, fmt.Sprintf("%d 条读取提示（跳过的无效或超长记录等），悬停查看", n)).FontSize(11.5).TextColor(muted), strings.Join(snap.Warnings[:min(12, n)], "\n"))
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
		m[2].note = fmt.Sprintf("%d 轮", timing.turns)
	}
	if cost := a.insightData().Cost; cost.Total > 0 || cost.UnpricedTokens.Total() > 0 {
		m[3].value, m[3].note = dollars(cost.Total), "按官方 API 价格估算"
		if n := cost.UnpricedTokens.Total(); n > 0 {
			m[3].note = tokenNumber(n) + " Token 未定价"
		}
	}
	return m
}

// metricStrip is a row of figures between hairlines; narrow panes wrap it
// into rows of two.
func (a *App) metricStrip(c *ui.Context, d *overviewData, narrow bool) {
	t := c.Theme()
	m := a.metrics(c, d)
	rows := [][]int{{0, 1, 2, 3}}
	if narrow {
		rows = [][]int{{0, 1}, {2, 3}}
	}
	ui.Column(c).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
		for r, row := range rows {
			line := ui.Row(c).Padding(16, 0)
			if r > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			line.Children(func() {
				for j, i := range row {
					metricCell(c, m[i], j)
				}
			})
		}
	})
}

func metricCell(c *ui.Context, m metric, j int) {
	t := c.Theme()
	box := ui.Column(c.Key(m.label)).Grow(1).Basis(0).MinWidth(0).Gap(6).Padding(0, 24)
	if j == 0 {
		box.Padding(0, 24, 0, 0)
	} else {
		box.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
	}
	box.Children(func() {
		ui.Text(c, m.label).FontSize(12.5).TextColor(t.TextMuted).SingleLine()
		number(c, m.value).FontSize(26).FontWeight(600)
		withTip(c, ui.Text(c, m.note).FontSize(12).TextColor(m.noteColor).SingleLine(), m.note)
	})
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
				withTip(c, ui.Text(c, it.note).FontSize(12).TextColor(token(t, "ink-3")).SingleLine(), it.note)
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
				withTip(c, ui.Text(c, recentLabel(s)).FontSize(13).SingleLine(), recentLabel(s))
				ui.Text(c, recentMeta(s)).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
			})
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
