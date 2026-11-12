package app

import (
	"encoding/json"
	"fmt"
	"time"

	"knack/internal/session"

	"github.com/egoist/mygo/ui"
)

// The turn list is the design's left column: a row per turn with what was
// asked, how long it ran, its phases and what stands out in it. Choosing a
// row shows that turn's steps beside it.

const (
	turnListWidth = 280
	// A long session lists this many turns until the rest are asked for.
	turnListShown = 12
	// An open sub-Agent lists at most this many of its own steps.
	subagentStepsShown = 40
	subagentIndent     = 18
)

// turnBadges counts what a turn's row calls out.
type turnBadges struct{ subagents, permissions, retries int }

func badgesOf(turn sessionTurnView) turnBadges {
	b := turnBadges{subagents: len(turn.turn.Subagents)}
	for _, s := range turn.steps {
		if s.tool != nil && s.tool.PermissionWait || s.wait != nil && s.wait.Kind == "permission" {
			b.permissions++
		}
		if s.phase == phaseRetry {
			b.retries++
		}
	}
	return b
}

// turnShown filters the list: every turn, turns with sub-Agents, or turns
// that waited for the person to confirm.
func turnShown(filter int, b turnBadges) bool {
	switch filter {
	case 1:
		return b.subagents > 0
	case 2:
		return b.permissions > 0
	}
	return true
}

func (a *App) turnList(c *ui.Context, d *sessionDetailData, index int) {
	t := c.Theme()
	content := a.sessionReplay(d).ready()
	badges := make([]turnBadges, len(d.turns))
	subs, waits := 0, 0
	peak := time.Duration(0)
	for i, turn := range d.turns {
		badges[i] = badgesOf(turn)
		if badges[i].subagents > 0 {
			subs++
		}
		if badges[i].permissions > 0 {
			waits++
		}
		peak = max(peak, turn.active.total)
	}
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Padding(0, 0, 8, 0).Children(func() {
		ui.Textf(c, "%d 轮对话", len(d.turns)).FontSize(15).FontWeight(600).Grow(1).SingleLine()
		filter := a.sessions.turnFilter
		segmented(c, "轮次筛选", &filter, "全部", fmt.Sprintf("子 Agent %d", subs), fmt.Sprintf("等你 %d", waits))
		if filter != a.sessions.turnFilter {
			a.sessions.turnFilter = filter
			c.Invalidate()
		}
	})
	var shown []int
	for i := range d.turns {
		if turnShown(a.sessions.turnFilter, badges[i]) {
			shown = append(shown, i)
		}
	}
	hidden := 0
	if !a.sessions.allTurns && len(shown) > turnListShown {
		// The chosen turn stays listed even past the cut.
		var cut []int
		for n, i := range shown {
			if n < turnListShown || i == index {
				cut = append(cut, i)
			}
		}
		hidden = len(shown) - len(cut)
		shown = cut
	}
	ui.Column(c).Gap(2).FocusGroup(ui.Vertical).Label("轮次列表").Children(func() {
		if len(shown) == 0 {
			ui.Text(c, "没有符合筛选的轮次").FontSize(13).TextColor(t.TextMuted).Padding(8, 10)
		}
		for _, i := range shown {
			a.turnRow(c, d, i, i == index, badges[i], peak, content)
		}
	})
	switch {
	case hidden > 0:
		overviewLink(c, fmt.Sprintf("显示其余 %d 轮", hidden), func() { a.sessions.allTurns = true })
	case a.sessions.allTurns && len(shown) > turnListShown:
		overviewLink(c, "收起", func() { a.sessions.allTurns = false })
	}
}

func (a *App) turnRow(c *ui.Context, d *sessionDetailData, i int, on bool, badge turnBadges, peak time.Duration, content *replayIndex) {
	t := c.Theme()
	muted := token(t, "ink-3")
	turn := d.turns[i]
	prompt, quiet := turnPrompt(turn, content)
	b := withTip(c, ui.ButtonBase(c.Key("turn:"+turn.turn.ID)).Column().AlignItems(ui.Stretch).Gap(6).Padding(8, 10).Radius(7).Border(1, ui.Transparent).
		Label(turnLabel(turn)), fmt.Sprintf("第 %d 轮 · %s\n%s", i+1, activeText(turn), prompt))
	switch {
	case on:
		b.Background(token(t, "selected"))
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	if on && a.sessions.focusTurn {
		b.Focus()
		a.sessions.focusTurn = false
	}
	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			n := number(c, fmt.Sprintf("%02d", i+1)).FontSize(11.5).TextColor(muted).Width(18).Shrink(0)
			p := ui.Text(c, prompt).FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			v := number(c, activeText(turn)).FontSize(11.5).TextColor(muted).Shrink(0)
			if on {
				n.TextColor(t.Text)
				p.TextColor(t.Text).FontWeight(500)
				v.TextColor(t.Text)
			}
			if quiet {
				p.TextColor(muted)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 26).Children(func() {
			miniPhases(c, turn.active, peak, 120)
			if badge.subagents > 0 {
				turnBadge(c, "bot", fmt.Sprint(badge.subagents), sessionColor("phase-sub", t.Dark), fmt.Sprintf("%d 个子 Agent", badge.subagents))
			}
			if badge.permissions > 0 {
				turnBadge(c, "hand", fmt.Sprint(badge.permissions), t.Warning, fmt.Sprintf("%d 次等你确认", badge.permissions))
			}
			if badge.retries > 0 {
				turnBadge(c, "refresh", fmt.Sprint(badge.retries), sessionColor("phase-retry", t.Dark), fmt.Sprintf("%d 次重试或压缩", badge.retries))
			}
		})
	})
	if b.Clicked() || b.Focused() && !on {
		a.chooseSessionTurn(d, i)
		c.Invalidate()
	}
}

// miniPhases is a turn's phases as one short bar, its length at the scale
// of the longest turn.
func miniPhases(c *ui.Context, p phaseTotals, peak time.Duration, width float32) {
	t := c.Theme()
	w := float32(18)
	if peak > 0 && p.total > 0 {
		w = max(w, float32(float64(p.total)/float64(peak))*width)
	}
	ui.Box(c).Width(w).Height(5).Shrink(0).Draw(func(painter *ui.Painter, r ui.Rect) {
		if p.total <= 0 {
			painter.Fill(r, t.SurfaceHover, 2)
			return
		}
		x := r.X
		part := func(value time.Duration, color ui.Color) {
			if value <= 0 {
				return
			}
			seg := float32(float64(value)/float64(p.total)) * r.W
			painter.Fill(ui.Rect{X: x, Y: r.Y, W: max(seg-1, 1), H: r.H}, color, 1)
			x += seg
		}
		for i, value := range p.values {
			part(value, sessionColor(phaseKeys[i], t.Dark))
		}
		part(p.unknown, t.SurfaceHover)
	})
}

func turnBadge(c *ui.Context, icon, text string, color ui.Color, tip string) {
	withTip(c, ui.Row(c).Gap(3).AlignItems(ui.Center).Shrink(0), tip).Label(tip).Children(func() {
		ui.Icon(c, glyph(icon)).FontSize(11.5).TextColor(color)
		number(c, text).FontSize(11).TextColor(color)
	})
}

// subagentGroups marks the steps that start a run of sub-Agents working at
// the same time: groups[i] is the run's length at its first step, else 0.
func subagentGroups(steps []sessionStep) []int {
	groups := make([]int, len(steps))
	for i := 0; i < len(steps); {
		if steps[i].child == nil || steps[i].start.IsZero() {
			i++
			continue
		}
		end, j := steps[i].end, i+1
		for j < len(steps) && steps[j].child != nil && !steps[j].start.IsZero() && steps[j].start.Before(end) {
			if steps[j].end.After(end) {
				end = steps[j].end
			}
			j++
		}
		if j-i > 1 {
			groups[i] = j - i
		}
		i = j
	}
	return groups
}

// subagentGroupRow heads sub-Agents that ran at the same time: one span
// from the first start to the last end, which the main Agent waited out.
func subagentGroupRow(c *ui.Context, turn sessionTurnView, steps []sessionStep, axis stepAxis) {
	t := c.Theme()
	color := sessionColor("phase-sub", t.Dark)
	span := sessionStep{start: steps[0].start, end: steps[0].end, phase: phaseSub}
	for _, s := range steps[1:] {
		if s.end.After(span.end) {
			span.end = s.end
		}
	}
	if span.end.After(span.start) {
		span.elapsed = session.Duration{Value: span.end.Sub(span.start), Known: true, Estimated: true}
	}
	label := fmt.Sprintf("并行 %d 个子 Agent · %s", len(steps), stepDuration(span))
	withTip(c, ui.Row(c).Height(34).Padding(0, 8).Gap(10).Radius(6).AlignItems(ui.Center).Background(t.SurfaceHover).Label(label), label).Children(func() {
		ui.Row(c).Width(stepLabelWidth).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(11).Shrink(0)
			ui.Icon(c, glyph("bot")).FontSize(15).TextColor(color).Shrink(0)
			ui.Textf(c, "并行 %d 个子 Agent", len(steps)).FontSize(13).FontWeight(600).MinWidth(0).SingleLine()
		})
		stepSpan(c, turn, span, axis, fade(color, 90))
		number(c, stepDuration(span)).FontSize(13).TextColor(t.TextMuted).Width(stepValueWidth).Shrink(0).TextAlign(ui.End).SingleLine()
	})
}

// subagentBrief is what the parent asked a sub-Agent to do and which kind
// of Agent it was, from the dispatching tool call's input.
func subagentBrief(s sessionStep, content *replayIndex) (description, kind string) {
	if s.tool == nil || content == nil {
		return "", ""
	}
	e, ok := content.byTool[s.tool.ID]
	if !ok {
		return "", ""
	}
	var args struct {
		Description string `json:"description"`
		Type        string `json:"subagent_type"`
	}
	if json.Unmarshal([]byte(e.Input), &args) != nil {
		return "", ""
	}
	return firstLine(args.Description), firstLine(args.Type)
}

// childSteps are a sub-Agent's own steps across its turns, in time order.
func childSteps(child *session.Session) []sessionStep {
	var out []sessionStep
	for _, t := range child.Turns {
		out = append(out, turnSteps(t)...)
	}
	return out
}

// subagentOpen is an open sub-Agent: its own steps on the parent turn's
// axis, nested under its row, then what it used.
func (a *App) subagentOpen(c *ui.Context, d *sessionDetailData, turn sessionTurnView, s sessionStep, axis stepAxis, content *replayIndex, indent float32) {
	t := c.Theme()
	muted := token(t, "ink-3")
	rail := fade(sessionColor("phase-sub", t.Dark), 110)
	steps := childSteps(s.child)
	left := indent + subagentIndent
	nested := func(body func()) {
		ui.Row(c).Padding(0, 8).Gap(0).AlignItems(ui.Stretch).Children(func() {
			// Lines up with the rail of childStepRow: its box, then a gap of 6.
			ui.Box(c).Width(left + 12).Shrink(0)
			ui.Box(c).Width(2).Shrink(0).Background(rail)
			ui.Column(c).Grow(1).MinWidth(0).Padding(0, 0, 0, 10).Children(body)
		})
	}
	ui.Column(c.Key("sub-open")).Label("子 Agent 步骤").Children(func() {
		if len(steps) == 0 {
			nested(func() {
				ui.Text(c, "子 Agent 日志没有已记录的步骤").FontSize(13).TextColor(t.TextMuted).Padding(6, 0)
			})
		}
		for i, step := range steps {
			if i == subagentStepsShown {
				nested(func() {
					ui.Textf(c, "其余 %d 步见子会话日志", len(steps)-i).FontSize(12).TextColor(muted).Padding(4, 0)
				})
				break
			}
			childStepRow(c, turn, step, axis, left)
		}
		nested(func() {
			ui.Row(c).Gap(14).Wrap().AlignItems(ui.Center).Padding(6, 0).Children(func() {
				for _, fact := range subagentFacts(s.child) {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Text(c, fact[0]).FontSize(11.5).TextColor(muted)
						number(c, fact[1]).FontSize(11.5)
					})
				}
				ui.Spacer(c)
				key := "sub-io:" + s.child.ID
				if s.tool != nil && content != nil {
					if _, ok := content.byTool[s.tool.ID]; ok {
						label := "任务与结果"
						if a.sessions.expanded[key] {
							label = "收起任务与结果"
						}
						overviewLink(c, label, func() {
							if a.sessions.expanded == nil {
								a.sessions.expanded = map[string]bool{}
							}
							a.sessions.expanded[key] = !a.sessions.expanded[key]
						})
					}
				}
				if s.child.Path != "" {
					overviewLink(c, "定位子会话日志", func() { a.reveal(s.child.Path) })
				}
			})
			if a.sessions.expanded["sub-io:"+s.child.ID] && content != nil {
				ui.Column(c).Gap(10).Padding(0, 0, 8, 0).Children(func() { a.stepContentBlocks(c, turn, s, content) })
			}
		})
	})
}

// childStepRow is one of a sub-Agent's steps, nested under it. Only the
// parent's log is read for content, so it shows names and timing.
func childStepRow(c *ui.Context, turn sessionTurnView, step sessionStep, axis stepAxis, indent float32) {
	t := c.Theme()
	muted := token(t, "ink-3")
	icon, name, _ := stepFace(step, nil)
	label := "子 Agent 步骤 · " + step.name + " · " + stepDuration(step)
	withTip(c, ui.Row(c).Height(28).Padding(0, 8).Gap(10).AlignItems(ui.Center).Label(label), label).Children(func() {
		ui.Row(c).Width(stepLabelWidth).Height(28).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(indent + 6).Shrink(0)
			ui.Box(c).Width(2).Height(28).Shrink(0).Background(fade(sessionColor("phase-sub", t.Dark), 110))
			ui.Box(c).Width(4).Shrink(0)
			ui.Icon(c, glyph(icon)).FontSize(13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, name).FontSize(13).MinWidth(0).SingleLine()
		})
		stepTrack(c, turn, step, axis)
		v := number(c, stepDuration(step)).FontSize(12).TextColor(muted).Width(stepValueWidth).Shrink(0).TextAlign(ui.End).SingleLine()
		if step.tool != nil && (step.tool.Failed || step.tool.Interrupted) || step.request != nil && step.request.APIError {
			v.TextColor(t.Warning)
		}
	})
}

// subagentFacts sums what a sub-Agent used. Missing usage is not zero.
func subagentFacts(child *session.Session) [][2]string {
	var tokens session.Tokens
	requests, tools := 0, 0
	known := false
	for _, t := range child.Turns {
		requests += len(t.Requests)
		tools += len(t.Tools)
		for _, r := range t.Requests {
			if r.UsageKnown {
				known = true
				tokens.Input += r.Tokens.Input
				tokens.Output += r.Tokens.Output
				tokens.CacheRead += r.Tokens.CacheRead
				tokens.CacheWrite += r.Tokens.CacheWrite
			}
		}
	}
	usage, cache := "暂不支持", "暂不支持"
	if known {
		usage = tokenNumber(tokens.Total())
		if in := tokens.InputTotal(); in > 0 {
			cache = fmt.Sprintf("%.0f%%", 100*float64(tokens.CacheRead)/float64(in))
		}
	}
	return [][2]string{{"Token", usage}, {"请求", fmt.Sprint(requests)}, {"工具", fmt.Sprint(tools)}, {"缓存", cache}}
}
