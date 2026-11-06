package app

import (
	"encoding/json"
	"fmt"
	"strings"

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

// replayTurn shows one turn as its agent loop, then what it used.
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
	shown := a.replayLoop(c, d, content, index, query)
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
	if len(meta) > 0 {
		number(c, strings.Join(meta, " · ")).FontSize(11).TextColor(muted).Padding(0, 0, 0, 36)
	}
	if n := content.context[index]; n > 0 && filter == replayAll && query == "" {
		a.replayContext(c, turn.turn.ID, content.turns[index], n)
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

// replayContext folds what the Agent injected without the person typing it:
// system prompts, instruction files, reminders. It stays closed until asked.
func (a *App) replayContext(c *ui.Context, turnID string, entries []session.Entry, n int) {
	if a.sessions.expanded == nil {
		a.sessions.expanded = map[string]bool{}
	}
	key := "ctx:" + turnID
	open := a.sessions.expanded[key]
	ui.Column(c).Gap(10).Padding(0, 0, 0, 36).Children(func() {
		label := fmt.Sprintf("查看 %d 条自动注入的上下文", n)
		if open {
			label = "收起自动注入的上下文"
		}
		ui.Row(c).Children(func() {
			overviewLink(c, label, func() { a.sessions.expanded[key] = !open })
		})
		if !open {
			return
		}
		for i, e := range entries {
			if e.Kind != session.EntryContext {
				continue
			}
			title := e.Name
			if title == "" {
				title = "自动注入的上下文"
			}
			a.replayText(c, fmt.Sprintf("%s:%d", key, i), title, e.Text, e.Truncated, codeBlock)
		}
	})
}
