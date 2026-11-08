package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/session"
	"knack/internal/skill"
)

// agentsPage is the sidebar's one entry for every Agent. It stands for the
// Agent last looked at; the Agents themselves are listed inside the page.
const (
	agentsPage      = "agents"
	agentRailBelow  = 1180 // the Agent list and a split page need the room
	agentListWide   = 232
	agentListNarrow = 184
)

// activeNav reports whether a sidebar entry is the current place.
func (a *App) activeNav(id string) bool {
	if a.page == id {
		return true
	}
	_, onAgent := a.agentPageRoot()
	return id == agentsPage && onAgent
}

// settleAgentsPage turns the Agents entry into a concrete Agent and keeps
// note of the one on show.
func (a *App) settleAgentsPage() {
	if _, ok := a.agentPageRoot(); ok {
		a.lastAgent = a.page
		return
	}
	if a.page != agentsPage {
		return
	}
	logged, rest := a.agentGroups()
	all := append(logged, rest...)
	for _, r := range all {
		if "root:"+r.ID == a.lastAgent {
			a.page = a.lastAgent
			return
		}
	}
	if len(all) > 0 {
		a.page, a.lastAgent = "root:"+all[0].ID, "root:"+all[0].ID
	}
}

// agentSessionCounts counts each Agent's main sessions in the snapshot.
func (a *App) agentSessionCounts() map[session.Agent]int {
	s := &a.sessions
	if s.agentCounts != nil && s.agentCountsOf == s.snapshot {
		return s.agentCounts
	}
	counts := map[session.Agent]int{}
	if s.snapshot != nil {
		for _, sess := range s.snapshot.Sessions {
			if sess.UnsupportedReason == "" {
				counts[sess.Agent]++
			}
		}
	}
	s.agentCounts, s.agentCountsOf = counts, s.snapshot
	return counts
}

// agentGroups splits the Agents into those with sessions on record, most
// sessions first, and those known only by their skills, by name.
func (a *App) agentGroups() (logged, rest []skill.Root) {
	counts := a.agentSessionCounts()
	sessions := func(r skill.Root) int { return counts[session.Agent(agentKey(a.home, r.Path))] }
	for _, r := range a.sidebarAgents() {
		if sessions(r) > 0 {
			logged = append(logged, r)
		} else {
			rest = append(rest, r)
		}
	}
	slices.SortStableFunc(logged, func(x, y skill.Root) int { return sessions(y) - sessions(x) })
	return logged, rest
}

// agentList is the page's own sidebar: every Agent, searchable, the ones in
// use first. It takes the title-bar band beside the page's, and leaves the
// traffic lights clear when it is the leftmost thing in the window.
func (a *App) agentList(c *ui.Context) {
	t := c.Theme()
	muted := token(t, "ink-3")
	bar := c.TitleBar()
	start := float32(14)
	if a.sideCollapsed {
		start = max(start, bar.Left)
	} else if a.rail {
		start = max(start, bar.Left-railWidth+8)
	}
	logged, rest := a.agentGroups()
	query := strings.ToLower(strings.TrimSpace(a.agentQuery))
	match := func(roots []skill.Root) []skill.Root {
		return slices.DeleteFunc(slices.Clone(roots), func(r skill.Root) bool {
			return query != "" && !strings.Contains(strings.ToLower(r.Name), query)
		})
	}
	total := len(logged) + len(rest)
	logged, rest = match(logged), match(rest)
	group := func(title string, roots []skill.Root) {
		if len(roots) == 0 {
			return
		}
		ui.Row(c).Padding(12, 8, 4, 8).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(muted).SingleLine()
			number(c, fmt.Sprint(len(roots))).FontSize(11.5).TextColor(muted)
		})
		for _, r := range roots {
			a.agentItem(c, r)
		}
	}
	ui.Column(c).Width(a.agentListW).Shrink(0).AlignSelf(ui.Stretch).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Height(controlsBandHeight(bar)).Shrink(0).Padding(0, 14, 0, start).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Text(c, "Agents").FontSize(15).FontWeight(600).SingleLine()
			number(c, fmt.Sprint(total)).FontSize(12.5).TextColor(muted)
		})
		ui.Row(c).Padding(0, 10).Shrink(0).Children(func() {
			ui.SearchField(c, &a.agentQuery).Grow(1).MinWidth(0).Height(30).Radius(7).Background(searchBackground(t)).
				Border(1, t.Border).Placeholder("搜索 Agent").PlaceholderColor(muted).Label("搜索 Agent")
		})
		ui.Sidebar(c, &a.page, func() {
			if len(logged)+len(rest) == 0 {
				ui.Text(c, "没有匹配的 Agent").FontSize(12.5).TextColor(muted).Padding(12, 8)
				return
			}
			group("有会话记录", logged)
			group("仅技能", rest)
		}).Grow(1).Padding(0, 10, 12, 10).Background(ui.Transparent).Label("Agent 列表")
	})
}

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
