package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Gaps shorter than this between turns are ordinary reading time; longer
// ones are shown as separators so idle hours never look like work.
const turnGapShown = 30 * time.Second

// sessionTiming lists the session turn by turn: what was asked, how long the
// turn worked, where that time went, and the wait before the next turn.
func (a *App) sessionTiming(c *ui.Context, d *sessionDetailData) {
	t := c.Theme()
	replay := a.sessionReplay(d)
	index := replay.ready()
	var active, waiting, away time.Duration
	var activePhases phaseTotals
	for _, turn := range d.turns {
		active += turn.active.total
		for i, v := range turn.active.values {
			activePhases.values[i] += v
		}
		activePhases.unknown += turn.active.unknown
		activePhases.total += turn.active.total
		if turn.gapAway {
			away += turn.gap
		} else {
			waiting += turn.gap
		}
	}
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, fmt.Sprintf("%d 轮", len(d.turns)))
			parts := []string{"活跃 ≈ " + session.FormatDuration(active)}
			if waiting > 0 {
				parts = append(parts, "等待你 ≈ "+session.FormatDuration(waiting))
			}
			if away > 0 {
				parts = append(parts, "离开 ≈ "+session.FormatDuration(away))
			}
			number(c, strings.Join(parts, " · ")).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		})
		phaseBar(c, activePhases, 10, t.Dark)
		a.sessionPhaseLegend(c, d, activePhases)
		if unlinked := unlinkedConcerns(d); unlinked > 0 {
			ui.Textf(c, "另有 %d 处子 Agent 耗时异常无法关联到轮次，见概览的调用时间线", unlinked).FontSize(10).TextColor(t.TextMuted).MaxLines(2)
		}
	})
	ui.Column(c).Gap(0).Children(func() {
		switch {
		case replay == nil || replay.err != "":
			msg := "回放内容暂不可用"
			if replay != nil {
				msg = replay.err
			}
			ui.Text(c, msg+"；仍可查看每轮耗时").FontSize(11).TextColor(t.TextMuted).Padding(0, 0, 8, 0)
		case index == nil:
			ui.Text(c, "正在读取对话内容…").FontSize(11).TextColor(t.TextMuted).Padding(0, 0, 8, 0)
		}
		if len(d.turns) == 0 {
			ui.Text(c, "没有可识别的轮次").FontSize(12).TextColor(t.TextMuted)
			return
		}
		peak := time.Duration(1)
		for _, turn := range d.turns {
			peak = max(peak, turn.active.total)
		}
		concerns := turnConcerns(d)
		ui.Column(c).Gap(4).FocusGroup(ui.Vertical).Label("轮次列表").Children(func() {
			for i, turn := range d.turns {
				a.turnRow(c, d, turn, index, concerns[i], peak)
				if turn.gap >= turnGapShown {
					turnGap(c, turn)
				}
			}
		})
	})
}

func (a *App) turnRow(c *ui.Context, d *sessionDetailData, turn sessionTurnView, index *replayIndex, concern *sessionConcern, peak time.Duration) {
	t := c.Theme()
	prompt, muted := turnPrompt(turn, index)
	b := ui.Button(c.Key(turn.turn.ID), "").Label(turnLabel(turn)).Tooltip(prompt).Column().Gap(6).Padding(8, 10).AlignItems(ui.Stretch).Justify(ui.Start).Border(1, t.Border).Background(ui.Transparent).Radius(6).FontWeight(400)
	if a.sessions.selectedTurn == turn.turn.ID {
		b.Border(1, t.Accent).Background(fade(t.Accent, 20))
		if a.sessions.focusTurn {
			b.Focus()
			a.sessions.focusTurn = false
		}
	}
	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			number(c, fmt.Sprintf("#%d", turn.index+1)).FontSize(11).TextColor(t.TextMuted).Width(30).Shrink(0)
			number(c, clockText(turn.turn.Start)).FontSize(11).TextColor(t.TextMuted).Shrink(0)
			p := ui.Text(c, prompt).FontSize(12).Grow(1).MinWidth(0).SingleLine()
			if muted {
				p.TextColor(t.TextMuted)
			}
			if concern != nil {
				ui.Icon(c, glyph("alert")).FontSize(12).TextColor(t.Warning).Label("本轮有耗时异常")
			}
			v := number(c, activeText(turn)).FontSize(12).FontWeight(600).Shrink(0)
			if concern != nil {
				v.TextColor(t.Warning)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 38).Children(func() {
			turnBar(c, turn.active, peak)
			if concern != nil {
				text := concern.title
				if concern.duration.Known && concern.title != "已中断" {
					text += " " + durationText(concern.duration)
				}
				ui.Text(c, text).FontSize(10).TextColor(t.Warning).Shrink(0).SingleLine().Tooltip(concern.note)
			}
			meta := []string{}
			if n := len(turn.turn.Requests); n > 0 {
				meta = append(meta, fmt.Sprintf("模型 %d", n))
			}
			if n := len(turn.turn.Tools); n > 0 {
				meta = append(meta, fmt.Sprintf("工具 %d", n))
			}
			if n := len(turn.turn.Subagents); n > 0 {
				meta = append(meta, fmt.Sprintf("子 Agent %d", n))
			}
			if turn.usage {
				meta = append(meta, tokenNumber(turn.tokens.Total())+" Token")
			}
			if turn.turn.Interrupted && (concern == nil || concern.title != "已中断") {
				meta = append(meta, "已中断")
			}
			number(c, strings.Join(meta, " · ")).FontSize(10).TextColor(t.TextMuted).Grow(1).MinWidth(0).Tooltip(strings.Join(meta, " · "))
		})
	})
	if b.Clicked() {
		a.chooseSessionTurn(d, turn.index)
		c.Invalidate()
	}
}

// turnBar draws the turn's active phases at a common scale, so bar lengths
// compare across turns. Unattributed time stays an outlined blank.
func turnBar(c *ui.Context, p phaseTotals, peak time.Duration) {
	t := c.Theme()
	ui.Box(c).Width(120).Height(6).Shrink(0).Label("本轮阶段").Tooltip(phaseDescription(p)).Draw(func(painter *ui.Painter, r ui.Rect) {
		if p.total <= 0 || peak <= 0 {
			return
		}
		w := max(2, float32(float64(p.total)/float64(peak))*r.W)
		painter.Fill(ui.Rect{X: r.X, Y: r.Y, W: w, H: r.H}, fade(t.TextMuted, 40), 2)
		x := r.X
		for i, value := range p.values {
			if i == int(phaseWait) && value == 0 {
				continue
			}
			part := float32(float64(value)/float64(p.total)) * w
			painter.Fill(ui.Rect{X: x, Y: r.Y, W: part, H: r.H}, sessionColor(phaseKeys[i], t.Dark), 0)
			x += part
		}
	})
}

func turnGap(c *ui.Context, turn sessionTurnView) {
	t := c.Theme()
	text := "等待你回复 ≈ " + session.FormatDuration(turn.gap)
	if turn.gapAway {
		text = "离开相关间隔 ≈ " + session.FormatDuration(turn.gap) + "（日志无精确起止）"
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(2, 10, 2, 48).Children(func() {
		ui.Box(c).Width(1).Height(14).Background(t.Border)
		number(c, text).FontSize(10).TextColor(t.TextMuted)
	})
}

// turnPrompt is the first line of what the person typed, or a muted reason.
func turnPrompt(turn sessionTurnView, index *replayIndex) (string, bool) {
	if index == nil {
		return "…", true
	}
	for _, e := range index.prompts[turn.index] {
		if line := promptLine(e.Text); line != "" {
			return line, false
		}
	}
	if index.context[turn.index] > 0 {
		return "自动注入的上下文（没有用户输入）", true
	}
	return "没有用户输入（续接或自动触发）", true
}

// promptLine is the first line of typed text. Attachment tags such as Codex's
// <image name=[Image #1] path="…"> become a short marker before it.
func promptLine(s string) string {
	images := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		for strings.HasPrefix(line, "<image") || strings.HasPrefix(line, "</image>") {
			if strings.HasPrefix(line, "<image") {
				images++
			}
			end := strings.IndexByte(line, '>')
			if end < 0 {
				line = ""
				break
			}
			line = strings.TrimSpace(line[end+1:])
		}
		if line != "" {
			if images > 0 {
				return fmt.Sprintf("[%d 张图片] %s", images, line)
			}
			return line
		}
	}
	if images > 0 {
		return fmt.Sprintf("[%d 张图片]", images)
	}
	return ""
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func activeText(turn sessionTurnView) string {
	if turn.turn.Start.IsZero() {
		return "暂不支持"
	}
	if turn.active.total < time.Second {
		return "<1s"
	}
	return "≈ " + session.FormatDuration(turn.active.total)
}

func durationText(d session.Duration) string {
	if !d.Known {
		return "暂不支持"
	}
	v := session.FormatDuration(d.Value)
	if d.Estimated {
		v = "≈ " + v
	}
	return v
}

func clockText(at time.Time) string {
	if at.IsZero() {
		return "--:--"
	}
	return at.Local().Format("15:04")
}

// The longest concern of each turn; concerns are sorted longest first.
func turnConcerns(d *sessionDetailData) []*sessionConcern {
	out := make([]*sessionConcern, len(d.turns))
	for i := range d.concerns {
		if c := &d.concerns[i]; c.turn >= 0 && out[c.turn] == nil {
			out[c.turn] = c
		}
	}
	return out
}

func unlinkedConcerns(d *sessionDetailData) int {
	n := 0
	for _, c := range d.concerns {
		if c.turn < 0 {
			n++
		}
	}
	return n
}

func phaseBar(c *ui.Context, p phaseTotals, height float32, dark bool) {
	border := c.Theme().Border
	ui.Box(c).Height(height).Border(1, border).Radius(3).Label("阶段占比条").Tooltip(phaseDescription(p)).Draw(func(painter *ui.Painter, r ui.Rect) {
		if p.total <= 0 {
			return
		}
		x := r.X
		for i, value := range p.values {
			w := float32(float64(value)/float64(p.total)) * r.W
			painter.Fill(ui.Rect{X: x, Y: r.Y, W: w, H: r.H}, sessionColor(phaseKeys[i], dark), 0)
			x += w
		}
	})
}

// The legend covers active time only; waits between turns are listed in the
// header and as separators, not as a share of the work.
func (a *App) sessionPhaseLegend(c *ui.Context, d *sessionDetailData, p phaseTotals) {
	t := c.Theme()
	groups := []struct {
		name, key string
		value     time.Duration
	}{
		{"模型", "phase-gen", p.values[phaseThink] + p.values[phaseGen]},
		{"工具", "phase-tool", p.values[phaseTool]},
		{"子 Agent", "phase-sub", p.values[phaseSub]},
		{"等待确认", "phase-wait", p.values[phaseWait]},
		{"重试与压缩", "phase-retry", p.values[phaseRetry]},
	}
	ui.Row(c).Gap(14).Wrap().Children(func() {
		for i, g := range groups {
			if g.value == 0 && i > 1 {
				continue
			}
			share := ""
			if p.total > 0 {
				share = fmt.Sprintf(" %.0f%%", 100*float64(g.value)/float64(p.total))
			}
			tip := g.name + " ≈ " + session.FormatDuration(g.value)
			if i == 0 && p.values[phaseThink] > 0 {
				tip += "，其中思考 ≈ " + session.FormatDuration(p.values[phaseThink])
			}
			ui.Row(c).Gap(5).AlignItems(ui.Center).Tooltip(tip).Children(func() {
				ui.Box(c).Size(7, 7).Radius(2).Background(sessionColor(g.key, t.Dark))
				ui.Text(c, g.name).FontSize(11)
				number(c, "≈ "+session.FormatDuration(g.value)+share).FontSize(11).TextColor(t.TextMuted)
			})
		}
		if p.unknown > 0 {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Tooltip("缺少对应阶段数据，常见于模型首字前的排队与日志间隙").Children(func() {
				ui.Box(c).Size(7, 7).Radius(2).Border(1, t.Border)
				ui.Text(c, "未归因").FontSize(11)
				number(c, session.FormatDuration(p.unknown)).FontSize(11).TextColor(t.TextMuted)
			})
		}
	})
	if !session.LookupAgent(d.session.Agent).PermissionWait {
		ui.Text(c, "模型时间按输入完成到响应结束的间隔估算；权限等待暂不支持").FontSize(10).TextColor(t.TextMuted).MaxLines(2)
	}
}
