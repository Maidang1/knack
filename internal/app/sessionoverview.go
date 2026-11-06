package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

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
			ui.Textf(c, "主 Agent 1 · 子 Agent %d", len(d.tracks)-1).FontSize(11.5).TextColor(t.TextMuted)
		})
		ui.Row(c).Padding(0, 0, 0, 112).Justify(ui.SpaceBetween).Children(func() {
			if !start.IsZero() && !end.IsZero() {
				number(c, start.Local().Format("15:04")).FontSize(11).TextColor(t.TextMuted)
				number(c, end.Local().Format("15:04")).FontSize(11).TextColor(t.TextMuted)
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
					ui.Text(c, label).Width(104).FontSize(11.5).SingleLine().Tooltip(label)
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
					number(c, sessionSpan(s)).FontSize(11).Width(66)
				})
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(note)
			})
		}
	})
}

// speedAxis rounds the fastest P90 up to a scale of four even steps.
func speedAxis(peak float64) (float64, []float64) {
	step := float64(10)
	for _, s := range []float64{10, 20, 25, 50, 70, 100, 150, 200, 250, 500, 1000} {
		step = s
		if s*4 >= peak {
			break
		}
	}
	return step * 4, []float64{0, step, step * 2, step * 3, step * 4}
}

const (
	speedNameWidth  = 190
	speedValueWidth = 52
)

// sessionModelSpeeds is each model's estimated output rate on one axis: the
// P10–P90 band in a tint of color, the median as a tick in it.
func (a *App) sessionModelSpeeds(c *ui.Context, d *sessionDetailData, color ui.Color) {
	t := c.Theme()
	muted := token(t, "ink-3")
	peak := float64(1)
	for _, m := range d.models {
		peak = max(peak, m.p90)
	}
	scale, ticks := speedAxis(peak)
	band := blend(color, t.Surface, 0.28)
	ui.Column(c).Gap(0).Children(func() {
		ui.Row(c).Gap(8).Padding(0, 0, 10, 0).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "模型输出速率")
			ui.Spacer(c)
			ui.Text(c, "P10–P90 区间 · 竖线为中位 · 估算").FontSize(11.5).TextColor(muted).SingleLine()
		})
		if len(d.models) == 0 {
			ui.Text(c, "暂不支持：没有模型请求记录").FontSize(12).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(12).Padding(0, 0, 6, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Box(c).Width(speedNameWidth).Shrink(0)
			ui.Box(c).Grow(1).Height(14).Draw(func(p *ui.Painter, r ui.Rect) {
				end := r.X - 8
				for i := len(ticks) - 1; i >= 0; i-- {
					v := ticks[i]
					text := fmt.Sprint(v)
					if i == len(ticks)-1 {
						text += " tok/s"
					}
					span := ui.Span{Text: text, Size: 11.5, Color: muted}
					w, _ := p.MeasureText(1000, span)
					x := r.X + float32(v/scale)*r.W - w/2
					switch i {
					case 0:
						x = r.X
					case len(ticks) - 1:
						x = r.X + r.W - w
					}
					// Right to left, so the scale's end always shows; a
					// label that would touch the one after it is left out.
					if i < len(ticks)-1 && x+w+8 > end {
						continue
					}
					end = x
					p.RichText(x, r.Y, w+2, span)
				}
			})
			ui.Box(c).Width(speedValueWidth).Shrink(0)
		})
		for _, m := range d.models {
			label := fmt.Sprintf("P10 %.1f · 中位数 %.1f · P90 %.1f tok/s · %d 个样本（估算）", m.p10, m.median, m.p90, m.samples)
			ui.Row(c.Key("model:"+m.name)).Padding(10, 0).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Column(c).Width(speedNameWidth).Shrink(0).Gap(3).Children(func() {
					ui.Text(c, m.name).FontSize(13).SingleLine().Tooltip(m.name)
					meta := fmt.Sprintf("%d 请求", m.requests)
					if m.sessions > 0 {
						meta = fmt.Sprintf("%d 会话 · %s", m.sessions, meta)
					}
					if m.usageKnown {
						meta += " · 输出 " + tokenNumber(m.output)
					}
					if m.samples > 0 && m.samples < 5 {
						meta += " · 样本少"
					}
					ui.Text(c, meta).FontSize(11.5).TextColor(muted).SingleLine().Tooltip(meta)
				})
				if m.samples == 0 {
					ui.Text(c, "速率暂不支持").FontSize(12).TextColor(t.TextMuted).Grow(1)
					ui.Text(c, "—").FontSize(13).TextColor(muted).Width(speedValueWidth).TextAlign(ui.End)
					return
				}
				ui.Box(c).Grow(1).Height(18).Label(m.name + " 速率范围 · " + label).Tooltip(label).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H/2 - 0.5, W: r.W, H: 1}, t.Border, 0)
					lo, hi := float32(m.p10/scale)*r.W, float32(m.p90/scale)*r.W
					p.Fill(ui.Rect{X: r.X + lo, Y: r.Y + 4, W: max(4, hi-lo), H: r.H - 8}, band, (r.H-8)/2)
					mid := float32(m.median/scale) * r.W
					p.Fill(ui.Rect{X: r.X + min(mid, r.W-2) - 1, Y: r.Y + 1, W: 2, H: r.H - 2}, color, 1)
				})
				number(c, fmt.Sprintf("%.0f", m.median)).FontSize(13).FontWeight(600).Width(speedValueWidth).TextAlign(ui.End).Tooltip("中位数 tok/s")
			})
		}
	})
}

// sessionCalls ranks what was called, each name over a bar of its count.
func (a *App) sessionCalls(c *ui.Context, title string, calls []callCount, unsupported bool, color ui.Color) {
	t := c.Theme()
	muted := token(t, "ink-3")
	total, peak := 0, 1
	for _, call := range calls {
		total += call.count
		peak = max(peak, call.count)
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, title)
			ui.Spacer(c)
			if !unsupported {
				ui.Text(c, "调用次数").FontSize(11.5).TextColor(muted).Tooltip(fmt.Sprintf("共 %d 次", total))
			}
		})
		if unsupported {
			ui.Text(c, title+"暂不支持").FontSize(12).TextColor(t.TextMuted)
			return
		}
		if len(calls) == 0 {
			ui.Text(c, "没有记录到调用").FontSize(12).TextColor(t.TextMuted)
		}
		for _, call := range calls {
			label := call.name
			if call.child {
				label += " · 含子 Agent 调用"
			}
			ui.Row(c).Gap(10).AlignItems(ui.Center).Label(fmt.Sprintf("%s ×%d", label, call.count)).Children(func() {
				ui.Text(c, call.name).FontSize(13).Width(150).Shrink(0).SingleLine().Tooltip(label)
				share := float32(call.count) / float32(peak)
				ui.Box(c).Grow(1).Height(8).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(ui.Rect{X: r.X, Y: r.Y, W: max(3, share*r.W), H: r.H}, color, 1.5)
				})
				number(c, fmt.Sprint(call.count)).FontSize(12).TextColor(t.TextMuted).Width(40).TextAlign(ui.End)
			})
		}
	})
}
