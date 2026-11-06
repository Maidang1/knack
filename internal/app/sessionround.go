package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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

// Arguments longer than this, or on several lines, show as their own block.
const inputInlineChars = 160

// inputParts splits a tool's JSON input into its short arguments, one per
// line, and its long ones, unescaped, so a written file or a script reads as
// itself rather than as one escaped line. Input that is not a JSON object is
// all short.
func inputParts(input string) (short string, long [][2]string) {
	var args map[string]any
	if json.Unmarshal([]byte(input), &args) != nil || len(args) == 0 {
		return input, nil
	}
	var lines []string
	for _, k := range slices.Sorted(maps.Keys(args)) {
		v, ok := args[k].(string)
		if !ok {
			// Lists and objects stay on one line when short, indented when not.
			v = encodeInput(args[k], "")
			if len(v) > inputInlineChars {
				v = encodeInput(args[k], "  ")
			}
		}
		if strings.Contains(v, "\n") || len(v) > inputInlineChars {
			long = append(long, [2]string{k, v})
			continue
		}
		lines = append(lines, k+": "+v)
	}
	return strings.Join(lines, "\n"), long
}

func encodeInput(v any, indent string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	enc.Encode(v)
	return strings.TrimSpace(b.String())
}

// toolInput shows what a tool was given: an edit as its diff, other input
// as its arguments. limit caps the diff's lines when above zero.
func (a *App) toolInput(c *ui.Context, key, input string, limit int) {
	short, long := inputParts(input)
	if diff := editDiff(input); len(diff) > 0 {
		var rest []string
		for _, line := range strings.Split(short, "\n") {
			if line != "" && !strings.HasPrefix(line, "old_string: ") && !strings.HasPrefix(line, "new_string: ") {
				rest = append(rest, line)
			}
		}
		if len(rest) > 0 {
			a.replayText(c, key, "输入", strings.Join(rest, "\n"), false, codeBlock)
		}
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "改动").FontSize(12).FontWeight(600).TextColor(c.Theme().TextMuted)
			diffBlock(c, diff, limit)
		})
		return
	}
	if short != "" || len(long) == 0 {
		a.replayText(c, key, "输入", short, false, codeBlock)
	}
	for _, part := range long {
		a.replayText(c, key+":"+part[0], "输入 · "+part[0], part[1], false, codeBlock)
	}
}

func turnEvents(content *replayIndex, turn int) []session.Entry {
	if content == nil || turn >= len(content.events) {
		return nil
	}
	return content.events[turn]
}

func stepTrack(c *ui.Context, turn sessionTurnView, step sessionStep, axis stepAxis) {
	stepSpan(c, turn, step, axis, sessionColor(phaseKeys[step.phase], c.Theme().Dark))
}

// stepSpan draws step's interval on the turn's axis in color.
func stepSpan(c *ui.Context, turn sessionTurnView, step sessionStep, axis stepAxis, color ui.Color) {
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
		paintSpan := func(left, right float32, fill ui.Color) {
			for _, cut := range axis.cuts {
				center := r.X + (axis.at(cut.from)+axis.at(cut.to))/2*r.W
				if right > center-3 && left < center+3 {
					if center-3 > left {
						p.Fill(ui.Rect{X: left, Y: r.Y + 2, W: center - 3 - left, H: 12}, fill, 2)
					}
					left = max(left, center+3)
				}
			}
			if right > left {
				p.Fill(ui.Rect{X: left, Y: r.Y + 2, W: right - left, H: 12}, fill, 2)
			}
		}
		paintSpan(x1, x1+w, color)
		if step.phase == phaseGen && step.thinking > 0 {
			end := axis.at(from+step.thinking)*r.W + r.X
			paintSpan(x1, min(end, x1+w), sessionColor("phase-think", t.Dark))
		}
	})
}

// How recorded text shows: prose wraps; code and tool output keep their
// lines, on a quiet face or a terminal's.
type blockKind int

const (
	proseBlock blockKind = iota
	codeBlock
	terminalBlock
)

// replayText shows recorded text, selectable, clipped until expanded.
func (a *App) replayText(c *ui.Context, key, title, text string, truncated bool, kind blockKind) {
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
			ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(t.TextMuted)
		}
		if strings.TrimSpace(text) == "" {
			ui.Text(c, "（空）").FontSize(11.5).TextColor(t.TextMuted)
			return
		}
		if kind != proseBlock {
			// Code and tool output keep their lines; long unbroken paths and
			// commands scroll instead of overflowing or being split.
			face, ink := t.SurfaceHover, t.Text
			if kind == terminalBlock {
				face, ink = token(t, "code-bg"), token(t, "code-ink")
			}
			ui.ScrollHorizontal(c.Key("replay-scroll:" + key)).Label(title).Background(face).Radius(7).Children(func() {
				ui.Text(c, shown).Font(monoFont).FontSize(12).LineHeight(1.55).TextColor(ink).NoWrap().Selectable().Padding(10, 12)
			})
		} else {
			ui.Text(c, shown).FontSize(13).LineHeight(1.6).Selectable()
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
					ui.Text(c, "超过 64 KiB 的部分未显示，可打开日志查看").FontSize(11).TextColor(t.TextMuted)
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
