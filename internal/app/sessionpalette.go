package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Light values are the Pencil variables. Dark values also clear 3:1 against
// the darkest theme's raised surfaces, not just the window background.
var sessionPalette = map[string][2]string{
	"agent-claude": {"#D97757", "#F29B79"},
	"agent-codex":  {"#4A4A50", "#A8A8B0"},
	"agent-other":  {"#B8B8BE", "#D0D0D6"},
	// Agents without their own color take these in registry order.
	"agent-extra-0": {"#5F8F6A", "#86B892"},
	"agent-extra-1": {"#B0708F", "#D394B2"},
	"agent-extra-2": {"#9A7B45", "#C6A46A"},
	"phase-think":   {"#C5CAF3", "#B3BAF5"},
	"phase-gen":     {"#5E6AD2", "#8995F5"},
	"phase-tool":    {"#2E9DA3", "#61C1C7"},
	"phase-sub":     {"#A98BDB", "#C1A3EB"},
	"phase-wait":    {"#C4C4CA", "#B9B9C2"},
	"phase-retry":   {"#E0A23B", "#F2BD66"},
	"data-neutral":  {"#8E8E93", "#B0B0B8"},
}

func sessionColor(name string, dark bool) ui.Color {
	i := 0
	if dark {
		i = 1
	}
	return ui.Hex(sessionPalette[name][i])
}

// Factory keeps the neutral "other" color: its logs have no usage, so it never
// shares a chart with the "其他" series.
var agentColors = map[session.Agent]string{session.Claude: "agent-claude", session.Codex: "agent-codex", session.Factory: "agent-other"}

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
