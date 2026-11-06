package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func (a *App) sessionOverview(c *ui.Context, d *sessionDetailData, width float32) {
	t := c.Theme()
	sum := d.summary
	tokens := tokenNumber(sum.Tokens.Total())
	tokenNote := fmt.Sprintf("输入 %s · 输出 %s", tokenNumber(sum.Tokens.InputTotal()), tokenNumber(sum.Tokens.Output))
	if sum.Requests == 0 {
		tokens = "暂不支持"
		tokenNote = "没有可识别的请求用量"
	}
	if sum.UnmeteredRequests > 0 {
		tokenNote = "已识别用量，部分请求暂不支持"
		if sum.UnmeteredRequests == sum.Requests {
			tokens = "暂不支持"
		}
	}
	rate := "暂不支持"
	rateNote := "日志缺少可计算的生成速率"
	if sum.OutputRate.Samples > 0 {
		rate = fmt.Sprintf("≈ %.0f tok/s", sum.OutputRate.Median)
		rateNote = fmt.Sprintf("中位数 · P90 %.0f", sum.OutputRate.P90)
	}
	cache := "暂不支持"
	cacheNote := "日志缺少输入用量"
	if sum.CacheHitKnown {
		cache = fmt.Sprintf("%.0f%%", sum.CacheHit*100)
		cacheNote = "读取 " + tokenNumber(sum.Tokens.CacheRead)
	}
	metrics := []struct{ label, value, note string }{{"Token", tokens, tokenNote}, {"输出速率", rate, rateNote}, {"缓存命中", cache, cacheNote}, {"轮次", fmt.Sprint(len(d.turns)), fmt.Sprintf("工具调用 %d 次", d.toolCount)}}
	draw := func(start, end int) {
		ui.Row(c).Gap(0).Children(func() {
			for i := start; i < end; i++ {
				m := metrics[i]
				box := ui.Column(c).Grow(1).MinWidth(0).Gap(5).Padding(5, 8)
				if i > start {
					box.BorderWidth(0, 0, 0, 1).BorderColor(t.Border)
				}
				box.Children(func() {
					ui.Text(c, m.label).FontSize(11).TextColor(t.TextMuted)
					number(c, m.value).FontSize(19).FontWeight(600)
					ui.Text(c, m.note).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(m.note)
				})
			}
		})
	}
	if width < 560 {
		ui.Column(c).Gap(10).Children(func() { draw(0, 2); draw(2, 4) })
	} else {
		draw(0, 4)
	}
	a.sessionAgentTimeline(c, d)
	a.sessionModelSpeeds(c, d)
	if width < 560 {
		a.sessionCalls(c, "Skill", d.skills, !session.LookupAgent(d.session.Agent).Skills)
		a.sessionCalls(c, "工具", d.tools, false)
	} else {
		ui.Row(c).Gap(20).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.sessionCalls(c, "Skill", d.skills, !session.LookupAgent(d.session.Agent).Skills) })
			ui.Column(c).Grow(1).MinWidth(0).Children(func() { a.sessionCalls(c, "工具", d.tools, false) })
		})
	}
	ui.Text(c, "时间与速率为估算；用量包含已关联父会话的子 Agent，缺失字段不按零处理。").FontSize(10).TextColor(t.TextMuted).MaxLines(3)
}

func (a *App) sessionAgentTimeline(c *ui.Context, d *sessionDetailData) {
	t := c.Theme()
	dark := t.Dark
	root := d.session
	start, end := root.Start, root.End
	for _, track := range d.tracks {
		if !track.session.Start.IsZero() && (start.IsZero() || track.session.Start.Before(start)) {
			start = track.session.Start
		}
		if track.session.End.After(end) {
			end = track.session.End
		}
	}
	span := end.Sub(start)
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(10).Children(func() {
			overviewTitle(c, "Agent 调用")
			ui.Spacer(c)
			ui.Textf(c, "主 Agent 1 · 子 Agent %d", len(d.tracks)-1).FontSize(11).TextColor(t.TextMuted)
		})
		ui.Row(c).Padding(0, 0, 0, 112).Justify(ui.SpaceBetween).Children(func() {
			if !start.IsZero() && !end.IsZero() {
				number(c, start.Local().Format("15:04")).FontSize(10).TextColor(t.TextMuted)
				number(c, end.Local().Format("15:04")).FontSize(10).TextColor(t.TextMuted)
			}
		})
		for i, tr := range d.tracks {
			s := tr.session
			label := "主 Agent"
			if i > 0 {
				label = "子 Agent · " + s.AgentID
			}
			if s.AgentID == "" && i > 0 {
				label = "子 Agent · " + s.ID
			}
			note := tr.note
			ui.Column(c.Key("track:"+s.ID)).Gap(4).Padding(4, 0).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, label).Width(104).FontSize(11).SingleLine().Tooltip(label)
					ui.Box(c).Grow(1).Height(12).Background(t.SurfaceHover).Radius(2).Label(label + "调用时间线").Tooltip(label + " · " + sessionSpan(s)).Draw(func(p *ui.Painter, r ui.Rect) {
						if span <= 0 || s.Start.IsZero() || s.End.IsZero() {
							return
						}
						from, to := s.Start, s.End
						if from.Before(start) {
							from = start
						}
						if to.After(end) {
							to = end
						}
						if !to.After(from) {
							return
						}
						x := float32(float64(from.Sub(start))/float64(span)) * r.W
						w := float32(float64(to.Sub(from))/float64(span)) * r.W
						p.Fill(ui.Rect{X: r.X + x, Y: r.Y + 3, W: max(2, w), H: 6}, agentColor(s.Agent, dark), 2)
					})
					number(c, sessionSpan(s)).FontSize(10).Width(66)
				})
				ui.Text(c, note).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(note)
			})
		}
	})
}

func (a *App) sessionModelSpeeds(c *ui.Context, d *sessionDetailData) {
	t := c.Theme()
	neutral := sessionColor("data-neutral", t.Dark)
	peak := float64(1)
	for _, m := range d.models {
		peak = max(peak, m.p90)
	}
	ui.Column(c).Gap(10).Children(func() {
		overviewTitle(c, "模型速率")
		ui.Text(c, "估算输出 tok/s · 条为 P10–P90，竖线为中位数").FontSize(10).TextColor(t.TextMuted).MaxLines(2)
		if len(d.models) == 0 {
			ui.Text(c, "暂不支持：没有模型请求记录").FontSize(11).TextColor(t.TextMuted)
		}
		for _, m := range d.models {
			ui.Column(c.Key("model:"+m.name)).Padding(6, 0).Gap(6).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, m.name).FontSize(11).Grow(1).MinWidth(0).SingleLine().Tooltip(m.name)
					number(c, fmt.Sprintf("%d 次请求", m.requests)).FontSize(10).TextColor(t.TextMuted)
					output := "输出暂不支持"
					if m.usageKnown {
						output = "输出 " + tokenNumber(m.output)
					}
					number(c, output).FontSize(10).TextColor(t.TextMuted)
				})
				if m.samples == 0 {
					ui.Text(c, "速率暂不支持").FontSize(11).TextColor(t.TextMuted)
					return
				}
				label := fmt.Sprintf("P10 %.1f · 中位数 %.1f · P90 %.1f tok/s · %d 个样本（估算）", m.p10, m.median, m.p90, m.samples)
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Grow(1).Height(16).Label(m.name + "速率范围").Tooltip(label).Draw(func(p *ui.Painter, r ui.Rect) {
						p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 1, W: r.W, H: 2}, t.Border, 0)
						lo, hi := float32(m.p10/peak)*r.W, float32(m.p90/peak)*r.W
						p.Fill(ui.Rect{X: r.X + lo, Y: r.Y + 4, W: max(2, hi-lo), H: 8}, neutral, 2)
						mid := float32(m.median/peak) * r.W
						p.Fill(ui.Rect{X: r.X + min(mid, r.W-2), Y: r.Y + 1, W: 2, H: r.H - 2}, t.Text, 0)
					})
					number(c, fmt.Sprintf("≈ %.0f tok/s", m.median)).FontSize(11).Width(78)
				})
				number(c, label).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(label)
			})
		}
	})
}

func (a *App) sessionCalls(c *ui.Context, title string, calls []callCount, unsupported bool) {
	t := c.Theme()
	total := 0
	for _, call := range calls {
		total += call.count
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).Children(func() {
			overviewTitle(c, title)
			ui.Spacer(c)
			if !unsupported {
				number(c, fmt.Sprintf("%d 次", total)).FontSize(11).TextColor(t.TextMuted)
			}
		})
		if unsupported {
			ui.Text(c, title+" 调用暂不支持").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if len(calls) == 0 {
			ui.Text(c, "没有记录到"+title+"调用").FontSize(11).TextColor(t.TextMuted)
		}
		for _, call := range calls {
			ui.Row(c).Gap(8).Children(func() {
				label := call.name
				if call.child {
					label += " · 含子 Agent 调用"
				}
				ui.Text(c, label).FontSize(11).Grow(1).MinWidth(0).SingleLine().Tooltip(label)
				number(c, fmt.Sprintf("×%d", call.count)).FontSize(11).TextColor(t.TextMuted)
			})
		}
	})
}
