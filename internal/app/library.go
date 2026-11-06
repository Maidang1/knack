package app

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/skill"
)

// libraryPage is the design's 技能库: every skill on the machine, by the
// source it lives in, with the Agents it reaches.
const libraryPage = "library"

var (
	libraryFilters = []string{"全部", "已挂载", "未挂出", "有问题"}
	librarySorts   = []string{"按名称", "按最近修改"}
)

// libraryEntries are the skills the library lists: each source's own, and
// an Agent's only when the pool has no skill of that name, since the pool's
// row already shows how it reaches the Agents.
func (a *App) libraryEntries() []skill.Entry {
	if a.snap == nil {
		return nil
	}
	var out []skill.Entry
	out = append(out, a.entriesFor("pool")...)
	for _, r := range a.repos {
		out = append(out, a.entriesFor("repo:"+r.Path)...)
	}
	for _, r := range a.roots(skill.RoleAgent) {
		for _, e := range a.entriesFor(r.ID) {
			if _, ok := a.snap.PoolEntry(e.Name); !ok {
				out = append(out, e)
			}
		}
	}
	for _, r := range a.roots(skill.RoleBuiltin) {
		out = append(out, a.entriesFor(r.ID)...)
	}
	return out
}

// librarySection names the group an entry is listed under.
func librarySection(e skill.Entry) string {
	switch {
	case e.System || e.RootRole == skill.RoleBuiltin:
		return "自带 · 只读"
	case e.RootRole == skill.RolePool:
		return "共享池"
	case e.RootRole == skill.RoleRepo:
		return "仓库 · " + e.RootName
	}
	return "Agent · " + e.RootName
}

// libraryReach is the Agents' entries of a skill: the pool's links and
// copies, or the entry itself when it lives in an Agent.
func (a *App) libraryReach(e skill.Entry) []skill.Entry {
	if e.RootRole == skill.RoleAgent || e.RootRole == skill.RoleBuiltin {
		return []skill.Entry{e}
	}
	var out []skill.Entry
	for _, r := range a.roots(skill.RoleAgent) {
		for _, x := range a.entriesFor(r.ID) {
			if x.Name == e.Name {
				out = append(out, x)
				break
			}
		}
	}
	return out
}

// libraryProblem is the worst thing wrong with a skill or its copies, as a
// relation; "" when nothing is.
func (a *App) libraryProblem(e skill.Entry, reach []skill.Entry) skill.Relation {
	worst := skill.Relation("")
	for _, x := range append([]skill.Entry{e}, reach...) {
		switch x.Relation {
		case skill.RelBroken:
			return skill.RelBroken
		case skill.RelStale, skill.RelEmpty:
			worst = cmp.Or(worst, x.Relation)
		}
	}
	return worst
}

func (a *App) libraryRows() []row {
	var groups [4][]row
	counts := [4]int{}
	for _, e := range a.libraryEntries() {
		if !matchQuery(a.query, e.Name, e.Description, e.RootName, e.Path, e.LinkTarget, e.FrontName) {
			continue
		}
		reach := a.libraryReach(e)
		problem := a.libraryProblem(e, reach)
		readOnly := e.System || e.RootRole == skill.RoleBuiltin
		mounted := len(reach) > 0
		in := []bool{true, mounted, !mounted && !readOnly, problem != ""}
		for i, ok := range in {
			if ok {
				counts[i]++
			}
		}
		if !in[max(0, min(a.libFilter, 3))] {
			continue
		}
		groups[0] = append(groups[0], row{
			key:      e.Path,
			name:     e.Name,
			subtitle: oneLine(e.Description),
			entry:    e,
			section:  librarySection(e),
			reach:    reach,
			problem:  problem,
		})
	}
	a.libCounts = counts
	rows := groups[0]
	order := map[string]int{}
	for _, r := range rows {
		if _, ok := order[r.section]; !ok {
			order[r.section] = len(order)
		}
	}
	slices.SortStableFunc(rows, func(x, y row) int {
		if c := cmp.Compare(order[x.section], order[y.section]); c != 0 {
			return c
		}
		if a.libSort == 1 {
			if c := y.entry.EntryModTime.Compare(x.entry.EntryModTime); c != 0 {
				return c
			}
		}
		return strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name))
	})
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].section == rows[i].section {
			j++
		}
		rows[i].count = j - i
		for k := i + 1; k < j; k++ {
			rows[k].section = ""
		}
		i = j
	}
	return rows
}

// libraryHead is the list column's filters.
func (a *App) libraryHead(c *ui.Context) {
	ui.Row(c).Padding(12, 16, 8, 16).Children(func() {
		chipTabs(c, "技能筛选", &a.libFilter, libraryFilters, a.libCounts[:])
	})
}

// librarySortButton orders each source's skills.
func (a *App) librarySortButton(c *ui.Context) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key("lib-sort")).Size(26, 26).Radius(6).Center().Label("排序：" + librarySorts[a.libSort]).Tooltip("排序：" + librarySorts[a.libSort])
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, glyph("sort")).FontSize(15).TextColor(token(t, "ink-2")) })
	b.PopoverMenuButton(func(m *ui.Menu) {
		for i, s := range librarySorts {
			if m.Item(s).Checked(i == a.libSort).Chosen() {
				a.libSort = i
				c.Invalidate()
			}
		}
	})
}

// paintLibraryRow is the design's library row: the name over what
// it does, and at the trailing edge what is wrong and the Agents it reaches.
func (a *App) paintLibraryRow(c *ui.Context, r row, selected bool) {
	t := c.Theme()
	if r.section != "" {
		ui.Row(c).Gap(6).Padding(14, 10, 4, 10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, r.section).FontSize(11.5).FontWeight(600).LetterSpacing(0.3).TextColor(token(t, "ink-3")).SingleLine()
			number(c, fmt.Sprint(r.count)).FontSize(11).TextColor(token(t, "ink-3"))
		})
	}
	selectedRow(c, selected).Padding(8, 10).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, r.name).FontSize(13).FontWeight(600).SingleLine()
			if r.subtitle != "" {
				ui.Text(c, r.subtitle).FontSize(12).TextColor(t.TextMuted).SingleLine()
			}
		})
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if r.problem != "" {
				tone := t.Warning
				if r.problem == skill.RelBroken {
					tone = t.Danger
				}
				ui.Icon(c, glyph(relationGlyph(r.problem))).FontSize(13).TextColor(tone).Tooltip(r.problem.Label())
			}
			if len(r.reach) > 0 {
				names := make([]string, 0, len(r.reach))
				for _, x := range r.reach {
					names = append(names, x.RootName+" · "+x.Relation.Label())
				}
				ui.Row(c).Gap(3).AlignItems(ui.Center).Tooltip(strings.Join(names, "\n")).Children(func() {
					for _, x := range r.reach {
						ui.Box(c).Size(7, 7).Radius(4).Background(a.entryAgentColor(c, x))
					}
				})
			}
		})
	})
}

// entryAgentColor is the data color of the Agent an entry lives in.
func (a *App) entryAgentColor(c *ui.Context, e skill.Entry) ui.Color {
	for _, r := range a.snap.Roots {
		if r.ID == e.RootID {
			return a.rootColor(c, r)
		}
	}
	return token(c.Theme(), "ink-3")
}

// sessionSearchLink offers the sessions a skill search also finds, so the
// sidebar's one field searches both.
func (a *App) sessionSearchLink(c *ui.Context) {
	query := strings.TrimSpace(a.query)
	if query == "" || a.page == "issues" || a.hidden["sessions"] {
		return
	}
	n := a.sessionMatches(query)
	if n == 0 {
		return
	}
	t := c.Theme()
	label := fmt.Sprintf("在会话中搜索“%s” · %d 个", query, n)
	b := ui.ButtonBase(c.Key("search-sessions")).Margin(0, 16, 6, 16).Padding(5, 8).Gap(6).Radius(6).AlignItems(ui.Center).Label(label)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Icon(c, glyph("messages")).FontSize(13).TextColor(token(t, "ink-2")).Shrink(0)
		ui.Text(c, "在会话中搜索").FontSize(13).TextColor(token(t, "ink-2")).Shrink(0)
		ui.Text(c, "“"+query+"”").FontSize(13).Shrink(1).MinWidth(0).SingleLine()
		ui.Spacer(c)
		number(c, fmt.Sprint(n)).FontSize(11.5).TextColor(token(t, "ink-3")).Shrink(0)
		ui.Icon(c, glyph("chevron-right")).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
	})
	if b.Clicked() {
		a.searchSessions(query)
		c.Invalidate()
	}
}
