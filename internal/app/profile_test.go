package app

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"knack/internal/session"
	"knack/internal/skill"
)

func TestHeatWeeks(t *testing.T) {
	today := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) // a Monday
	day := func(back int) string { return today.AddDate(0, 0, -back).Format("2006-01-02") }
	days := map[string]*profileDay{
		day(1): {tokens: 100, requests: 1},
		day(2): {tokens: 200, requests: 1},
		day(4): {tokens: 300, requests: 1},
		day(5): {tokens: 900_000, requests: 1},
	}
	weeks, byRequests := heatWeeks(days, today)
	if byRequests || len(weeks) != heatWeekCount || len(weeks[0]) != 7 || len(weeks[52]) != 1 {
		t.Fatalf("weeks %d, first %d, last %d, byRequests %t", len(weeks), len(weeks[0]), len(weeks[52]), byRequests)
	}
	if weeks[0][0].at.Weekday() != time.Monday || weeks[52][0].date != day(0) {
		t.Fatalf("grid runs %s to %s", weeks[0][0].date, weeks[52][0].date)
	}
	// One huge day must not push the ordinary ones to the bottom step.
	last := weeks[51]
	if peak := last[2]; peak.value != 900_000 || peak.level != 4 {
		t.Fatalf("peak day %+v", peak)
	}
	if last[3].level != 2 {
		t.Fatalf("an ordinary day fell to step %d", last[3].level)
	}
	if last[6].level != 1 || last[4].level != 0 {
		t.Fatalf("steps: %+v", last)
	}
	current, longest, active, peak := heatRecordsOf(weeks)
	if current != 2 || longest != 2 || active != 4 || peak.tokens != 900_000 {
		t.Fatalf("current %d longest %d active %d peak %d", current, longest, active, peak.tokens)
	}
	// Requests rank a year that has no token counts.
	weeks, byRequests = heatWeeks(map[string]*profileDay{day(0): {requests: 3}}, today)
	if !byRequests || weeks[52][0].level != 4 {
		t.Fatalf("requests did not rank: %t %+v", byRequests, weeks[52][0])
	}
	if current, _, _, _ := heatRecordsOf(weeks); current != 1 {
		t.Fatalf("today's streak %d", current)
	}
}

func TestPeakWindow(t *testing.T) {
	var hours [24]int
	hours[22], hours[23], hours[0], hours[12] = 5, 5, 5, 5
	from, share := peakWindow(hours)
	if from != 21 && from != 22 || share != .75 {
		t.Fatalf("peak from %d share %f", from, share)
	}
	if from, share := peakWindow([24]int{}); from != 0 || share != 0 {
		t.Fatalf("empty day: %d %f", from, share)
	}
}

func TestHeatGeometry(t *testing.T) {
	if cell, n := heatGeometry(960, heatWeekCount); cell != heatMaxCell || n != heatWeekCount {
		t.Fatalf("roomy grid: %v %d", cell, n)
	}
	if cell, n := heatGeometry(760, heatWeekCount); cell >= heatMaxCell || n != heatWeekCount {
		t.Fatalf("medium grid should shrink its squares: %v %d", cell, n)
	}
	cell, n := heatGeometry(300, heatWeekCount)
	if cell != heatMinCell || n >= heatWeekCount || heatLabel+heatGap+float32(n)*(cell+heatGap)-heatGap > 300 {
		t.Fatalf("narrow grid overflows: %v %d", cell, n)
	}
}

func TestProfileName(t *testing.T) {
	home := t.TempDir()
	if got := profileName(home); got != filepath.Base(home) {
		t.Fatalf("fallback %q", got)
	}
	config := "[core]\n\tname = wrong\n[user]\n\temail = a@b.c\n\tname = \"maidang1\"\n[alias]\n\tname = wrong\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := profileName(home); got != "maidang1" {
		t.Fatalf("git name %q", got)
	}
}

// profileFixture adds a year of sessions, more tools and Skills than the
// lists preview, and a day whose date the test can click.
func profileFixture(t *testing.T) (*App, session.Session) {
	t.Helper()
	a := agentPageFixture(t)
	snap := *a.sessions.snapshot
	snap.Sessions = append([]session.Session(nil), snap.Sessions...)
	r := rand.New(rand.NewSource(7))
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	var latest session.Session
	for back := 340; back >= 1; back-- {
		if r.Intn(10) < 4 {
			continue
		}
		at := noon.AddDate(0, 0, -back).Add(time.Duration(r.Intn(9)-2) * time.Hour)
		agent := session.Claude
		if r.Intn(4) == 0 {
			agent = session.Codex
		}
		var tools []session.ToolCall
		var skills []session.SkillCall
		for i := range 8 {
			tools = append(tools, session.ToolCall{ID: fmt.Sprintf("t%d-%d", back, i), Name: fmt.Sprintf("tool-%d", i*i%7), Start: at, End: at})
			skills = append(skills, session.SkillCall{Name: fmt.Sprintf("skill-%d", i), At: at})
		}
		s := session.Session{ID: fmt.Sprintf("year-%d", back), Path: fmt.Sprintf("/logs/year-%d.jsonl", back), Title: "历史会话", Agent: agent, Start: at, End: at.Add(10 * time.Minute), Turns: []session.Turn{{
			ID: fmt.Sprintf("year-turn-%d", back), Start: at, Tools: tools, Skills: skills,
			Requests: []session.Request{{ID: fmt.Sprintf("year-req-%d", back), Model: "claude-opus-5-5", End: at.Add(time.Minute), UsageKnown: true,
				Tokens: session.Tokens{Input: 1000, Output: int64(500 + r.Intn(50_000)), CacheRead: int64(r.Intn(3_000_000))}, Generation: session.Duration{Known: true, Value: 3 * time.Minute}}},
		}}}
		snap.Sessions = append(snap.Sessions, s)
		latest = s
	}
	a.sessions.snapshot = &snap
	a.sessions.overview, a.sessions.profile = nil, nil
	a.sessions.period = 3
	return a, latest
}

func TestProfilePage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1440, 1288, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a, latest := profileFixture(t)
				tt := ui.NewTester(a.View, width, 2400) // tall enough that no layout needs scrolling
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				if a.page != "overview" || tt.HasText("个人主页") || tt.HasText("用量与耗时") {
					t.Fatalf("page %s: %q", a.page, tt.Texts())
				}
				for _, label := range []string{"个人资料", "活跃足迹", "活跃足迹图表", "个人纪录", "Token 构成", "耗时构成", "一天中的节奏", "每小时轮次", "常用排行"} {
					r, ok := tt.Find(label)
					if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > float32(width)+1 {
						t.Fatalf("clipped %s: %+v (%v)", label, r, ok)
					}
				}
				exportSessionPreview(t, tt, fmt.Sprintf("home-%d-dark-%t.png", width, dark))
				a.sessions.period = 2
				tt.Frame()
				if !tt.HasText("高亮近 30 天") {
					t.Fatalf("range not named: %q", tt.Texts())
				}
				exportSessionPreview(t, tt, fmt.Sprintf("home-30d-%d-dark-%t.png", width, dark))
				a.sessions.period = 3
				tt.Frame()

				p := a.profileData()
				if p.sessions == 0 || p.first.IsZero() || p.active < 100 || p.turns == 0 {
					t.Fatalf("thin profile: %+v", p)
				}
				day := profileDay{}
				for _, week := range p.weeks {
					for _, d := range week {
						if d.date == latest.Start.Local().Format("2006-01-02") && d.tokens > 0 {
							day = d
						}
					}
				}
				if day.date == "" {
					t.Fatal("latest day not on the grid")
				}
				if last := p.weeks[len(p.weeks)-1]; len(last) < 7 {
					// A week under way starts at the top row, not centered.
					first, _ := tt.Find(heatDescription(p, p.weeks[len(p.weeks)-2][0]))
					today, _ := tt.Find(heatDescription(p, last[0]))
					if first.Y != today.Y {
						t.Fatalf("the current week floats: %v vs %v", today.Y, first.Y)
					}
				}
				// A narrow window may drop the oldest weeks but never the latest.
				click(t, tt, heatDescription(p, day))
				if a.sessions.profileDay != day.date || !tt.HasText(dayLabel(day.date)) {
					t.Fatalf("selection %q", a.sessions.profileDay)
				}

				// One ranking, Skills or tools, previews its head and opens in place.
				if tt.HasText("skill-7") || tt.HasText("tool-1 ×") {
					t.Fatal("ranking not previewed")
				}
				click(t, tt, "全部 10 个技能 →")
				if !tt.HasText("skill-7") || !tt.HasText("收起") {
					t.Fatalf("ranking did not open: %q", tt.Texts())
				}
				click(t, tt, "工具")
				if a.sessions.profileTab != 1 || !tt.HasText("全部 10 个工具 →") {
					t.Fatalf("tools not shown: %d %q", a.sessions.profileTab, tt.Texts())
				}
			})
		}
	}
}

func TestHeatFill(t *testing.T) {
	theme := appTheme(&ui.Theme{Dark: true})
	from := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	inside, before := from.AddDate(0, 0, 3), from.AddDate(0, 0, -3)
	if got := heatFill(theme, profileDay{at: inside, level: 3}, from); got != heatColor(3, true) {
		t.Fatalf("active day in range dimmed: %v", got)
	}
	if got := heatFill(theme, profileDay{at: before, level: 3}, from); got == heatColor(3, true) {
		t.Fatal("day before the period did not recede")
	}
	if got := heatFill(theme, profileDay{at: inside}, from); got == heatColor(0, true) {
		t.Fatal("empty day in range looks like one outside it")
	}
	if got := heatFill(theme, profileDay{at: before, level: 3}, time.Time{}); got != heatColor(3, true) {
		t.Fatal("all-time view highlighted a range")
	}
	if got := heatFill(theme, profileDay{at: from}, from); got == heatColor(0, true) {
		t.Fatal("the period's first day fell outside it")
	}
}

// The sidebar has one entry for every Agent; they are listed inside the page.
func TestAgentsNavigation(t *testing.T) {
	a := agentPageFixture(t)
	tt := ui.NewTester(a.View, 1440, 1000)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	if tt.HasText("AGENTS") {
		t.Fatal("sidebar still lists Agents")
	}
	if _, ok := tt.Find("Agent 列表"); ok {
		t.Fatal("home shows the Agent list")
	}
	click(t, tt, "Agents")
	root, ok := a.agentPageRoot()
	if !ok || agentKey(a.home, root.Path) != "claude" {
		t.Fatalf("Agents opened %s", a.page)
	}
	list, ok := tt.Find("Agent 列表")
	if !ok || list.X < a.sideW || list.X+list.W > a.sideW+agentListWide+1 {
		t.Fatalf("list misplaced: %+v", list)
	}
	for _, text := range []string{"有会话记录", "仅技能"} {
		if !tt.HasText(text) {
			t.Fatalf("missing group %s: %q", text, tt.Texts())
		}
	}
	exportSessionPreview(t, tt, "agents-1440.png")

	click(t, tt, "搜索 Agent")
	tt.Type("cod")
	if tt.HasText("有会话记录") && !tt.HasText("Codex") {
		t.Fatalf("search: %q", tt.Texts())
	}
	if _, ok := tt.Find("Cursor"); ok {
		t.Fatal("search kept a non-match")
	}
	click(t, tt, "Codex")
	a.agentQuery = ""
	tt.Frame()
	if root, ok := a.agentPageRoot(); !ok || agentKey(a.home, root.Path) != "codex" {
		t.Fatalf("list opened %s", a.page)
	}
	if !a.activeNav(agentsPage) || !a.activeNav(a.page) {
		t.Fatal("Agents entry not marked current")
	}
	click(t, tt, "首页")
	click(t, tt, "Agents")
	if root, ok := a.agentPageRoot(); !ok || agentKey(a.home, root.Path) != "codex" {
		t.Fatalf("Agents did not return to the last one: %s", a.page)
	}
	click(t, tt, "首页")
	click(t, tt, "Factory")
	if root, ok := a.agentPageRoot(); !ok || agentKey(a.home, root.Path) != "factory" {
		t.Fatalf("home row opened %s", a.page)
	}

	// Beside a split page the sidebar gives way to the icon rail sooner.
	tt = ui.NewTester(a.View, 1100, 800)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	if !a.rail || a.sidebarWidth() != railWidth+agentListWide {
		t.Fatalf("rail %t width %v", a.rail, a.sidebarWidth())
	}
	a.page = "overview"
	tt.Frame()
	tt.Frame()
	if a.rail {
		t.Fatal("a page without the list went to the rail")
	}
	a.page = agentsPage
	a.lastAgent = ""
	tt = ui.NewTester(a.View, 820, 700)
	if a.sidebarWidth() != railWidth+agentListNarrow {
		t.Fatalf("narrow width %v", a.sidebarWidth())
	}
	exportSessionPreview(t, tt, "agents-820.png")
}

// An Agent's logo prefers full color, and an Agent without one has none.
func TestAgentLogos(t *testing.T) {
	home := t.TempDir()
	root := func(key string) skill.Root {
		return skill.Root{Role: skill.RoleAgent, Path: filepath.Join(home, "."+key, "skills")}
	}
	for key, want := range map[string]string{"claude": "color:claude", "qoder": "color:qoder", "verdent": "color:verdent", "crush": "color:crush", "cline": "logo:cline", "mux": "logo:mux", "pochi": "logo:pochi"} {
		if got := sideLogo(home, root(key)); got == nil || got != svgs[want] {
			t.Errorf("%s: wrong logo, want %s", key, want)
		}
	}
	if sideLogo(home, root("no-such-agent")) != nil {
		t.Error("an unknown Agent got a logo")
	}
	for _, dir := range []string{".claude", ".cline", ".codebuddy", ".commandcode", ".continue", ".factory", ".goose", ".grok", ".junie", ".kilocode", ".kode", ".mcpjam", ".mux", ".neovate", ".openhands", ".pi", ".pochi", ".qoder", ".qwen", ".roo", ".verdent", ".zencoder"} {
		writeSkill(t, filepath.Join(home, dir, "skills", "demo"), "demo")
	}
	for _, dark := range []bool{false, true} {
		a := New(home)
		a.startScan()
		tt := ui.NewTester(a.View, 1440, 900)
		tt.SetDark(dark)
		click(t, tt, "Agents")
		click(t, tt, "Verdent")
		exportSessionPreview(t, tt, fmt.Sprintf("agent-logos-dark-%t.png", dark))
		list, ok := tt.Find("Agent 列表")
		if !ok {
			t.Fatal("no Agent list")
		}
		// Its logo paints in the brand's own color, not a tint of the text.
		green, im := 0, tt.Image()
		for y := int(list.Y); y < int(list.Y+list.H); y++ {
			for x := int(list.X); x < int(list.X+40); x++ {
				if p := im.RGBAAt(x, y); p.R == 0x0C && p.G == 0xBA && p.B == 0x8C {
					green++
				}
			}
		}
		if green < 8 {
			t.Fatalf("Verdent logo not drawn in its color (%d pixels)", green)
		}
	}
}

// The filter scopes the page; the year's grid follows it.
func TestProfileAgentFilter(t *testing.T) {
	a, _ := profileFixture(t)
	tt := ui.NewTester(a.View, 1280, 1000)
	all := a.profileData()
	a.sessions.agent = "Codex"
	tt = ui.NewTester(a.View, 1280, 1000)
	scoped := a.profileData()
	if scoped == all || scoped.active == 0 || scoped.active >= all.active || scoped.sessions >= all.sessions {
		t.Fatalf("filter ignored: all %d/%d, codex %d/%d", all.active, all.sessions, scoped.active, scoped.sessions)
	}
	for _, week := range scoped.weeks {
		for _, day := range week {
			for _, ag := range day.agents {
				if ag.agent != session.Codex {
					t.Fatalf("%s leaked %s", day.date, ag.agent)
				}
			}
		}
	}
	if !tt.HasText("Codex") {
		t.Fatalf("texts: %q", tt.Texts())
	}
}

// With nothing recorded the page says so rather than showing zeros.
func TestProfileEmpty(t *testing.T) {
	a := New(t.TempDir())
	a.startScan()
	tt := ui.NewTester(a.View, 1280, 900)
	for _, text := range []string{"没有本地会话", "这段时间没有可识别的轮次"} {
		if !tt.HasText(text) {
			t.Fatalf("missing %q: %q", text, tt.Texts())
		}
	}
	exportSessionPreview(t, tt, "home-empty.png")
}
