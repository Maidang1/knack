package session

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Filter is a half-open range [From, To). Tokens belong to request completion
// dates; session counts use session-range overlap. Zero bounds mean all time.
type Filter struct {
	From, To time.Time
	Location *time.Location
	Agent    Agent
}

func (f Filter) includes(at time.Time) bool {
	if at.IsZero() {
		return f.From.IsZero() && f.To.IsZero()
	}
	return (f.From.IsZero() || !at.Before(f.From)) && (f.To.IsZero() || at.Before(f.To))
}

func (f Filter) overlaps(s Session) bool {
	if s.Start.IsZero() || s.End.IsZero() {
		return f.From.IsZero() && f.To.IsZero()
	}
	return (f.From.IsZero() || !s.End.Before(f.From)) && (f.To.IsZero() || s.Start.Before(f.To))
}

type RateStats struct {
	Median, P90 float64
	Samples     int
	Estimated   bool
}

type Group struct {
	Name                                  string
	Sessions, Requests, UnmeteredRequests int
	Tokens                                Tokens
	OutputRate                            RateStats
	CacheHit                              float64
	CacheHitKnown                         bool
}

type Daily struct {
	Date     string
	Tokens   Tokens
	Requests int
}

type SkillUsage struct {
	Name            string
	Calls, Sessions int
}

type Summary struct {
	Sessions, Subagents, Requests, UnmeteredRequests int
	Tokens                                           Tokens
	OutputRate                                       RateStats
	CacheHit                                         float64
	CacheHitKnown                                    bool
	ByAgent, ByModel                                 []Group
	Daily                                            []Daily
	Skills                                           []SkillUsage
	Incomplete                                       bool
}

type groupState struct {
	group    Group
	rates    []float64
	sessions map[string]bool
}

// Aggregate counts children once, including children whose tool/turn linkage is
// unknown. Child work contributes usage, but never inflates top-level sessions.
func Aggregate(snapshot *Snapshot, filter Filter) Summary {
	var out Summary
	if snapshot == nil {
		return out
	}
	out.Incomplete = snapshot.Partial || len(snapshot.Warnings) > 0
	loc := filter.Location
	if loc == nil {
		loc = time.Local
	}
	agents, models := map[string]*groupState{}, map[string]*groupState{}
	days := map[string]*Daily{}
	skills := map[string]*SkillUsage{}
	skillSessions := map[string]map[string]bool{}
	var rates []float64
	group := func(groups map[string]*groupState, key string) *groupState {
		if key == "" {
			key = "未知模型"
		}
		g := groups[key]
		if g == nil {
			g = &groupState{group: Group{Name: key}, sessions: map[string]bool{}}
			groups[key] = g
		}
		return g
	}
	var visit func(Session, bool, string)
	visit = func(s Session, child bool, owner string) {
		if filter.Agent != "" && filter.Agent != s.Agent {
			return
		}
		if s.UnsupportedReason != "" {
			out.Incomplete = true
			return
		}
		key := string(s.Agent) + ":" + s.ID
		if owner == "" {
			owner = key
		}
		var ag *groupState
		if filter.overlaps(s) {
			ag = group(agents, string(s.Agent))
			if child {
				out.Subagents++
			} else {
				out.Sessions++
				ag.sessions[key] = true
			}
			out.Incomplete = out.Incomplete || s.Incomplete
		}
		for _, t := range s.Turns {
			for _, r := range t.Requests {
				if !filter.includes(r.End) {
					continue
				}
				mg := group(models, r.Model)
				if ag == nil {
					ag = group(agents, string(s.Agent))
				}
				mg.sessions[owner] = true
				out.Requests++
				ag.group.Requests++
				mg.group.Requests++
				if !r.UsageKnown {
					out.UnmeteredRequests++
					ag.group.UnmeteredRequests++
					mg.group.UnmeteredRequests++
				}
				out.Tokens.add(r.Tokens)
				ag.group.Tokens.add(r.Tokens)
				mg.group.Tokens.add(r.Tokens)
				if rate, ok := r.OutputRate(); ok {
					rates = append(rates, rate)
					ag.rates = append(ag.rates, rate)
					mg.rates = append(mg.rates, rate)
				}
				if !r.End.IsZero() {
					date := r.End.In(loc).Format("2006-01-02")
					day := days[date]
					if day == nil {
						day = &Daily{Date: date}
						days[date] = day
					}
					day.Tokens.add(r.Tokens)
					day.Requests++
				}
			}
			for _, sk := range t.Skills {
				if !filter.includes(sk.At) {
					continue
				}
				usage := skills[sk.Name]
				if usage == nil {
					usage = &SkillUsage{Name: sk.Name}
					skills[sk.Name] = usage
					skillSessions[sk.Name] = map[string]bool{}
				}
				usage.Calls++
				skillSessions[sk.Name][owner] = true
			}
			for _, sub := range t.Subagents {
				visit(sub.Session, true, owner)
			}
		}
	}
	for _, s := range snapshot.Sessions {
		visit(s, false, "")
	}
	for _, s := range snapshot.UnlinkedSubagents {
		owner := string(s.Agent) + ":" + s.ParentID
		visit(s, true, owner)
	}
	out.OutputRate = rateStats(rates)
	out.Incomplete = out.Incomplete || out.UnmeteredRequests > 0
	out.CacheHit, out.CacheHitKnown = cacheHit(out.Tokens)
	finish := func(groups map[string]*groupState) []Group {
		var result []Group
		for _, g := range groups {
			g.group.Sessions = len(g.sessions)
			g.group.OutputRate = rateStats(g.rates)
			g.group.CacheHit, g.group.CacheHitKnown = cacheHit(g.group.Tokens)
			result = append(result, g.group)
		}
		slices.SortFunc(result, func(a, b Group) int {
			if a.Tokens.Total() != b.Tokens.Total() {
				if a.Tokens.Total() > b.Tokens.Total() {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Name, b.Name)
		})
		return result
	}
	out.ByAgent = finish(agents)
	out.ByModel = finish(models)
	for _, d := range days {
		out.Daily = append(out.Daily, *d)
	}
	slices.SortFunc(out.Daily, func(a, b Daily) int { return strings.Compare(a.Date, b.Date) })
	for name, sk := range skills {
		sk.Sessions = len(skillSessions[name])
		out.Skills = append(out.Skills, *sk)
	}
	slices.SortFunc(out.Skills, func(a, b SkillUsage) int {
		if a.Calls != b.Calls {
			return b.Calls - a.Calls
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

func rateStats(values []float64) RateStats {
	if len(values) == 0 {
		return RateStats{}
	}
	slices.Sort(values)
	n := len(values)
	median := values[n/2]
	if n%2 == 0 {
		median = (values[n/2-1] + median) / 2
	}
	return RateStats{Median: median, P90: values[int(math.Ceil(float64(n)*.9))-1], Samples: n, Estimated: true}
}

func cacheHit(tokens Tokens) (float64, bool) {
	if tokens.InputTotal() == 0 {
		return 0, false
	}
	return float64(tokens.CacheRead) / float64(tokens.InputTotal()), true
}

// FormatDuration is shared by overview and the later session/detail pages.
func FormatDuration(d time.Duration) string {
	seconds := int64(max(0, d) / time.Second)
	hours, minutes := seconds/3600, (seconds%3600)/60
	if hours > 0 {
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		if seconds%60 == 0 {
			return fmt.Sprintf("%dm", minutes)
		}
		return fmt.Sprintf("%dm%02ds", minutes, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}
