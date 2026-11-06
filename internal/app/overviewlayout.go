package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/skill"
)

// The overview is a grid of cards. On wide panes the two columns stack
// independently, so a tall card on one side never leaves a gap on the other.
const overviewGap = 14

func (a *App) overviewPane(c *ui.Context) {
	t := c.Theme()
	d := a.overviewData()
	width, _ := c.Size()
	contentW := width - a.sidebarWidth() - 2*20
	ui.Scroll(c.Key("overview-scroll")).Grow(1).MinHeight(0).Label("总览内容").Children(func() {
		ui.Column(c).Padding(16, 20, 20, 20).Gap(overviewGap).Children(func() {
			if a.sessions.err != "" {
				ui.Text(c, a.sessions.err).FontSize(12).TextColor(t.Warning)
			}
			a.metricStrip(c, d, contentW < 640)
			if contentW < 760 {
				a.dailyChart(c, d)
				a.overviewIssues(c, d)
				a.agentUsage(c, d)
				a.recentSessions(c, d)
			} else {
				ui.Row(c).Gap(overviewGap).AlignItems(ui.Start).Children(func() {
					ui.Column(c).Grow(3).MinWidth(0).Gap(overviewGap).Children(func() {
						a.dailyChart(c, d)
						a.agentUsage(c, d)
					})
					ui.Column(c).Grow(2).MinWidth(0).Gap(overviewGap).Children(func() {
						a.overviewIssues(c, d)
						a.recentSessions(c, d)
					})
				})
			}
			a.overviewNotes(c, d)
		})
	})
}

// overviewCard is a titled card. Its header holds the title, an optional
// muted note, and a trailing control such as a link or legend.
func overviewCard(c *ui.Context, title, note string, trailing, body func()) {
	t := c.Theme()
	card(c).Padding(12, 14, 12, 14).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Height(22).Children(func() {
			overviewTitle(c, title)
			if note != "" {
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).SingleLine().MinWidth(0).Shrink(1)
			}
			ui.Spacer(c)
			if trailing != nil {
				trailing()
			}
		})
		body()
	})
}

func (a *App) overviewNotes(c *ui.Context, d *overviewData) {
	t := c.Theme()
	var notes []string
	if d.summary.Incomplete {
		notes = append(notes, "部分日志或用量暂不支持，Token 为已识别部分")
	}
	notes = append(notes, "速率与时长均为估算", "缓存命中 = 缓存读取 / 全部输入 Token")
	ui.Column(c).Gap(4).Padding(0, 2).Children(func() {
		ui.Text(c, strings.Join(notes, " · ")).FontSize(10).TextColor(t.TextMuted).MaxLines(2)
		if n := len(a.sessions.agentProblems); n > 0 {
			ui.Text(c, fmt.Sprintf("自定义 Agent 配置有 %d 个问题，相关 Agent 未加载，悬停查看", n)).FontSize(10).TextColor(t.Warning).Tooltip(strings.Join(a.sessions.agentProblems, "\n"))
		}
		snap := a.sessions.snapshot
		if snap == nil {
			return
		}
		if snap.Partial && a.sessions.loading {
			ui.Text(c, "正在读取剩余日志，数字会随读取进度更新").FontSize(10).TextColor(t.TextMuted)
		} else if snap.Partial {
			ui.Text(c, "部分日志未能读取，当前统计不完整；重新扫描可再次尝试").FontSize(10).TextColor(t.Warning)
		}
		for _, source := range snap.Sources {
			if source.Files > 0 && !source.Supported {
				ui.Text(c, sessionAgentName(source.Agent)+"："+source.Reason).FontSize(10).TextColor(t.TextMuted)
			}
		}
		// Per-file details are diagnostics: summarize them, keep the list
		// one hover away instead of printing raw file names in the page.
		if n := len(snap.Warnings); n > 0 {
			ui.Text(c, fmt.Sprintf("%d 条读取提示（跳过的无效或超长记录等），悬停查看", n)).FontSize(10).TextColor(t.TextMuted).Tooltip(strings.Join(snap.Warnings[:min(12, n)], "\n"))
		}
	})
}

// metricStrip is one card with four figures split by hairlines; narrow panes
// wrap it into two rows.
func (a *App) metricStrip(c *ui.Context, d *overviewData, narrow bool) {
	card(c).Children(func() {
		if narrow {
			a.metricRow(c, d, 0, 2)
			ui.Box(c).Height(1).Background(c.Theme().Border)
			a.metricRow(c, d, 2, 4)
		} else {
			a.metricRow(c, d, 0, 4)
		}
	})
}

func (a *App) metricRow(c *ui.Context, d *overviewData, start, end int) {
	t := c.Theme()
	s := d.summary
	delta := s.Sessions - d.previous.Sessions
	comparison := "全部本地会话"
	if d.period < 3 {
		span := []string{"昨天", "前 7 天", "前 30 天"}[d.period]
		switch {
		case d.previous.Sessions == 0:
			comparison = span + "没有会话"
		case delta == 0:
			comparison = "与" + span + "持平"
		default:
			comparison = fmt.Sprintf("较%s %+d", span, delta)
		}
	}
	rate := "暂不支持"
	rateNote := "没有可计算的输出速率"
	if s.OutputRate.Samples > 0 {
		rate = fmt.Sprintf("≈ %.0f tok/s", s.OutputRate.Median)
		rateNote = fmt.Sprintf("中位数 · P90 %.0f · %d 次请求", s.OutputRate.P90, s.OutputRate.Samples)
	}
	metrics := []struct{ label, value, note string }{
		{"会话", fmt.Sprint(s.Sessions), comparison},
		{"Token", tokenNumber(s.Tokens.Total()), fmt.Sprintf("输入 %s · 输出 %s", tokenNumber(s.Tokens.InputTotal()), tokenNumber(s.Tokens.Output))},
		{"输出速率", rate, rateNote},
		{"耗时异常", fmt.Sprintf("%d 个会话", len(d.anomalies)), fmt.Sprintf("共 %d 处 · 慢步骤 / 重试 / 中断", len(s.Anomalies))},
	}
	if s.Requests > 0 && s.UnmeteredRequests == s.Requests && s.Tokens.Total() == 0 {
		metrics[1].value = "暂不支持"
		metrics[1].note = "日志没有受支持的用量字段"
	}
	ui.Row(c).Gap(0).Children(func() {
		for i := start; i < end; i++ {
			m := metrics[i]
			var box ui.Element
			if i == 3 {
				box = ui.Button(c.Key(m.label), "").Column().Grow(1).Basis(0).MinWidth(0).Padding(10, 14).Gap(3).Justify(ui.Start).AlignItems(ui.Stretch).Border(0, ui.Transparent).Background(ui.Transparent).Radius(0).Label("查看耗时异常会话").FontWeight(400).Disabled(len(d.anomalies) == 0)
			} else {
				box = ui.Column(c.Key(m.label)).Grow(1).Basis(0).MinWidth(0).Padding(10, 14).Gap(3)
			}
			if i > start {
				box.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
			}
			box.Children(func() {
				ui.Text(c, m.label).FontSize(11).TextColor(t.TextMuted)
				v := number(c, m.value).FontSize(20).FontWeight(600)
				if i == 3 && len(d.anomalies) > 0 {
					v.TextColor(t.Warning)
				}
				ui.Text(c, m.note).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(m.note)
			})
			if i == 3 && box.Clicked() {
				a.openSessionAnomalies(d)
			}
		}
	})
}

// listRow is a borderless, full-width row button separated by a hairline,
// the dense list style of the overview cards.
func listRow(c *ui.Context, key any, label string, last bool) ui.Element {
	t := c.Theme()
	b := ui.Button(c.Key(key), "").Label(label).Padding(7, 2).Gap(8).AlignItems(ui.Center).Justify(ui.Start).Border(0, ui.Transparent).Background(ui.Transparent).Radius(0).FontWeight(400)
	if !last {
		b.BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
	}
	return b
}

func (a *App) overviewIssues(c *ui.Context, d *overviewData) {
	t := c.Theme()
	type issueRow struct {
		count       int
		title, note string
		kind        int
	}
	var rows []issueRow
	for _, r := range []issueRow{
		{a.countKind(skill.IssueBroken), "个 skill 断链", "检查链接目标，在问题页修复", 1},
		{a.countKind(skill.IssueOrphan), "个共享 skill 未挂到 Agent", "在共享池选择需要使用的 Agent", 5},
		{a.countKind(skill.IssueEmpty), "个 skill 目录为空", "在问题页检查并移走空目录", 4},
	} {
		if r.count > 0 {
			rows = append(rows, r)
		}
	}
	// The sessions with the most anomalies answer "which ones" in place.
	var top []session.Session
	for _, s := range d.recent {
		if d.anomalyCount[sessionID(s)] > 0 {
			top = append(top, s)
		}
	}
	slices.SortStableFunc(top, func(x, y session.Session) int { return d.anomalyCount[sessionID(y)] - d.anomalyCount[sessionID(x)] })
	top = top[:min(4, len(top))]
	overviewCard(c, "需要处理", "", func() {
		overviewLink(c, "查看全部问题", func() { a.page = "issues"; a.query = "" })
	}, func() {
		if len(rows) == 0 && len(d.anomalies) == 0 {
			ui.Text(c, "目前没有需要处理的事").FontSize(12).TextColor(t.TextMuted).Padding(4, 0)
			return
		}
		ui.Column(c).Children(func() {
			for i, r := range rows {
				title := fmt.Sprintf("%d %s", r.count, r.title)
				b := listRow(c, "issue:"+title, title, i == len(rows)-1 && len(d.anomalies) == 0)
				b.Children(func() {
					ui.Icon(c, glyph("alert")).FontSize(12).TextColor(t.Warning)
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, title).FontSize(12).SingleLine()
						ui.Text(c, r.note).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(r.note)
					})
				})
				if b.Clicked() {
					a.page = "issues"
					a.filter = r.kind
					a.query = ""
				}
			}
			if len(d.anomalies) == 0 {
				return
			}
			title := fmt.Sprintf("%d 个会话有耗时异常", len(d.anomalies))
			b := listRow(c, "issue:anomalies", title, len(top) == 0)
			b.Children(func() {
				ui.Icon(c, glyph("alert")).FontSize(12).TextColor(t.Warning)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					ui.Text(c, title).FontSize(12).SingleLine()
					ui.Text(c, "慢步骤、重试和中断；在会话中逐轮查看").FontSize(10).TextColor(t.TextMuted).SingleLine()
				})
			})
			if b.Clicked() {
				a.openSessionAnomalies(d)
			}
			for i, s := range top {
				label := recentLabel(s)
				row := listRow(c, "anomaly:"+sessionID(s), label+" · 耗时异常", i == len(top)-1).Padding(6, 2, 6, 22)
				row.Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(agentColor(s.Agent, t.Dark))
					ui.Text(c, label).FontSize(11).Grow(1).MinWidth(0).SingleLine().Tooltip(label)
					number(c, s.End.Local().Format("01/02")).FontSize(10).TextColor(t.TextMuted)
					number(c, fmt.Sprintf("%d 处", d.anomalyCount[sessionID(s)])).FontSize(11).TextColor(t.Warning).Width(44).TextAlign(ui.End)
				})
				if row.Clicked() {
					a.openSession(sessionID(s), 1)
				}
			}
		})
	})
}

func (a *App) recentSessions(c *ui.Context, d *overviewData) {
	t := c.Theme()
	overviewCard(c, "最近会话", "", func() {
		overviewLink(c, "查看全部会话", func() { a.page = "sessions" })
	}, func() {
		if len(d.recent) == 0 {
			ui.Text(c, "当前范围没有本地会话").FontSize(12).TextColor(t.TextMuted).Padding(4, 0)
			return
		}
		shown := d.recent[:min(6, len(d.recent))]
		ui.Column(c).Children(func() {
			for i, s := range shown {
				warning := d.anomalies[sessionID(s)]
				row := listRow(c, s.Path, recentRowLabel(s), i == len(shown)-1).Column().Gap(3).AlignItems(ui.Stretch)
				row.Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, recentLabel(s)).FontSize(12).Grow(1).MinWidth(0).SingleLine().Tooltip(recentLabel(s))
						if warning {
							ui.Icon(c, glyph("alert")).FontSize(11).TextColor(t.Warning).Label("有耗时异常")
						}
						label := "—"
						if !s.Start.IsZero() && s.End.Sub(s.Start) >= time.Second {
							label = "≈ " + session.FormatDuration(s.End.Sub(s.Start))
						}
						v := number(c, label).FontSize(11).TextColor(t.TextMuted).Tooltip("会话跨度估算，包含轮间等待")
						if warning {
							v.TextColor(t.Warning)
						}
					})
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(agentColor(s.Agent, t.Dark))
						meta := sessionAgentName(s.Agent)
						if strings.TrimSpace(s.Title) != "" && s.CWD != "" {
							meta += " · " + filepath.Base(s.CWD)
						}
						meta += " · " + s.End.Local().Format("01/02 15:04")
						number(c, meta).FontSize(10).TextColor(t.TextMuted).Grow(1).MinWidth(0)
					})
				})
				if row.Clicked() {
					a.openSession(sessionID(s), 0)
				}
			}
		})
	})
}
