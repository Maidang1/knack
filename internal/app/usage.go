package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/session"
)

// usagePage is every Agent's usage over the period, the breakdowns the
// home page leaves out; the toolbar's Agent filter narrows it to one.
const usagePage = "usage"

func (a *App) usagePane(c *ui.Context) {
	a.usageBody(c, "usage", overviewFilter(a.sessions.period, a.sessions.agent, time.Now()).Agent)
}

// usageTimingData is where the period's active time went, turn by turn,
// across the main sessions.
type usageTimingData struct {
	phases        phaseTotals
	waiting, away time.Duration
	turns         int
}

// usageTiming adds each turn's own work the way the session page does, so
// the two agree; sub-Agents count inside the turn that ran them.
func (a *App) usageTimingFor(agent session.Agent, d *overviewData) *usageTimingData {
	if d.timing != nil {
		return d.timing
	}
	out := &usageTimingData{}
	d.timing = out
	if d.snapshot == nil {
		return out
	}
	f := overviewFilter(d.period, d.agent, time.Now())
	if agent == "" {
		agent = f.Agent
	}
	for _, s := range d.snapshot.Sessions {
		if s.UnsupportedReason != "" || agent != "" && s.Agent != agent {
			continue
		}
		for i, t := range s.Turns {
			if !filterTime(f, t.Start) {
				continue
			}
			end := s.End
			if i+1 < len(s.Turns) && !s.Turns[i+1].Start.IsZero() {
				end = s.Turns[i+1].Start
			}
			activeEnd := turnActiveEnd(t)
			if activeEnd.After(end) {
				activeEnd = end
			}
			claims := turnClaims(t)
			p := partitionPhases(t.Start, activeEnd, claims)
			for k, v := range p.values {
				out.phases.values[k] += v
			}
			out.phases.unknown += p.unknown
			out.phases.total += p.total
			if end.After(activeEnd) && !activeEnd.IsZero() {
				if partitionPhases(t.Start, end, claims).away > 0 {
					out.away += end.Sub(activeEnd)
				} else {
					out.waiting += end.Sub(activeEnd)
				}
			}
			out.turns++
		}
	}
	return out
}

// usageTiming is the period's 耗时构成.
func (a *App) usageTiming(c *ui.Context, agent session.Agent, d *overviewData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	timing := a.usageTimingFor(agent, d)
	ui.Column(c).Gap(10).Label("耗时构成").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "耗时构成 · "+periodText(a.sessions.period))
			if timing.turns > 0 {
				avg := timing.phases.total / time.Duration(timing.turns)
				number(c, fmt.Sprintf("%d 轮 · 活跃 ≈ %s · 平均每轮 ≈ %s", timing.turns, session.FormatDuration(timing.phases.total), session.FormatDuration(avg))).
					FontSize(12).TextColor(muted).MinWidth(0).SingleLine()
			}
		})
		if timing.turns == 0 {
			ui.Text(c, "这段时间没有可识别的轮次").FontSize(13).TextColor(t.TextMuted)
			return
		}
		phaseBar(c, timing.phases, timing.waiting, timing.away)
		ui.Text(c, "按轮累加活跃时间，与会话详情同一口径；轮与轮之间的等待不计入。模型时间为估算。").FontSize(11.5).TextColor(muted).MaxLines(2)
	})
}
