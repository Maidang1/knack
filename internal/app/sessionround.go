package app

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Collapsed text blocks show this much; the rest is one click away. Laying
// out a 64 KiB tool output on every frame is wasted work when nobody reads it.
const (
	replayPreviewLines = 12
	replayPreviewChars = 1600
)

// Step rows: padding 8, phase dot 6 and gap 8 precede the name column; a gap
// 8, the 88pt duration and padding 8 follow the track. The axis-break overlay
// uses the same offsets.
const (
	stepTrackLeft  = 8 + 6 + 8 + 8
	stepTrackRight = 8 + 88 + 8
)

func (a *App) closeSessionRound() {
	a.sessions.roundOpen = false
	a.sessions.tab = 1
	a.sessions.focusTurn = true
}

// sessionRound replays one turn as a trace: the prompt, then every model
// request, tool call, sub-Agent and wait on a shared time axis, each with the
// content the log recorded for it.
func (a *App) sessionRound(c *ui.Context, d *sessionDetailData, width float32) {
	index := -1
	for i, turn := range d.turns {
		if turn.turn.ID == a.sessions.selectedTurn {
			index = i
			break
		}
	}
	if index < 0 {
		a.closeSessionRound()
		return
	}
	turn := d.turns[index]
	t := c.Theme()
	replay := a.sessionReplay(d)
	content := replay.ready()
	labelWidth := float32(150)
	if width < 560 {
		labelWidth = 104
	}
	ui.Column(c).Gap(14).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "全部轮次").Label("返回全部轮次").FontSize(11).AutoFocus().Clicked() {
				a.closeSessionRound()
				c.Invalidate()
			}
			ui.Spacer(c)
			number(c, fmt.Sprintf("%d / %d", index+1, len(d.turns))).FontSize(11).TextColor(t.TextMuted)
			if ui.Button(c, "上一轮").FontSize(11).Disabled(index == 0).Clicked() {
				a.chooseSessionTurn(d, index-1)
				c.Invalidate()
			}
			if ui.Button(c, "下一轮").FontSize(11).Disabled(index == len(d.turns)-1).Clicked() {
				a.chooseSessionTurn(d, index+1)
				c.Invalidate()
			}
		})
		a.turnPromptCard(c, d, turn, replay, content)
		axis := newStepAxis(turn, a.foldLongSteps)
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				overviewTitle(c, "调用链")
				ui.Spacer(c)
				mode := 0
				if a.foldLongSteps {
					mode = 1
				}
				tabs := ui.TabsBase(c.Key("step-scale"), &mode, 2)
				tabs.List.Gap(3).Label("步骤时间比例").Children(func() {
					for i, label := range []string{"真实比例", "折叠长步骤"} {
						b := tabs.Tab(i).Padding(4, 7).Border(1, t.Border).Radius(4).Label(label)
						if i == mode {
							b.Border(1, t.Accent).Background(fade(t.Accent, 24))
						}
						b.Children(func() { ui.Text(c, label).FontSize(11) })
					}
				})
				if tabs.List.Changed() {
					a.setFoldLongSteps(mode == 1)
					c.Invalidate()
				}
			})
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for i := range int(phaseCount) {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(2).Background(sessionColor(phaseKeys[i], t.Dark))
						ui.Text(c, phaseNames[i]).FontSize(10).TextColor(t.TextMuted)
					})
				}
			})
			axisLabel := "时间轴暂不支持"
			if axis.total > 0 {
				axisLabel = "以本轮开始为 0 · 末端 " + session.FormatDuration(axis.total)
			}
			for _, cut := range axis.cuts {
				axisLabel += " · 折叠 " + session.FormatDuration(cut.to-cut.from) + "（+" + session.FormatDuration(cut.from) + " 至 +" + session.FormatDuration(cut.to) + "）"
			}
			if a.foldLongSteps && len(axis.cuts) == 0 {
				axisLabel += " · 本轮没有符合条件的长步骤"
			}
			ui.Text(c, axisLabel).FontSize(10).TextColor(t.TextMuted).MaxLines(2)
			a.sessions.selectedStep = max(0, min(a.sessions.selectedStep, len(turn.steps)-1))
			ui.Column(c.Key("steps:" + turn.turn.ID)).Gap(4).FocusGroup(ui.Vertical).Label("单轮步骤瀑布图").DrawOver(func(p *ui.Painter, r ui.Rect) {
				// One neutral axis break crosses the chart, never a phase-colored band.
				trackX, trackW := r.X+labelWidth+stepTrackLeft, r.W-labelWidth-stepTrackLeft-stepTrackRight
				for _, cut := range axis.cuts {
					x := trackX + (axis.at(cut.from)+axis.at(cut.to))/2*trackW
					for _, dx := range []float32{-2, 2} {
						p.Fill(ui.Rect{X: float32(math.Round(float64(x + dx))), Y: r.Y, W: 1, H: r.H}, t.TextMuted, 0)
					}
				}
			}).Children(func() {
				if len(turn.steps) == 0 {
					ui.Text(c, "本轮没有已记录的步骤").FontSize(12).TextColor(t.TextMuted)
				}
				for i, step := range turn.steps {
					a.stepRow(c, d, turn, i, step, axis, content, labelWidth)
				}
			})
			ui.Text(c, "区间为日志估算；并行步骤分行展示，不相加。超时事件只表示阈值。").FontSize(10).TextColor(t.TextMuted).MaxLines(2)
		})
		if events := turnEvents(content, index); len(events) > 0 {
			ui.Column(c).Gap(4).Children(func() {
				for _, e := range events {
					text := e.Name
					if e.Text != "" {
						text += " · " + firstLine(e.Text)
					}
					number(c, stepClock(e.Start)+"  "+text).FontSize(11).TextColor(t.TextMuted).SingleLine().Tooltip(e.Text)
				}
			})
		}
	})
}

func (a *App) turnPromptCard(c *ui.Context, d *sessionDetailData, turn sessionTurnView, replay *replayState, content *replayIndex) {
	t := c.Theme()
	ui.Column(c).Padding(12, 14).Gap(8).Background(t.Surface).Border(1, t.Border).Radius(7).Label("单轮摘要").Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Textf(c, "第 %d 轮", turn.index+1).FontSize(14).FontWeight(600)
			ui.Spacer(c)
			number(c, stepClock(turn.turn.Start)).FontSize(11).TextColor(t.TextMuted)
		})
		switch {
		case replay == nil || replay.err != "":
			ui.Text(c, "对话内容暂不可用").FontSize(12).TextColor(t.TextMuted)
		case content == nil:
			ui.Text(c, "正在读取对话内容…").FontSize(12).TextColor(t.TextMuted)
		default:
			prompts := content.prompts[turn.index]
			if len(prompts) == 0 {
				text, _ := turnPrompt(turn, content)
				ui.Text(c, text).FontSize(12).TextColor(t.TextMuted)
			}
			for i, e := range prompts {
				a.replayText(c, fmt.Sprintf("prompt:%s:%d", turn.turn.ID, i), "", e.Text, e.Truncated, false)
			}
			if n := content.context[turn.index]; n > 0 {
				ui.Textf(c, "另有 %d 条自动注入的上下文（指令、提醒、命令输出），未展示", n).FontSize(10).TextColor(t.TextMuted)
			}
		}
		parts := []string{"活跃 " + activeText(turn)}
		if turn.gap > 0 {
			name := "之后等待你 ≈ "
			if turn.gapAway {
				name = "之后离开 ≈ "
			}
			parts = append(parts, name+session.FormatDuration(turn.gap))
		}
		if turn.turn.Elapsed.Known {
			parts = append(parts, "日志报告 "+session.FormatDuration(turn.turn.Elapsed.Value))
		}
		parts = append(parts, fmt.Sprintf("模型 %d 次 · 工具 %d 次", len(turn.turn.Requests), len(turn.turn.Tools)))
		if n := len(turn.turn.Subagents); n > 0 {
			parts = append(parts, fmt.Sprintf("子 Agent %d 个", n))
		}
		if turn.usage {
			parts = append(parts, tokenNumber(turn.tokens.Total())+" Token")
		}
		number(c, strings.Join(parts, " · ")).FontSize(11).TextColor(t.TextMuted).MaxLines(2)
		models := []string{}
		for _, r := range turn.turn.Requests {
			if r.Model != "" && !slices.Contains(models, r.Model) {
				models = append(models, r.Model)
			}
		}
		if len(models) > 0 {
			ui.Text(c, "模型 · "+strings.Join(models, "、")).FontSize(11).TextColor(t.TextMuted).SingleLine()
		}
		if turn.turn.Interrupted {
			ui.Text(c, "本轮已中断").FontSize(11).TextColor(t.Warning)
		}
	})
}

func (a *App) stepRow(c *ui.Context, d *sessionDetailData, turn sessionTurnView, i int, step sessionStep, axis stepAxis, content *replayIndex, labelWidth float32) {
	t := c.Theme()
	selected := i == a.sessions.selectedStep
	name, preview := stepContent(step, content)
	label := fmt.Sprintf("步骤 %d · %s · %s", i+1, step.name, stepDuration(step))
	b := ui.Button(c.Key(i), "").Label(label).Tooltip(label).Column().Gap(4).Padding(6, 8).AlignItems(ui.Stretch).Justify(ui.Start).Border(1, t.Border).Radius(5).Background(ui.Transparent).FontWeight(400)
	if selected {
		b.Border(1, t.Accent).Background(fade(t.Accent, 20))
	}
	b.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(2).Background(sessionColor(phaseKeys[step.phase], t.Dark)).Shrink(0)
			ui.Text(c, name).FontSize(11).Width(labelWidth).Shrink(0).SingleLine().Tooltip(name)
			stepTrack(c, turn, step, axis)
			value := stepDuration(step)
			if a.foldLongSteps && step.elapsed.Known && session.FoldLongStep(step.elapsed.Value, turn.phases.total) && !step.start.IsZero() && !step.end.IsZero() {
				value += " ∕∕"
			}
			v := number(c, value).FontSize(11).Width(88).Shrink(0).TextAlign(ui.End)
			if stepFailed(step, content) {
				v.TextColor(t.Warning)
			}
		})
		if preview != "" {
			ui.Text(c, preview).FontSize(10).TextColor(t.TextMuted).SingleLine().Padding(0, 0, 0, 14)
		}
	})
	if b.Clicked() || b.Focused() {
		if a.sessions.selectedStep != i {
			a.sessions.selectedStep = i
			c.Invalidate()
		}
	}
	if selected {
		a.sessionStepDetail(c, turn, step, d.session.Agent, content)
	}
}

// stepContent names a step by what it did and previews its recorded content.
func stepContent(s sessionStep, content *replayIndex) (string, string) {
	name := s.name
	if content == nil {
		return name, ""
	}
	if r := s.request; r != nil {
		var reply, thinking string
		for _, e := range content.byRequest[r.ID] {
			switch e.Kind {
			case session.EntryAssistant:
				if reply == "" {
					reply = firstLine(e.Text)
				}
			case session.EntryThinking:
				if thinking == "" {
					thinking = firstLine(e.Text)
				}
			}
		}
		calls := content.calls[r.ID]
		switch {
		case reply != "":
			return name, reply
		case len(calls) > 0:
			names := []string{}
			for _, call := range calls {
				names = append(names, call.Name)
			}
			return name, "调用 " + strings.Join(compactNames(names), "、")
		case thinking != "":
			return name, "思考：" + thinking
		}
		return name, ""
	}
	if s.tool != nil {
		if e, ok := content.byTool[s.tool.ID]; ok {
			return name, inputPreview(e.Input)
		}
	}
	return name, ""
}

func stepFailed(s sessionStep, content *replayIndex) bool {
	if s.tool != nil && (s.tool.Failed || s.tool.Interrupted) {
		return true
	}
	if s.request != nil && s.request.APIError {
		return true
	}
	if s.tool != nil && content != nil {
		return content.byTool[s.tool.ID].Error
	}
	return false
}

// compactNames turns [exec exec Read] into [exec ×2, Read], keeping order.
func compactNames(names []string) []string {
	var out []string
	counts := map[string]int{}
	for _, n := range names {
		if counts[n] == 0 {
			out = append(out, n)
		}
		counts[n]++
	}
	for i, n := range out {
		if counts[n] > 1 {
			out[i] = fmt.Sprintf("%s ×%d", n, counts[n])
		}
	}
	return out
}

// inputPreview shows the most telling argument of a tool call on one line.
func inputPreview(input string) string {
	var args map[string]any
	if json.Unmarshal([]byte(input), &args) == nil {
		for _, key := range []string{"command", "cmd", "description", "file_path", "path", "pattern", "query", "url", "prompt", "skill", "script"} {
			if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
				return firstLine(v)
			}
		}
		compact, _ := json.Marshal(args)
		return string(compact)
	}
	return firstLine(input)
}

func turnEvents(content *replayIndex, turn int) []session.Entry {
	if content == nil || turn >= len(content.events) {
		return nil
	}
	return content.events[turn]
}

func stepTrack(c *ui.Context, turn sessionTurnView, step sessionStep, axis stepAxis) {
	t := c.Theme()
	ui.Box(c).Grow(1).MinWidth(40).Height(16).Label("步骤时间条").Draw(func(p *ui.Painter, r ui.Rect) {
		if axis.total <= 0 || turn.turn.Start.IsZero() {
			return
		}
		if step.start.IsZero() || step.end.IsZero() || step.end.Before(step.start) {
			return
		}
		from, to := step.start.Sub(turn.turn.Start), step.end.Sub(turn.turn.Start)
		if from > axis.total || to < 0 {
			return
		}
		x1, x2 := r.X+axis.at(from)*r.W, r.X+axis.at(to)*r.W
		w := min(r.W, max(2, x2-x1))
		x1 = min(x1, r.X+r.W-w)
		color := sessionColor(phaseKeys[step.phase], t.Dark)
		paintSpan := func(left, right float32, fill ui.Color) {
			for _, cut := range axis.cuts {
				center := r.X + (axis.at(cut.from)+axis.at(cut.to))/2*r.W
				if right > center-3 && left < center+3 {
					if center-3 > left {
						p.Fill(ui.Rect{X: left, Y: r.Y + 4, W: center - 3 - left, H: 8}, fill, 2)
					}
					left = max(left, center+3)
				}
			}
			if right > left {
				p.Fill(ui.Rect{X: left, Y: r.Y + 4, W: right - left, H: 8}, fill, 2)
			}
		}
		paintSpan(x1, x1+w, color)
		if step.phase == phaseGen && step.thinking > 0 {
			end := axis.at(from+step.thinking)*r.W + r.X
			paintSpan(x1, min(end, x1+w), sessionColor("phase-think", t.Dark))
		}
	})
}

func (a *App) sessionStepDetail(c *ui.Context, turn sessionTurnView, s sessionStep, agent session.Agent, content *replayIndex) {
	t := c.Theme()
	ui.Column(c).Padding(10, 12).Gap(10).Border(1, t.Accent).Background(fade(t.Accent, 10)).Radius(6).Label("步骤详情").Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, s.name).FontSize(13).FontWeight(600).MaxLines(2)
			number(c, s.kind+" · "+stepDuration(s)+" · "+stepClock(s.start)+" → "+stepClock(s.end)).FontSize(11).TextColor(t.TextMuted)
		})
		facts := stepFacts(s, agent)
		if len(facts) > 0 {
			ui.Column(c).Gap(3).Children(func() {
				for _, fact := range facts {
					ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
						ui.Text(c, fact[0]).FontSize(11).Width(84).Shrink(0).TextColor(t.TextMuted)
						ui.Text(c, fact[1]).FontSize(11).Grow(1).MinWidth(0)
					})
				}
			})
		}
		if s.tool != nil && s.tool.PermissionWait {
			ui.Text(c, "权限等待为整个工具间隔的估算，包含执行时间。").FontSize(10).TextColor(t.TextMuted)
		}
		if content == nil {
			return
		}
		key := turn.turn.ID + ":" + s.name
		if r := s.request; r != nil {
			entries := content.byRequest[r.ID]
			if len(entries) == 0 && len(content.calls[r.ID]) == 0 {
				ui.Text(c, "日志没有记录这次请求的内容").FontSize(11).TextColor(t.TextMuted)
			}
			for i, e := range entries {
				switch e.Kind {
				case session.EntryThinking:
					if strings.TrimSpace(e.Text) == "" {
						ui.Text(c, "思考 · 日志只保存了签名，没有思考正文").FontSize(11).TextColor(t.TextMuted)
						continue
					}
					a.replayText(c, fmt.Sprintf("%s:%d", key, i), "思考", e.Text, e.Truncated, false)
				case session.EntryAssistant:
					a.replayText(c, fmt.Sprintf("%s:%d", key, i), "回复", e.Text, e.Truncated, false)
				case session.EntryEvent:
					a.replayText(c, fmt.Sprintf("%s:%d", key, i), e.Name, e.Text, e.Truncated, false)
				}
			}
			if calls := content.calls[r.ID]; len(calls) > 0 {
				names := []string{}
				for _, call := range calls {
					names = append(names, call.Name)
				}
				ui.Text(c, "发起工具调用 · "+strings.Join(compactNames(names), "、")+"（见后续步骤）").FontSize(11).TextColor(t.TextMuted).MaxLines(3)
			}
		}
		if s.tool != nil {
			e, ok := content.byTool[s.tool.ID]
			if !ok {
				ui.Text(c, "日志没有记录这次调用的输入和输出").FontSize(11).TextColor(t.TextMuted)
				return
			}
			a.replayText(c, key+":in", "输入", e.Input, false, true)
			title := "输出"
			if e.Error {
				title = "输出 · 失败"
			}
			if e.End.IsZero() {
				ui.Text(c, "没有记录输出").FontSize(11).TextColor(t.TextMuted)
			} else {
				a.replayText(c, key+":out", title, e.Text, e.Truncated, true)
			}
		}
	})
}

// replayText shows recorded text, selectable, clipped until expanded.
func (a *App) replayText(c *ui.Context, key, title, text string, truncated, mono bool) {
	t := c.Theme()
	if a.sessions.expanded == nil {
		a.sessions.expanded = map[string]bool{}
	}
	text = strings.TrimRight(text, "\n ")
	lines := strings.Count(text, "\n") + 1
	long := lines > replayPreviewLines || len(text) > replayPreviewChars
	open := a.sessions.expanded[key]
	shown := text
	if long && !open {
		shown = clipLines(text, replayPreviewLines, replayPreviewChars)
	}
	ui.Column(c.Key("replay:" + key)).Gap(4).Children(func() {
		if title != "" {
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(t.TextMuted)
		}
		if strings.TrimSpace(text) == "" {
			ui.Text(c, "（空）").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if mono {
			// Code and tool output keep their lines; long unbroken paths and
			// commands scroll instead of overflowing or being split.
			ui.ScrollHorizontal(c.Key("replay-scroll:" + key)).Label(title).Background(t.Surface).Radius(5).Children(func() {
				ui.Text(c, shown).Font("SF Mono, Menlo, monospace").FontSize(11).NoWrap().Selectable().Padding(8, 10)
			})
		} else {
			ui.Text(c, shown).FontSize(12).Selectable()
		}
		if long || truncated {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if long {
					label := fmt.Sprintf("展开全文（%d 行）", lines)
					if open {
						label = "收起"
					}
					overviewLink(c, label, func() { a.sessions.expanded[key] = !open })
				}
				if truncated {
					ui.Text(c, "超过 64 KiB 的部分未显示，可打开日志查看").FontSize(10).TextColor(t.TextMuted)
				}
			})
		}
	})
}

func clipLines(s string, lines, chars int) string {
	n := 0
	for i, r := range s {
		if r == '\n' {
			n++
			if n == lines {
				return s[:i] + "\n…"
			}
		}
		if i >= chars {
			return s[:i] + "…"
		}
	}
	return s
}
