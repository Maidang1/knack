package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func seriesName(id session.Agent) string {
	if id == "" {
		return "其他"
	}
	return sessionAgentName(id)
}

func (day overviewDay) total() int64 {
	var n int64
	for _, v := range day.tokens {
		n += v
	}
	return n
}

// Only Agents with usage that day are listed; zero entries are noise.
func dayDescription(day overviewDay, series []session.Agent) string {
	total := day.total()
	if total == 0 {
		return day.date + " · 没有 Token"
	}
	text := fmt.Sprintf("%s · %s Token", day.date, tokenNumber(total))
	for i, id := range series {
		if day.tokens[i] > 0 {
			text += fmt.Sprintf(" · %s %s", seriesName(id), tokenNumber(day.tokens[i]))
		}
	}
	return text
}

func (a *App) dailyChart(c *ui.Context, d *overviewData) {
	t := c.Theme()
	dark := t.Dark
	var peak int64
	for _, day := range d.days {
		peak = max(peak, day.total())
	}
	card(c).Padding(12, 14).Gap(8).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Height(22).Children(func() {
			overviewTitle(c, "每日 Token")
			ui.Spacer(c)
			for _, id := range d.series {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(6, 6).Radius(3).Background(agentColor(id, dark))
					ui.Text(c, seriesName(id)).FontSize(11).TextColor(t.TextMuted)
				})
			}
		})
		ui.Row(c).Height(136).Gap(8).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(38).Justify(ui.SpaceBetween).Children(func() {
				number(c, tokenNumber(peak)).FontSize(10).TextColor(t.TextMuted)
				number(c, tokenNumber(peak/2)).FontSize(10).TextColor(t.TextMuted)
				number(c, "0").FontSize(10).TextColor(t.TextMuted)
			})
			ui.Row(c).Grow(1).Gap(3).AlignItems(ui.Stretch).FocusGroup(ui.Horizontal).Label("每日 Token 图表").BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				for i, day := range d.days {
					description := filteredDayDescription(day, d)
					bar := ui.Button(c.Key(day.date), "").Grow(1).Height(135).MinWidth(0).Padding(0).Border(0, ui.Transparent).Background(ui.Transparent).Label(description).Tooltip(description)
					if i == a.sessions.day {
						bar.Background(fade(t.Accent, 20)).Radius(3)
					}
					bar.Draw(func(p *ui.Painter, r ui.Rect) {
						if peak == 0 {
							return
						}
						y := r.Y + r.H
						w := min(r.W, float32(18))
						for j, value := range day.tokens {
							h := float32(float64(value)/float64(peak)) * r.H
							y -= h
							p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: y, W: w, H: h}, agentColor(d.series[j], dark), 0)
						}
					})
					if bar.Clicked() {
						a.sessions.day = i
						c.Invalidate()
					}
				}
			})
		})
		axis := ui.Row(c).Padding(0, 0, 0, 46).Justify(ui.SpaceBetween)
		if len(d.days) == 1 {
			axis.Justify(ui.Center)
		}
		axis.Children(func() {
			if len(d.days) > 0 {
				last := -1
				for _, i := range []int{0, len(d.days) / 2, len(d.days) - 1} {
					if i == last {
						continue
					}
					last = i
					ui.Text(c, strings.ReplaceAll(d.days[i].date[5:], "-", "/")).FontSize(10).TextColor(t.TextMuted)
				}
			}
		})
		selected := a.sessions.day
		if selected < 0 || selected >= len(d.days) {
			selected = len(d.days) - 1
		}
		if selected >= 0 {
			caption := filteredDayDescription(d.days[selected], d)
			if d.period == 3 {
				caption += " · 图表显示最近 60 日，顶部汇总覆盖全部历史"
			}
			number(c, caption).FontSize(10).TextColor(t.TextMuted).SingleLine().Tooltip(caption)
		}
	})
}

func filteredDayDescription(day overviewDay, d *overviewData) string {
	if d.agent == "" || d.agent == allAgents {
		return dayDescription(day, d.series)
	}
	return fmt.Sprintf("%s · %s · %s Token", day.date, d.agent, tokenNumber(day.total()))
}
