package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// The session page is the design's 会话详情: the session's totals, where
// its active time went, a bar per turn, and the chosen turn's steps; a step
// opens in place to show what the log recorded. The conversation itself is
// one click away, in replay.

const (
	stepLabelWidth = 240
	stepValueWidth = 64
)

// Step rows: padding 8 and a gap 10 precede the track; a gap 10, the
// duration and padding 8 follow it. The axis-break overlay uses the same
// offsets.
const (
	stepTrackLeft  = 8 + stepLabelWidth + 10
	stepTrackRight = 10 + stepValueWidth + 8
)

// sessionTurnIndex is the turn the page shows: the one chosen, else the
// turn with the longest concern, else the first.
func (a *App) sessionTurnIndex(d *sessionDetailData) int {
	if len(d.turns) == 0 {
		return -1
	}
	for i, turn := range d.turns {
		if turn.turn.ID == a.sessions.selectedTurn {
			return i
		}
	}
	index := 0
	if len(d.concerns) > 0 && d.concerns[0].turn >= 0 {
		index = d.concerns[0].turn
	}
	a.chooseSessionTurn(d, index)
	return index
}

func (a *App) sessionPage(c *ui.Context, d *sessionDetailData, width float32) {
	t := c.Theme()
	root := d.session
	inner := width - 64
	ui.Scroll(c.Key("session-page:" + a.sessions.selected)).Grow(1).MinHeight(0).Label("会话详情内容").Children(func() {
		ui.Column(c).Padding(22, 32, 32, 32).Gap(22).Children(func() {
			a.sessionHeader(c, d, inner)
			if root.UnsupportedReason != "" {
				ui.Text(c, root.UnsupportedReason).FontSize(13).TextColor(t.TextMuted)
				return
			}
			if root.Incomplete {
				ui.Text(c, "当前会话记录不完整").FontSize(13).TextColor(t.Warning)
			}
			a.sessionPhases(c, d)
			index := a.sessionTurnIndex(d)
			a.sessionDiagnosis(c, d, index)
			switch {
			case index < 0:
				ui.Text(c, "没有可识别的轮次").FontSize(13).TextColor(t.TextMuted)
			case inner >= 860:
				// The turns on the left, the chosen turn's steps beside them.
				ui.Row(c).Gap(24).AlignItems(ui.Start).Children(func() {
					ui.Column(c).Width(turnListWidth).Shrink(0).Gap(2).Children(func() { a.turnList(c, d, index) })
					ui.Column(c).Grow(1).MinWidth(0).Gap(10).Padding(0, 0, 0, 24).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).
						Children(func() { a.turnDetail(c, d, index) })
				})
			default:
				ui.Column(c).Gap(2).Children(func() { a.turnList(c, d, index) })
				ui.Column(c).Gap(10).Children(func() { a.turnDetail(c, d, index) })
			}
			a.sessionExtras(c, d, inner)
		})
	})
}

// sessionHeader names the session and states its totals on the right, as
// the home page's metrics do.
func (a *App) sessionHeader(c *ui.Context, d *sessionDetailData, width float32) {
	t := c.Theme()
	root := d.session
	muted := token(t, "ink-3")
	title := func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(8).Children(func() {
			ui.Text(c, recentLabel(root)).FontSize(22).FontWeight(600).SingleLine().Tooltip(recentLabel(root))
			ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Shrink(0).Background(agentColor(root.Agent, t.Dark))
				ui.Text(c, sessionAgentName(root.Agent)).FontSize(13).Shrink(0).SingleLine()
				sep := func() { ui.Text(c, "·").FontSize(13).TextColor(muted).Shrink(0) }
				if len(d.models) > 0 {
					sep()
					number(c, d.models[0].name).FontSize(13).TextColor(t.TextMuted).Shrink(0).SingleLine()
				}
				if root.CWD != "" {
					sep()
					number(c, shortPath(a.home, root.CWD)).FontSize(13).TextColor(t.TextMuted).MinWidth(0).SingleLine().Tooltip(root.CWD)
				}
				if root.GitBranch != "" {
					sep()
					number(c, root.GitBranch).FontSize(13).TextColor(t.TextMuted).MinWidth(0).SingleLine()
				}
				if span := sessionClockRange(root); span != "" {
					sep()
					ui.Text(c, span).FontSize(13).TextColor(t.TextMuted).Shrink(0).SingleLine()
				}
			})
		})
	}
	stats := sessionStats(d)
	cells := func(align ui.Align) {
		for _, m := range stats {
			ui.Column(c).Gap(3).AlignItems(align).Shrink(0).Tooltip(m.tip).Children(func() {
				ui.Text(c, m.label).FontSize(11.5).TextColor(muted)
				v := number(c, m.value).FontSize(17).FontWeight(600)
				if m.muted {
					v.TextColor(muted)
				}
				if m.note != "" {
					number(c, m.note).FontSize(11).TextColor(muted)
				}
			})
		}
	}
	if width >= 980 {
		ui.Row(c).Gap(28).AlignItems(ui.Start).Children(func() {
			title()
			cells(ui.End)
		})
		return
	}
	ui.Column(c).Gap(16).Children(func() {
		title()
		ui.Row(c).Gap(28).Wrap().Children(func() { cells(ui.Start) })
	})
}

type sessionStat struct {
	label, value, note, tip string
	muted                   bool
}

func sessionStats(d *sessionDetailData) []sessionStat {
	root, sum := d.session, d.summary
	length := sessionStat{label: "时长", value: "暂不支持", muted: true}
	if !root.Start.IsZero() && !root.End.Before(root.Start) {
		length = sessionStat{label: "时长", value: session.FormatDuration(root.End.Sub(root.Start)), tip: "第一条到最后一条记录，含等待你回复"}
	}
	tokens := sessionStat{label: "Token", value: tokenNumber(sum.Tokens.Total()), tip: fmt.Sprintf("输入 %s · 输出 %s", tokenNumber(sum.Tokens.InputTotal()), tokenNumber(sum.Tokens.Output))}
	if sum.Requests == 0 || sum.UnmeteredRequests == sum.Requests {
		tokens = sessionStat{label: "Token", value: "暂不支持", muted: true, tip: "日志没有记录请求用量"}
	} else if sum.UnmeteredRequests > 0 {
		tokens.tip = "部分请求没有用量记录，只计已识别部分"
	}
	if sum.CacheHitKnown {
		tokens.note = fmt.Sprintf("缓存 %.0f%%", 100*sum.CacheHit)
	}
	rate := sessionStat{label: "输出速率", value: "暂不支持", muted: true, tip: "日志缺少可计算的生成速率"}
	if sum.OutputRate.Samples > 0 {
		rate = sessionStat{label: "输出速率", value: fmt.Sprintf("%.0f tok/s", sum.OutputRate.Median), note: fmt.Sprintf("P90 %.0f · 估算", sum.OutputRate.P90), tip: "输出 Token / 估算生成时间，中位数"}
	}
	return []sessionStat{
		length, tokens, rate,
		{label: "轮次 / 工具", value: fmt.Sprintf("%d / %d", len(d.turns), d.toolCount)},
		{label: "子 Agent", value: fmt.Sprint(len(d.tracks) - 1)},
	}
}

// sessionClockRange is when the session ran, as "11/6 14:02 – 14:26".
func sessionClockRange(s session.Session) string {
	if s.Start.IsZero() || s.End.IsZero() {
		return ""
	}
	from, to := s.Start.Local(), s.End.Local()
	end := to.Format("15:04")
	if from.YearDay() != to.YearDay() || from.Year() != to.Year() {
		end = fmt.Sprintf("%d/%d %s", to.Month(), to.Day(), end)
	}
	return fmt.Sprintf("%d/%d %s – %s", from.Month(), from.Day(), from.Format("15:04"), end)
}

// activeTotals adds the turns' own work, leaving out the waits between
// them, which it returns apart.
func activeTotals(d *sessionDetailData) (p phaseTotals, waiting, away time.Duration) {
	for _, turn := range d.turns {
		for i, v := range turn.active.values {
			p.values[i] += v
		}
		p.unknown += turn.active.unknown
		p.total += turn.active.total
		if turn.gapAway {
			away += turn.gap
		} else {
			waiting += turn.gap
		}
	}
	return p, waiting, away
}

// sessionPhases is where the active time went: one bar of the phases with
// their legend. Waits between turns are not work, so they are stated apart.
func (a *App) sessionPhases(c *ui.Context, d *sessionDetailData) {
	muted := token(c.Theme(), "ink-3")
	p, waiting, away := activeTotals(d)
	ui.Column(c).Gap(10).Children(func() {
		phaseBar(c, p, waiting, away)
		if !session.LookupAgent(d.session.Agent).PermissionWait {
			ui.Text(c, "模型时间按输入完成到响应结束的间隔估算；权限等待暂不支持").FontSize(11.5).TextColor(muted).MaxLines(2)
		}
		if unlinked := unlinkedConcerns(d); unlinked > 0 {
			ui.Textf(c, "另有 %d 处子 Agent 耗时异常无法关联到轮次，见下方的 Agent 调用", unlinked).FontSize(11.5).TextColor(muted).MaxLines(2)
		}
	})
}

// phaseBar is one bar of the active time's phases with their legend; the
// waits between turns are not work, so they are stated apart.
func phaseBar(c *ui.Context, p phaseTotals, waiting, away time.Duration) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Box(c).Height(10).Label("阶段占比条").Tooltip(phaseDescription(p)).Draw(func(painter *ui.Painter, r ui.Rect) {
		if p.total <= 0 {
			painter.Fill(r, t.SurfaceHover, 2)
			return
		}
		x := r.X
		part := func(value time.Duration, color ui.Color) {
			if value <= 0 {
				return
			}
			w := float32(float64(value)/float64(p.total)) * r.W
			painter.Fill(ui.Rect{X: x, Y: r.Y, W: max(w-2, 1), H: r.H}, color, 2)
			x += w
		}
		for i, value := range p.values {
			part(value, sessionColor(phaseKeys[i], t.Dark))
		}
		part(p.unknown, t.SurfaceHover)
	})
	ui.Row(c).Gap(18).Wrap().AlignItems(ui.Center).Children(func() {
		for i, value := range p.values {
			if value == 0 && i > int(phaseGen) {
				continue
			}
			share := ""
			if p.total > 0 {
				share = fmt.Sprintf("%.0f%%", 100*float64(value)/float64(p.total))
			}
			legendValue(c, sessionColor(phaseKeys[i], t.Dark), phaseNames[i], session.FormatDuration(value), share, "")
		}
		if p.unknown > 0 {
			legendValue(c, t.SurfaceHover, "未归因", session.FormatDuration(p.unknown), "", "缺少对应阶段数据，常见于模型首字前的排队与日志间隙")
		}
		ui.Spacer(c)
		var waits []string
		if waiting > 0 {
			waits = append(waits, "轮间等待你 ≈ "+session.FormatDuration(waiting))
		}
		if away > 0 {
			waits = append(waits, "离开 ≈ "+session.FormatDuration(away))
		}
		if len(waits) > 0 {
			number(c, strings.Join(waits, " · ")).FontSize(11.5).TextColor(muted).Tooltip("轮与轮之间的间隔不计入活跃时间")
		}
	})
}

// legendValue is a legend entry with its amount and share.
func legendValue(c *ui.Context, color ui.Color, name, value, share, tip string) {
	t := c.Theme()
	row := ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0)
	if tip != "" {
		row.Tooltip(tip)
	}
	row.Children(func() {
		ui.Box(c).Size(8, 8).Radius(2).Background(color)
		ui.Text(c, name).FontSize(13).TextColor(t.TextMuted)
		number(c, value).FontSize(13)
		if share != "" {
			number(c, share).FontSize(11).TextColor(token(t, "ink-3"))
		}
	})
}

// turnDetail is the chosen turn: what was asked, then every step on the
// turn's own time axis.
func (a *App) turnDetail(c *ui.Context, d *sessionDetailData, index int) {
	t := c.Theme()
	muted := token(t, "ink-3")
	turn := d.turns[index]
	replay := a.sessionReplay(d)
	content := replay.ready()
	axis := newStepAxis(turn, a.foldLongSteps)
	groups := subagentGroups(turn.steps)
	permissions := 0
	for _, s := range turn.steps {
		if s.tool != nil && s.tool.PermissionWait || s.wait != nil && s.wait.Kind == "permission" {
			permissions++
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Textf(c, "第 %d 轮", index+1).FontSize(15).FontWeight(600).Shrink(0)
		parts := []string{activeText(turn), fmt.Sprintf("%d 步", len(turn.steps))}
		if n := len(turn.turn.Subagents); n > 0 {
			sub := fmt.Sprintf("%d 个子 Agent", n)
			if slices.ContainsFunc(groups, func(g int) bool { return g > 1 }) {
				sub += " 并行"
			}
			parts = append(parts, sub)
		}
		if permissions > 0 {
			parts = append(parts, fmt.Sprintf("%d 次权限确认", permissions))
		}
		number(c, strings.Join(parts, " · ")).FontSize(13).TextColor(muted).Grow(1).MinWidth(0).SingleLine()
		if turn.turn.Interrupted {
			ui.Text(c, "本轮已中断").FontSize(13).TextColor(t.Warning).Shrink(0)
		}
		fold := a.foldLongSteps
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Switch(c, &fold).Label("折叠长步骤")
			ui.Text(c, "折叠长步骤").FontSize(13).TextColor(t.TextMuted)
		})
		if fold != a.foldLongSteps {
			a.setFoldLongSteps(fold)
			c.Invalidate()
		}
	})
	ui.Row(c).Gap(10).Padding(9, 12).Radius(7).Background(t.SurfaceHover).AlignItems(ui.Start).Label("本轮输入").Children(func() {
		ui.Icon(c, glyph("user")).FontSize(15).TextColor(t.TextMuted).Margin(1, 0, 0, 0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			switch {
			case replay == nil || replay.err != "":
				ui.Text(c, "对话内容暂不可用").FontSize(13).TextColor(t.TextMuted)
			case content == nil:
				ui.Text(c, "正在读取对话内容…").FontSize(13).TextColor(t.TextMuted)
			default:
				text, quiet := turnPrompt(turn, content)
				if prompts := content.prompts[turn.index]; len(prompts) > 0 && !quiet {
					text = clipLines(strings.TrimSpace(commandText(prompts[0].Text)), 3, 400)
				}
				p := ui.Text(c, text).FontSize(13).MaxLines(3).Selectable()
				if quiet {
					p.TextColor(t.TextMuted)
				}
				if n := content.context[turn.index]; n > 0 {
					ui.Textf(c, "另有 %d 条自动注入的上下文，可在回放中展开", n).FontSize(11.5).TextColor(muted)
				}
			}
		})
	})
	ui.Row(c).Padding(4, 8, 0, 8).Gap(10).Children(func() {
		ui.Box(c).Width(stepLabelWidth).Shrink(0)
		ui.Row(c).Grow(1).MinWidth(40).Justify(ui.SpaceBetween).Children(func() {
			if axis.total <= 0 {
				ui.Text(c, "时间轴暂不支持").FontSize(11).TextColor(muted)
				return
			}
			marks := []time.Duration{0, axis.total}
			if len(axis.cuts) == 0 {
				marks = []time.Duration{0, axis.total / 4, axis.total / 2, axis.total * 3 / 4, axis.total}
			}
			for _, m := range marks {
				number(c, session.FormatDuration(m)).FontSize(11).TextColor(muted)
			}
		})
		ui.Box(c).Width(stepValueWidth).Shrink(0)
	})
	a.sessions.selectedStep = max(0, min(a.sessions.selectedStep, len(turn.steps)-1))
	ui.Column(c.Key("steps:" + turn.turn.ID)).FocusGroup(ui.Vertical).Label("单轮步骤瀑布图").Children(func() {
		if len(turn.steps) == 0 {
			ui.Text(c, "本轮没有已记录的步骤").FontSize(13).TextColor(t.TextMuted).Padding(8)
		}
		// The choice can move while the rows run; the open step follows next frame.
		chosen, open := a.sessions.selectedStep, a.sessions.stepOpen
		// Sub-Agents that ran at the same time sit under one heading, indented.
		groupEnd := -1
		for i, step := range turn.steps {
			if groups[i] > 1 {
				subagentGroupRow(c, turn, turn.steps[i:i+groups[i]], axis)
				groupEnd = i + groups[i]
			}
			indent := float32(0)
			if i < groupEnd {
				indent = subagentIndent
			}
			a.stepRow(c, turn, i, step, axis, content, indent)
			switch {
			case i != chosen || !open:
			case step.child != nil:
				a.subagentOpen(c, d, turn, step, axis, content, indent)
			default:
				ui.Column(c.Key("step-open")).Padding(4, 8, 12, 30).Children(func() {
					ui.Column(c).Padding(2, 0, 2, 16).BorderWidth(0, 0, 0, 2).BorderColor(token(t, "line-strong")).
						Children(func() { a.stepInspector(c, d, index, content) })
				})
			}
		}
	})
	var notes []string
	for _, cut := range axis.cuts {
		notes = append(notes, "折叠 "+session.FormatDuration(cut.to-cut.from)+"（+"+session.FormatDuration(cut.from)+" 至 +"+session.FormatDuration(cut.to)+"）")
	}
	if a.foldLongSteps && len(axis.cuts) == 0 {
		notes = append(notes, "本轮没有符合条件的长步骤")
	}
	notes = append(notes, "区间为日志估算；并行步骤分行展示，不相加")
	ui.Text(c, strings.Join(notes, " · ")).FontSize(11.5).TextColor(muted).MaxLines(2)
	for _, e := range turnEvents(content, index) {
		text := e.Name
		if e.Text != "" {
			text += " · " + firstLine(e.Text)
		}
		number(c, stepClock(e.Start)+"  "+text).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Tooltip(e.Text)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(10, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if turn.gap > 0 {
			ui.Icon(c, glyph("hourglass")).FontSize(13).TextColor(muted)
			text := "回复后 " + session.FormatDuration(turn.gap) + " 才收到下一条消息，计入「等待你」，不算本轮耗时。"
			if turn.gapAway {
				text = "之后约 " + session.FormatDuration(turn.gap) + " 没有记录（日志无精确起止），视为离开，不算本轮耗时。"
			}
			ui.Text(c, text).FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(0).MaxLines(2)
		} else {
			ui.Spacer(c)
		}
		if outlineButton(c, "prev-turn", "上一轮").Disabled(index == 0).Clicked() {
			a.chooseSessionTurn(d, index-1)
			a.sessions.focusTurn = true
			c.Invalidate()
		}
		if outlineButton(c, "next-turn", "下一轮").Disabled(index == len(d.turns)-1).Clicked() {
			a.chooseSessionTurn(d, index+1)
			a.sessions.focusTurn = true
			c.Invalidate()
		}
	})
}

// stepFace is how a step shows: an icon, a short name and what it worked
// on.
func stepFace(s sessionStep, content *replayIndex) (icon, name, detail string) {
	_, detail = stepContent(s, content)
	switch {
	case s.request != nil:
		icon, name = "pen", "生成"
		if strings.HasPrefix(detail, "思考：") {
			icon, name, detail = "sparkle", "思考", strings.TrimPrefix(detail, "思考：")
		}
		if s.request.Retry {
			icon, name = "refresh", "重试"
		}
		if s.request.APIError {
			icon, name = "alert", "请求失败"
		}
	case s.child != nil:
		// Named by its task when the dispatching call recorded one, with
		// the kind of Agent beside it; else by its id.
		icon, name, detail = "bot", "子 Agent", strings.TrimPrefix(s.name, "子 Agent · ")
		if task, kind := subagentBrief(s, content); task != "" {
			name, detail = task, kind
		}
	case s.tool != nil:
		icon, name = toolIcon(s.tool.Name), s.tool.Name
		if name == "" {
			name = "工具"
		}
		if s.tool.AgentID != "" {
			icon = "bot"
		}
	case s.wait != nil:
		icon, name = "hourglass", s.name
	default:
		icon, name = "alert", s.name
	}
	return icon, name, detail
}

// toolIcon picks a glyph by what the tool does, across the Agents' names.
func toolIcon(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "edit") || strings.Contains(n, "write") || strings.Contains(n, "patch"):
		return "file-edit"
	case strings.Contains(n, "read") || n == "view" || n == "ls" || strings.Contains(n, "list"):
		return "file"
	case strings.Contains(n, "grep") || strings.Contains(n, "glob") || strings.Contains(n, "search") || strings.Contains(n, "find"):
		return "search"
	case strings.Contains(n, "bash") || strings.Contains(n, "shell") || strings.Contains(n, "exec") || strings.Contains(n, "command"):
		return "terminal"
	case n == "skill":
		return "wand"
	case n == "task" || n == "agent" || strings.Contains(n, "subagent"):
		return "bot"
	case strings.Contains(n, "web") || strings.Contains(n, "fetch"):
		return "link"
	}
	return "zap"
}

func stepLabel(i int, step sessionStep) string {
	return fmt.Sprintf("步骤 %d · %s · %s", i+1, step.name, stepDuration(step))
}

func (a *App) stepRow(c *ui.Context, turn sessionTurnView, i int, step sessionStep, axis stepAxis, content *replayIndex, indent float32) {
	t := c.Theme()
	selected := i == a.sessions.selectedStep
	open := selected && a.sessions.stepOpen
	icon, name, detail := stepFace(step, content)
	label := stepLabel(i, step)
	b := ui.ButtonBase(c.Key(i)).Height(36).Padding(0, 8).Gap(10).Radius(6).AlignItems(ui.Center).Border(1, ui.Transparent).Label(label).Tooltip(label)
	switch {
	case selected:
		b.Background(token(t, "selected"))
	case b.Hovered():
		b.Background(t.SurfaceHover)
	}
	b.DrawOver(func(p *ui.Painter, r ui.Rect) {
		// One neutral axis break crosses the rows, never a phase-colored
		// band; an open step's content interrupts it.
		trackX, trackW := r.X+stepTrackLeft, r.W-stepTrackLeft-stepTrackRight
		for _, cut := range axis.cuts {
			x := trackX + (axis.at(cut.from)+axis.at(cut.to))/2*trackW
			for _, dx := range []float32{-2, 2} {
				p.Fill(ui.Rect{X: float32(math.Round(float64(x + dx))), Y: r.Y, W: 1, H: r.H}, t.TextMuted, 0)
			}
		}
	})
	b.Children(func() {
		ui.Row(c).Width(stepLabelWidth).Shrink(0).Gap(6).Padding(0, 0, 0, indent).AlignItems(ui.Center).Children(func() {
			chevron := "chevron-right"
			if open {
				chevron = "chevron-down"
			}
			ui.Icon(c, glyph(chevron)).FontSize(11.5).TextColor(token(t, "ink-3")).Shrink(0)
			glyphColor := t.TextMuted
			if step.child != nil {
				glyphColor = sessionColor("phase-sub", t.Dark)
			}
			ui.Icon(c, glyph(icon)).FontSize(15).TextColor(glyphColor).Shrink(0)
			n := ui.Text(c, name).FontSize(13).Shrink(1).MinWidth(0).SingleLine().MaxWidth(150)
			if selected {
				n.FontWeight(600)
			}
			if detail != "" {
				number(c, detail).FontSize(12).TextColor(token(t, "ink-3")).MinWidth(0).SingleLine()
			}
		})
		stepTrack(c, turn, step, axis)
		value := stepDuration(step)
		if a.foldLongSteps && step.elapsed.Known && session.FoldLongStep(step.elapsed.Value, turn.phases.total) && !step.start.IsZero() && !step.end.IsZero() {
			value += " ∕∕"
		}
		v := number(c, value).FontSize(13).TextColor(t.TextMuted).Width(stepValueWidth).Shrink(0).TextAlign(ui.End).SingleLine()
		if stepFailed(step, content) {
			v.TextColor(t.Warning)
		}
	})
	switch {
	case b.Clicked():
		// A click opens the step, or closes it when it is already open.
		a.sessions.stepOpen = !open
		a.sessions.selectedStep = i
		c.Invalidate()
	case b.FocusVisible() && !selected:
		// Arrow keys move the choice; an open step stays open as it moves.
		a.sessions.selectedStep = i
		c.Invalidate()
	}
}

// stepInspector is the chosen step, open under its row: its facts, then
// the content the log recorded for it, when content has been read.
func (a *App) stepInspector(c *ui.Context, d *sessionDetailData, index int, content *replayIndex) {
	t := c.Theme()
	muted := token(t, "ink-3")
	turn := d.turns[index]
	if len(turn.steps) == 0 {
		ui.Text(c, "本轮没有已记录的步骤").FontSize(13).TextColor(t.TextMuted)
		return
	}
	s := turn.steps[max(0, min(a.sessions.selectedStep, len(turn.steps)-1))]
	ui.Column(c).Gap(12).Label("步骤详情").Children(func() {
		facts := append([][2]string{{"总耗时", stepDuration(s)}, {"开始于", stepClock(s.start)}}, stepFacts(s, d.session.Agent)...)
		ui.Row(c).Wrap().Gap(14).Children(func() {
			for _, fact := range facts {
				ui.Column(c).Gap(2).Width(150).Children(func() {
					ui.Text(c, fact[0]).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
					v := ui.Text(c, fact[1]).FontSize(13).MaxLines(2).Tooltip(fact[1])
					if fact[1] == "暂不支持" || fact[1] == "未记录" {
						v.TextColor(muted)
					}
				})
			}
		})
		if s.tool != nil && s.tool.PermissionWait {
			ui.Text(c, "权限等待为整个工具间隔的估算，包含执行时间。").FontSize(11.5).TextColor(muted)
		}
		if content != nil {
			a.stepContentBlocks(c, turn, s, content)
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph("lock")).FontSize(13).TextColor(muted)
			ui.Text(c, "内容从原始日志按需读取，只留在内存，不写入缓存。").FontSize(12).TextColor(muted).MinWidth(0)
		})
	})
}

// stepContentBlocks shows what the log recorded for a step: a request's
// thinking and reply, a tool's input and output.
func (a *App) stepContentBlocks(c *ui.Context, turn sessionTurnView, s sessionStep, content *replayIndex) {
	t := c.Theme()
	key := turn.turn.ID + ":" + s.name
	if r := s.request; r != nil {
		entries := content.byRequest[r.ID]
		if len(entries) == 0 && len(content.calls[r.ID]) == 0 {
			ui.Text(c, "日志没有记录这次请求的内容").FontSize(13).TextColor(t.TextMuted)
		}
		// Thinking logged as a signature-only block beside one with text
		// shows once, as the text; signatures alone are noted once.
		thought, noted := false, false
		for _, e := range entries {
			thought = thought || e.Kind == session.EntryThinking && strings.TrimSpace(e.Text) != ""
		}
		for i, e := range entries {
			switch e.Kind {
			case session.EntryThinking:
				if strings.TrimSpace(e.Text) == "" {
					if !thought && !noted {
						ui.Text(c, "思考 · 日志只保存了签名，没有思考正文").FontSize(13).TextColor(t.TextMuted)
					}
					noted = true
					continue
				}
				a.replayText(c, fmt.Sprintf("%s:%d", key, i), "思考", e.Text, e.Truncated, proseBlock)
			case session.EntryAssistant:
				a.replayText(c, fmt.Sprintf("%s:%d", key, i), "回复", e.Text, e.Truncated, proseBlock)
			case session.EntryEvent:
				a.replayText(c, fmt.Sprintf("%s:%d", key, i), e.Name, e.Text, e.Truncated, proseBlock)
			}
		}
		if calls := content.calls[r.ID]; len(calls) > 0 {
			names := []string{}
			for _, call := range calls {
				names = append(names, call.Name)
			}
			ui.Text(c, "发起工具调用 · "+strings.Join(compactNames(names), "、")+"（见后续步骤）").FontSize(13).TextColor(t.TextMuted).MaxLines(3)
		}
	}
	if s.tool != nil {
		e, ok := content.byTool[s.tool.ID]
		if !ok {
			ui.Text(c, "日志没有记录这次调用的输入和输出").FontSize(13).TextColor(t.TextMuted)
			return
		}
		a.toolInput(c, key+":in", e.Input, 0)
		title := "输出"
		if e.Error {
			title = "输出 · 失败"
		}
		if e.End.IsZero() {
			ui.Text(c, "没有记录输出").FontSize(13).TextColor(t.TextMuted)
		} else {
			a.replayText(c, key+":out", title, e.Text, e.Truncated, terminalBlock)
		}
	}
}

// sessionExtras are the session's other measures: who ran when, how fast
// each model wrote, and what was called.
func (a *App) sessionExtras(c *ui.Context, d *sessionDetailData, width float32) {
	t := c.Theme()
	unsupported := !session.LookupAgent(d.session.Agent).Skills
	color := agentColor(d.session.Agent, t.Dark)
	tool := sessionColor("phase-tool", t.Dark)
	ui.Column(c).Gap(22).Padding(22, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		if width >= 860 {
			ui.Row(c).Gap(homeGap).AlignItems(ui.Start).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(22).Children(func() {
					a.sessionAgentTimeline(c, d)
					a.sessionModelSpeeds(c, d, color)
				})
				ui.Column(c).Width(homeSide).Shrink(0).Gap(22).Children(func() {
					a.sessionCalls(c, "Skill 调用", d.skills, unsupported, t.Text)
					a.sessionCalls(c, "工具调用", d.tools, false, tool)
				})
			})
		} else {
			a.sessionAgentTimeline(c, d)
			a.sessionModelSpeeds(c, d, color)
			a.sessionCalls(c, "Skill 调用", d.skills, unsupported, t.Text)
			a.sessionCalls(c, "工具调用", d.tools, false, tool)
		}
		ui.Text(c, "时间与速率为估算；用量包含已关联父会话的子 Agent，缺失字段不按零处理。").FontSize(11.5).TextColor(token(t, "ink-3")).MaxLines(3)
	})
}

// sessionBar is the title bar of an open session: where it sits, and its
// actions. In the replay it holds the conversation's search and filter.
func (a *App) sessionBar(c *ui.Context) {
	t := c.Theme()
	s := &a.sessions
	rows := a.sessionRows()
	d := a.selectedSessionData(rows)
	title := "会话"
	if d != nil {
		title = recentLabel(d.session)
	}
	back := func(label, text string) {
		b := ui.ButtonBase(c.Key("session-back")).Gap(6).Padding(4, 6).Radius(6).AlignItems(ui.Center).Label(label).Shrink(0)
		hover := b.Hovered()
		if hover {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			ui.Icon(c, glyph("arrow-left")).FontSize(15).TextColor(t.TextMuted)
			if text != "" {
				label := ui.Text(c, text).FontSize(13).TextColor(t.TextMuted).SingleLine()
				if hover {
					label.TextColor(t.Text)
				}
			}
		})
		if b.Clicked() {
			a.closeSession()
			c.Invalidate()
		}
	}
	if s.replayOpen {
		back("返回会话详情", "")
		ui.Text(c, title).FontSize(15).FontWeight(600).MinWidth(0).Shrink(1).SingleLine().Tooltip(title)
		ui.Box(c).Grow(1).MinWidth(8)
		if n := a.replayMatches(d); n >= 0 {
			ui.Textf(c, "%d 轮匹配", n).FontSize(12).TextColor(token(t, "ink-3")).Shrink(0)
		}
		ui.SearchField(c, &s.replayQuery).Label("在对话中查找").Placeholder("在对话中查找").Width(200).Height(28).Radius(7).
			Background(t.Surface).Border(1, t.Border).PlaceholderColor(token(t, "ink-3")).Shrink(1).MinWidth(100)
		segmented(c, "对话筛选", &s.replayFilter, "全部", "只看工具", "只看回复")
		return
	}
	back("返回全部会话", "全部会话")
	ui.Icon(c, glyph("chevron-right")).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
	ui.Text(c, title).FontSize(13).MinWidth(0).Shrink(1).SingleLine().Tooltip(title)
	ui.Box(c).Grow(1).MinWidth(8)
	step := func(icon, label string, by int) {
		b := ui.ButtonBase(c.Key(label)).Size(28, 28).Radius(7).Center().Background(t.Surface).Border(1, token(t, "line-strong")).
			Label(label).Tooltip(label).Shrink(0).Disabled(s.index+by < 0 || s.index+by >= len(rows))
		if b.Hovered() {
			b.Background(t.SurfaceHover)
		}
		b.Children(func() { ui.Icon(c, glyph(icon)).FontSize(15).TextColor(t.TextMuted) })
		if b.Clicked() {
			a.stepSession(by)
			c.Invalidate()
		}
	}
	step("chevron-up", "上一个会话", -1)
	step("chevron-down", "下一个会话", 1)
	if d == nil {
		return
	}
	if outlineButton(c, "reveal-log", "定位原始日志").Height(28).Tooltip("在访达中显示原始日志").Clicked() {
		a.reveal(d.session.Path)
	}
	replay := primaryButton(c, "open-replay", "messages", "回放对话").Disabled(d.session.UnsupportedReason != "")
	if replay.Clicked() {
		s.replayOpen = true
		s.focusTurn = true
		c.Invalidate()
	}
}
