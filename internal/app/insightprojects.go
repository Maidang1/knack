package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// ---- 项目 ----

func (a *App) insightProjects(c *ui.Context) {
	t := c.Theme()
	list := a.insightData().Projects
	if len(list) == 0 {
		emptyState(c, "项目", "folder", "这段时间没有会话", "项目按会话的工作目录归类。", nil)
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
					withTip(c, ui.Text(c, p.Name).FontSize(13).SingleLine(), p.Path)
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
			contextMenu(c, row, func(m *menu) {
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
	a.sessions.browser = nil
}

func countOrDash(n int) string {
	if n == 0 {
		return "—"
	}
	return groupDigits(n)
}
