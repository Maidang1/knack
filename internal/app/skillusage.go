package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
	"knack/internal/skill"
)

type activityRecord struct {
	session session.Session
	child   bool
}
type skillActivity struct {
	at           time.Time
	owner, label string
	child        bool
}
type skillSessionUse struct {
	id, label string
	calls     int
	last      time.Time
}
type skillUsageData struct {
	calls, main, child int
	last               time.Time
	sessions           []skillSessionUse
	days               map[string]int
}
type sessionActivity struct {
	snapshot *session.Snapshot
	records  []activityRecord
	skills   map[string][]skillActivity
	usage    map[string]*skillUsageData
}

func (a *App) sessionActivity() *sessionActivity {
	if a.activity != nil && a.activity.snapshot == a.sessions.snapshot {
		return a.activity
	}
	d := &sessionActivity{snapshot: a.sessions.snapshot, skills: map[string][]skillActivity{}, usage: map[string]*skillUsageData{}}
	a.activity = d
	if d.snapshot == nil {
		return d
	}
	roots := map[string]session.Session{}
	parents := map[string]string{}
	var index func(session.Session, string)
	index = func(s session.Session, owner string) {
		key := sessionID(s)
		if _, ok := parents[key]; ok {
			return
		}
		parents[key] = owner
		for _, t := range s.Turns {
			for _, sub := range t.Subagents {
				index(sub.Session, owner)
			}
		}
	}
	for _, s := range d.snapshot.Sessions {
		roots[sessionID(s)] = s
		index(s, sessionID(s))
	}
	for _, s := range d.snapshot.UnlinkedSubagents {
		if _, ok := parents[sessionID(s)]; !ok {
			index(s, string(s.Agent)+":"+s.ParentID)
		}
	}
	ownerFor := func(key string) string {
		seen := map[string]bool{}
		for key != "" && !seen[key] {
			if _, ok := roots[key]; ok {
				return key
			}
			seen[key] = true
			key = parents[key]
		}
		return ""
	}
	seen := map[string]bool{}
	var visit func(session.Session, bool)
	visit = func(s session.Session, child bool) {
		if s.UnsupportedReason != "" {
			return
		}
		key := sessionID(s)
		if seen[key] {
			return
		}
		seen[key] = true
		d.records = append(d.records, activityRecord{session: s, child: child})
		owner := ownerFor(key)
		label := recentLabel(s) + " · 父会话日志缺失"
		if root, ok := roots[owner]; ok {
			label = recentLabel(root)
		}
		for _, t := range s.Turns {
			for _, sk := range t.Skills {
				call := skillActivity{at: sk.At, owner: owner, label: label, child: child}
				d.skills[string(s.Agent)+"\x00"+sk.Name] = append(d.skills[string(s.Agent)+"\x00"+sk.Name], call)
				d.skills["\x00"+sk.Name] = append(d.skills["\x00"+sk.Name], call)
			}
			for _, sub := range t.Subagents {
				visit(sub.Session, true)
			}
		}
	}
	for _, s := range d.snapshot.Sessions {
		visit(s, false)
	}
	for _, s := range d.snapshot.UnlinkedSubagents {
		visit(s, true)
	}
	return d
}

func (d *sessionActivity) skillUsage(agent session.Agent, names []string, f session.Filter) *skillUsageData {
	key := string(agent) + "|" + strings.Join(names, "\x00") + "|" + f.From.String() + "|" + f.To.String()
	if cached := d.usage[key]; cached != nil {
		return cached
	}
	result := &skillUsageData{days: map[string]int{}}
	sessions := map[string]*skillSessionUse{}
	for _, name := range names {
		for _, call := range d.skills[string(agent)+"\x00"+name] {
			if call.at.After(result.last) {
				result.last = call.at
			}
			if !filterTime(f, call.at) {
				continue
			}
			result.calls++
			if call.child {
				result.child++
			} else {
				result.main++
			}
			result.days[call.at.Local().Format("2006-01-02")]++
			sessionKey := call.owner
			if sessionKey == "" {
				sessionKey = call.label
			}
			item := sessions[sessionKey]
			if item == nil {
				item = &skillSessionUse{id: call.owner, label: call.label}
				sessions[sessionKey] = item
			}
			item.calls++
			if call.at.After(item.last) {
				item.last = call.at
			}
		}
	}
	for _, item := range sessions {
		result.sessions = append(result.sessions, *item)
	}
	slices.SortFunc(result.sessions, func(a, b skillSessionUse) int {
		if x := b.last.Compare(a.last); x != 0 {
			return x
		}
		return strings.Compare(a.label, b.label)
	})
	d.usage[key] = result
	return result
}

func (a *App) skillUsage(c *ui.Context, e skill.Entry) {
	t := c.Theme()
	agent := session.Agent("")
	if e.RootRole == skill.RoleAgent || e.RootRole == skill.RoleBuiltin {
		agent = session.Agent(agentKey(a.home, e.RootPath))
	}
	ui.Column(c.Key("skill-usage:" + e.Path)).Gap(10).Label("技能使用情况").Children(func() {
		muted := token(t, "ink-3")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "使用情况").FontSize(15).FontWeight(600).SingleLine().Shrink(0)
			ui.Spacer(c)
			note := "按日志中的 Skill 调用名匹配，未记录实际加载路径；不能据此判断同名副本是否被使用。"
			if agent == "" {
				note += "跨 Agent 汇总已识别调用；Codex、Cursor 等缺少调用记录的来源不计为零。"
			}
			ui.Text(c, "按调用名统计，同名副本合并计数").FontSize(12).TextColor(muted).Shrink(1).MinWidth(0).SingleLine().Tooltip(note)
			segmented(c, "技能使用时间范围", &a.skillPeriod, "7 天", "30 天", "90 天")
		})
		if agent != "" {
			if reason := skillSupport(agent); reason != "" {
				ui.Text(c, reason).FontSize(13).TextColor(t.TextMuted)
				return
			}
		}
		days := []int{7, 30, 90}[max(0, min(a.skillPeriod, 2))]
		usage := a.sessionActivity().skillUsage(agent, skillNames(e), skillRange(days, time.Now()))
		last := "—"
		if !usage.last.IsZero() {
			last = ago(time.Now(), usage.last)
		}
		summary := fmt.Sprintf("近 %d 天调用 %d 次 · %d 个会话 · 主 Agent %d 次 · 子 Agent %d 次", days, usage.calls, len(usage.sessions), usage.main, usage.child)
		ui.Row(c).Gap(28).AlignItems(ui.End).Label(summary).Children(func() {
			stat := func(value, label, tip string) {
				ui.Column(c).Gap(3).Shrink(0).Tooltip(tip).Children(func() {
					if strings.ContainsFunc(value, func(r rune) bool { return r > 0x2e80 }) {
						ui.Text(c, value).FontSize(16).FontWeight(600).Height(24).AlignContent(ui.End)
					} else {
						number(c, value).FontSize(20).FontWeight(600)
					}
					ui.Text(c, label).FontSize(12).TextColor(t.TextMuted)
				})
			}
			stat(fmt.Sprint(usage.calls), "次调用", fmt.Sprintf("主 Agent %d 次 · 子 Agent %d 次", usage.main, usage.child))
			stat(fmt.Sprint(len(usage.sessions)), "个会话", "")
			stat(last, "最后使用", usage.last.Local().Format("2006/01/02 15:04"))
			ui.Column(c).Grow(1).MinWidth(80).Children(func() { skillDailyChart(c, usage, days, agent) })
		})
		if usage.calls == 0 {
			ui.Text(c, "这段时间内没有被调用").FontSize(13).TextColor(t.TextMuted)
		}
		if snap := a.sessions.snapshot; snap == nil || snap.Partial || len(snap.Warnings) > 0 {
			ui.Text(c, "本地日志尚未完整读取，调用次数可能不完整。").FontSize(11).TextColor(t.Warning)
		}
	})
}

// skillDailyChart is a bar a day, in the Agent's color, or neutral when it
// adds up several Agents.
func skillDailyChart(c *ui.Context, usage *skillUsageData, days int, agent session.Agent) {
	now := time.Now()
	start := skillRange(days, now).From
	t := c.Theme()
	color := token(t, "ink-3")
	if agent != "" {
		color = agentColor(agent, t.Dark)
	}
	peak := 1
	for _, count := range usage.days {
		peak = max(peak, count)
	}
	ui.Row(c).Gap(2).Height(40).AlignItems(ui.Stretch).FocusGroup(ui.Horizontal).Label("Skill 每日调用").Children(func() {
		for i := 0; i < days; i++ {
			day := start.AddDate(0, 0, i).Format("2006-01-02")
			count := usage.days[day]
			label := fmt.Sprintf("%s · %d 次调用", day, count)
			ui.ButtonBase(c.Key(day)).Grow(1).Basis(0).MinWidth(0).Label(label).Tooltip(label).Draw(func(p *ui.Painter, r ui.Rect) {
				if count == 0 {
					p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, t.Border, 0)
					return
				}
				h := max(2, float32(count)/float32(peak)*r.H)
				p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, color, 1)
			})
		}
	})
}
