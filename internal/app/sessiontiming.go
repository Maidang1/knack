package app

import (
	"fmt"
	"strings"
	"time"

	"knack/internal/session"
)

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
