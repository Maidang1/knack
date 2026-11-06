package app

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

// Light values are the Pencil variables. Dark values also clear 3:1 against
// the darkest theme's raised surfaces, not just the window background.
var sessionPalette = map[string][2]string{
	"agent-claude":  {"#D97757", "#F29B79"},
	"agent-codex":   {"#3F3F46", "#A8A8B0"},
	"agent-cursor":  {"#7C8597", "#9AA3B5"},
	"agent-factory": {"#C9A227", "#DDBB4A"},
	"agent-gemini":  {"#4C7BE0", "#7EA2F0"},
	"agent-other":   {"#B8B8BE", "#D0D0D6"},
	// Agents without their own color take these in registry order.
	"agent-extra-0": {"#5F8F6A", "#86B892"},
	"agent-extra-1": {"#B0708F", "#D394B2"},
	"agent-extra-2": {"#9A7B45", "#C6A46A"},
	"phase-think":   {"#C9C6F2", "#B3BAF5"},
	"phase-gen":     {"#6159D6", "#8995F5"},
	"phase-tool":    {"#2E9DA3", "#61C1C7"},
	"phase-sub":     {"#B08BDB", "#C1A3EB"},
	"phase-wait":    {"#CFCFD4", "#78787F"},
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
