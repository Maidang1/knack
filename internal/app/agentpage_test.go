package app

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"knack/internal/session"
)

func agentPageFixture(t *testing.T) *App {
	t.Helper()
	a := overviewFixture(t)
	writeSkill(t, filepath.Join(a.home, ".claude/skills/unused"), "unused")
	writeSkill(t, filepath.Join(a.home, ".codex/skills/build"), "build")
	writeSkill(t, filepath.Join(a.home, ".factory/skills/build"), "build")
	a.startScan()
	snap := *a.sessions.snapshot
	snap.Sessions = append([]session.Session(nil), snap.Sessions...)
	now := time.Now().Add(-time.Minute)
	other := session.Session{ID: "other-claude", Title: "构建另一项功能", Agent: session.Claude, Start: now.Add(-time.Hour), End: now, Turns: []session.Turn{{ID: "other-turn", Skills: []session.SkillCall{{Name: "build", At: now}}, Tools: []session.ToolCall{{ID: "other-tool", Name: "Read", Start: now, End: now}}, Requests: []session.Request{{ID: "other-request", Model: "claude-other", End: now, UsageKnown: true, Tokens: session.Tokens{Input: 10, Output: 7}, Generation: session.Duration{Known: true, Value: time.Second}}}}}}
	snap.Sessions = append(snap.Sessions, other)
	old := other
	old.ID = "old"
	old.Title = "历史构建"
	old.Start = now.AddDate(0, 0, -40)
	old.End = old.Start
	old.Turns = []session.Turn{{ID: "old-turn", Skills: []session.SkillCall{{Name: "build", At: old.Start}}}}
	snap.Sessions = append(snap.Sessions, old)
	child := session.Session{ID: "unlinked-activity", ParentID: "parent", Agent: session.Claude, Start: now, End: now, Turns: []session.Turn{{ID: "child-turn", Skills: []session.SkillCall{{Name: "build", At: now}}}}}
	snap.UnlinkedSubagents = append(snap.UnlinkedSubagents, child)
	a.sessions.snapshot = &snap
	a.sessions.overview = nil
	return a
}

func TestAgentPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := agentPageFixture(t)
				a.sessions.agent = "Factory" // A root page must never accidentally show this filter.
				tt := ui.NewTester(a.View, width, 1100)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				tt.SetDark(dark)
				click(t, tt, "Claude")
				root, ok := a.agentPageRoot()
				if !ok || agentKey(a.home, root.Path) != "claude" {
					t.Fatal("sidebar did not open Agent page")
				}
				for _, label := range []string{"Agent 页面标签", "Agent 技能", "Agent 会话", "Agent 用量", "技能详情内容"} {
					r, ok := tt.Find(label)
					if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > float32(width)+1 {
						t.Fatalf("clipped %s: %+v", label, r)
					}
				}
				if _, ok := tt.Find("统计 Agent"); ok {
					t.Fatal("Agent page repeats global filter")
				}
				click(t, tt, "build")
				if !tt.HasText("近 30 天调用 3 次 · 2 个会话") || !tt.HasText("主 Agent 2 次 · 子 Agent 1 次") || !tt.HasText("近 30 天 3 次") {
					t.Fatalf("skill attribution wrong: %q", tt.Texts())
				}

				click(t, tt, "90 天")
				if !tt.HasText("近 90 天调用 4 次 · 3 个会话") {
					t.Fatal("skill range does not include older calls")
				}
				click(t, tt, "7 天")
				if !tt.HasText("近 7 天调用 3 次 · 2 个会话") {
					t.Fatal("skill range leaks older calls")
				}
				click(t, tt, "30 天")
				exportSessionPreview(t, tt, fmt.Sprintf("agent-skills-%d-dark-%t.png", width, dark))
				skillPane, _ := tt.Find("技能详情内容")
				tt.Scroll(skillPane.X+skillPane.W/2, skillPane.Y+100, 0, 180)
				exportSessionPreview(t, tt, fmt.Sprintf("skill-usage-%d-dark-%t.png", width, dark))
				click(t, tt, "unused")
				if !tt.HasText("这段时间内没有被调用") {
					t.Fatal("unused skill missing scoped empty state")
				}
				click(t, tt, "build")
				click(t, tt, "Agent 会话")
				if a.sessions.agent != "Factory" || a.sessionAgentFilter() != "Claude" || len(a.sessionRows()) != 2 {
					t.Fatal("Agent filter changed global preference or leaked other Agents")
				}
				for _, s := range a.sessionRows() {
					if s.Agent != session.Claude {
						t.Fatal("cross-Agent row")
					}
				}
				exportSessionPreview(t, tt, fmt.Sprintf("agent-sessions-%d-dark-%t.png", width, dark))
				tt.Key(0, ui.KeyRight)
				if a.state(a.page).agentTab != 2 || !tt.Focused("Agent 用量") {
					t.Fatal("Agent tab arrow navigation")
				}
				d := a.overviewData()
				f := overviewFilter(a.sessions.period, "Claude", time.Now())
				want := session.Aggregate(a.sessions.snapshot, f)
				if d.summary.Tokens != want.Tokens || !tt.HasText("Skill 调用排行") || !tt.HasText("工具调用排行") || !tt.HasText("每日 Token") || !tt.HasText("claude-other") {
					t.Fatal("missing scoped usage")
				}
				if tt.HasText("gpt-test") || tt.HasText("factory-test") {
					t.Fatal("model stats leak across Agents")
				}
				exportSessionPreview(t, tt, fmt.Sprintf("agent-usage-%d-dark-%t.png", width, dark))
				// Return via keyboard, then jump from a recorded Skill use into the existing detail.
				tt.Key(0, ui.KeyLeft)
				tt.Key(0, ui.KeyLeft)
				if a.state(a.page).agentTab != 0 {
					t.Fatal("cannot return to skills")
				}
				click(t, tt, "关联会话")
				focusSessionControl(t, tt, "使用会话 · 构建另一项功能")
				tt.Key(0, ui.KeyEnter)
				if a.page != "sessions" || a.sessions.selected != "claude:other-claude" || !a.sessions.open {
					t.Fatal("Skill-to-session deep link")
				}
				// A different Agent starts on its own skills tab, then keeps a scoped empty-state.
				click(t, tt, "Cursor")
				click(t, tt, "Agent 会话")
				if len(a.sessionRows()) != 0 || !tt.HasText("Cursor 没有本地会话日志，会话与用量暂不支持") {
					t.Fatal("Cursor displays other Agents as its own")
				}
				click(t, tt, "Agent 用量")
				if !tt.HasText("Cursor 没有本地会话日志，会话与用量暂不支持") || tt.HasText("0 Token") {
					t.Fatal("Cursor missing capability shown as zero")
				}
				click(t, tt, "Factory")
				click(t, tt, "Agent 用量")
				if !tt.HasText("暂不支持 · Factory 本地日志没有 Token 用量字段，Token、缓存与输出速率暂不支持") || tt.HasText("0 Token") {
					t.Fatal("Factory missing capability shown as zero")
				}
				exportSessionPreview(t, tt, fmt.Sprintf("agent-factory-usage-%d-dark-%t.png", width, dark))
				click(t, tt, "Codex")
				click(t, tt, "Agent 用量")
				// Codex loads skills by reading their SKILL.md, which the logs record.
				if tt.HasText("Codex 本地日志没有可靠的 Skill 调用记录，Skill 使用情况暂不支持") {
					t.Fatal("Codex Skill loads reported as unsupported")
				}
				// Replacing the snapshot invalidates the activity index and its query cache.
				before := a.sessionActivity()
				snapshot := *a.sessions.snapshot
				a.sessions.snapshot = &snapshot
				if a.sessionActivity() == before {
					t.Fatal("activity cache did not refresh")
				}
			})
		}
	}
}

func TestSessionStage4Corrections(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				a := sessionRoundFixture(t)
				a.foldLongSteps = true
				a.sessions.stepOpen = true
				tt := ui.NewTester(a.View, width, 1000)
				tt.SetDark(dark)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				// Only the inspector's own texts: step rows name their tools.
				texts := tt.Texts()
				from := slices.Index(texts, "步骤详情")
				to := slices.IndexFunc(texts, func(s string) bool { return strings.HasPrefix(s, "内容从原始日志按需读取") })
				if from < 0 || to < from {
					t.Fatalf("missing step inspector: %q", texts)
				}
				inspector := texts[from:to]
				if slices.Contains(inspector, "工具") || slices.Contains(inspector, "Skill") || slices.Contains(inspector, "是否超时") || slices.Contains(inspector, "权限等待来源") {
					t.Fatalf("model details show inapplicable metadata: %q", inspector)
				}
				// An open step's content interrupts the break; check it across closed rows.
				a.sessions.stepOpen = false
				tt.Frame()
				chart, _ := tt.Find("单轮步骤瀑布图")
				axis := newStepAxis(a.sessions.detail.turns[0], true)
				cut := axis.cuts[0]
				x := chart.X + stepTrackLeft + (axis.at(cut.from)+axis.at(cut.to))/2*(chart.W-stepTrackLeft-stepTrackRight) - 2
				im := tt.Image()
				continuous := 0
				var neutral ui.Color
				// Native theme values, captured before painting, also verify the break is not phase-wait.
				ui.NewTester(func(c *ui.Context) { neutral = appTheme(c.Theme()).TextMuted }, 1, 1).SetDark(dark)
				for y := int(chart.Y); y < int(chart.Y+chart.H); y++ {
					p := im.RGBAAt(int(math.Round(float64(x))), y)
					if p.R == neutral.R && p.G == neutral.G && p.B == neutral.B {
						continuous++
					}
				}
				if continuous < int(chart.H)-20 {
					t.Fatalf("missing continuous neutral break: %d/%f", continuous, chart.H)
				}
				exportSessionPreview(t, tt, fmt.Sprintf("corrected-folded-%d-dark-%t.png", width, dark))
				// A synthetic away interval is much longer than active work; it must not set the scale.
				root := a.sessions.detail.session
				root.Turns = append([]session.Turn(nil), root.Turns...)
				root.Turns[0].Waits = append([]session.Wait(nil), root.Turns[0].Waits...)
				awayStart := root.Turns[0].End
				root.Turns[0].Waits = append(root.Turns[0].Waits, session.Wait{Kind: "away", Start: awayStart, End: awayStart.Add(4 * time.Hour), Elapsed: session.Duration{Known: true, Estimated: true, Value: 4 * time.Hour}})
				root.Turns[1].Start = root.Turns[1].Start.Add(4 * time.Hour)
				root.Turns[1].End = root.Turns[1].End.Add(4 * time.Hour)
				root.End = root.End.Add(4 * time.Hour)
				a.sessions.snapshot = &session.Snapshot{Sessions: []session.Session{root}}
				tt.Frame()
				// The away gap is a separator after the turn, never part of its active time.
				if !tt.HasText("之后约 4h 没有记录（日志无精确起止）") || a.sessions.detail.turns[0].active.total >= time.Hour {
					t.Fatalf("missing turn/away wording %q", tt.Texts())
				}
				for _, text := range tt.Texts() {
					if strings.HasPrefix(text, "#") && len(text) > 2 && strings.Contains(text, " · ") {
						t.Fatal("concern looks ranked")
					}
				}
				exportSessionPreview(t, tt, fmt.Sprintf("corrected-timing-%d-dark-%t.png", width, dark))
			})
		}
	}
	// Detail metadata depends on step type, including wait and compact events.
	at := time.Now()
	for _, tc := range []struct {
		name            string
		step            sessionStep
		present, absent []string
	}{
		{"tool", sessionStep{name: "Read", tool: &session.ToolCall{Name: "Read"}}, []string{"工具", "是否超时"}, []string{"模型", "Token", "Skill", "权限等待来源"}},
		{"reply", sessionStep{name: "等待你回复", wait: &session.Wait{Kind: "reply"}}, []string{"等待类型"}, []string{"工具", "Token", "是否超时", "权限等待来源"}},
		{"compact", sessionStep{name: "上下文压缩", event: &session.Event{Kind: "compact", At: at}}, []string{"压缩耗时"}, []string{"工具", "模型", "是否中断"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{}
			d := &sessionDetailData{session: session.Session{Agent: session.Claude}, turns: []sessionTurnView{{steps: []sessionStep{tc.step}}}}
			tt := ui.NewTester(func(c *ui.Context) { a.stepInspector(c, d, 0, nil) }, 500, 500)
			for _, text := range tc.present {
				if !slices.Contains(tt.Texts(), text) {
					t.Fatalf("missing %s", text)
				}
			}
			for _, text := range tc.absent {
				if slices.Contains(tt.Texts(), text) {
					t.Fatalf("inapplicable %s", text)
				}
			}
		})
	}
}
