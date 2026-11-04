package skill

import (
	"path/filepath"
	"slices"
	"strings"
)

func analyze(s *Snapshot) {
	s.Issues = nil
	s.PoolIndex = map[string]int{}
	for i, e := range s.Entries {
		if e.RootRole == RolePool {
			if _, ok := s.PoolIndex[e.Name]; !ok {
				s.PoolIndex[e.Name] = i
			}
		}
	}
	s.Linked = map[string][]string{}
	agentNames := map[string]bool{}
	for i := range s.Entries {
		e := &s.Entries[i]
		pool, hasPool := s.PoolEntry(e.Name)
		switch {
		case e.RootRole == RolePool:
			e.Relation = poolRelation(*e)
		case e.System || e.RootRole == RoleBuiltin:
			e.Relation = systemRelation(*e)
		default:
			e.Relation = relate(*e, pool, hasPool)
		}
		if e.RootRole == RoleAgent && !e.System {
			agentNames[e.Name] = true
		}
		if e.Relation == RelLinked {
			s.Linked[e.Name] = append(s.Linked[e.Name], e.RootName)
		}
	}
	for name := range s.Linked {
		slices.Sort(s.Linked[name])
		s.Linked[name] = unique(s.Linked[name])
	}

	for _, e := range s.Entries {
		if e.RootRole == RoleRepo || e.RootRole == RoleBuiltin || e.System {
			continue
		}
		switch e.Relation {
		case RelBroken:
			s.Issues = append(s.Issues, Issue{Kind: IssueBroken, Entry: e, Detail: brokenDetail(e)})
		case RelEmpty:
			s.Issues = append(s.Issues, Issue{Kind: IssueEmpty, Entry: e, Detail: emptyDetail(e)})
		case RelStale:
			if e.RootRole == RoleAgent {
				s.Issues = append(s.Issues, Issue{Kind: IssueStale, Entry: e, Detail: "和共享池同名，目录内容不一致。改池子不会改到这一份。"})
			}
		case RelSame:
			if e.RootRole == RoleAgent {
				s.Issues = append(s.Issues, Issue{Kind: IssueDuplicate, Entry: e, Detail: "和共享池内容一致，但是一份实体副本。之后改池子，这里不会跟着变。"})
			}
		}
	}
	for name, idx := range s.PoolIndex {
		e := s.Entries[idx]
		if !e.Usable() || agentNames[name] {
			continue
		}
		s.Issues = append(s.Issues, Issue{
			Kind:   IssueOrphan,
			Entry:  e,
			Detail: "没有任何 agent 目录软链或复制它。Grok 直接读共享池，所以仍然能看见。",
		})
	}
	slices.SortFunc(s.Issues, func(a, b Issue) int {
		if c := issueRank(a.Kind) - issueRank(b.Kind); c != 0 {
			return c
		}
		if c := strings.Compare(a.Entry.RootName, b.Entry.RootName); c != 0 {
			return c
		}
		return strings.Compare(a.Entry.Name, b.Entry.Name)
	})
	s.Summaries = summaries(s)
	s.Graphs = map[string]*Graph{}
	for _, root := range s.Roots {
		s.Graphs[root.ID] = BuildGraph(s.EntriesFor(root.ID))
	}
}

func poolRelation(e Entry) Relation {
	switch e.Kind {
	case KindBroken:
		return RelBroken
	case KindEmpty:
		return RelEmpty
	case KindSkill, KindLink:
		return RelPool
	default:
		return RelUnknown
	}
}

func systemRelation(e Entry) Relation {
	switch e.Kind {
	case KindBroken:
		return RelBroken
	case KindEmpty:
		return RelEmpty
	default:
		return RelSystem
	}
}

func relate(e Entry, pool Entry, hasPool bool) Relation {
	switch e.Kind {
	case KindBroken:
		return RelBroken
	case KindEmpty:
		return RelEmpty
	case KindLink:
		if hasPool && pointsAtPool(e, pool) {
			return RelLinked
		}
		return RelExternal
	case KindSkill:
		if !hasPool || !pool.Usable() {
			return RelLocal
		}
		if e.TreeHash == "" || pool.TreeHash == "" {
			return RelCopy
		}
		if e.TreeHash == pool.TreeHash {
			return RelSame
		}
		return RelStale
	default:
		return RelUnknown
	}
}

func pointsAtPool(e, pool Entry) bool {
	if e.LinkAbs == "" || pool.Path == "" {
		return false
	}
	return filepath.Clean(e.LinkAbs) == filepath.Clean(pool.Path)
}

func brokenDetail(e Entry) string {
	if e.LinkTarget == "" {
		return "软链读不出来。"
	}
	return "软链指向已经不存在的路径：" + e.LinkTarget
}

func emptyDetail(e Entry) string {
	if e.Vacant {
		return "目录是空的，没有 SKILL.md。"
	}
	if len(e.Children) > 0 {
		return "自己没有 SKILL.md，下面有子 skill：" + strings.Join(e.Children, "、")
	}
	return "目录里没有 SKILL.md。"
}

func issueRank(k IssueKind) int {
	switch k {
	case IssueBroken:
		return 0
	case IssueStale:
		return 1
	case IssueDuplicate:
		return 2
	case IssueEmpty:
		return 3
	case IssueOrphan:
		return 4
	default:
		return 5
	}
}

func unique(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func summaries(s *Snapshot) []Summary {
	byID := map[string]*Summary{}
	var order []string
	for _, root := range s.Roots {
		sm := Summary{Root: root}
		byID[root.ID] = &sm
		order = append(order, root.ID)
	}
	for _, e := range s.Entries {
		sm := byID[e.RootID]
		if sm == nil {
			continue
		}
		sm.Total++
		switch e.Relation {
		case RelLinked:
			sm.Linked++
		case RelCopy:
			sm.Copy++
		case RelSame:
			sm.Same++
		case RelStale:
			sm.Stale++
		case RelBroken:
			sm.Broken++
		case RelEmpty:
			sm.Empty++
		case RelLocal:
			sm.Local++
		case RelExternal:
			sm.External++
		case RelSystem:
			sm.System++
		}
	}
	for _, issue := range s.Issues {
		if sm := byID[issue.Entry.RootID]; sm != nil {
			sm.Issues++
		}
	}
	out := make([]Summary, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// Label is the short Chinese name of a relation.
func (r Relation) Label() string {
	switch r {
	case RelPool:
		return "共享池"
	case RelLinked:
		return "软链"
	case RelCopy:
		return "副本待比较"
	case RelSame:
		return "一致副本"
	case RelStale:
		return "不一致"
	case RelLocal:
		return "独有"
	case RelExternal:
		return "外部软链"
	case RelBroken:
		return "断链"
	case RelEmpty:
		return "无 SKILL.md"
	case RelSystem:
		return "自带"
	default:
		return "未知"
	}
}

// Tone is a hint for how loudly to draw the relation.
func (r Relation) Tone() string {
	switch r {
	case RelBroken, RelStale:
		return "danger"
	case RelCopy, RelSame, RelEmpty, RelExternal:
		return "warning"
	case RelLocal:
		return "ok"
	default:
		return "muted"
	}
}

// Label is the short Chinese name of an issue kind.
func (k IssueKind) Label() string {
	switch k {
	case IssueBroken:
		return "断链"
	case IssueStale:
		return "不一致"
	case IssueDuplicate:
		return "一致副本"
	case IssueEmpty:
		return "无 SKILL.md"
	case IssueOrphan:
		return "未挂出"
	default:
		return string(k)
	}
}
