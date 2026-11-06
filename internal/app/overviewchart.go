package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

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

// chartColor is the violet step of the daily chart's j-th series, largest
// first; the shared "其他" series stays neutral.
func chartColor(series []session.Agent, j int, dark bool) ui.Color {
	if series[j] == "" || j >= 3 {
		return sessionColor("data-neutral", dark)
	}
	return sessionColor(fmt.Sprintf("chart-%d", j+1), dark)
}

// dailyChart stacks each day's Token by Agent in steps of violet. The chosen
// day is in full color and the others recede, so the caption below reads as
// its legend.
func (a *App) dailyChart(c *ui.Context, d *overviewData) {
	t := c.Theme()
	dark := t.Dark
	muted := token(t, "ink-3")
	var peak int64
	for _, day := range d.days {
		peak = max(peak, day.total())
	}
	selected := a.sessions.day
	if selected < 0 || selected >= len(d.days) {
		selected = len(d.days) - 1
	}
	ui.Column(c).Gap(14).Children(func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "每日 Token").FontSize(15).FontWeight(600).Grow(1).SingleLine()
			for j, id := range d.series {
				legendItem(c, chartColor(d.series, j, dark), seriesName(id))
			}
		})
		ui.Column(c).Gap(6).Children(func() {
			ui.Row(c).Height(160).Gap(8).AlignItems(ui.Stretch).Children(func() {
				ui.Column(c).Width(36).Shrink(0).Justify(ui.SpaceBetween).AlignItems(ui.End).Children(func() {
					for _, v := range []int64{peak, peak * 2 / 3, peak / 3, 0} {
						number(c, axisNumber(v)).FontSize(11).TextColor(muted)
					}
				})
				ui.Row(c).Grow(1).Gap(4).AlignItems(ui.Stretch).FocusGroup(ui.Horizontal).Label("每日 Token 图表").Children(func() {
					for i, day := range d.days {
						description := filteredDayDescription(day, d)
						bar := ui.ButtonBase(c.Key(day.date)).Grow(1).Basis(0).MinWidth(0).Label(description).Tooltip(description)
						on := i == selected
						bar.Draw(func(p *ui.Painter, r ui.Rect) {
							if peak == 0 {
								return
							}
							y := r.Y + r.H
							w := min(r.W, float32(42))
							for j, value := range day.tokens {
								h := float32(float64(value)/float64(peak)) * r.H
								if h <= 0 {
									continue
								}
								color := chartColor(d.series, j, dark)
								if !on {
									color = fade(color, 110)
								}
								y -= h
								// A one-point seam separates the stacked Agents.
								p.Fill(ui.Rect{X: r.X + (r.W-w)/2, Y: y + 1, W: w, H: max(h-1, 1)}, color, 1)
							}
						})
						if bar.Clicked() {
							a.sessions.day = i
							c.Invalidate()
						}
					}
				})
			})
			ui.Row(c).Padding(0, 0, 0, 38).Gap(4).Children(func() {
				axisTicks(c, len(d.days), selected, false, func(i int) string { return dayTick(d.days, i) })
			})
		})
		if selected >= 0 {
			day := d.days[selected]
			caption := strings.TrimPrefix(filteredDayDescription(day, d), day.date+" · ")
			if d.period == 3 {
				caption += " · 图表显示最近 60 日"
			}
			ui.Row(c).Gap(8).Padding(8, 12).Radius(8).Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center).Shrink(0).
				Children(func() {
					ui.Text(c, dayLabel(day.date)).FontSize(13).FontWeight(600).SingleLine()
					ui.Text(c, caption).FontSize(13).TextColor(t.TextMuted).SingleLine().MinWidth(0).Tooltip(caption)
				})
		}
	})
}

// axisTicks labels every few of n bars, always the chosen one, then the
// last and the first. Each label spans the bars around its own, so it is
// centered under its bar and is never cut short: a span of k cells grows k
// times and adds back the k-1 gaps of 4 it covers. pill marks the chosen
// label as the design's ink chip; otherwise it is bold.
func axisTicks(c *ui.Context, n, selected int, pill bool, label func(int) string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	every := max(1, (n+13)/14)
	if every%2 == 0 {
		every++
	}
	half := every / 2
	var marks []int
	add := func(i int) {
		if i < 0 || i >= n {
			return
		}
		for _, m := range marks {
			if i-m < every && m-i < every {
				return
			}
		}
		marks = append(marks, i)
	}
	add(selected)
	add(n - 1)
	for i := 0; i < n; i += every {
		add(i)
	}
	slices.Sort(marks)
	cell := func(from, to int) ui.Element {
		k := float32(to - from + 1)
		return ui.Row(c).Grow(k).Basis((k - 1) * 4).MinWidth(0)
	}
	next := 0
	for _, i := range marks {
		// A label at an edge spans inward instead, the same width.
		from, to := i-half, i+half
		if from < 0 {
			from, to = 0, to-from
		}
		if to > n-1 {
			from, to = from-(to-n+1), n-1
		}
		from, to = max(from, next), min(to, n-1)
		if from > next {
			cell(next, from-1)
		}
		align := ui.Center
		if i-from < to-i {
			align = ui.Start
		} else if i-from > to-i {
			align = ui.End
		}
		on := i == selected
		cell(from, to).Justify(align).Children(func() {
			text := number(c, label(i)).FontSize(11).TextColor(muted).SingleLine()
			switch {
			case on && pill:
				text.TextColor(t.AccentText).FontWeight(600).Padding(1, 6).Radius(4).Background(t.Accent)
			case on:
				text.TextColor(t.Text).FontWeight(600)
			}
		})
		next = to + 1
	}
	if next < n {
		cell(next, n-1)
	}
}

// dayTick labels a bar: month and day where the month starts, else the day.
func dayTick(days []overviewDay, i int) string {
	at, err := time.Parse("2006-01-02", days[i].date)
	if err != nil {
		return days[i].date
	}
	if i == 0 || at.Day() == 1 {
		return fmt.Sprintf("%d/%d", at.Month(), at.Day())
	}
	return fmt.Sprint(at.Day())
}

// dayLabel is a date with its weekday, as "11/2 周一".
func dayLabel(date string) string {
	at, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return fmt.Sprintf("%d/%d %s", at.Month(), at.Day(), []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[at.Weekday()])
}

func filteredDayDescription(day overviewDay, d *overviewData) string {
	if d.agent == "" || d.agent == allAgents {
		return dayDescription(day, d.series)
	}
	return fmt.Sprintf("%s · %s · %s Token", day.date, d.agent, tokenNumber(day.total()))
}

// axisNumber is a tick of the token axis: whole units past ten, so a tick
// fits the axis without an ellipsis (462M, not 462.1M).
func axisNumber(n int64) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%dM", (n+500_000)/1_000_000)
	case n >= 10_000:
		return fmt.Sprintf("%dK", (n+500)/1000)
	}
	return tokenNumber(n)
}
