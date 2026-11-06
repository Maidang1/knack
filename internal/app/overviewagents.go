package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"knack/internal/skill"
)

func (a *App) agentUsage(c *ui.Context, d *overviewData) {
	t := c.Theme()
	overviewCard(c, "Agent 使用情况", "按 Token 排序", nil, func() {
		// Numeric columns share the width evenly, so values line up under
		// their headers without a dead gap. The model, the only free-length
		// value, gets an aligned second line.
		const agentW = 92
		column := func(text string, end bool) ui.Element {
			e := number(c, text).Grow(1).Basis(0).MinWidth(0)
			if end {
				e.TextAlign(ui.End)
			}
			return e
		}
		ui.Column(c).Children(func() {
			ui.Row(c).Gap(8).Padding(0, 0, 6, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, "Agent").Width(agentW).Shrink(0).FontSize(10).TextColor(t.TextMuted)
				for i, h := range []string{"会话", "Token", "缓存命中", "输出速率", "最近活跃"} {
					column(h, i == 4).FontSize(10).TextColor(t.TextMuted)
				}
			})
			for _, agent := range d.agents {
				sum := agent.summary
				empty := sum.Sessions == 0 && sum.Requests == 0
				cell := func(value string, supported bool, tip string, end bool) {
					e := column(value, end).FontSize(12).Tooltip(tip)
					if !supported || empty {
						e.FontSize(11).TextColor(t.TextMuted)
					}
				}
				ui.Column(c.Key(string(agent.id))).Gap(3).Padding(7, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Row(c).Width(agentW).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Box(c).Size(6, 6).Radius(3).Background(agentColor(agent.id, t.Dark))
							ui.Text(c, sessionAgentName(agent.id)).FontSize(12).FontWeight(600).SingleLine()
						})
						cell(fmt.Sprint(sum.Sessions), true, "主会话数，不含子 Agent", false)
						tokens, tokensOK := tokenNumber(sum.Tokens.Total()), true
						if sum.UnmeteredRequests > 0 && sum.Tokens.Total() == 0 {
							tokens, tokensOK = "暂不支持", false
						}
						cell(tokens, tokensOK, "已识别 Token，不含缺失用量的请求", false)
						cache, cacheOK := "暂不支持", sum.CacheHitKnown
						if cacheOK {
							cache = fmt.Sprintf("%.0f%%", 100*sum.CacheHit)
						} else if empty {
							cache = "—"
						}
						cell(cache, cacheOK, "缓存读取 / 全部输入 Token", false)
						rate, rateOK := "暂不支持", sum.OutputRate.Samples > 0
						if rateOK {
							rate = fmt.Sprintf("≈ %.0f tok/s", sum.OutputRate.Median)
						} else if empty {
							rate = "—"
						}
						cell(rate, rateOK, "输出 Token / 估算生成时间，中位数", false)
						active := "—"
						if !agent.last.IsZero() {
							active = agent.last.Local().Format("01/02 15:04")
						}
						column(active, true).FontSize(11).TextColor(t.TextMuted)
					})
					note := "当前范围没有会话"
					if len(sum.ByModel) > 0 {
						note = sum.ByModel[0].Name
						if sum.Tokens.Total() > 0 {
							note += fmt.Sprintf(" · %.0f%% Token", 100*float64(sum.ByModel[0].Tokens.Total())/float64(sum.Tokens.Total()))
						}
						if len(sum.ByModel) > 1 {
							note += fmt.Sprintf(" · 另 %d 个模型", len(sum.ByModel)-1)
						}
					}
					ui.Text(c, note).FontSize(10).TextColor(t.TextMuted).SingleLine().Padding(0, 0, 0, 12).Tooltip("主力模型 " + note)
				})
			}
			ui.Row(c).Gap(8).Padding(8, 0, 0, 0).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Width(agentW).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Border(1, t.Border)
					ui.Text(c, "Cursor").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Text(c, "不在本地保存会话日志，暂时无法统计").FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			})
			if a.hasUntrackedSessionAgents() {
				ui.Text(c, "其他 Agent：尚未接入本地会话格式，暂不支持").FontSize(10).TextColor(t.TextMuted).Padding(4, 0, 0, 0)
			}
		})
	})
}

func (a *App) hasUntrackedSessionAgents() bool {
	for _, r := range a.roots(skill.RoleAgent) {
		key := agentKey(a.home, r.Path)
		if key != "claude" && key != "codex" && key != "factory" && key != "cursor" {
			return true
		}
	}
	return false
}
