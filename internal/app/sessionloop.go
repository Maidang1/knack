package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// A turn replays as a transcript, the way the Agent showed it: what the
// person typed, the model's thinking and replies, and each tool call as its
// name and argument with the first lines of what came back. Calls that
// only looked around — reads, searches, listings — fold into one line.

// replayStep is one entry of the transcript, or a run of look-around calls.
type replayStep struct {
	entries []session.Entry
	keys    []string
}

// Output shows this many lines until opened.
const replayOutputLines = 3

func (a *App) replayLoop(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) int {
	turn := d.turns[index]
	filter := a.sessions.replayFilter
	if a.sessions.expanded == nil {
		a.sessions.expanded = map[string]bool{}
	}
	requests := map[string]*session.Request{}
	for i := range turn.turn.Requests {
		requests[turn.turn.Requests[i].ID] = &turn.turn.Requests[i]
	}
	tools := map[string]*session.ToolCall{}
	for i := range turn.turn.Tools {
		tools[turn.turn.Tools[i].ID] = &turn.turn.Tools[i]
	}
	// A request's thinking may be logged as several blocks, some holding
	// only a signature; it shows once, with text when any block has it.
	thought := map[string]bool{}
	for _, e := range content.turns[index] {
		if e.Kind == session.EntryThinking && strings.TrimSpace(e.Text) != "" {
			thought[e.RequestID] = true
		}
	}
	var steps []replayStep
	lastThinking := ""
	for i, e := range content.turns[index] {
		if e.Kind == session.EntryContext || !entryMatches(e, query) {
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
		if e.Kind == session.EntryThinking {
			empty := strings.TrimSpace(e.Text) == ""
			if empty && thought[e.RequestID] || empty && e.RequestID != "" && lastThinking == e.RequestID {
				continue
			}
			lastThinking = e.RequestID
		}
		key := fmt.Sprintf("%s:%d", turn.turn.ID, i)
		if n := len(steps); n > 0 && lookingAround(e) && lookingAround(steps[n-1].entries[0]) {
			steps[n-1].entries = append(steps[n-1].entries, e)
			steps[n-1].keys = append(steps[n-1].keys, key)
			continue
		}
		steps = append(steps, replayStep{entries: []session.Entry{e}, keys: []string{key}})
	}
	ui.Column(c).Gap(14).Label("对话记录").Children(func() {
		for _, s := range steps {
			e, key := s.entries[0], s.keys[0]
			switch {
			case lookingAround(e):
				a.replayLooks(c, s, tools, d.session.CWD)
			case e.Kind == session.EntryUser:
				a.replayPrompt(c, key, e)
			case e.Kind == session.EntryThinking:
				a.replayThinking(c, key, e, requests[e.RequestID])
			case e.Kind == session.EntryAssistant:
				replayBullet(c, c.Theme().Text, func() {
					a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock)
				})
			case e.Kind == session.EntryTool:
				a.replayTool(c, key, e, tools[e.ToolID], d.session.CWD)
			case e.Kind == session.EntryEvent:
				a.replayEvent(c, key, e)
			}
		}
	})
	return len(steps)
}

// lookingAround reports a tool call that only read, searched or listed.
func lookingAround(e session.Entry) bool {
	return e.Kind == session.EntryTool && describeTool(e.Name, e.Input).look != lookNone
}

// replayBullet sets content beside a dot in color, the dot level with the
// content's first line.
func replayBullet(c *ui.Context, color ui.Color, content func()) {
	ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Width(14).Shrink(0).Padding(7, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(7, 7).Radius(4).Background(color)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(content)
	})
}

// replayResult hangs what a step returned under it, behind a ⎿.
func replayResult(c *ui.Context, content func()) {
	ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
		ui.Text(c, "⎿").Font(monoFont).FontSize(12).LineHeight(1.6).TextColor(token(c.Theme(), "ink-3")).Width(14).Shrink(0).Padding(0, 0, 0, 2)
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(content)
	})
}

func (a *App) replayPrompt(c *ui.Context, key string, e session.Entry) {
	t := c.Theme()
	ui.Row(c).Gap(8).Padding(8, 10).Radius(7).AlignItems(ui.Start).Background(t.SurfaceHover).Label("用户输入").Children(func() {
		ui.Text(c, ">").Font(monoFont).FontSize(13).LineHeight(1.6).TextColor(token(t, "ink-3")).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock)
		})
		if !e.Start.IsZero() {
			number(c, stepClock(e.Start)).FontSize(11.5).LineHeight(1.9).TextColor(token(t, "ink-3")).Shrink(0)
		}
	})
}

// replayThinking is the model's thinking, one muted line until opened. Logs
// that kept only its signature show how long it took, or nothing.
func (a *App) replayThinking(c *ui.Context, key string, e session.Entry, r *session.Request) {
	t := c.Theme()
	muted := token(t, "ink-3")
	took := time.Duration(0)
	if r != nil && r.Thinking.Known {
		took = r.Thinking.Value
	}
	if !e.Start.IsZero() && e.End.After(e.Start) {
		took = e.End.Sub(e.Start)
	}
	head := "思考"
	if took >= time.Second {
		head = "思考了 " + session.FormatDuration(took)
	}
	line := func(text string) {
		ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
			ui.Icon(c, glyph("sparkle")).FontSize(12).TextColor(muted).Width(14).Shrink(0)
			ui.Text(c, head).FontSize(13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, text).FontSize(13).TextColor(muted).MinWidth(0).SingleLine()
		})
	}
	if strings.TrimSpace(e.Text) == "" {
		// Only the signature was kept: the time it took is all there is.
		if took >= time.Second {
			line("")
		}
		return
	}
	// Thinking often opens on a bold heading; its first line reads without
	// the markup.
	preview := strings.ReplaceAll(strings.ReplaceAll(firstLine(e.Text), "****", " · "), "**", "")
	open := a.sessions.expanded["think:"+key]
	b := ui.ButtonBase(c.Key("think:" + key)).AlignSelf(ui.Stretch).Justify(ui.Start).Label(head + " · " + preview)
	b.Children(func() { line(preview) })
	if b.Clicked() {
		a.sessions.expanded["think:"+key] = !open
		c.Invalidate()
	}
	if open {
		ui.Column(c).Padding(0, 0, 0, 22).Children(func() {
			ui.Column(c).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 2).BorderColor(t.Border).Children(func() {
				a.replayText(c, "thinking:"+key, "", e.Text, e.Truncated, proseBlock)
			})
		})
	}
}

func (a *App) replayEvent(c *ui.Context, key string, e session.Entry) {
	t := c.Theme()
	color := token(t, "ink-3")
	if e.Error {
		color = t.Danger
	}
	replayBullet(c, color, func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, e.Name).FontSize(13).FontWeight(600)
			loopClock(c, e.Start)
		})
		if e.Text != "" {
			replayResult(c, func() { a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock) })
		}
	})
}

// replayTool shows a call as its name and argument — Bash(go test ./...) —
// and under it what came back: a few words when that says it (240 行), the
// first lines otherwise. The name opens the full input; the output opens
// in full from its last line.
func (a *App) replayTool(c *ui.Context, key string, e session.Entry, tool *session.ToolCall, cwd string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	name := e.Name
	if name == "" {
		name = "工具"
	}
	desc := describeTool(name, e.Input)
	failed := e.Error || tool != nil && (tool.Failed || tool.Interrupted)
	dot := t.Success
	switch {
	case failed:
		dot = t.Danger
	case e.End.IsZero():
		dot = muted
	}
	args := toolArgs(desc, cwd)
	inKey := "tool:" + key
	inOpen := a.sessions.expanded[inKey]
	replayBullet(c, dot, func() {
		b := ui.ButtonBase(c.Key(inKey)).AlignSelf(ui.Stretch).Justify(ui.Start).AlignItems(ui.Start).Gap(12).Label(strings.TrimSpace(name + " " + args)).Tooltip("查看完整输入")
		b.Children(func() {
			ui.Row(c).Gap(0).AlignItems(ui.Start).MinWidth(0).Grow(1).Children(func() {
				ui.Text(c, name).Font(monoFont).FontSize(13).LineHeight(1.6).FontWeight(700).Shrink(0)
				if args != "" {
					ui.Text(c, "("+headLines(args, 2, 240)+")").Font(monoFont).FontSize(13).LineHeight(1.6).TextColor(t.TextMuted).MinWidth(0)
				}
			})
			meta := func(text string, ink ui.Color) {
				number(c, text).FontSize(12).LineHeight(1.8).TextColor(ink).Shrink(0)
			}
			if tool != nil && tool.PermissionWait {
				meta("等你确认", token(t, "warn-ink"))
			}
			if tool != nil && tool.Elapsed.Known && tool.Elapsed.Value >= time.Second {
				meta(durationText(tool.Elapsed), muted)
			}
		})
		if b.Clicked() {
			a.sessions.expanded[inKey] = !inOpen
			c.Invalidate()
		}
		if inOpen {
			ui.Column(c).Padding(2, 0, 4, 0).Children(func() { a.toolInput(c, "in:"+key, e.Input, 0) })
		}
		replayResult(c, func() { a.toolResult(c, key, e, desc, failed) })
	})
}

// toolResult is what a call returned: a plan as its items, an edit as its
// change, a read or search as how much it found, anything else as its first
// lines.
func (a *App) toolResult(c *ui.Context, key string, e session.Entry, desc toolDesc, failed bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	note := func(text string) {
		ui.Text(c, text).FontSize(12.5).LineHeight(1.6).TextColor(t.TextMuted)
	}
	if len(desc.todos) > 0 {
		todoList(c, desc.todos)
		return
	}
	if diff := editDiff(e.Input); len(diff) > 0 && !failed {
		add, del := 0, 0
		for _, l := range diff {
			switch l.sign {
			case '+':
				add++
			case '-':
				del++
			}
		}
		note(fmt.Sprintf("新增 %d 行，删除 %d 行", add, del))
		limit := 8
		if a.sessions.expanded["diff:"+key] {
			limit = 0
		}
		diffBlock(c, diff, limit)
		if len(diff) > 8 {
			label := "收起"
			if limit > 0 {
				label = fmt.Sprintf("… +%d 行", len(diff)-8)
			}
			moreLink(c, "diff:"+key, label, func() { a.sessions.expanded["diff:"+key] = limit > 0 })
		}
		return
	}
	if e.End.IsZero() {
		note("没有记录输出")
		return
	}
	text := strings.TrimRight(e.Text, "\n ")
	if strings.TrimSpace(text) == "" {
		note("（无输出）")
		return
	}
	outKey := "out:" + key
	open := a.sessions.expanded[outKey]
	if !failed && !open {
		if s := toolOutcome(e.Name, text); s != "" {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				note(s)
				moreLink(c, outKey, "查看输出", func() { a.sessions.expanded[outKey] = true })
			})
			return
		}
	}
	ink := t.Text
	if failed {
		ink = t.Danger
	}
	shown, more, clipped := text, 0, false
	if !open {
		shown, more, clipped = outputHead(text, replayOutputLines)
	}
	ui.Text(c, shown).Font(monoFont).FontSize(12).LineHeight(1.6).TextColor(ink).Selectable()
	switch {
	case open:
		moreLink(c, outKey, "收起", func() { a.sessions.expanded[outKey] = false })
	case more > 0:
		moreLink(c, outKey, fmt.Sprintf("… +%d 行", more), func() { a.sessions.expanded[outKey] = true })
	case clipped:
		moreLink(c, outKey, "… 展开全文", func() { a.sessions.expanded[outKey] = true })
	}
	if open && e.Truncated {
		ui.Text(c, "超过 64 KiB 的部分未显示，可打开日志查看").FontSize(11).TextColor(muted)
	}
}

// replayLooks folds a run of reads, searches and listings into one line —
// 读取了 README.md、go.mod，搜索了 2 个模式 — that opens to each call.
func (a *App) replayLooks(c *ui.Context, s replayStep, tools map[string]*session.ToolCall, cwd string) {
	t := c.Theme()
	var files []string
	searches, lists, failed := 0, 0, 0
	for _, e := range s.entries {
		d := describeTool(e.Name, e.Input)
		switch d.look {
		case lookRead:
			files = append(files, d.subject)
		case lookSearch:
			searches++
		case lookList:
			lists++
		}
		if tool := tools[e.ToolID]; e.Error || tool != nil && (tool.Failed || tool.Interrupted) {
			failed++
		}
	}
	var parts []string
	switch {
	case len(files) > 3:
		parts = append(parts, fmt.Sprintf("读取了 %d 个文件", len(files)))
	case len(files) > 0:
		parts = append(parts, "读取了 "+strings.Join(files, "、"))
	}
	if searches > 0 {
		parts = append(parts, fmt.Sprintf("搜索了 %d 个模式", searches))
	}
	if lists > 0 {
		parts = append(parts, fmt.Sprintf("列出了 %d 个目录", lists))
	}
	summary := strings.Join(parts, "，")
	key := "looks:" + s.keys[0]
	open := a.sessions.expanded[key]
	dot := token(t, "ink-3")
	if failed > 0 {
		dot = t.Danger
	}
	replayBullet(c, dot, func() {
		b := ui.ButtonBase(c.Key(key)).AlignSelf(ui.Stretch).Justify(ui.Start).Gap(8).AlignItems(ui.Center).Label(summary)
		b.Children(func() {
			ui.Text(c, summary).FontSize(13).LineHeight(1.6).TextColor(t.TextMuted).MinWidth(0)
			if failed > 0 {
				ui.Textf(c, "%d 次失败", failed).FontSize(12).TextColor(t.Danger).Shrink(0)
			}
			label := "展开"
			if open {
				label = "收起"
			}
			ui.Text(c, label).FontSize(12).TextColor(t.Accent).Shrink(0)
		})
		if b.Clicked() {
			a.sessions.expanded[key] = !open
			c.Invalidate()
		}
		if !open {
			return
		}
		ui.Column(c).Gap(10).Padding(4, 0, 0, 0).Children(func() {
			for i, e := range s.entries {
				a.replayTool(c, s.keys[i], e, tools[e.ToolID], cwd)
			}
		})
	})
}

// toolArgs is the argument a call reads by: the command it ran, the path
// it worked on (from the session's folder when inside it), or its subject.
func toolArgs(d toolDesc, cwd string) string {
	switch {
	case d.command != "":
		return d.command
	case d.path != "":
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, d.path); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
		return d.path
	}
	return d.subject
}

// outputHead is the first lines of output that have something on them,
// clipped to a few hundred characters, how many such lines follow, and
// whether the characters were clipped.
func outputHead(text string, lines int) (string, int, bool) {
	var kept []string
	more := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(kept) == lines {
			more++
			continue
		}
		kept = append(kept, l)
	}
	head := strings.Join(kept, "\n")
	if cut := clipLines(head, lines, 600); cut != head {
		return strings.TrimSuffix(cut, "\n…"), more, true
	}
	return head, more, false
}

// headLines keeps the first lines of s that have something on them, with
// an ellipsis when more follow.
func headLines(s string, lines, chars int) string {
	var kept []string
	rest := false
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if len(kept) == lines {
			rest = true
			break
		}
		kept = append(kept, strings.TrimRight(l, " "))
	}
	out := strings.Join(kept, "\n")
	if len(out) > chars {
		out, rest = out[:chars], true
		for !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	if rest {
		out += "…"
	}
	return out
}

func moreLink(c *ui.Context, key, label string, action func()) {
	b := ui.Button(c.Key("more:"+key), label).Padding(0).Height(20).Border(0, ui.Transparent).Background(ui.Transparent).TextColor(token(c.Theme(), "ink-3")).FontSize(12).AlignSelf(ui.Start)
	if b.Clicked() {
		action()
		c.Invalidate()
	}
}

// todoList shows a plan as its items, each marked by where it stood.
func todoList(c *ui.Context, todos []todoItem) {
	t := c.Theme()
	ui.Column(c).Gap(4).Label("待办").Children(func() {
		for _, item := range todos {
			ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
				icon, ink, text := "dashed", token(t, "ink-3"), t.TextMuted
				switch item.status {
				case "done":
					icon, ink = "check", t.Success
				case "active":
					icon, ink, text = "arrow-right", t.Text, t.Text
				}
				ui.Icon(c, glyph(icon)).FontSize(12).TextColor(ink).Shrink(0).Padding(3, 0, 0, 0)
				label := ui.Text(c, item.text).FontSize(13).LineHeight(1.5).TextColor(text).MinWidth(0)
				if item.status == "done" {
					label.TextColor(token(t, "ink-3"))
				}
				if item.status == "active" {
					label.FontWeight(600)
				}
			})
		}
	})
}

func loopClock(c *ui.Context, at time.Time) {
	if at.IsZero() {
		return
	}
	number(c, stepClock(at)).FontSize(11.5).TextColor(token(c.Theme(), "ink-3"))
}
