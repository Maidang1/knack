package app

import (
	"cmp"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/insight"
	"knack/internal/session"
)

// ---- 摩擦 ----

func (a *App) insightFriction(c *ui.Context) {
	r := a.insightData().Friction
	failRate := 0.0
	if r.ToolCalls > 0 {
		failRate = float64(r.FailedCalls) / float64(r.ToolCalls)
	}
	loops := fmt.Sprint(len(r.Loops))
	loopNote := fmt.Sprintf("同一调用连续 ≥%d 次", insight.LoopRepeats)
	if !r.RepeatKnown {
		loops, loopNote = "暂不支持", "日志没有工具输入，或缓存来自旧版本"
	}
	figures(c, []metric{
		{label: "工具调用", value: groupDigits(r.ToolCalls), note: fmt.Sprintf("%s 失败 / 中断 / 超时", percent(float64(r.FailedCalls), float64(r.ToolCalls))), warn: failRate > .05},
		{label: "被中断的轮次", value: groupDigits(r.InterruptedTurns), note: fmt.Sprintf("共 %d 轮 · %s", r.Turns, percent(float64(r.InterruptedTurns), float64(r.Turns))), warn: r.InterruptedTurns > 0},
		{label: "等待确认", value: session.FormatDuration(r.PermissionTime), note: fmt.Sprintf("%d 次人工确认工具调用", r.PermissionWaits)},
		{label: "疑似循环", value: loops, note: loopNote, warn: len(r.Loops) > 0},
		{label: "API 错误", value: groupDigits(r.APIErrors), note: fmt.Sprintf("%d 次推断的重试 · %d 次上下文压缩", r.Retries, r.Compactions), warn: r.APIErrors > 0},
	})
	width, _ := c.Size()
	wide := width-a.sidebarWidth() >= 1000
	tools := func() { a.frictionTools(c, r) }
	side := func() {
		a.frictionLoops(c, r)
		a.frictionPermissions(c, r)
	}
	if wide {
		ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(28).Children(tools)
			ui.Column(c).Width(400).Shrink(0).Gap(28).Children(side)
		})
	} else {
		tools()
		side()
	}
	note(c, "失败按工具结果的错误标记统计，中断与超时来自 Claude Code 的工具结果；其他 Agent 的日志可能不记录。循环按工具名和输入指纹（不保存输入内容）判断。")
}

func (a *App) frictionTools(c *ui.Context, r insight.FrictionReport) {
	t := c.Theme()
	ui.Column(c).Label("工具").Children(func() {
		sectionHead(c, "工具", "", nil)
		if len(r.Tools) == 0 {
			ui.Text(c, "这段时间没有工具调用").FontSize(13).TextColor(t.TextMuted)
			return
		}
		widths := []float32{52, 52, 44, 44, 44, 52}
		tableHead(c, "按失败次数排序", []string{"调用", "失败率", "中断", "超时", "重复", "确认"}, widths)
		for _, tool := range r.Tools[:min(20, len(r.Tools))] {
			bad := tool.Failed + tool.Interrupted + tool.TimeOut
			ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).
				Label(fmt.Sprintf("%s %d 次调用，%d 次失败", tool.Name, tool.Calls, bad)).Children(func() {
				ui.Text(c, tool.Name).FontSize(13).Grow(1).MinWidth(0).SingleLine().Tooltip(tool.Name)
				cell(c, groupDigits(tool.Calls), widths[0], true).TextColor(t.TextMuted)
				rate := cell(c, percent(float64(bad), float64(tool.Calls)), widths[1], true)
				if tool.FailureRate() > .1 && bad >= 3 {
					rate.TextColor(t.Warning)
				}
				for i, n := range []int{tool.Interrupted, tool.TimeOut, tool.Repeats, tool.PermissionWaits} {
					e := cell(c, countOrDash(n), widths[2+i], true)
					if n == 0 {
						e.TextColor(token(t, "ink-3"))
					}
				}
			})
		}
	})
}

func countOrDash(n int) string {
	if n == 0 {
		return "—"
	}
	return groupDigits(n)
}

func (a *App) frictionLoops(c *ui.Context, r insight.FrictionReport) {
	t := c.Theme()
	ui.Column(c).Label("疑似循环").Children(func() {
		link := ""
		if len(r.Loops) > 8 {
			link = "全部 " + fmt.Sprint(len(r.Loops))
			if a.insights.allLoops {
				link = "收起"
			}
		}
		sectionHead(c, "疑似循环", link, func() { a.insights.allLoops = !a.insights.allLoops })
		if !r.RepeatKnown {
			ui.Text(c, "需要重新读取日志才能识别：点侧栏的“重新扫描”。").FontSize(13).TextColor(t.TextMuted)
			return
		}
		if len(r.Loops) == 0 {
			ui.Text(c, "没有发现连续重复的工具调用").FontSize(13).TextColor(t.TextMuted)
			return
		}
		loops := r.Loops
		if !a.insights.allLoops {
			loops = loops[:min(8, len(loops))]
		}
		for i, l := range loops {
			title := l.Title
			if strings.TrimSpace(title) == "" {
				title = sessionAgentName(l.Agent) + " · " + cmp.Or(filepath.Base(l.CWD), l.SessionID)
			}
			row := listRow(c, fmt.Sprintf("loop:%d", i), title)
			row.Children(func() {
				ui.Icon(c, glyph("refresh")).FontSize(13).TextColor(t.Warning).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, title).FontSize(13).SingleLine().Tooltip(title)
					ui.Text(c, fmt.Sprintf("第 %d 轮 · %s · %s", l.TurnNumber, l.Tool, l.At.Local().Format("01/02 15:04"))).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
				})
				number(c, fmt.Sprintf("×%d", l.Repeats)).FontSize(13).FontWeight(600).TextColor(t.Warning).Shrink(0)
			})
			if row.Clicked() {
				a.openSession(string(l.Agent) + ":" + l.SessionID)
			}
		}
	})
}

func (a *App) frictionPermissions(c *ui.Context, r insight.FrictionReport) {
	t := c.Theme()
	cfg := a.configFor(a.insights.configDir)
	hints := insight.PermissionHints(r.Tools, cfg.Permissions, 3)
	ui.Column(c).Gap(8).Label("常需确认的工具").Children(func() {
		overviewTitle(c, "常需确认的工具")
		if len(hints) == 0 {
			ui.Text(c, "没有反复需要人工确认的工具").FontSize(13).TextColor(t.TextMuted)
			return
		}
		for _, h := range hints[:min(8, len(hints))] {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, h.Tool).FontSize(13).Grow(1).MinWidth(0).SingleLine()
				state := "没有 allow 规则"
				color := t.Warning
				if h.Covered {
					state, color = "已有部分规则", token(t, "ink-3")
				}
				ui.Text(c, state).FontSize(11.5).TextColor(color).Shrink(0)
				number(c, fmt.Sprintf("%d 次", h.Waits)).FontSize(13).Width(52).TextAlign(ui.End).Shrink(0)
			})
		}
		note(c, "确认来自 Claude Code 记录的“用户临时允许”。常用且安全的命令可以写进 settings.json 的 permissions.allow，例如 Bash(go test:*)。规则对照的是「配置」页所选目录的设置。")
	})
}

// ---- 项目 ----

func (a *App) insightProjects(c *ui.Context) {
	t := c.Theme()
	list := a.insightData().Projects
	if len(list) == 0 {
		emptyState(c, "项目", "folder", t.TextMuted, "这段时间没有会话", "项目按会话的工作目录归类。", nil)
		return
	}
	totalCost := 0.0
	for _, p := range list {
		totalCost += p.Cost
	}
	top := list[0]
	figures(c, []metric{
		{label: "项目", value: groupDigits(len(list)), note: "按会话的工作目录归类"},
		{label: "花费最多", value: top.Name, note: fmt.Sprintf("%s · 占 %s", dollars(top.Cost), percent(top.Cost, totalCost))},
		{label: "估算费用合计", value: dollars(totalCost), note: "未定价模型不计入"},
	})
	shown := list
	if !a.insights.allProject {
		shown = list[:min(20, len(list))]
	}
	ui.Column(c).Label("项目列表").Children(func() {
		link := ""
		if len(list) > 20 {
			link = fmt.Sprintf("全部 %d", len(list))
			if a.insights.allProject {
				link = "收起"
			}
		}
		sectionHead(c, "项目", link, func() { a.insights.allProject = !a.insights.allProject })
		widths := []float32{44, 40, 60, 64, 52, 72}
		tableHead(c, "目录", []string{"会话", "轮次", "Token", "费用", "失败率", "开局中位"}, widths)
		peak := top.Cost
		for i, p := range shown {
			row := listRow(c, fmt.Sprintf("project:%d", i), p.Name+" · "+shortPath(a.home, p.Path))
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, p.Name).FontSize(13).SingleLine().Tooltip(p.Path)
					meta := []string{strings.Join(p.Agents, "、")}
					if !p.Last.IsZero() {
						meta = append(meta, "最近 "+ago(time.Now(), p.Last))
					}
					if len(p.Skills) > 0 {
						var names []string
						for _, s := range p.Skills[:min(3, len(p.Skills))] {
							names = append(names, s.Name)
						}
						meta = append(meta, "常用技能 "+strings.Join(names, "、"))
					}
					ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
				})
				shareBar(c, p.Cost, peak, token(t, "ink-2"), 60)
				cell(c, groupDigits(p.Sessions), widths[0], true).TextColor(t.TextMuted)
				cell(c, groupDigits(p.Turns), widths[1], true).TextColor(t.TextMuted)
				cell(c, tokenNumber(p.Tokens.Total()), widths[2], true).TextColor(t.TextMuted)
				if p.Cost == 0 && p.UnpricedTokens > 0 {
					cell(c, "未定价", widths[3], true).TextColor(token(t, "ink-3"))
				} else {
					cell(c, dollars(p.Cost), widths[3], true).FontWeight(600)
				}
				rate := cell(c, percent(float64(p.FailedCalls), float64(p.ToolCalls)), widths[4], true)
				if p.ToolCalls >= 20 && float64(p.FailedCalls)/float64(p.ToolCalls) > .05 {
					rate.TextColor(t.Warning)
				}
				cell(c, tokenNumber(p.MedianBaseline), widths[5], true).TextColor(t.TextMuted)
			})
			if row.Clicked() && p.Path != "" {
				a.openProjectSessions(p.Path)
			}
			row.ContextMenu(func(m *ui.Menu) {
				if p.Path == "" {
					return
				}
				if m.Item("查看会话").Chosen() {
					a.openProjectSessions(p.Path)
				}
				if m.Item("查看配置").Chosen() {
					a.insights.configDir = p.Path
					a.insights.tab = tabConfig
				}
				if m.Item("在访达中显示").Chosen() {
					a.reveal(p.Path)
				}
			})
		}
	})
	note(c, "点一行查看该目录的会话；右键可查看这个目录生效的配置。开局中位 = 该目录会话首个请求输入的中位数。")
}

// openProjectSessions lists the sessions started in dir.
func (a *App) openProjectSessions(dir string) {
	a.page = "sessions"
	a.sessions.open = false
	a.sessions.replayOpen = false
	a.sessions.query = filepath.Clean(dir)
	a.query = a.sessions.query
	a.sessions.anomaliesOnly = false
	a.sessions.browser = nil
}
