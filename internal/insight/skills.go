package insight

import (
	"cmp"
	"slices"
	"strings"
	"time"
	"unicode"

	"knack/internal/session"
	"knack/internal/skill"
)

// SkillHealth is one skill an Agent loads: what its description costs in
// every session's context, how often it was called, and what to fix.
type SkillHealth struct {
	Agent        session.Agent
	RootID       string
	RootName     string
	Name, Path   string
	Description  string
	System       bool
	DescTokens   int // estimated tokens of the name and description in the skill list
	UsageKnown   bool
	Calls30      int
	Calls90      int
	LastUsed     time.Time
	Findings     []Finding
	OverlapsWith []string
}

// Finding is one problem with a skill's front matter, worst first.
type Finding struct {
	Severity int // 2 error, 1 warning, 0 suggestion
	Text     string
}

// Overlap is two skills of one Agent whose descriptions share much of their
// vocabulary, so the model may pick either for the same request.
type Overlap struct {
	RootName string
	A, B     string
	Score    float64
	Shared   []string
}

type SkillRootSummary struct {
	RootID, RootName string
	Agent            session.Agent
	Skills           int
	StandingTokens   int // estimated tokens every session spends listing them
	Unused           int // usable, user-installed and not called in 90 days
	UsageKnown       bool
}

type SkillReport struct {
	Skills   []SkillHealth // grouped by root, then by name
	Roots    []SkillRootSummary
	Overlaps []Overlap
}

// Unused reports a user-installed skill that was never called in 90 days,
// when the Agent's logs record Skill calls at all.
func (h SkillHealth) Unused() bool { return h.UsageKnown && !h.System && h.Calls90 == 0 }

// OverlapThreshold is the Jaccard similarity of two descriptions' terms
// above which two skills are reported as competing.
const OverlapThreshold = 0.45

// AnalyzeSkills reads the skills each Agent loads. agentOf maps a skill
// root to the Agent whose session logs record its calls; usageKnown says
// whether that Agent's logs record Skill calls.
func AnalyzeSkills(skills *skill.Snapshot, sessions *session.Snapshot, agentOf func(skill.Root) session.Agent, usageKnown func(session.Agent) bool, now time.Time) SkillReport {
	var out SkillReport
	if skills == nil {
		return out
	}
	calls := skillCalls(sessions)
	for _, root := range skills.Roots {
		if root.Role != skill.RoleAgent && root.Role != skill.RoleBuiltin {
			continue
		}
		agent := agentOf(root)
		known := usageKnown(agent)
		sum := SkillRootSummary{RootID: root.ID, RootName: root.Name, Agent: agent, UsageKnown: known}
		var group []SkillHealth
		for _, e := range skills.EntriesFor(root.ID) {
			if e.Kind != skill.KindSkill && e.Kind != skill.KindLink && e.Kind != skill.KindBroken {
				continue
			}
			h := SkillHealth{Agent: agent, RootID: root.ID, RootName: root.Name, Name: e.Name, Path: e.Path, Description: e.Description,
				System: e.System || root.Role == skill.RoleBuiltin, UsageKnown: known}
			h.DescTokens = EstimateTokens(cmp.Or(e.FrontName, e.Name) + ": " + e.Description)
			for _, name := range uniqueNames(e) {
				for _, at := range calls[agent][name] {
					if !at.Before(now.AddDate(0, 0, -90)) {
						h.Calls90++
						if !at.Before(now.AddDate(0, 0, -30)) {
							h.Calls30++
						}
					}
					if at.After(h.LastUsed) {
						h.LastUsed = at
					}
				}
			}
			h.Findings = lint(e)
			sum.Skills++
			if e.Usable() {
				sum.StandingTokens += h.DescTokens
			}
			if h.Unused() && e.Usable() {
				sum.Unused++
			}
			group = append(group, h)
		}
		if len(group) == 0 {
			continue
		}
		overlaps := findOverlaps(root.Name, group)
		for _, o := range overlaps {
			for i := range group {
				switch group[i].Name {
				case o.A:
					group[i].OverlapsWith = append(group[i].OverlapsWith, o.B)
				case o.B:
					group[i].OverlapsWith = append(group[i].OverlapsWith, o.A)
				}
			}
		}
		out.Overlaps = append(out.Overlaps, overlaps...)
		slices.SortFunc(group, func(x, y SkillHealth) int { return strings.Compare(x.Name, y.Name) })
		out.Skills = append(out.Skills, group...)
		out.Roots = append(out.Roots, sum)
	}
	slices.SortStableFunc(out.Overlaps, func(x, y Overlap) int { return cmp.Compare(y.Score, x.Score) })
	return out
}

func uniqueNames(e skill.Entry) []string {
	if e.FrontName != "" && e.FrontName != e.Name {
		return []string{e.Name, e.FrontName}
	}
	return []string{e.Name}
}

// skillCalls collects Skill call times by Agent and skill name, including
// sub-Agents' calls.
func skillCalls(snap *session.Snapshot) map[session.Agent]map[string][]time.Time {
	out := map[session.Agent]map[string][]time.Time{}
	if snap == nil {
		return out
	}
	var visit func(session.Session)
	visit = func(s session.Session) {
		for _, t := range s.Turns {
			for _, sk := range t.Skills {
				if out[s.Agent] == nil {
					out[s.Agent] = map[string][]time.Time{}
				}
				out[s.Agent][sk.Name] = append(out[s.Agent][sk.Name], sk.At)
				// Plugin skills are called as plugin:name.
				if _, name, ok := strings.Cut(sk.Name, ":"); ok && name != "" {
					out[s.Agent][name] = append(out[s.Agent][name], sk.At)
				}
			}
			for _, sub := range t.Subagents {
				visit(sub.Session)
			}
		}
	}
	for _, s := range snap.Sessions {
		visit(s)
	}
	for _, s := range snap.UnlinkedSubagents {
		visit(s)
	}
	return out
}

// Claude Code truncates longer descriptions in the skill list.
const maxDescription = 1024

func lint(e skill.Entry) []Finding {
	var out []Finding
	add := func(severity int, text string) { out = append(out, Finding{severity, text}) }
	if e.Kind == skill.KindBroken {
		add(2, "软链目标不存在，Agent 无法加载")
	}
	if e.MetaErr != "" {
		add(2, "front matter 无法解析："+e.MetaErr)
	}
	if !e.HasDocument && e.Kind != skill.KindBroken {
		add(2, "缺少 SKILL.md")
	}
	desc := strings.TrimSpace(e.Description)
	n := len([]rune(desc))
	switch {
	case e.HasDocument && desc == "":
		add(2, "缺少 description，Agent 无从判断何时调用")
	case n > maxDescription:
		add(1, "description 超过 1024 个字符，可能被截断")
	case n > 0 && n < 40:
		add(1, "description 过短，难以准确触发")
	}
	if e.FrontName != "" && e.FrontName != e.Name {
		add(1, "front matter 的 name（"+e.FrontName+"）与目录名不同")
	}
	if desc != "" && !hasTriggerCue(desc) {
		add(0, "description 没有说明何时使用（如 “Use when…” 或 “当…时”）")
	}
	slices.SortStableFunc(out, func(x, y Finding) int { return y.Severity - x.Severity })
	return out
}

var triggerCues = []string{"use when", "use this", "use for", "when the user", "when you", "when asked", "triggers", "trigger", "invoke", "used when", "use it",
	"当", "用于", "适用", "触发", "需要", "时使用", "场景"}

func hasTriggerCue(desc string) bool {
	lower := strings.ToLower(desc)
	for _, cue := range triggerCues {
		if strings.Contains(lower, cue) {
			return true
		}
	}
	return false
}

// EstimateTokens approximates a tokenizer: CJK characters about one token
// each, other text about four characters a token.
func EstimateTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "use": true, "when": true, "with": true, "this": true, "that": true, "from": true, "into": true,
	"user": true, "users": true, "asks": true, "ask": true, "you": true, "your": true, "are": true, "any": true, "not": true, "all": true,
	"skill": true, "skills": true, "using": true, "used": true, "also": true, "such": true, "like": true, "via": true, "its": true, "can": true,
	"should": true, "will": true, "has": true, "have": true, "about": true, "other": true, "more": true, "than": true, "only": true, "each": true,
	"triggers": true, "trigger": true, "mentions": true, "wants": true, "need": true, "needs": true, "e.g": true, "etc": true,
}

// terms are a description's lowercase words of three or more letters and
// its CJK character pairs, without the words every description shares.
func terms(text string) map[string]bool {
	out := map[string]bool{}
	var word []rune
	var prev rune
	flush := func() {
		if len(word) >= 3 {
			w := string(word)
			if !stopWords[w] {
				out[w] = true
			}
		}
		word = word[:0]
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.Is(unicode.Han, r):
			flush()
			if prev != 0 {
				out[string([]rune{prev, r})] = true
			}
			prev = r
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			word = append(word, r)
		default:
			flush()
		}
		prev = 0
	}
	flush()
	return out
}

func findOverlaps(rootName string, group []SkillHealth) []Overlap {
	sets := make([]map[string]bool, len(group))
	for i, h := range group {
		sets[i] = terms(h.Description)
	}
	var out []Overlap
	for i := range group {
		for j := i + 1; j < len(group); j++ {
			a, b := sets[i], sets[j]
			if len(a) < 4 || len(b) < 4 {
				continue
			}
			var shared []string
			for w := range a {
				if b[w] {
					shared = append(shared, w)
				}
			}
			score := float64(len(shared)) / float64(len(a)+len(b)-len(shared))
			if score < OverlapThreshold {
				continue
			}
			slices.Sort(shared)
			x, y := group[i].Name, group[j].Name
			if y < x {
				x, y = y, x
			}
			out = append(out, Overlap{RootName: rootName, A: x, B: y, Score: score, Shared: shared[:min(len(shared), 8)]})
		}
	}
	return out
}
