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
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "使用情况")
			ui.Spacer(c)
			ui.Segmented(c, &a.skillPeriod, "7 天", "30 天", "90 天").Label("技能使用时间范围").Height(24)
		})
		if agent != "" {
			if reason := skillSupport(agent); reason != "" {
				ui.Text(c, reason).FontSize(11).TextColor(t.TextMuted)
				return
			}
		}
		days := []int{7, 30, 90}[max(0, min(a.skillPeriod, 2))]
		usage := a.sessionActivity().skillUsage(agent, skillNames(e), skillRange(days, time.Now()))
		ui.Textf(c, "近 %d 天调用 %d 次 · %d 个会话", days, usage.calls, len(usage.sessions)).FontSize(13).FontWeight(600)
		last := "未记录"
		if !usage.last.IsZero() {
			last = usage.last.Local().Format("2006/01/02 15:04")
		}
		ui.Text(c, "最后使用 · "+last).FontSize(11).TextColor(t.TextMuted)
		if usage.calls == 0 {
			ui.Text(c, "这段时间内没有被调用").FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Textf(c, "主 Agent %d 次 · 子 Agent %d 次", usage.main, usage.child).FontSize(11).TextColor(t.TextMuted)
			skillDailyChart(c, usage, days)
			for _, use := range usage.sessions {
				label := "使用会话 · " + use.label
				b := ui.Button(c.Key(use.id+use.label), "").Label(label).Border(1, t.Border).Background(ui.Transparent).Radius(5).Padding(7, 9).Disabled(use.id == "")
				b.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, use.label).FontSize(11).SingleLine().Tooltip(use.label)
						ui.Textf(c, "%s · %d 次", use.last.Local().Format("01/02 15:04"), use.calls).FontSize(10).TextColor(t.TextMuted)
					})
				})
				if b.Clicked() {
					a.openSession(use.id, 0)
					c.Invalidate()
				}
			}
		}
		ui.Text(c, "按日志中的 Skill 调用名匹配，未记录实际加载路径；不能据此判断同名副本是否被使用。").FontSize(10).TextColor(t.TextMuted)
		if agent == "" {
			ui.Text(c, "跨 Agent 汇总已识别调用；Codex、Cursor 等缺少调用记录的来源不计为零。").FontSize(10).TextColor(t.TextMuted)
		}
		if snap := a.sessions.snapshot; snap == nil || snap.Partial || len(snap.Warnings) > 0 {
			ui.Text(c, "本地日志尚未完整读取，调用次数可能不完整。").FontSize(10).TextColor(t.Warning)
		}
	})
}

func skillDailyChart(c *ui.Context, usage *skillUsageData, days int) {
	now := time.Now()
	start := skillRange(days, now).From
	neutral := sessionColor("data-neutral", c.Theme().Dark)
	peak := 1
	for _, count := range usage.days {
		peak = max(peak, count)
	}
	ui.Row(c).Gap(2).Height(32).FocusGroup(ui.Horizontal).Label("Skill 每日调用").Children(func() {
		for i := 0; i < days; i++ {
			day := start.AddDate(0, 0, i).Format("2006-01-02")
			count := usage.days[day]
			label := fmt.Sprintf("%s · %d 次调用", day, count)
			ui.Button(c.Key(day), "").Grow(1).MinWidth(0).Height(32).Padding(0).Border(0, ui.Transparent).Background(ui.Transparent).Label(label).Tooltip(label).Draw(func(p *ui.Painter, r ui.Rect) {
				if count == 0 {
					return
				}
				h := float32(count) / float32(peak) * r.H
				p.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, neutral, 1)
			})
		}
	})
}
