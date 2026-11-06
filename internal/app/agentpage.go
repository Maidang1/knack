package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/skill"
)

func (a *App) agentPageRoot() (skill.Root, bool) {
	root, ok := a.rootByPage(a.page)
	return root, ok && (root.Role == skill.RoleAgent || root.Role == skill.RoleBuiltin)
}

// The page supplies the Agent scope without overwriting the global filter.
func (a *App) sessionAgentFilter() string {
	if root, ok := a.agentPageRoot(); ok {
		return sessionAgentName(session.Agent(agentKey(a.home, root.Path)))
	}
	return a.sessions.agent
}

func (a *App) agentTabs(c *ui.Context) {
	t := c.Theme()
	tabs := ui.TabsBase(c.Key("agent-tabs:"+a.page), &a.state(a.page).agentTab, 3)
	tabs.List.Gap(4).Label("Agent 页面标签").Children(func() {
		for i, name := range []string{"技能", "会话", "用量"} {
			tab := tabs.Tab(i).Padding(4, 8).Radius(4).Border(1, ui.Transparent).Label("Agent " + name)
			if a.state(a.page).agentTab == i {
				tab.Border(1, t.Accent).Background(fade(t.Accent, 24))
			}
			tab.Children(func() { ui.Text(c, name).FontSize(12) })
		}
	})
	if tabs.List.Changed() {
		c.Invalidate()
	}
}

func logSupport(agent session.Agent) string {
	info := session.LookupAgent(agent)
	if info.Unsupported == "" {
		return ""
	}
	name := info.Name
	if name == string(agent) {
		name = "该 Agent"
	}
	return name + " " + info.Unsupported + "，会话与用量暂不支持"
}
func skillSupport(agent session.Agent) string {
	if reason := logSupport(agent); reason != "" {
		return reason
	}
	if info := session.LookupAgent(agent); !info.Skills {
		return cmp.Or(info.SkillNote, info.Name+" 本地日志没有可靠的 Skill 调用记录，Skill 使用情况暂不支持")
	}
	return ""
}

func (a *App) agentUsagePane(c *ui.Context, root skill.Root) {
	agent := session.Agent(agentKey(a.home, root.Path))
	t := c.Theme()
	d := a.overviewData()
	ui.Scroll(c.Key("agent-usage:" + root.ID)).Grow(1).MinHeight(0).Label("Agent 用量内容").Children(func() {
		ui.Column(c).Padding(20).Gap(20).Children(func() {
			ui.Text(c, strings.TrimSuffix(root.Name, " 自带")+" · 用量").FontSize(18).FontWeight(600)
			if reason := logSupport(agent); reason != "" {
				ui.Text(c, reason).FontSize(13).TextColor(t.TextMuted)
				return
			}
			if a.sessions.err != "" {
				ui.Text(c, a.sessions.err).FontSize(11).TextColor(t.Warning)
			}
			sum := d.summary
			known := sum.Requests > sum.UnmeteredRequests
			info := session.LookupAgent(agent)
			reason := cmp.Or(info.UsageNote, "当前范围没有可识别的请求用量")
			if info.TimingNote != "" {
				ui.Text(c, info.TimingNote).FontSize(11).TextColor(t.TextMuted)
			}
			if sum.Incomplete {
				ui.Text(c, "当前统计不完整；数值仅包含已识别的本地记录。").FontSize(11).TextColor(t.Warning)
			}
			ui.Column(c).Gap(10).Label("Agent Token 明细").Children(func() {
				overviewTitle(c, "Token")
				if !known {
					ui.Text(c, "暂不支持 · "+reason).FontSize(12).TextColor(t.TextMuted)
					return
				}
				ui.Text(c, tokenNumber(sum.Tokens.Total())+" Token").FontSize(24).FontWeight(600)
				for _, group := range [][]struct {
					name  string
					value int64
				}{
					{{"输入", sum.Tokens.Input}, {"输出", sum.Tokens.Output}},
					{{"缓存读取", sum.Tokens.CacheRead}, {"缓存写入", sum.Tokens.CacheWrite}},
				} {
					ui.Row(c).Gap(20).Children(func() {
						for _, m := range group {
							ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
								ui.Text(c, m.name).FontSize(11).TextColor(t.TextMuted)
								number(c, tokenNumber(m.value)).FontSize(17)
							})
						}
					})
				}
			})
			f := overviewFilter(a.sessions.period, a.sessionAgentFilter(), time.Now())
			var models []modelSpeed
			rates := map[string][]float64{}
			tools := map[string]*callCount{}
			for _, record := range a.sessionActivity().records {
				if record.session.Agent != agent {
					continue
				}
				for _, turn := range record.session.Turns {
					for _, r := range turn.Requests {
						if !filterTime(f, r.End) {
							continue
						}
						if v, ok := r.OutputRate(); ok {
							name := r.Model
							if name == "" {
								name = "未知模型"
							}
							rates[name] = append(rates[name], v)
						}
					}
					for _, tool := range turn.Tools {
						if !filterTime(f, tool.Start) {
							continue
						}
						name := supportedText(tool.Name)
						item := tools[name]
						if item == nil {
							item = &callCount{name: name}
							tools[name] = item
						}
						item.count++
						item.child = item.child || record.child
					}
				}
			}
			for _, g := range sum.ByModel {
				v := rates[g.Name]
				slices.Sort(v)
				p10 := float64(0)
				if len(v) > 0 {
					p10 = percentile(v, .1)
				}
				models = append(models, modelSpeed{name: g.Name, requests: g.Requests, output: g.Tokens.Output, usageKnown: g.Requests > g.UnmeteredRequests, samples: g.OutputRate.Samples, p10: p10, median: g.OutputRate.Median, p90: g.OutputRate.P90})
			}
			a.sessionModelSpeeds(c, &sessionDetailData{models: models})
			if known {
				a.dailyChart(c, d)
			} else {
				ui.Column(c).Gap(6).Children(func() {
					overviewTitle(c, "每日 Token")
					ui.Text(c, "暂不支持 · "+reason).FontSize(11).TextColor(t.TextMuted)
				})
			}
			var skills []callCount
			for _, sk := range sum.Skills {
				skills = append(skills, callCount{name: sk.Name, count: sk.Calls})
			}
			ui.Column(c).Label("Skill 调用排行").Children(func() {
				a.sessionCalls(c, "Skill 调用排行", skills, skillSupport(agent) != "")
				if reason := skillSupport(agent); reason != "" {
					ui.Text(c, reason).FontSize(11).TextColor(t.TextMuted)
				}
			})
			a.sessionCalls(c, "工具调用排行", sortedCalls(tools), false)
			ui.Text(c, "Token 与总览使用同一口径；请求按完成日期、工具和 Skill 按调用日期归属。子 Agent 仅计一次，速率为估算。").FontSize(10).TextColor(t.TextMuted)
		})
	})
}

func filterTime(f session.Filter, at time.Time) bool {
	if at.IsZero() {
		return f.From.IsZero() && f.To.IsZero()
	}
	return (f.From.IsZero() || !at.Before(f.From)) && (f.To.IsZero() || at.Before(f.To))
}

// Usage matching is by recorded invocation name, not an inferred file path.
func skillNames(e skill.Entry) []string {
	names := []string{e.Name}
	if e.FrontName != "" && e.FrontName != e.Name {
		names = append(names, e.FrontName)
	}
	return names
}
func skillRange(days int, now time.Time) session.Filter {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return session.Filter{From: today.AddDate(0, 0, -days+1), To: today.AddDate(0, 0, 1), Location: now.Location()}
}

func (a *App) skillListUsage(e skill.Entry) string {
	agent := session.Agent(agentKey(a.home, e.RootPath))
	if skillSupport(agent) != "" {
		return "调用暂不支持"
	}
	usage := a.sessionActivity().skillUsage(agent, skillNames(e), skillRange(30, time.Now()))
	return fmt.Sprintf("近 30 天 %d 次", usage.calls)
}
