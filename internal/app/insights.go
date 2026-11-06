package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/insight"
	"knack/internal/session"
)

// insightsPage is the sidebar's 诊断: what the sessions cost, the context
// each starts with, which skills earn their keep, where work stalls, and
// the configuration behind it. Everything is read-only.
const insightsPage = "insights"

var insightTabs = []string{"费用", "上下文", "技能", "摩擦", "项目", "配置"}

type insightsState struct {
	tab      int
	prices   insight.Prices
	priceErr string
	priceGen int // bumped on every load, so reports know their prices

	report     *insightReport
	skills     *insightSkills
	configs    map[string]*insight.ConfigReport // by project dir; "" is the user's own
	configGen  int
	configDir  string
	allUnused  bool
	allLoops   bool
	allSkills  bool
	allProject bool
	allRoots   bool
}

type insightReport struct {
	snapshot *session.Snapshot
	period   int
	agent    string
	date     string
	priceGen int
	report   insight.Report
}

type insightSkills struct {
	snapshot any
	sessions *session.Snapshot
	date     string
	report   insight.SkillReport
}

func (a *App) pricesPath() string {
	return filepath.Join(a.home, ".config", "skill-manager", "prices.json")
}

// loadPrices reads the price table; a broken file keeps the defaults and
// says why.
func (a *App) loadPrices() {
	p, err := insight.LoadPrices(a.pricesPath())
	a.insights.prices, a.insights.priceErr = p, ""
	a.insights.priceGen++
	if err != nil {
		a.insights.priceErr = err.Error()
	}
	a.insights.report = nil
}

func (a *App) insightData() insight.Report {
	s := &a.sessions
	now := time.Now()
	date := now.Format("2006-01-02")
	agent := a.sessionAgentFilter()
	if r := a.insights.report; r != nil && r.snapshot == s.snapshot && r.period == s.period && r.agent == agent && r.date == date && r.priceGen == a.insights.priceGen {
		return r.report
	}
	r := &insightReport{snapshot: s.snapshot, period: s.period, agent: agent, date: date, priceGen: a.insights.priceGen}
	r.report = insight.Analyze(s.snapshot, insight.Options{Filter: overviewFilter(s.period, agent, now), Prices: a.insights.prices})
	a.insights.report = r
	return r.report
}

func (a *App) insightsPane(c *ui.Context) {
	t := c.Theme()
	counts := make([]string, len(insightTabs))
	ui.Row(c).Shrink(0).Padding(0, 32).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		underlineTabs(c, "insight-tabs", "诊断标签", "诊断 ", &a.insights.tab, insightTabs, counts)
	})
	if a.sessions.snapshot == nil && a.insights.tab != 2 && a.insights.tab != 5 {
		if a.sessions.loading {
			emptyState(c, "诊断", "hourglass", t.TextMuted, "正在读取本地会话", "首次读取可能需要一会儿，读完的部分会先显示。", nil)
		} else {
			emptyState(c, "诊断", "messages", t.TextMuted, "没有本地会话", "诊断基于 Claude、Codex 等 Agent 的本地会话日志。", nil)
		}
		return
	}
	ui.Scroll(c.Key(fmt.Sprintf("insight-scroll:%d", a.insights.tab))).Grow(1).MinHeight(0).Label("诊断内容").Children(func() {
		ui.Column(c).Padding(24, 32, 36, 32).Gap(28).Children(func() {
			if a.sessions.err != "" {
				ui.Text(c, a.sessions.err).FontSize(12).TextColor(t.Warning)
			}
			if snap := a.sessions.snapshot; snap != nil && snap.Partial && a.sessions.loading {
				ui.Text(c, "正在读取剩余日志，数字会随读取进度更新").FontSize(11.5).TextColor(token(t, "ink-3"))
			}
			switch a.insights.tab {
			case 0:
				a.insightCost(c)
			case 1:
				a.insightContext(c)
			case 2:
				a.insightSkills(c)
			case 3:
				a.insightFriction(c)
			case 4:
				a.insightProjects(c)
			case 5:
				a.insightConfig(c)
			}
		})
	})
}

// figures is a row of numbers between hairlines, the home page's strip
// without its links. It wraps into rows of three when narrow.
func figures(c *ui.Context, items []metric) {
	t := c.Theme()
	width, _ := c.Size()
	per := len(items)
	if width < 1100 && per > 3 {
		per = 3
		if len(items) == 4 {
			per = 2
		}
	}
	ui.Column(c).BorderWidth(1, 0, 1, 0).BorderColor(t.Border).Children(func() {
		for start := 0; start < len(items); start += per {
			row := items[start:min(len(items), start+per)]
			line := ui.Row(c).Padding(12, 0)
			if start > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			line.Children(func() {
				for j, m := range row {
					box := ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(4).Padding(0, 20).Label(m.label + " " + m.value)
					if j == 0 {
						box.Padding(0, 20, 0, 0)
					} else {
						box.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
					}
					box.Children(func() {
						ui.Text(c, m.label).FontSize(12).TextColor(t.TextMuted).SingleLine()
						v := number(c, m.value).FontSize(22).FontWeight(600)
						if m.warn {
							v.TextColor(t.Warning)
						}
						color := m.noteColor
						if color.A == 0 {
							color = token(t, "ink-3")
						}
						ui.Text(c, m.note).FontSize(11.5).TextColor(color).SingleLine().Tooltip(m.note)
					})
				}
				for range per - len(row) {
					ui.Box(c).Grow(1).Basis(0)
				}
			})
		}
	})
}

// note is a quiet explanation under a block.
func note(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(11.5).LineHeight(1.5).TextColor(token(c.Theme(), "ink-3")).MaxLines(4)
}

// callout is a tinted box that says what to do about a finding.
func callout(c *ui.Context, icon string, tone ui.Color, text string) {
	t := c.Theme()
	ui.Row(c).Padding(10, 12).Gap(8).Radius(8).Background(fade(tone, 22)).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, glyph(icon)).FontSize(13).TextColor(tone).Shrink(0)
		ui.Text(c, text).FontSize(12).LineHeight(1.5).TextColor(t.Text).Grow(1).MinWidth(0)
	})
}

// column is one cell of a table row: fixed width, end-aligned numbers.
func cell(c *ui.Context, text string, width float32, numeric bool) ui.Element {
	var e ui.Element
	if numeric {
		e = number(c, text).FontSize(12).TextAlign(ui.End)
	} else {
		e = ui.Text(c, text).FontSize(12).SingleLine()
	}
	return e.Width(width).Shrink(0).Tooltip(text)
}

// tableHead is a table's quiet column names over a hairline.
func tableHead(c *ui.Context, first string, cols []string, widths []float32) {
	t := c.Theme()
	ui.Row(c).Padding(0, 0, 6, 0).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Text(c, first).FontSize(11).TextColor(token(t, "ink-3")).Grow(1).MinWidth(0).SingleLine()
		for i, col := range cols {
			ui.Text(c, col).FontSize(11).TextColor(token(t, "ink-3")).Width(widths[i]).Shrink(0).TextAlign(ui.End).SingleLine()
		}
	})
}

// shareBar draws value against peak as a thin bar.
func shareBar(c *ui.Context, value, peak float64, color ui.Color, width float32) {
	share := float32(0)
	if peak > 0 {
		share = float32(value / peak)
	}
	ui.Box(c).Width(width).Shrink(0).Height(6).Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, fade(color, 40), 1.5)
		if share > 0 {
			p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(2, share*r.W), H: r.H}, color, 1.5)
		}
	})
}

func dollars(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return "<$0.01"
	case v < 100:
		return fmt.Sprintf("$%.2f", v)
	case v < 10000:
		return fmt.Sprintf("$%.0f", v)
	}
	return fmt.Sprintf("$%.1fK", v/1000)
}

func percent(part, whole float64) string {
	if whole <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", 100*part/whole)
}

// ---- 费用 ----

func (a *App) insightCost(c *ui.Context) {
	t := c.Theme()
	r := a.insightData().Cost
	priced, unpriced := r.PricedTokens.Total(), r.UnpricedTokens.Total()
	figures(c, []metric{
		{label: "估算费用 · " + periodText(a.sessions.period), value: dollars(r.Total), note: fmt.Sprintf("%s Token 已定价", tokenNumber(priced))},
		{label: "缓存节省", value: dollars(r.CacheSavings), note: "缓存读取按输入价计算时的差额", noteColor: t.Success},
		{label: "未定价 Token", value: tokenNumber(unpriced), note: fmt.Sprintf("%d 个模型没有价格", len(r.UnpricedModels)), warn: unpriced > priced},
		{label: "已定价占比", value: percent(float64(priced), float64(priced+unpriced)), note: "按 Token 计"},
	})
	if a.insights.priceErr != "" {
		callout(c, "alert", t.Warning, a.insights.priceErr+"。已使用内置价格。")
	}
	if unpriced > 0 {
		ui.Column(c).Gap(8).Children(func() {
			callout(c, "info", t.Accent, "部分模型没有内置价格（"+strings.Join(r.UnpricedModels[:min(6, len(r.UnpricedModels))], "、")+ellipsis(len(r.UnpricedModels), 6)+
				"），这部分 Token 不计入费用。可在价格表里按模型名或前缀补充美元 / 百万 Token 的价格。")
			ui.Row(c).Gap(8).Children(func() {
				if outlineButton(c, "open-prices", "打开价格表").Clicked() {
					a.openPriceFile()
				}
				if outlineButton(c, "reload-prices", "重新读取").Clicked() {
					a.loadPrices()
					c.Invalidate()
				}
			})
		})
	}
	width, _ := c.Size()
	wide := width-a.sidebarWidth() >= 980
	left := func() {
		a.costDaily(c, r)
		a.costSessions(c, r)
	}
	right := func() {
		a.costModels(c, r)
		a.costAgents(c, r)
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
	note(c, "按 Anthropic 官方 API 价格估算：缓存写入按 5 分钟缓存的 1.25 倍输入价，Haiku 5.5 按 10 万 Token 以内的档位。订阅套餐、云平台和代理的实际计费不同，这里只用于比较相对开销。")
}

func ellipsis(n, shown int) string {
	if n > shown {
		return fmt.Sprintf(" 等 %d 个", n)
	}
	return ""
}

// openPriceFile opens prices.json, first writing a template when there is
// none. It is the only file the page writes, and only on this click.
func (a *App) openPriceFile() {
	path := a.pricesPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		var models []string
		for _, m := range a.insightData().Cost.UnpricedModels {
			if !strings.HasPrefix(m, "<") && m != "未知模型" {
				models = append(models, fmt.Sprintf("    %q: {\"input\": 0, \"output\": 0, \"cache_read\": 0, \"cache_write\": 0}", strings.ToLower(m)))
			}
			if len(models) == 8 {
				break
			}
		}
		body := "{\n  \"models\": {\n" + strings.Join(models, ",\n") + "\n  }\n}\n"
		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err == nil {
			err = os.WriteFile(path, []byte(body), 0o600)
		}
		if err != nil {
			a.notice = "无法创建价格表：" + err.Error()
			return
		}
	}
	a.openPath(path)
	a.notice = "改好价格表后点“重新读取”"
}

func (a *App) costDaily(c *ui.Context, r insight.CostReport) {
	t := c.Theme()
	ui.Column(c).Gap(10).Label("每日费用").Children(func() {
		overviewTitle(c, "每日费用")
		if len(r.Daily) == 0 {
			ui.Text(c, "这段时间没有已定价的请求").FontSize(12).TextColor(t.TextMuted)
			return
		}
		days := r.Daily[max(0, len(r.Daily)-60):]
		peak := 0.0
		for _, d := range days {
			peak = max(peak, d.Cost)
		}
		color := token(t, "ink-2")
		ui.Box(c).Height(120).Label(fmt.Sprintf("%d 天的每日费用，最高 %s", len(days), dollars(peak))).Draw(func(p *ui.Painter, rect ui.Rect) {
			gap := float32(2)
			w := max(2, (rect.W-gap*float32(len(days)-1))/float32(len(days)))
			for i, d := range days {
				h := float32(0)
				if peak > 0 {
					h = max(1, float32(d.Cost/peak)*rect.H)
				}
				p.Fill(ui.Rect{X: rect.X + float32(i)*(w+gap), Y: rect.Y + rect.H - h, W: w, H: h}, color, 1.5)
			}
		})
		ui.Row(c).Children(func() {
			ui.Text(c, days[0].Date).FontSize(11).TextColor(token(t, "ink-3"))
			ui.Spacer(c)
			ui.Text(c, "最高 "+dollars(peak)).FontSize(11).TextColor(token(t, "ink-3"))
			ui.Spacer(c)
			ui.Text(c, days[len(days)-1].Date).FontSize(11).TextColor(token(t, "ink-3"))
		})
	})
}

func (a *App) costSessions(c *ui.Context, r insight.CostReport) {
	t := c.Theme()
	ui.Column(c).Label("最贵的会话").Children(func() {
		sectionHead(c, "最贵的会话", "", nil)
		if len(r.Sessions) == 0 {
			ui.Text(c, "这段时间没有带用量的会话").FontSize(12).TextColor(t.TextMuted)
			return
		}
		peak := r.Sessions[0].Cost
		for i, s := range r.Sessions[:min(10, len(r.Sessions))] {
			title := s.Title
			if strings.TrimSpace(title) == "" {
				title = sessionAgentName(s.Agent) + " · " + filepath.Base(s.CWD)
			}
			meta := []string{sessionAgentName(s.Agent), tokenNumber(s.Tokens.Total()) + " Token"}
			if s.CWD != "" {
				meta = append(meta, filepath.Base(s.CWD))
			}
			if s.SubagentShare > 0 {
				meta = append(meta, "子 Agent 占 "+percent(s.SubagentShare, s.Cost))
			}
			if s.UnpricedTokens > 0 {
				meta = append(meta, tokenNumber(s.UnpricedTokens)+" 未定价")
			}
			row := listRow(c, fmt.Sprintf("cost-session:%d", i), title+" "+dollars(s.Cost))
			row.Children(func() {
				ui.Box(c).Size(3, 30).Radius(2).Shrink(0).Background(agentColor(s.Agent, t.Dark))
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					ui.Text(c, title).FontSize(13).SingleLine().Tooltip(title)
					ui.Text(c, strings.Join(meta, " · ")).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
				})
				shareBar(c, s.Cost, peak, agentColor(s.Agent, t.Dark), 80)
				number(c, dollars(s.Cost)).FontSize(12.5).FontWeight(600).Width(64).TextAlign(ui.End).Shrink(0)
			})
			if row.Clicked() {
				a.openSession(string(s.Agent) + ":" + s.ID)
			}
		}
	})
}

func (a *App) costModels(c *ui.Context, r insight.CostReport) {
	t := c.Theme()
	ui.Column(c).Label("按模型").Children(func() {
		sectionHead(c, "按模型", "", nil)
		widths := []float32{56, 64}
		tableHead(c, "模型", []string{"Token", "费用"}, widths)
		for _, m := range r.ByModel[:min(12, len(r.ByModel))] {
			ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, m.Model).Font(monoFont).FontSize(12).Grow(1).MinWidth(0).SingleLine().Tooltip(fmt.Sprintf("%s · %d 次请求", m.Model, m.Requests))
				cell(c, tokenNumber(m.Tokens.Total()), widths[0], true).TextColor(t.TextMuted)
				cost := dollars(m.Cost)
				if !m.Priced {
					cost = "未定价"
				}
				e := cell(c, cost, widths[1], true)
				if !m.Priced {
					e.TextColor(token(t, "ink-3"))
				}
			})
		}
		if len(r.ByModel) > 12 {
			note(c, fmt.Sprintf("另有 %d 个模型", len(r.ByModel)-12))
		}
	})
}

func (a *App) costAgents(c *ui.Context, r insight.CostReport) {
	t := c.Theme()
	if len(r.ByAgent) == 0 {
		return
	}
	ui.Column(c).Gap(8).Label("按 Agent").Children(func() {
		overviewTitle(c, "按 Agent")
		peak := r.ByAgent[0].Cost
		for _, g := range r.ByAgent {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, g.Name).FontSize(12).Width(110).Shrink(0).SingleLine()
				color := token(t, "ink-2")
				if info, ok := session.AgentByName(g.Name); ok {
					color = agentColor(info.ID, t.Dark)
				}
				ui.Box(c).Grow(1).MinWidth(0).Height(8).Draw(func(p *ui.Painter, rect ui.Rect) {
					if peak > 0 {
						p.Fill(ui.Rect{X: rect.X, Y: rect.Y, W: max(3, float32(g.Cost/peak)*rect.W), H: rect.H}, color, 1.5)
					}
				})
				number(c, dollars(g.Cost)).FontSize(12).Width(64).TextAlign(ui.End).Shrink(0)
			})
		}
	})
}

// ---- 上下文 ----

func (a *App) insightContext(c *ui.Context) {
	t := c.Theme()
	r := a.insightData().Context
	var all []int64
	compactions := 0
	for _, s := range r.Sessions {
		all = append(all, s.Baseline)
		compactions += s.Compactions
	}
	median, p90 := int64(0), int64(0)
	if len(all) > 0 {
		sorted := slices.Clone(all)
		slices.Sort(sorted)
		median, p90 = sorted[(len(sorted)-1)/2], sorted[int(float64(len(sorted)-1)*.9)]
	}
	cfg := a.configFor("")
	instr := 0
	for _, n := range cfg.InstructionTokens {
		instr += n
	}
	standing := 0
	for _, root := range a.skillInsights().Roots {
		standing = max(standing, root.StandingTokens)
	}
	figures(c, []metric{
		{label: "首个请求输入 · 中位", value: tokenNumber(median), note: fmt.Sprintf("%d 个会话", len(r.Sessions))},
		{label: "首个请求输入 · P90", value: tokenNumber(p90), note: "开局就占用的上下文"},
		{label: "全局指令文件", value: tokenNumber(int64(instr)), note: "CLAUDE.md、AGENTS.md 及其导入 · 估算"},
		{label: "技能列表 · 最多的 Agent", value: tokenNumber(int64(standing)), note: "每个会话都要列出的技能描述 · 估算"},
		{label: "上下文压缩", value: groupDigits(compactions), note: "会话里触发的压缩次数", warn: compactions > 0},
	})
	note(c, "首个请求的输入 = 系统提示词 + 工具定义 + 指令文件 + 技能列表 + 第一条消息，是每次开会话的固定开销的上限。数值来自日志里的逐请求用量；技能和指令文件的 Token 按字符数估算。")
	width, _ := c.Size()
	wide := width-a.sidebarWidth() >= 980
	groups := func(title string, list []insight.ContextGroup, limit int) {
		ui.Column(c).Label(title).Children(func() {
			sectionHead(c, title, "", nil)
			if len(list) == 0 {
				ui.Text(c, "这段时间没有可计算的会话").FontSize(12).TextColor(t.TextMuted)
				return
			}
			widths := []float32{40, 56, 56, 64}
			tableHead(c, "", []string{"会话", "中位", "P90", "峰值中位"}, widths)
			peak := int64(1)
			for _, g := range list {
				peak = max(peak, g.P90)
			}
			for _, g := range list[:min(limit, len(list))] {
				ui.Row(c).Padding(7, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Label(fmt.Sprintf("%s 中位 %s", g.Name, tokenNumber(g.Median))).Children(func() {
					ui.Text(c, g.Name).FontSize(12.5).Grow(1).MinWidth(0).SingleLine().Tooltip(g.Name)
					shareBar(c, float64(g.Median), float64(peak), token(t, "ink-2"), 60)
					cell(c, fmt.Sprint(g.Sessions), widths[0], true).TextColor(t.TextMuted)
					cell(c, tokenNumber(g.Median), widths[1], true)
					cell(c, tokenNumber(g.P90), widths[2], true).TextColor(t.TextMuted)
					cell(c, tokenNumber(g.MedianPeak), widths[3], true).TextColor(t.TextMuted)
				})
			}
		})
	}
	sessions := func() {
		ui.Column(c).Label("开局最重的会话").Children(func() {
			sectionHead(c, "开局最重的会话", "", nil)
			for i, s := range r.Sessions[:min(10, len(r.Sessions))] {
				title := s.Title
				if strings.TrimSpace(title) == "" {
					title = sessionAgentName(s.Agent) + " · " + filepath.Base(s.CWD)
				}
				row := listRow(c, fmt.Sprintf("ctx-session:%d", i), title)
				row.Children(func() {
					ui.Box(c).Size(3, 30).Radius(2).Shrink(0).Background(agentColor(s.Agent, t.Dark))
					ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
						ui.Text(c, title).FontSize(13).SingleLine().Tooltip(title)
						meta := fmt.Sprintf("%s · 峰值 %s · %d 次请求", filepath.Base(s.CWD), tokenNumber(s.Peak), s.Requests)
						if s.Compactions > 0 {
							meta += fmt.Sprintf(" · 压缩 %d 次", s.Compactions)
						}
						ui.Text(c, meta).FontSize(11).TextColor(token(t, "ink-3")).SingleLine()
					})
					number(c, tokenNumber(s.Baseline)).FontSize(12.5).FontWeight(600).Width(64).TextAlign(ui.End).Shrink(0)
				})
				if row.Clicked() {
					a.openSession(string(s.Agent) + ":" + s.ID)
				}
			}
		})
	}
	if wide {
		ui.Row(c).Gap(40).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(28).Children(func() {
				groups("按项目", r.ByProject, 15)
			})
			ui.Column(c).Width(420).Shrink(0).Gap(28).Children(func() {
				groups("按 Agent", r.ByAgent, 8)
				sessions()
			})
		})
	} else {
		groups("按 Agent", r.ByAgent, 8)
		groups("按项目", r.ByProject, 15)
		sessions()
	}
	callout(c, "info", t.Accent, "想让开局更轻：在「技能」里卸载长期不用的技能、缩短冗长的描述；在「配置」里查看各层 CLAUDE.md / AGENTS.md 和未使用的 MCP 服务器。")
}
