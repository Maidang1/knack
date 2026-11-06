package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// A turn replays as the agent loop it was: the person's input, then for
// each model call its thinking, its reply, the tools it called and what
// they returned, down one rail in the order the log recorded them.

// loopNode is one step on the rail: a marker in the step's color, what
// happened, and the content under it. toggle, when given, makes the title
// row a button that opens the step.
type loopNode struct {
	icon, title, label string
	key                string // tells toggling steps apart; label is what a screen reader hears
	color              ui.Color
	meta               func()
	body               func()
	toggle             func()
	open               bool
	marker             bool // a model call heading rather than a step
}

func (a *App) replayLoop(c *ui.Context, d *sessionDetailData, content *replayIndex, index int, query string) int {
	t := c.Theme()
	muted := token(t, "ink-3")
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
	toggle := func(key string) func() {
		return func() { a.sessions.expanded[key] = !a.sessions.expanded[key] }
	}
	var nodes []loopNode
	calls, request, lastThinking := 0, "", ""
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
		key := fmt.Sprintf("%s:%d", turn.turn.ID, i)
		if e.RequestID != "" && e.RequestID != request && e.Kind != session.EntryUser {
			request = e.RequestID
			calls++
			nodes = append(nodes, modelCallNode(c, calls, e, requests[e.RequestID]))
		}
		switch e.Kind {
		case session.EntryUser:
			nodes = append(nodes, loopNode{icon: "user", title: "用户输入", color: t.Text,
				meta: func() { loopClock(c, e.Start) },
				body: func() { a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock) }})
		case session.EntryThinking:
			empty := strings.TrimSpace(e.Text) == ""
			if empty && thought[e.RequestID] || empty && e.RequestID != "" && lastThinking == e.RequestID {
				continue
			}
			lastThinking = e.RequestID
			nodes = append(nodes, a.thinkingNode(c, key, e, requests[e.RequestID], toggle("think:"+key)))
		case session.EntryAssistant:
			nodes = append(nodes, loopNode{icon: "pen", title: "模型回复", color: sessionColor("phase-gen", t.Dark),
				body: func() { a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock) }})
		case session.EntryTool:
			call, result := a.toolNodes(c, key, e, tools[e.ToolID], toggle)
			nodes = append(nodes, call, result)
		case session.EntryEvent:
			color := muted
			if e.Error {
				color = t.Danger
			}
			nodes = append(nodes, loopNode{icon: "alert", title: e.Name, color: color,
				meta: func() { loopClock(c, e.Start) },
				body: func() {
					if e.Text != "" {
						a.replayText(c, "msg:"+key, "", e.Text, e.Truncated, proseBlock)
					}
				}})
		}
	}
	ui.Column(c).Label("Agent 循环").Children(func() {
		for i, n := range nodes {
			loopRow(c, n, i == len(nodes)-1)
		}
	})
	steps := 0
	for _, n := range nodes {
		if !n.marker {
			steps++
		}
	}
	return steps
}

// loopRow draws a node: its marker on the rail, the line on to the next
// node, and its title and content beside them.
func loopRow(c *ui.Context, n loopNode, last bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Row(c).Gap(12).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(22).Shrink(0).AlignItems(ui.Center).Children(func() {
			if n.marker {
				ui.Box(c).Width(1).Height(6).Background(t.Border)
				ui.Box(c).Size(7, 7).Radius(4).Shrink(0).Background(n.color)
			} else {
				// The tint carries the step's color; the glyph stays legible on it.
				ui.Box(c).Size(22, 22).Radius(11).Shrink(0).Center().Background(fade(n.color, 64)).Children(func() {
					ui.Icon(c, glyph(n.icon)).FontSize(12).TextColor(t.Text)
				})
			}
			if !last {
				ui.Box(c).Width(1).Grow(1).MinHeight(6).Background(t.Border)
			}
		})
		bottom := float32(16)
		if n.marker {
			bottom = 8
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Padding(0, 0, bottom, 0).Children(func() {
			head := func() {
				if n.toggle != nil {
					icon := "chevron-right"
					if n.open {
						icon = "chevron-down"
					}
					ui.Icon(c, glyph(icon)).FontSize(11).TextColor(muted).Shrink(0)
				}
				title := ui.Text(c, n.title).FontSize(12.5).FontWeight(600).Shrink(0).SingleLine()
				if n.marker {
					title.FontSize(11.5).TextColor(t.TextMuted)
				}
				if n.meta != nil {
					n.meta()
				}
			}
			if n.toggle != nil {
				label := n.label
				if label == "" {
					label = n.title
				}
				b := ui.ButtonBase(c.Key("loop:" + n.key)).Height(22).Gap(8).AlignSelf(ui.Stretch).AlignItems(ui.Center).Justify(ui.Start).Label(label)
				b.Children(head)
				if b.Clicked() {
					n.toggle()
					c.Invalidate()
				}
			} else {
				ui.Row(c).Height(22).Gap(8).AlignItems(ui.Center).Children(head)
			}
			if n.body != nil {
				n.body()
			}
		})
	})
}

// modelCallNode heads one go round the loop: which model, how long it
// took and what it wrote.
func modelCallNode(c *ui.Context, k int, e session.Entry, r *session.Request) loopNode {
	t := c.Theme()
	var parts []string
	model := e.Name
	if r != nil && r.Model != "" {
		model = r.Model
	}
	if model != "" {
		parts = append(parts, model)
	}
	if r != nil {
		if r.Elapsed.Known {
			parts = append(parts, durationText(r.Elapsed))
		}
		if r.UsageKnown {
			parts = append(parts, "输出 "+groupDigits(int(r.Tokens.Output))+" tok")
		}
	}
	color := sessionColor("phase-gen", t.Dark)
	if r != nil && (r.APIError || r.Retry) {
		color = sessionColor("phase-retry", t.Dark)
	}
	return loopNode{marker: true, title: fmt.Sprintf("第 %d 次模型调用", k), color: color, meta: func() {
		number(c, strings.Join(parts, " · ")).FontSize(11).TextColor(token(t, "ink-3")).MinWidth(0).SingleLine()
	}}
}

// thinkingNode is the model's thinking, folded until opened. Logs that kept
// only the thinking's signature say so instead of opening to nothing.
func (a *App) thinkingNode(c *ui.Context, key string, e session.Entry, r *session.Request, toggle func()) loopNode {
	t := c.Theme()
	muted := token(t, "ink-3")
	took := time.Duration(0)
	if r != nil && r.Thinking.Known {
		took = r.Thinking.Value
	}
	if !e.Start.IsZero() && e.End.After(e.Start) {
		took = e.End.Sub(e.Start)
	}
	meta := ""
	if took >= time.Second {
		meta = "思考了 " + session.FormatDuration(took)
	}
	n := loopNode{icon: "sparkle", title: "模型思考", color: sessionColor("phase-think", t.Dark)}
	if strings.TrimSpace(e.Text) == "" {
		n.meta = func() {
			if meta != "" {
				number(c, meta).FontSize(11.5).TextColor(muted)
			}
			ui.Text(c, "日志只保存了签名，没有思考正文").FontSize(11.5).TextColor(muted).MinWidth(0).SingleLine()
		}
		return n
	}
	n.open = a.sessions.expanded["think:"+key]
	n.label, n.key = "模型思考 · "+firstLine(e.Text), "think:"+key
	n.toggle = toggle
	n.meta = func() {
		if meta != "" {
			number(c, meta).FontSize(11.5).TextColor(muted)
		}
		if !n.open {
			ui.Text(c, firstLine(e.Text)).FontSize(12).TextColor(muted).MinWidth(0).SingleLine()
		}
	}
	if n.open {
		n.body = func() {
			ui.Column(c).Padding(0, 0, 0, 12).BorderWidth(0, 0, 0, 2).BorderColor(t.Border).Children(func() {
				a.replayText(c, "thinking:"+key, "", e.Text, e.Truncated, proseBlock)
			})
		}
	}
	return n
}

// toolNodes are a tool call and what it returned, as two steps: the call
// opens to its input, the return to its output.
func (a *App) toolNodes(c *ui.Context, key string, e session.Entry, tool *session.ToolCall, toggle func(string) func()) (loopNode, loopNode) {
	t := c.Theme()
	muted := token(t, "ink-3")
	name := e.Name
	if name == "" {
		name = "工具"
	}
	sub := tool != nil && tool.AgentID != ""
	preview := inputPreview(e.Input)
	diff := editDiff(e.Input)
	color := sessionColor("phase-tool", t.Dark)
	callTitle, returnTitle, icon := "调用工具", "工具返回", toolIcon(name)
	if sub {
		color, callTitle, returnTitle, icon = sessionColor("phase-sub", t.Dark), "调用子 Agent", "子 Agent 返回", "bot"
	}
	call := loopNode{icon: icon, title: callTitle, color: color, label: name + " · " + preview, key: "tool:" + key, open: a.sessions.expanded["tool:"+key], toggle: toggle("tool:" + key)}
	call.meta = func() {
		ui.Text(c, name).FontSize(12.5).Shrink(0).SingleLine()
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
	}
	if call.open {
		call.body = func() { a.toolInput(c, "in:"+key, e.Input, diffPreviewLines) }
	}
	failed := e.Error || tool != nil && (tool.Failed || tool.Interrupted)
	resultColor := color
	if failed {
		resultColor = t.Danger
	}
	result := loopNode{icon: "arrow-left", title: returnTitle, color: resultColor}
	status := func() {
		switch {
		case e.End.IsZero():
			ui.Text(c, "没有记录输出").FontSize(11.5).TextColor(muted).Shrink(0)
		case failed:
			ui.Row(c).Padding(1, 6).Radius(4).Background(token(t, "danger-bg")).Shrink(0).Children(func() {
				ui.Text(c, "失败").FontSize(11).TextColor(t.Danger)
			})
		}
		if tool != nil {
			number(c, durationText(tool.Elapsed)).FontSize(11.5).TextColor(muted).Shrink(0)
		}
	}
	if e.End.IsZero() || strings.TrimSpace(e.Text) == "" {
		result.meta = func() {
			status()
			if !e.End.IsZero() {
				ui.Text(c, "（空）").FontSize(11.5).TextColor(muted)
			}
		}
		return call, result
	}
	result.open = a.sessions.expanded["out:"+key]
	result.label, result.key = returnTitle+" · "+name, "out:"+key
	result.toggle = toggle("out:" + key)
	result.meta = func() {
		status()
		if !result.open {
			number(c, firstLine(e.Text)).FontSize(11.5).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
		}
	}
	if result.open {
		result.body = func() { a.replayText(c, "out:"+key, "", e.Text, e.Truncated, terminalBlock) }
	}
	return call, result
}

func loopClock(c *ui.Context, at time.Time) {
	if at.IsZero() {
		return
	}
	number(c, stepClock(at)).FontSize(11).TextColor(token(c.Theme(), "ink-3"))
}
