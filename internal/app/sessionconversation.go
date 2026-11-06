package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// The conversation replay is the design's 对话回放: the turns down the side
// and the chosen turn as it was, its messages, thinking and tool calls in
// the order the log recorded them. Content is read on demand and only held
// in memory.

const (
	replayAll = iota
	replayToolsOnly
	replayRepliesOnly
)

// Diffs longer than this show their start; the rest is in the input.
const diffPreviewLines = 40

func (a *App) sessionConversation(c *ui.Context, d *sessionDetailData, width float32) {
	t := c.Theme()
	s := &a.sessions
	replay := a.sessionReplay(d)
	content := replay.ready()
	index := a.sessionTurnIndex(d)
	query := strings.ToLower(strings.TrimSpace(s.replayQuery))
	listWidth := float32(232)
	if width < 760 {
		listWidth = 180
	}
	ui.Column(c).Width(listWidth).Shrink(0).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Height(40).Padding(0, 16).Gap(8).AlignItems(ui.Center).Shrink(0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Textf(c, "%d 轮", len(d.turns)).FontSize(13).FontWeight(600).Grow(1)
			if query != "" && content != nil {
				n := 0
				for i := range d.turns {
					if turnMatches(content, i, query) {
						n++
					}
				}
				ui.Textf(c, "%d 轮匹配", n).FontSize(11.5).TextColor(token(t, "ink-3"))
			}
		})
		ui.Scroll(c.Key("replay-turns")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Padding(8).Gap(2).FocusGroup(ui.Vertical).Label("轮次列表").Children(func() {
				a.replayTurnList(c, d, content, index, query)
			})
		})
	})
	key := "replay-body"
	if index >= 0 {
		key += ":" + d.turns[index].turn.ID
	}
	ui.Scroll(c.Key(key)).Grow(1).MinWidth(0).Label("对话回放").Children(func() {
		ui.Row(c).Justify(ui.Center).Padding(20, 32, 40, 32).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).MaxWidth(760).Gap(14).Children(func() {
				ui.Row(c).Gap(8).Padding(8, 12).Radius(7).Background(t.SurfaceHover).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph("lock")).FontSize(12).TextColor(t.TextMuted)
					ui.Text(c, "正文从原始日志按需读取，只保留在内存，关闭后释放。").FontSize(12).TextColor(t.TextMuted).MinWidth(0)
				})
				switch {
				case replay == nil || replay.err != "":
					msg := "回放内容暂不可用"
					if replay != nil && replay.err != "" {
						msg = replay.err
					}
					ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted)
				case content == nil:
					ui.Text(c, "正在读取对话内容…").FontSize(13).TextColor(t.TextMuted)
				case index < 0:
					ui.Text(c, "没有可识别的轮次").FontSize(13).TextColor(t.TextMuted)
				default:
					a.replayTurn(c, d, content, index, query)
				}
			})
		})
	})
}

func (a *App) replayTurnList(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	concerns := turnConcerns(d)
	for i, turn := range d.turns {
		if query != "" && content != nil && !turnMatches(content, i, query) {
			continue
		}
		on := i == index
		prompt, quiet := turnPrompt(turn, content)
		b := ui.ButtonBase(c.Key("replay-turn:"+turn.turn.ID)).Padding(8, 10).Gap(8).Radius(7).AlignItems(ui.Start).Border(1, ui.Transparent).
			Label(turnLabel(turn)).Tooltip(prompt)
		switch {
		case on:
			b.Background(token(t, "raised")).Border(1, t.Border)
			if a.sessions.focusTurn {
				b.Focus()
				a.sessions.focusTurn = false
			}
		case b.Hovered():
			b.Background(t.SurfaceHover)
		}
		b.Children(func() {
			number(c, fmt.Sprint(i+1)).FontSize(11).TextColor(muted).Width(18).Shrink(0).TextAlign(ui.End).Margin(2, 0, 0, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				p := ui.Text(c, prompt).FontSize(13).SingleLine()
				if on {
					p.FontWeight(600)
				}
				if quiet {
					p.TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					if concerns[i] != nil {
						ui.Icon(c, glyph("issues")).FontSize(11).TextColor(t.Warning).Label("本轮有耗时异常")
						number(c, activeText(turn)).FontSize(11).TextColor(t.Warning)
					} else {
						number(c, activeText(turn)).FontSize(11).TextColor(muted)
					}
				})
			})
		})
		if b.Clicked() || b.Focused() && !on {
			a.chooseSessionTurn(d, i)
			c.Invalidate()
		}
	}
}

// turnMatches reports whether any entry of turn i holds query.
func turnMatches(content *replayIndex, i int, query string) bool {
	for _, e := range content.turns[i] {
		if entryMatches(e, query) {
			return true
		}
	}
	return false
}

func entryMatches(e session.Entry, query string) bool {
	return query == "" || strings.Contains(strings.ToLower(e.Text), query) || strings.Contains(strings.ToLower(e.Input), query) || strings.Contains(strings.ToLower(e.Name), query)
}

// replayTurn shows one turn's entries. The Agent's name heads each run of
// its entries, as a chat shows who speaks.
func (a *App) replayTurn(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	turn := d.turns[index]
	filter := a.sessions.replayFilter
	ui.Row(c).Gap(10).AlignItems(ui.Center).Padding(6, 0, 0, 0).Children(func() {
		ui.Textf(c, "第 %d 轮", index+1).FontSize(12.5).FontWeight(600).Shrink(0)
		number(c, clockText(turn.turn.Start)+" · "+activeText(turn)).FontSize(12).TextColor(muted).Shrink(0)
		ui.Box(c).Grow(1).Height(1).Background(t.Border)
	})
	models := map[string]string{}
	thinking := map[string]time.Duration{}
	for _, r := range turn.turn.Requests {
		models[r.ID] = r.Model
		if r.Thinking.Known {
			thinking[r.ID] = r.Thinking.Value
		}
	}
	tools := map[string]*session.ToolCall{}
	for i := range turn.turn.Tools {
		tools[turn.turn.Tools[i].ID] = &turn.turn.Tools[i]
	}
	speaker := ""
	shown := 0
	for i, e := range content.turns[index] {
		if !entryMatches(e, query) {
			continue
		}
		switch filter {
		case replayToolsOnly:
			if e.Kind != session.EntryTool {
				continue
			}
		case replayRepliesOnly:
			if e.Kind != session.EntryUser && e.Kind != session.EntryAssistant {
				continue
			}
		}
		key := fmt.Sprintf("%s:%d", turn.turn.ID, i)
		switch e.Kind {
		case session.EntryContext:
			continue
		case session.EntryUser:
			speaker = "user"
			shown++
			replaySpeaker(c, "user", "你", "", t.SurfaceHover, func() {
				a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock)
			})
			continue
		case session.EntryEvent:
			if e.RequestID == "" {
				shown++
				ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(0, 0, 0, 36).Children(func() {
					ui.Icon(c, glyph("alert")).FontSize(12).TextColor(muted)
					text := e.Name
					if e.Text != "" {
						text += " · " + firstLine(e.Text)
					}
					number(c, stepClock(e.Start)+"  "+text).FontSize(11.5).TextColor(muted).SingleLine().MinWidth(0).Tooltip(e.Text)
				})
				continue
			}
		}
		if speaker != "agent" {
			speaker = "agent"
			replaySpeaker(c, string(d.session.Agent), sessionAgentName(d.session.Agent), models[e.RequestID], agentColor(d.session.Agent, t.Dark), nil)
		}
		shown++
		ui.Column(c).Padding(0, 0, 0, 36).Gap(8).Children(func() {
			switch e.Kind {
			case session.EntryThinking:
				a.replayThinking(c, key, e, thinking[e.RequestID])
			case session.EntryAssistant:
				a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock)
			case session.EntryTool:
				a.replayTool(c, key, e, tools[e.ToolID])
			case session.EntryEvent:
				a.replayText(c, "msg:"+key, e.Name, e.Text, e.Truncated, proseBlock)
			}
		})
	}
	if shown == 0 {
		msg := "本轮没有可回放的内容"
		if query != "" || filter != replayAll {
			msg = "本轮没有符合筛选的内容"
		}
		ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted).Padding(0, 0, 0, 36)
	}
	var meta []string
	if turn.usage {
		meta = append(meta, "输出 "+groupDigits(int(turn.tokens.Output))+" tok")
		if input := turn.tokens.InputTotal(); input > 0 {
			meta = append(meta, fmt.Sprintf("缓存命中 %.0f%%", 100*float64(turn.tokens.CacheRead)/float64(input)))
		}
	}
	if n := content.context[index]; n > 0 {
		meta = append(meta, fmt.Sprintf("另有 %d 条自动注入的上下文未展示", n))
	}
	if len(meta) > 0 {
		number(c, strings.Join(meta, " · ")).FontSize(11).TextColor(muted).Padding(0, 0, 0, 36)
	}
	ui.Row(c).Gap(8).Justify(ui.End).Padding(10, 0, 0, 0).Children(func() {
		if outlineButton(c, "replay-prev", "上一轮").Disabled(index == 0).Clicked() {
			a.chooseSessionTurn(d, index-1)
			a.sessions.focusTurn = true
			c.Invalidate()
		}
		if outlineButton(c, "replay-next", "下一轮").Disabled(index == len(d.turns)-1).Clicked() {
			a.chooseSessionTurn(d, index+1)
			a.sessions.focusTurn = true
			c.Invalidate()
		}
	})
}

// replaySpeaker heads a message: an avatar, the speaker and the model.
// who is "user" or the Agent's id, whose glyph marks its avatar when it has
// one. body, when given, follows under the name.
func replaySpeaker(c *ui.Context, who, name, model string, face ui.Color, body func()) {
	t := c.Theme()
	ui.Row(c).Gap(12).AlignItems(ui.Start).Margin(6, 0, 0, 0).Children(func() {
		avatar := ui.Box(c).Size(24, 24).Radius(12).Shrink(0).Center().Background(face)
		_, known := glyphs[who]
		switch {
		case who == "user":
			avatar.Children(func() { ui.Icon(c, glyph("user")).FontSize(13).TextColor(t.TextMuted) })
		case known:
			avatar.Children(func() { ui.Icon(c, glyph(who)).FontSize(13).TextColor(ui.Hex("#FFFFFF")) })
		default:
			initial := strings.ToUpper(string([]rune(name)[:1]))
			avatar.Children(func() { ui.Text(c, initial).FontSize(11).FontWeight(700).TextColor(ui.Hex("#FFFFFF")) })
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Height(24).Children(func() {
				ui.Text(c, name).FontSize(12.5).FontWeight(600)
				if model != "" {
					number(c, model).FontSize(11).TextColor(token(t, "ink-3"))
				}
			})
			if body != nil {
				body()
			}
		})
	})
}

// replayThinking is thinking folded to a chip, as the design shows it.
func (a *App) replayThinking(c *ui.Context, key string, e session.Entry, measured time.Duration) {
	t := c.Theme()
	if a.sessions.expanded == nil {
		a.sessions.expanded = map[string]bool{}
	}
	open := a.sessions.expanded["think:"+key]
	took := measured
	if !e.Start.IsZero() && e.End.After(e.Start) {
		took = e.End.Sub(e.Start)
	}
	label := "思考"
	if took >= time.Second {
		label = "思考了 " + session.FormatDuration(took)
	}
	empty := strings.TrimSpace(e.Text) == ""
	chip := ui.ButtonBase(c.Key("think:"+key)).Padding(3, 8).Gap(6).Radius(6).AlignItems(ui.Center).AlignSelf(ui.Start).Background(t.SurfaceHover).Label(label).Disabled(empty)
	chip.Children(func() {
		icon := "chevron-right"
		if open {
			icon = "chevron-down"
		}
		ui.Icon(c, glyph(icon)).FontSize(11).TextColor(token(t, "ink-3"))
		ui.Box(c).Size(7, 7).Radius(2).Background(sessionColor("phase-think", t.Dark))
		ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
		if empty {
			ui.Text(c, "· 日志只保存了签名").FontSize(12).TextColor(token(t, "ink-3"))
		}
	})
	if chip.Clicked() {
		a.sessions.expanded["think:"+key] = !open
		c.Invalidate()
	}
	if open && !empty {
		ui.Column(c).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 2).BorderColor(t.Border).Children(func() {
			a.replayText(c, "thinking:"+key, "", e.Text, e.Truncated, proseBlock)
		})
	}
}

// replayTool is a tool call as a card: what it was asked, folded until
// opened to its input and output. Edits open to their diff.
func (a *App) replayTool(c *ui.Context, key string, e session.Entry, tool *session.ToolCall) {
	t := c.Theme()
	muted := token(t, "ink-3")
	if a.sessions.expanded == nil {
		a.sessions.expanded = map[string]bool{}
	}
	open := a.sessions.expanded["tool:"+key]
	name := e.Name
	if name == "" {
		name = "工具"
	}
	preview := inputPreview(e.Input)
	diff := editDiff(e.Input)
	card := ui.Column(c.Key("tool-card:"+key)).Radius(7).Background(t.Surface).Border(1, t.Border)
	if open {
		card.Border(1, token(t, "line-strong"))
	}
	card.Children(func() {
		head := ui.ButtonBase(c.Key("tool:"+key)).Padding(7, 10).Gap(8).AlignItems(ui.Center).Radius(7).Label(name + " · " + preview)
		if head.Hovered() {
			head.Background(t.SurfaceHover)
		}
		head.Children(func() {
			icon := "chevron-right"
			if open {
				icon = "chevron-down"
			}
			ui.Icon(c, glyph(icon)).FontSize(11).TextColor(muted).Shrink(0)
			glyphName := toolIcon(name)
			if tool != nil && tool.AgentID != "" {
				glyphName = "bot"
			}
			ui.Icon(c, glyph(glyphName)).FontSize(14).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, name).FontSize(13).FontWeight(600).Shrink(0).SingleLine()
			number(c, preview).FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine().Tooltip(preview)
			if len(diff) > 0 {
				add, del := 0, 0
				for _, l := range diff {
					switch l.sign {
					case '+':
						add++
					case '-':
						del++
					}
				}
				number(c, fmt.Sprintf("+%d -%d", add, del)).FontSize(11.5).TextColor(muted).Shrink(0)
			}
			if tool != nil && tool.PermissionWait {
				ui.Row(c).Gap(4).Padding(1, 6).Radius(4).AlignItems(ui.Center).Background(token(t, "warn-bg")).Shrink(0).Children(func() {
					ui.Icon(c, glyph("hand")).FontSize(11).TextColor(token(t, "warn-ink"))
					ui.Text(c, "等你确认").FontSize(11).TextColor(token(t, "warn-ink"))
				})
			}
			if e.Error || tool != nil && (tool.Failed || tool.Interrupted) {
				ui.Row(c).Padding(1, 6).Radius(4).Background(token(t, "danger-bg")).Shrink(0).Children(func() {
					ui.Text(c, "失败").FontSize(11).TextColor(t.Danger)
				})
			}
			if tool != nil {
				number(c, durationText(tool.Elapsed)).FontSize(11.5).TextColor(muted).Shrink(0)
			}
		})
		if head.Clicked() {
			a.sessions.expanded["tool:"+key] = !open
			c.Invalidate()
		}
		if !open {
			return
		}
		ui.Column(c).Padding(0, 10, 10, 10).Gap(10).Children(func() {
			a.toolInput(c, "in:"+key, e.Input, diffPreviewLines)
			switch {
			case e.End.IsZero():
				ui.Text(c, "没有记录输出").FontSize(12).TextColor(t.TextMuted)
			default:
				title := "输出"
				if e.Error {
					title = "输出 · 失败"
				}
				a.replayText(c, "out:"+key, title, e.Text, e.Truncated, terminalBlock)
			}
		})
	})
}

type diffLine struct {
	sign byte // '+', '-', or ' ' for a file heading
	text string
}

// editDiff reads an edit's change from its input: Claude's old and new
// strings, or the lines a patch adds and removes. Other input has none.
func editDiff(input string) []diffLine {
	var args map[string]any
	if json.Unmarshal([]byte(input), &args) == nil {
		old, okOld := args["old_string"].(string)
		next, okNew := args["new_string"].(string)
		if okOld && okNew {
			var out []diffLine
			for _, l := range strings.Split(old, "\n") {
				out = append(out, diffLine{'-', l})
			}
			for _, l := range strings.Split(next, "\n") {
				out = append(out, diffLine{'+', l})
			}
			return out
		}
		for _, k := range []string{"patch", "input"} {
			if v, ok := args[k].(string); ok {
				return patchLines(v)
			}
		}
		return nil
	}
	return patchLines(input)
}

func patchLines(patch string) []diffLine {
	if !strings.Contains(patch, "*** Begin Patch") {
		return nil
	}
	var out []diffLine
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "*** Update File: "), strings.HasPrefix(l, "*** Add File: "), strings.HasPrefix(l, "*** Delete File: "):
			out = append(out, diffLine{' ', strings.TrimPrefix(l, "*** ")})
		case strings.HasPrefix(l, "+"):
			out = append(out, diffLine{'+', l[1:]})
		case strings.HasPrefix(l, "-"):
			out = append(out, diffLine{'-', l[1:]})
		}
	}
	return out
}

// diffBlock shows an edit's lines, the first limit of them when limit is
// above zero.
func diffBlock(c *ui.Context, lines []diffLine, limit int) {
	t := c.Theme()
	shown := lines
	if limit > 0 {
		shown = lines[:min(len(lines), limit)]
	}
	ui.ScrollHorizontal(c).Label("改动").Radius(6).Border(1, t.Border).Children(func() {
		ui.Column(c).Padding(4, 0).Children(func() {
			for _, l := range shown {
				row := ui.Row(c).Gap(10).Padding(1, 12)
				sign, ink := string(l.sign), t.Text
				switch l.sign {
				case '+':
					row.Background(token(t, "diff-add"))
					ink = t.Success
				case '-':
					row.Background(token(t, "diff-del"))
					ink = t.Danger
				default:
					sign = ""
				}
				row.Children(func() {
					ui.Text(c, sign).Font(monoFont).FontSize(11.5).TextColor(ink).Width(8).Shrink(0)
					text := ui.Text(c, l.text).Font(monoFont).FontSize(11.5).NoWrap().Selectable()
					if l.sign == ' ' {
						text.TextColor(t.TextMuted).FontWeight(600)
					}
				})
			}
		})
	})
	if extra := len(lines) - len(shown); extra > 0 {
		ui.Textf(c, "另有 %d 行改动未显示，可在会话详情中打开这一步查看全部", extra).FontSize(11).TextColor(token(t, "ink-3"))
	}
}
