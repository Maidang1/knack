package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Data hues stay clear of the violet accent (theme.go), so a series never
// reads as a selection: generation is blue, thinking a pale sky, tools teal,
// sub-Agents magenta. Dark values clear 3:1 against every surface a chart
// sits on, including a selected row.
var sessionPalette = map[string][2]string{
	"agent-claude":  {"#D97757", "#F29B79"},
	"agent-codex":   {"#3F3F46", "#AAAAB2"},
	"agent-cursor":  {"#7D8699", "#9BA5B8"},
	"agent-factory": {"#C9A227", "#DDBB4A"},
	"agent-gemini":  {"#4C7BE0", "#7EA2F0"},
	"agent-other":   {"#BABAC0", "#D0D0D6"},
	// Agents without their own color take these in registry order.
	"agent-extra-0": {"#5F8F6A", "#86B892"},
	"agent-extra-1": {"#B0708F", "#D394B2"},
	"agent-extra-2": {"#9A7B45", "#C6A46A"},
	"phase-think":   {"#A8CDE8", "#9BC4E1"},
	"phase-gen":     {"#2973D0", "#6CA7F5"},
	"phase-tool":    {"#229C9C", "#5BC8C8"},
	"phase-sub":     {"#C64998", "#E885BF"},
	"phase-wait":    {"#D0D0D6", "#7F7F87"},
	"phase-retry":   {"#E69C3A", "#F1AF57"},
	"data-neutral":  {"#919199", "#B0B0B9"},
	// The daily Token chart is the one place the theme's violet carries
	// data: its stacked Agents step from the accent to a pale tint.
	"chart-1": {"#703FD5", "#8760EB"},
	"chart-2": {"#9B85EC", "#B3A1FD"},
	"chart-3": {"#C8BFF5", "#DBD5FF"},
}

func sessionColor(name string, dark bool) ui.Color {
	i := 0
	if dark {
		i = 1
	}
	return ui.Hex(sessionPalette[name][i])
}

var agentColors = map[session.Agent]string{session.Claude: "agent-claude", session.Codex: "agent-codex", session.Factory: "agent-factory"}

func agentColor(id session.Agent, dark bool) ui.Color {
	if name, ok := agentColors[id]; ok {
		return sessionColor(name, dark)
	}
	extra := 0
	for _, info := range session.Agents() {
		if info.ID == id {
			return sessionColor(fmt.Sprintf("agent-extra-%d", extra%3), dark)
		}
		if _, ok := agentColors[info.ID]; !ok {
			extra++
		}
	}
	return sessionColor("agent-other", dark)
}
