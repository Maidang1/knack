package app

import (
	"cmp"
	"fmt"
	"path/filepath"
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

// agentTabs is the underline tab row under an Agent page's title bar.
func (a *App) agentTabs(c *ui.Context, root skill.Root) {
	t := c.Theme()
	agent := session.Agent(agentKey(a.home, root.Path))
	counts := []string{fmt.Sprint(len(a.agentEntries(root))), "", ""}
	if logSupport(agent) == "" {
		counts[1] = fmt.Sprint(len(a.sessionRows()))
	}
	ui.Row(c).Shrink(0).Padding(0, 32).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		underlineTabs(c, "agent-tabs:"+a.page, "Agent 页面标签", "Agent ", &a.state(a.page).agentTab, []string{"技能", "会话", "用量"}, counts)
	})
}

// agentMeta is the line beside an Agent's name: where it keeps its skills
// and how much there is.
func (a *App) agentMeta(c *ui.Context, root skill.Root) {
	t := c.Theme()
	parts := []string{shortPath(a.home, filepath.Dir(root.Path))}
	parts = append(parts, fmt.Sprintf("%d 个技能", len(a.agentEntries(root))))
	agent := session.Agent(agentKey(a.home, root.Path))
	if logSupport(agent) == "" {
		var last time.Time
		for _, s := range a.sessionRows() {
			if s.End.After(last) {
				last = s.End
			}
		}
		if !last.IsZero() {
			parts = append(parts, "最近活跃 "+ago(time.Now(), last))
		}
	}
	text := strings.Join(parts, " · ")
	ui.Text(c, text).FontSize(12).TextColor(token(t, "ink-3")).SingleLine().Shrink(1).MinWidth(0).Tooltip(root.Path)
}

// noLogs stands in for the sessions and usage of an Agent whose logs can
// not be read, and points back to what does work.
func (a *App) noLogs(c *ui.Context, root skill.Root, reason string) {
	t := c.Theme()
	info := session.LookupAgent(session.Agent(agentKey(a.home, root.Path)))
	name := strings.TrimSuffix(root.Name, " 自带")
	body := name + " 不在本机保存可读的会话记录，所以这里暂不支持会话和用量统计。技能管理不受影响。"
	emptyState(c, reason, "file-question", t.TextMuted, name+" "+info.Unsupported, body, func() {
		if outlineButton(c, "no-logs-skills", fmt.Sprintf("查看 %s 的 %d 个技能", name, len(a.agentEntries(root)))).Height(28).Clicked() {
			a.state(a.page).agentTab = 0
			c.Invalidate()
		}
	})
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
	a.usageBody(c, "agent-usage:"+root.ID, session.Agent(agentKey(a.home, root.Path)))
}

// usageBody is the usage of one Agent, or of every Agent the period's
// filter keeps when agent is "": tokens, where the active time went, model
// speeds and what was called.
func (a *App) usageBody(c *ui.Context, key string, agent session.Agent) {
	t := c.Theme()
	muted := token(t, "ink-3")
	color := token(t, "ink-2")
	if agent != "" {
		color = agentColor(agent, t.Dark)
	}
	d := a.overviewData()
	width, height := c.Size()
	sum := d.summary
	known := sum.Requests > sum.UnmeteredRequests
	reason := "当前范围没有可识别的请求用量"
	skillNote := ""
	if agent != "" {
		info := session.LookupAgent(agent)
		reason = cmp.Or(info.UsageNote, reason)
		skillNote = skillSupport(agent)
	}
	unsupported := agent != "" && logSupport(agent) != ""
	// head is the period's totals; it stays put while the lists below scroll.
	head := func() {
		if unsupported {
			ui.Text(c, logSupport(agent)).FontSize(13).TextColor(t.TextMuted)
			return
		}
		if a.sessions.err != "" {
			ui.Text(c, a.sessions.err).FontSize(11).TextColor(t.Warning)
		}
		if note := session.LookupAgent(agent).TimingNote; agent != "" && note != "" {
			ui.Text(c, note).FontSize(11).TextColor(muted)
		}
		a.tokenMix(c, sum, known, reason, color)
		a.usageTiming(c, agent, d)
	}
	models, skills, tools := a.agentCalls(agent, sum)
	left := func() {
		a.sessionModelSpeeds(c, &sessionDetailData{models: models}, color)
		if known {
			a.dailyChart(c, d)
		} else {
			ui.Column(c).Gap(6).Children(func() {
				overviewTitle(c, "每日 Token")
				ui.Text(c, "暂不支持 · "+reason).FontSize(11.5).TextColor(t.TextMuted)
			})
		}
	}
	right := func() {
		ui.Column(c).Label("Skill 调用排行").Children(func() {
			a.sessionCalls(c, "常用技能", skills, skillNote != "", t.Text)
			if skillNote != "" {
				ui.Text(c, skillNote).FontSize(11).TextColor(muted).MaxLines(3)
			}
		})
		ui.Column(c).Label("工具调用排行").Children(func() {
			a.sessionCalls(c, "常用工具", tools, false, sessionColor("phase-tool", t.Dark))
		})
		ui.Row(c).Padding(12, 14).Gap(8).Radius(8).Background(t.SurfaceHover).Children(func() {
			ui.Icon(c, glyph("info")).FontSize(13).TextColor(muted).Shrink(0)
			ui.Text(c, "Token 与首页同一口径：请求按完成日期、工具和 Skill 按调用日期归属。子 Agent 只计一次，速率为估算。技能按调用名统计，没出现在排行里不代表从未使用。").
				FontSize(11.5).LineHeight(1.5).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		})
	}
	wide := width-a.sidebarWidth() >= 900
	if wide && height >= usageSplitHeight && !unsupported {
		// The totals stay put; the two lists below scroll on their own.
		ui.Column(c).Grow(1).MinHeight(0).Label("Agent 用量内容").Children(func() {
			ui.Column(c).Shrink(0).Padding(24, 32, 20, 32).Gap(28).Children(head)
			ui.Row(c).Grow(1).MinHeight(0).Gap(32).Padding(0, 24, 0, 32).AlignItems(ui.Stretch).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Scroll(c.Key(key + ":left")).Grow(1).MinWidth(0).Label("用量左栏").Children(func() {
					ui.Column(c).Padding(20, 8, 36, 0).Gap(32).Children(left)
				})
				ui.Scroll(c.Key(key + ":right")).Width(388).Shrink(0).Label("用量右栏").Children(func() {
					ui.Column(c).Padding(20, 8, 36, 0).Gap(28).Children(right)
				})
			})
		})
		return
	}
	ui.Scroll(c.Key(key)).Grow(1).MinHeight(0).Label("Agent 用量内容").Children(func() {
		ui.Column(c).Padding(24, 32, 36, 32).Gap(28).Children(func() {
			head()
			if unsupported {
				return
			}
			if wide {
				ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(32).Children(left)
					ui.Column(c).Width(380).Shrink(0).Gap(28).Children(right)
				})
			} else {
				left()
				right()
			}
		})
	})
}

// Below this window height the fixed totals would leave the lists too
// little room, so the whole page scrolls instead.
const usageSplitHeight = 720

// tokenMix is the period's tokens as one number and one stacked bar, each
// kind in a shade of the Agent's color.
func (a *App) tokenMix(c *ui.Context, sum session.Summary, known bool, reason string, color ui.Color) {
	t := c.Theme()
	ui.Column(c).Gap(10).Label("Agent Token 明细").Children(func() {
		overviewTitle(c, "Token 构成 · "+periodText(a.sessions.period))
		if !known {
			ui.Text(c, "暂不支持 · "+reason).FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
			number(c, tokenNumber(sum.Tokens.Total())).FontSize(30).FontWeight(600).Label(tokenNumber(sum.Tokens.Total()) + " Token")
			if sum.CacheHitKnown {
				ui.Textf(c, "缓存命中 %.0f%%", sum.CacheHit*100).FontSize(12).TextColor(t.Success).Padding(0, 0, 4, 0)
			}
			ui.Spacer(c)
			cost := a.insightData().Cost
			if cost.Total > 0 || cost.UnpricedTokens.Total() > 0 {
				text := "估算 " + dollars(cost.Total)
				if cost.UnpricedTokens.Total() > 0 {
					text += " · " + tokenNumber(cost.UnpricedTokens.Total()) + " Token 未定价"
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 4, 0).Children(func() {
					number(c, text).FontSize(12).TextColor(t.TextMuted).Tooltip("按 Anthropic 官方 API 价格估算，未定价的模型不计入")
					overviewLink(c, "费用明细 →", func() {
						a.sessions.agent = cmp.Or(a.sessionAgentFilter(), allAgents)
						a.page = insightsPage
						a.insights.tab = 0
					})
				})
			}
		})
		deep := blend(color, ui.Hex("#000000"), 0.72)
		if t.Dark {
			deep = blend(color, ui.Hex("#FFFFFF"), 0.55)
		}
		parts := []struct {
			name  string
			value int64
			color ui.Color
		}{
			{"缓存读取", sum.Tokens.CacheRead, blend(color, t.Surface, 0.5)},
			{"输出", sum.Tokens.Output, color},
			{"缓存写入", sum.Tokens.CacheWrite, blend(color, t.Surface, 0.3)},
			{"输入", sum.Tokens.Input, deep},
		}
		total := float32(max(1, sum.Tokens.Total()))
		ui.Box(c).Height(18).Label("Token 构成条").Draw(func(p *ui.Painter, r ui.Rect) {
			x := r.X
			for _, part := range parts {
				if part.value == 0 {
					continue
				}
				w := max(2, float32(part.value)/total*r.W-2)
				p.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, part.color, 3)
				x += w + 2
			}
		})
		ui.Row(c).Gap(18).Wrap().Children(func() {
			for _, part := range parts {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(8, 8).Radius(2).Background(part.color)
					ui.Text(c, part.name).FontSize(12).TextColor(t.TextMuted)
					number(c, tokenNumber(part.value)).FontSize(12)
				})
			}
		})
	})
}

func periodText(period int) string {
	if period < 0 || period >= len(periodLabels) {
		return ""
	}
	if period == 0 || period == len(periodLabels)-1 {
		return periodLabels[period]
	}
	return "近 " + periodLabels[period]
}

// agentCalls are one Agent's per-model rates, Skills and tools in the
// period.
func (a *App) agentCalls(agent session.Agent, sum session.Summary) ([]modelSpeed, []callCount, []callCount) {
	f := overviewFilter(a.sessions.period, a.sessionAgentFilter(), time.Now())
	rates := map[string][]float64{}
	tools := map[string]*callCount{}
	if agent == "" {
		agent = f.Agent
	}
	for _, record := range a.sessionActivity().records {
		if agent != "" && record.session.Agent != agent {
			continue
		}
		for _, turn := range record.session.Turns {
			for _, r := range turn.Requests {
				if !filterTime(f, r.End) {
					continue
				}
				if v, ok := r.OutputRate(); ok {
					name := cmp.Or(r.Model, "未知模型")
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
	var models []modelSpeed
	for _, g := range sum.ByModel {
		v := rates[g.Name]
		slices.Sort(v)
		p10 := float64(0)
		if len(v) > 0 {
			p10 = percentile(v, .1)
		}
		models = append(models, modelSpeed{name: g.Name, sessions: g.Sessions, requests: g.Requests, output: g.Tokens.Output, usageKnown: g.Requests > g.UnmeteredRequests,
			samples: g.OutputRate.Samples, p10: p10, median: g.OutputRate.Median, p90: g.OutputRate.P90})
	}
	var skills []callCount
	for _, sk := range sum.Skills {
		skills = append(skills, callCount{name: sk.Name, count: sk.Calls})
	}
	return models, skills, sortedCalls(tools)
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
