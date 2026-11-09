package app

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/session"
)

const (
	heatWeekCount = 53 // 52 full weeks and the one under way
	heatGap       = float32(3)
	heatLabel     = float32(23) // the weekday column, before its gap
	heatMaxCell   = float32(14)
	heatMinCell   = float32(8)
	peakHours     = 4
	callPreview   = 5
)

// Steps of the heatmap, none to most. They are fills under a text-free
// square, not series, so they sit outside sessionPalette's contrast rule.
var heatLevels = [5][2]string{
	{"#ECECF3", "#201F28"},
	{"#DDD3FA", "#3A2E66"},
	{"#B39EF0", "#5A41A8"},
	{"#8760E0", "#7D53E1"},
	{"#5A2FC2", "#B9A9FE"},
}

func heatColor(level int, dark bool) ui.Color {
	if dark {
		return ui.Hex(heatLevels[level][1])
	}
	return ui.Hex(heatLevels[level][0])
}

type profileAgentDay struct {
	agent  session.Agent
	tokens int64
}

type profileDay struct {
	date     string
	at       time.Time
	tokens   int64
	requests int
	value    int64 // what the heatmap ranks
	level    int
	agents   []profileAgentDay
}

type profileData struct {
	snapshot         *session.Snapshot
	period           int
	agent, date      string
	name             string
	sessions         int
	first            time.Time
	from             time.Time // where the period starts; zero for all time
	weeks            [][]profileDay
	byRequests       bool
	current, longest int
	active           int
	peak             profileDay
	hours            [24]int
	turns            int
	peakFrom         int
	peakShare        float64
	models           []modelSpeed
	skills, tools    []callCount
}

func (a *App) profileData() *profileData {
	s := &a.sessions
	now := time.Now()
	date := now.Format("2006-01-02")
	agent := a.sessionAgentFilter()
	if p := s.profile; p != nil && p.snapshot == s.snapshot && p.period == s.period && p.agent == agent && p.date == date {
		return p
	}
	p := &profileData{snapshot: s.snapshot, period: s.period, agent: agent, date: date, name: profileName(a.home)}
	f := overviewFilter(s.period, agent, now)
	p.from = f.From
	if s.snapshot != nil {
		for _, sess := range s.snapshot.Sessions {
			if sess.UnsupportedReason != "" || f.Agent != "" && sess.Agent != f.Agent {
				continue
			}
			p.sessions++
			if !sess.Start.IsZero() && (p.first.IsZero() || sess.Start.Before(p.first)) {
				p.first = sess.Start
			}
			for _, turn := range sess.Turns {
				if !turn.Start.IsZero() && filterTime(f, turn.Start) {
					p.hours[turn.Start.In(now.Location()).Hour()]++
					p.turns++
				}
			}
		}
	}
	p.peakFrom, p.peakShare = peakWindow(p.hours)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	year := f
	year.From, year.To = heatStart(today), today.AddDate(0, 0, 1)
	days := map[string]*profileDay{}
	for _, info := range session.Agents() {
		if info.Unsupported != "" || f.Agent != "" && f.Agent != info.ID {
			continue
		}
		year.Agent = info.ID
		for _, d := range session.Aggregate(s.snapshot, year).Daily {
			day := days[d.Date]
			if day == nil {
				day = &profileDay{}
				days[d.Date] = day
			}
			day.tokens += d.Tokens.Total()
			day.requests += d.Requests
			if d.Tokens.Total() > 0 {
				day.agents = append(day.agents, profileAgentDay{info.ID, d.Tokens.Total()})
			}
		}
	}
	for _, day := range days {
		slices.SortStableFunc(day.agents, func(x, y profileAgentDay) int { return cmp.Compare(y.tokens, x.tokens) })
	}
	p.weeks, p.byRequests = heatWeeks(days, today)
	p.current, p.longest, p.active, p.peak = heatRecordsOf(p.weeks)
	p.models, p.skills, p.tools = a.agentCalls("", a.overviewData().summary)
	s.profile = p
	return p
}

// heatStart is the Monday the grid opens on, 52 weeks before this week's.
func heatStart(today time.Time) time.Time {
	return today.AddDate(0, 0, -(int(today.Weekday())+6)%7-52*7)
}

// heatWeeks lays the days out in Monday-first columns ending today and ranks
// them by quartile of the active days, so one huge day does not flatten the
// rest. Where a whole year has requests but no token counts, the requests
// rank instead.
func heatWeeks(days map[string]*profileDay, today time.Time) ([][]profileDay, bool) {
	var tokens, requests int64
	for _, d := range days {
		tokens += d.tokens
		requests += int64(d.requests)
	}
	byRequests := tokens == 0 && requests > 0
	var values []int64
	for _, d := range days {
		d.value = d.tokens
		if byRequests {
			d.value = int64(d.requests)
		}
		if d.value > 0 {
			values = append(values, d.value)
		}
	}
	slices.Sort(values)
	level := func(v int64) int {
		n := len(values)
		switch {
		case v <= 0:
			return 0
		case v >= values[n-1]:
			return 4
		case v <= values[n/4]:
			return 1
		case v <= values[n/2]:
			return 2
		case v <= values[n*3/4]:
			return 3
		}
		return 4
	}
	var weeks [][]profileDay
	for at := heatStart(today); !at.After(today); at = at.AddDate(0, 0, 1) {
		if int(at.Weekday()) == 1 {
			weeks = append(weeks, nil)
		}
		day := profileDay{}
		if d := days[at.Format("2006-01-02")]; d != nil {
			day = *d
		}
		day.date, day.at, day.level = at.Format("2006-01-02"), at, level(day.value)
		weeks[len(weeks)-1] = append(weeks[len(weeks)-1], day)
	}
	return weeks, byRequests
}

// heatRecordsOf reads the grid's streaks. A today without activity yet does
// not end the current streak.
func heatRecordsOf(weeks [][]profileDay) (current, longest, active int, peak profileDay) {
	var flat []profileDay
	for _, week := range weeks {
		flat = append(flat, week...)
	}
	run := 0
	for _, d := range flat {
		if d.value == 0 {
			run = 0
			continue
		}
		run++
		active++
		longest = max(longest, run)
		if d.value > peak.value {
			peak = d
		}
	}
	end := len(flat) - 1
	if end >= 0 && flat[end].value == 0 {
		end--
	}
	for ; end >= 0 && flat[end].value > 0; end-- {
		current++
	}
	return current, longest, active, peak
}

// peakWindow is the busiest run of peakHours hours, round midnight, and its
// share of the turns.
func peakWindow(hours [24]int) (int, float64) {
	total, best, from := 0, -1, 0
	for h := range hours {
		total += hours[h]
		sum := 0
		for k := range peakHours {
			sum += hours[(h+k)%24]
		}
		if sum > best {
			best, from = sum, h
		}
	}
	if total == 0 {
		return 0, 0
	}
	return from, float64(best) / float64(total)
}

// profileName is the name the user commits under, else their home folder's.
func profileName(home string) string {
	for _, file := range []string{".gitconfig", ".config/git/config"} {
		b, err := os.ReadFile(filepath.Join(home, file))
		if err != nil {
			continue
		}
		section := ""
		for line := range strings.Lines(string(b)) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = strings.ToLower(strings.Trim(line, "[] "))
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if ok && section == "user" && strings.EqualFold(strings.TrimSpace(key), "name") {
				if name := strings.Trim(strings.TrimSpace(value), `"`); name != "" {
					return name
				}
			}
		}
	}
	return filepath.Base(home)
}

// homeHabits are the clauses that say how this person works, built only
// from what the period recorded.
func homeHabits(sum session.Summary, tools []callCount) []string {
	var parts []string
	if len(sum.ByModel) > 0 && sum.ByModel[0].Tokens.Total() > 0 {
		parts = append(parts, "主力模型 "+sum.ByModel[0].Name)
	}
	if len(tools) > 0 {
		parts = append(parts, "最常用 "+tools[0].name)
	}
	return parts
}

// heatGeometry fits the grid to its width: smaller squares first, then
// fewer of the oldest weeks.
func heatGeometry(width float32, weeks int) (float32, int) {
	room := width - heatLabel - heatGap
	cell := float32(int((room - heatGap*float32(weeks-1)) / float32(weeks)))
	cell = min(heatMaxCell, max(heatMinCell, cell))
	return cell, max(1, min(weeks, int((room+heatGap)/(cell+heatGap))))
}

// heatFill is a day's square. With a period chosen, its days stand out: the
// days before it recede and its empty days lift off the background.
func heatFill(t *ui.Theme, day profileDay, from time.Time) ui.Color {
	color := heatColor(day.level, t.Dark)
	switch {
	case from.IsZero():
		return color
	case !day.at.Before(from) && day.level == 0:
		return token(t, "line-strong")
	case day.at.Before(from) && day.level > 0:
		return blend(color, t.Background, 0.5)
	}
	return color
}

// profileHeatmap is the last year, a square a day. Each square is a button
// in one focus group, so Tab stops once and the arrows walk the days.
func (a *App) profileHeatmap(c *ui.Context, p *profileData, width float32) {
	t := c.Theme()
	muted := token(t, "ink-3")
	cell, columns := heatGeometry(width, len(p.weeks))
	weeks := p.weeks[len(p.weeks)-columns:]
	selected := a.heatSelection(p)
	inset := heatLabel + heatGap
	ui.Column(c).Gap(12).Label("活跃足迹").Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "活跃足迹")
			caption := "近 52 周 · 按每日 Token"
			if p.byRequests {
				caption = "近 52 周 · 按每日请求数"
			}
			if !p.from.IsZero() {
				caption += " · 高亮" + periodText(p.period)
			}
			ui.Text(c, caption).FontSize(12).TextColor(muted).SingleLine().Shrink(1).MinWidth(0)
			ui.Spacer(c)
			ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Label("颜色越深越多").Children(func() {
				ui.Text(c, "少").FontSize(11.5).TextColor(muted).Padding(0, 2, 0, 0)
				for level := range heatLevels {
					ui.Box(c).Size(12, 12).Radius(3).Background(heatColor(level, t.Dark))
				}
				ui.Text(c, "多").FontSize(11.5).TextColor(muted).Padding(0, 0, 0, 2)
			})
		})
		ui.Column(c).Gap(5).Children(func() {
			ui.Box(c).Height(14).Draw(func(painter *ui.Painter, r ui.Rect) {
				for i := 1; i < len(weeks)-2; i++ {
					if weeks[i][0].at.Month() == weeks[i-1][0].at.Month() {
						continue
					}
					span := ui.Span{Text: fmt.Sprintf("%d 月", weeks[i][0].at.Month()), Size: 11, Color: muted}
					w, _ := painter.MeasureText(1000, span)
					painter.RichText(r.X+inset+float32(i)*(cell+heatGap), r.Y, w+2, span)
				}
			})
			ui.Row(c).Gap(heatGap).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Size(heatLabel, 7*cell+6*heatGap).Shrink(0).Draw(func(painter *ui.Painter, r ui.Rect) {
					for _, row := range []struct {
						day  string
						line float32
					}{{"一", 0}, {"三", 2}, {"五", 4}} {
						span := ui.Span{Text: row.day, Size: 11, Color: muted}
						w, h := painter.MeasureText(1000, span)
						painter.RichText(r.X, r.Y+row.line*(cell+heatGap)+(cell-h)/2, w+2, span)
					}
				})
				ui.Row(c).Gap(heatGap).AlignItems(ui.Start).FocusGroup(ui.Horizontal | ui.Vertical).Label("活跃足迹图表").Children(func() {
					for _, week := range weeks {
						ui.Column(c).Gap(heatGap).Shrink(0).Children(func() {
							for _, day := range week {
								description := heatDescription(p, day)
								square := ui.ButtonBase(c.Key("heat:"+day.date)).Size(cell, cell).Shrink(0).Radius(2.5).
									Background(heatFill(t, day, p.from)).Label(description).Tooltip(description)
								if day.date == selected.date {
									square.Border(1.5, t.Text)
								}
								if square.Clicked() {
									a.sessions.profileDay = day.date
									c.Invalidate()
								}
							}
						})
					}
				})
			})
		})
		ui.Row(c).Gap(10).Padding(0, 0, 0, inset).AlignItems(ui.Center).Label("所选日期").Children(func() {
			ui.Text(c, dayLabel(selected.date)).FontSize(12.5).FontWeight(600).SingleLine()
			number(c, heatAmountLong(p, selected)).FontSize(12.5).TextColor(t.TextMuted)
			for _, ag := range selected.agents {
				ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Box(c).Size(7, 7).Radius(3.5).Background(agentColor(ag.agent, t.Dark))
					number(c, sessionAgentName(ag.agent)+" "+tokenNumber(ag.tokens)).FontSize(12.5).TextColor(t.TextMuted)
				})
			}
		})
		ui.Row(c).Gap(32).Wrap().Padding(10, 0, 0, inset).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Label("个人纪录").Children(func() {
			for _, r := range [][2]string{
				{"当前连续", fmt.Sprintf("%d 天", p.current)},
				{"最长连续", fmt.Sprintf("%d 天", p.longest)},
				{"活跃天数", fmt.Sprintf("%d 天", p.active)},
				{"单日最高", heatAmount(p, p.peak)},
			} {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Text(c, r[0]).FontSize(12).TextColor(muted).SingleLine()
					number(c, r[1]).FontSize(14).FontWeight(600)
				})
			}
		})
	})
}

// heatSelection is the day the caption describes: the one chosen, else the
// latest with any activity, else today.
func (a *App) heatSelection(p *profileData) profileDay {
	var latest, last profileDay
	for _, week := range p.weeks {
		for _, day := range week {
			if day.date == a.sessions.profileDay {
				return day
			}
			last = day
			if day.value > 0 {
				latest = day
			}
		}
	}
	if latest.date != "" {
		return latest
	}
	return last
}

func heatAmount(p *profileData, day profileDay) string {
	switch {
	case day.value == 0:
		return "—"
	case p.byRequests:
		return fmt.Sprintf("%d 次", day.requests)
	}
	return tokenNumber(day.tokens)
}

func heatAmountLong(p *profileData, day profileDay) string {
	switch {
	case day.value == 0:
		return "没有活动"
	case p.byRequests:
		return fmt.Sprintf("%d 次请求", day.requests)
	}
	return tokenNumber(day.tokens) + " Token"
}

func heatDescription(p *profileData, day profileDay) string {
	return day.date + " · " + heatAmountLong(p, day)
}

// profileMix is where the period's tokens and active time went.
func (a *App) profileMix(c *ui.Context, d *overviewData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	sum := d.summary
	agent := overviewFilter(d.period, d.agent, time.Now()).Agent
	color := token(t, "ink-2")
	reason := "当前范围没有可识别的请求用量"
	if agent != "" {
		color = agentColor(agent, t.Dark)
		reason = cmp.Or(session.LookupAgent(agent).UsageNote, reason)
	}
	caption := func(title, note string, link func()) {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, title)
			if note != "" {
				number(c, note).FontSize(12).TextColor(muted).MinWidth(0)
			}
			ui.Spacer(c)
			if link != nil {
				link()
			}
		})
	}
	ui.Column(c).Gap(10).Label("Token 构成").Children(func() {
		caption("Token 构成", periodText(d.period), func() { overviewLink(c, "费用明细 →", a.openCostDetail) })
		if sum.Requests <= sum.UnmeteredRequests {
			ui.Text(c, "暂不支持 · "+reason).FontSize(13).TextColor(t.TextMuted)
			return
		}
		tokenBar(c, sum, color, 10)
	})
	timing := a.usageTimingFor("", d)
	ui.Column(c).Gap(10).Label("耗时构成").Children(func() {
		note := ""
		if timing.turns > 0 {
			note = fmt.Sprintf("%d 轮 · 活跃 ≈ %s", timing.turns, session.FormatDuration(timing.phases.total))
		}
		caption("时间都花在哪", note, nil)
		if timing.turns == 0 {
			ui.Text(c, "这段时间没有可识别的轮次").FontSize(13).TextColor(t.TextMuted)
			return
		}
		phaseBar(c, timing.phases, timing.waiting, timing.away)
		if note := session.LookupAgent(agent).TimingNote; agent != "" && note != "" {
			ui.Text(c, note).FontSize(11.5).TextColor(muted)
		}
		ui.Text(c, "按轮累加活跃时间，与会话详情同一口径；轮与轮之间的等待不计入。模型时间为估算。").FontSize(11.5).TextColor(muted).MaxLines(2)
	})
}

// profileRhythm is when in the day the turns start, in the local zone.
func (a *App) profileRhythm(c *ui.Context, p *profileData) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Column(c).Gap(10).Label("一天中的节奏").Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "一天中的节奏")
			ui.Text(c, "按轮次开始时间").FontSize(12).TextColor(muted).SingleLine()
		})
		if p.turns == 0 {
			ui.Text(c, "这段时间没有可识别的轮次").FontSize(13).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			number(c, fmt.Sprintf("%02d:00 – %02d:00", p.peakFrom, (p.peakFrom+peakHours)%24)).FontSize(20).FontWeight(600)
			ui.Textf(c, "最活跃，占 %.0f%%", p.peakShare*100).FontSize(12).TextColor(muted).SingleLine()
		})
		peak := 1
		for _, n := range p.hours {
			peak = max(peak, n)
		}
		ui.Row(c).Height(60).Gap(3).Label("每小时轮次").Children(func() {
			for hour, n := range p.hours {
				color := t.SurfaceHover
				if n > 0 {
					color = token(t, "line-strong")
					if (hour-p.peakFrom+24)%24 < peakHours {
						color = t.Accent
					}
				}
				tip := fmt.Sprintf("%02d:00 – %02d:00 · %d 轮", hour, (hour+1)%24, n)
				ui.Box(c.Key(hour)).Grow(1).Basis(0).MinWidth(0).AlignSelf(ui.Stretch).Label(tip).Tooltip(tip).Draw(func(painter *ui.Painter, r ui.Rect) {
					h := max(2, float32(n)/float32(peak)*r.H)
					painter.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}, color, 2)
				})
			}
		})
		ui.Row(c).Justify(ui.SpaceBetween).Children(func() {
			for _, tick := range []string{"0", "6", "12", "18", "24"} {
				number(c, tick).FontSize(11).TextColor(muted)
			}
		})
	})
}

// profileRanking is what the period reached for, Skills or tools under one
// heading. The list shows its head and opens in place.
func (a *App) profileRanking(c *ui.Context, p *profileData) {
	t := c.Theme()
	s := &a.sessions
	muted := token(t, "ink-3")
	agent := overviewFilter(p.period, p.agent, time.Now()).Agent
	skillNote := ""
	if agent != "" {
		skillNote = skillSupport(agent)
	}
	calls, noun, color := p.skills, "技能", t.Text
	if s.profileTab == 1 {
		calls, noun, color = p.tools, "工具", sessionColor("phase-tool", t.Dark)
	}
	total := 0
	for _, call := range calls {
		total += call.count
	}
	unsupported := s.profileTab == 0 && skillNote != ""
	ui.Column(c).Gap(9).Label("常用排行").Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			overviewTitle(c, "常用")
			if total > 0 {
				number(c, fmt.Sprintf("%d 次 · %d 个%s", total, len(calls), noun)).FontSize(12).TextColor(muted).MinWidth(0)
			}
			ui.Spacer(c)
			segmented(c, "常用类别", &s.profileTab, "技能", "工具")
		})
		if unsupported {
			ui.Text(c, skillNote).FontSize(11.5).TextColor(muted).MaxLines(3)
			return
		}
		if len(calls) == 0 {
			ui.Text(c, "没有记录到调用").FontSize(12).TextColor(t.TextMuted)
			return
		}
		limit := callPreview
		if s.profileOpen[s.profileTab] {
			limit = 0
		}
		callRows(c, calls, color, limit, 104)
		if len(calls) > callPreview {
			label := fmt.Sprintf("全部 %d 个%s →", len(calls), noun)
			if s.profileOpen[s.profileTab] {
				label = "收起"
			}
			ui.Row(c).Children(func() {
				overviewLink(c, label, func() { s.profileOpen[s.profileTab] = !s.profileOpen[s.profileTab] })
			})
		}
	})
}
