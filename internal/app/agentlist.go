package app

import (
	"slices"
	"strings"

	"knack/internal/skill"
)

// Keep directory identities intact for management. Only navigation and list
// composition change; every bundled entry retains its read-only source role.
func (a *App) sidebarAgents() []skill.Root {
	agents := a.roots(skill.RoleAgent)
	keys := map[string]bool{}
	for _, r := range agents {
		keys[agentKey(a.home, r.Path)] = true
	}
	for _, r := range a.roots(skill.RoleBuiltin) {
		key := agentKey(a.home, r.Path)
		if keys[key] {
			continue
		}
		keys[key] = true
		r.Name = strings.TrimSuffix(r.Name, " 自带")
		agents = append(agents, r)
	}
	slices.SortFunc(agents, func(a, b skill.Root) int { return strings.Compare(a.Name, b.Name) })
	return agents
}

func (a *App) agentEntries(root skill.Root) []skill.Entry {
	entries := slices.Clone(a.entriesFor(root.ID))
	if root.Role == skill.RoleAgent || root.Role == skill.RoleBuiltin {
		key := agentKey(a.home, root.Path)
		for _, r := range a.roots(skill.RoleBuiltin) {
			if r.ID != root.ID && agentKey(a.home, r.Path) == key {
				entries = append(entries, a.entriesFor(r.ID)...)
			}
		}
	}
	slices.SortStableFunc(entries, func(a, b skill.Entry) int {
		ar, br := a.System || a.RootRole == skill.RoleBuiltin, b.System || b.RootRole == skill.RoleBuiltin
		if ar != br {
			if ar {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return entries
}
