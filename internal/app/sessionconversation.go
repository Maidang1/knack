package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"knack/internal/session"

	"github.com/egoist/mygo/ui"
)

// The conversation replay is the design's 对话回放: every turn in one
// scroll, its messages, thinking and tool calls in the order the log
// recorded them. A slim outline down the side marks where each turn sits
// in it. Content is read on demand and only held in memory.

const (
	replayAll = iota
	replayToolsOnly
	replayRepliesOnly
)

// The outline down the side of the replay: one dash per turn. They are all
// the same length until the pointer comes near one; then that dash grows and
// its neighbours fall away from it.
const (
	replayMapWidth   = 56
	replayMapIndent  = 18
	replayMapStep    = 16
	replayMapDash    = 3
	replayMapDashMin = 16
	replayMapDashMax = 40
	// How many dashes the growth reaches down.
	replayMapFalloff = 4
)

// Diffs longer than this show their start; the rest is in the input.
const diffPreviewLines = 40

func (a *App) sessionConversation(c *ui.Context, d *sessionDetailData) {
	t := c.Theme()
	s := &a.sessions
	replay := a.sessionReplay(d)
	content := replay.ready()
	index := a.sessionTurnIndex(d)
	query := strings.ToLower(strings.TrimSpace(s.replayQuery))
	ui.Column(c).Width(replayMapWidth).Shrink(0).Justify(ui.Center).Label("railbox").Children(func() {
		a.replayMap(c, d, content, index, query)
	})
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Label("mainbox").Children(func() {
		ui.Row(c).Justify(ui.Center).Padding(16, 32, 8, 32).Shrink(0).Children(func() {
			ui.Row(c).Grow(1).MaxWidth(760).Gap(8).Padding(8, 12).Radius(7).Background(t.SurfaceHover).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, glyph("lock")).FontSize(13).TextColor(t.TextMuted)
				ui.Text(c, "正文从原始日志按需读取，只保留在内存，关闭后释放。").FontSize(13).TextColor(t.TextMuted).MinWidth(0)
			})
		})
		notice := func(msg string) {
			ui.Row(c).Justify(ui.Center).Padding(8, 32).Children(func() {
				ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted).Grow(1).MaxWidth(760)
			})
		}
		switch {
		case replay == nil || replay.err != "":
			msg := "回放内容暂不可用"
			if replay != nil && replay.err != "" {
				msg = replay.err
			}
			notice(msg)
		case content == nil:
			notice("正在读取对话内容…")
		case index < 0:
			notice("没有可识别的轮次")
		default:
			a.replayTurns(c, d, content, index, query)
		}
	})
}

// replayTurns lays every turn the query matches down one list, building
// only those in view. Choosing a turn on the side scrolls to it; scrolling
// marks the turn at the top as the chosen one.
func (a *App) replayTurns(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) {
	s := &a.sessions
	if id := sessionID(d.session); s.replayFor != id {
		s.replayList, s.replayFor, s.replayAt, s.replayTop, s.replayJumped = ui.ListState{}, id, "", -1, 0
	}
	var rows []int
	for i := range d.turns {
		if query == "" || turnMatches(content, i, query) {
			rows = append(rows, i)
		}
	}
	if len(rows) == 0 {
		ui.Text(c, "没有符合筛选的轮次").FontSize(13).TextColor(c.Theme().TextMuted).Padding(8, 32)
		return
	}
	row := func(turn int) int {
		for r, i := range rows {
			if i >= turn {
				return r
			}
		}
		return len(rows) - 1
	}
	first, last := s.replayList.Visible()
	switch {
	case s.selectedTurn != s.replayAt:
		s.replayList.ScrollTo(row(index), ui.Start)
		s.replayAt, s.replayJumped = s.selectedTurn, 3
	case last < first:
	case s.replayJumped > 0:
		// A scroll asked for while input is handled can be dropped: ask
		// again until the turn is at the top, or the list ends before it
		// could be. Where it landed is the top then.
		if first == row(index) || last == len(rows)-1 || s.replayJumped == 1 {
			s.replayTop, s.replayJumped = first, 0
			break
		}
		s.replayList.ScrollTo(row(index), ui.Start)
		s.replayJumped--
		c.Invalidate()
	case first != s.replayTop && first < len(rows):
		s.replayTop = first
		a.chooseSessionTurn(d, rows[first])
		s.replayAt = s.selectedTurn
	}
	s.replayList.Key = func(r int) any {
		if r < 0 || r >= len(rows) {
			return nil
		}
		return d.turns[rows[r]].turn.ID
	}
	ui.List(c, &s.replayList, len(rows), func(r int) {
		ui.Row(c).Justify(ui.Center).Padding(8, 32, 24, 32).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).MaxWidth(760).Gap(14).Children(func() {
				a.replayTurn(c, d, content, rows[r], query)
			})
		})
	}).Grow(1).MinHeight(0).Background(ui.Transparent).Label("对话回放")
}

// replayMap is the transcript's outline: one dash per turn, the block of
// them centred down the side. Hovering a dash grows it and fades its
// neighbours away, and shows what the turn asked; choosing it scrolls the
// transcript there.
func (a *App) replayMap(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	accent := token(t, "accent")
	hover := a.sessions.replayMapHover
	ui.Scroll(c.Key("replay-map")).MaxHeightPercent(100).MinHeight(0).Children(func() {
		ui.Column(c).Padding(14, 0).Gap(0).FocusGroup(ui.Vertical).Label("对话大纲").Children(func() {
			for i, turn := range d.turns {
				if query != "" && content != nil && !turnMatches(content, i, query) {
					continue
				}
				on := i == index
				prompt, _ := turnPrompt(turn, content)
				length := replayMapDashMin
				if hover >= 0 {
					length = replayMapLength(i - hover)
				}
				color := fade(muted, 150)
				if on {
					color = accent
				}
				b := withTip(c, ui.ButtonBase(c.Key("replay-turn:"+turn.turn.ID)).Width(replayMapWidth).Height(replayMapStep).
					Padding(0, 0, 0, replayMapIndent).Radius(4).Justify(ui.Start).
					Label(turnLabel(turn)), replayPreview(turn, prompt))
				switch {
				case b.Hovered():
					// The dash under the pointer leads, in the brightest ink.
					hover, a.sessions.replayMapHover = i, i
					color = t.Text
				case hover == i:
					// The pointer left this dash.
					hover, a.sessions.replayMapHover = -1, -1
				}
				b.Children(func() {
					ui.Box(c).Width(float32(length)).Height(replayMapDash).Radius(replayMapDash / 2).Shrink(0).Background(color)
				})
				if on {
					if a.sessions.focusTurn {
						b.Focus()
						a.sessions.focusTurn = false
					}
					// The dash follows the replay as it scrolls.
					if a.sessions.replayAnchor != turn.turn.ID {
						a.sessions.replayAnchor = turn.turn.ID
						b.ScrollIntoView()
					}
				}
				// Moving the focus onto a dash chooses its turn; a dash that
				// keeps the focus does not take the choice back from scrolling.
				focused := b.Focused() && a.sessions.anchorFocus != turn.turn.ID
				if b.Focused() {
					a.sessions.anchorFocus = turn.turn.ID
				} else if a.sessions.anchorFocus == turn.turn.ID {
					a.sessions.anchorFocus = ""
				}
				if b.Clicked() || focused && !on {
					a.chooseSessionTurn(d, i)
					c.Invalidate()
				}
			}
		})
	})
}

// replayMapLength is how long a dash is, distance dashes from the one under
// the pointer: the one under it is the longest, and the ones around it fall
// away until they are all the same again.
func replayMapLength(distance int) int {
	if distance < 0 {
		distance = -distance
	}
	if distance <= 0 {
		return replayMapDashMax
	}
	if distance >= replayMapFalloff {
		return replayMapDashMin
	}
	fall := 1 - float32(distance)/float32(replayMapFalloff)
	length := float32(replayMapDashMin) + float32(replayMapDashMax-replayMapDashMin)*fall*fall
	return int(length)
}

// replayMatches is how many turns the conversation's search holds, or -1
// when it holds nothing to search.
func (a *App) replayMatches(d *sessionDetailData) int {
	if d == nil {
		return -1
	}
	query := strings.ToLower(strings.TrimSpace(a.sessions.replayQuery))
	if query == "" {
		return -1
	}
	content := a.sessionReplay(d).ready()
	if content == nil {
		return -1
	}
	n := 0
	for i := range d.turns {
		if turnMatches(content, i, query) {
			n++
		}
	}
	return n
}

// replayPreview is what a dash shows on hover: which turn it is, how long
// it ran, and what was asked.
func replayPreview(turn sessionTurnView, prompt string) string {
	preview := fmt.Sprintf("第 %d 轮 · %s", turn.index+1, activeText(turn))
	if prompt != "" {
		preview += "\n" + clipLines(prompt, 3, 240)
	}
	return preview
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
		ui.Textf(c, "第 %d 轮", index+1).FontSize(13).FontWeight(600).Shrink(0)
		number(c, clockText(turn.turn.Start)+" · "+activeText(turn)).FontSize(13).TextColor(muted).Shrink(0)
		ui.Box(c).Grow(1).Height(1).Background(t.Border)
	})
	shown := a.replayLoop(c, d, content, index, query)
	if shown == 0 {
		msg := "本轮没有可回放的内容"
		if query != "" || filter != replayAll {
			msg = "本轮没有符合筛选的内容"
		}
		ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted).Padding(0, 0, 0, 22)
	}
	var meta []string
	if turn.usage {
		meta = append(meta, "输出 "+groupDigits(int(turn.tokens.Output))+" tok")
		if input := turn.tokens.InputTotal(); input > 0 {
			meta = append(meta, fmt.Sprintf("缓存命中 %.0f%%", 100*float64(turn.tokens.CacheRead)/float64(input)))
		}
	}
	if len(meta) > 0 {
		number(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(muted).Padding(0, 0, 0, 22)
	}
	if n := content.context[index]; n > 0 && filter == replayAll && query == "" {
		a.replayContext(c, turn.turn.ID, content.turns[index], n)
	}
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
					ui.Text(c, sign).Font(monoFont).FontSize(12).TextColor(ink).Width(8).Shrink(0)
					text := ui.Text(c, l.text).Font(monoFont).FontSize(12).NoWrap().Selectable()
					if l.sign == ' ' {
						text.TextColor(t.TextMuted).FontWeight(600)
					}
				})
			}
		})
	})
	if extra := len(lines) - len(shown); extra > 0 {
		ui.Textf(c, "另有 %d 行改动未显示，可在会话详情中打开这一步查看全部", extra).FontSize(11.5).TextColor(token(t, "ink-3"))
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
	ui.Column(c).Gap(10).Padding(0, 0, 0, 22).Children(func() {
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
