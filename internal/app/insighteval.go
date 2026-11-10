package app

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/feedback"

	"knack/internal/insight"
	"knack/internal/session"
)

// 技能评测 judges skills by the sessions that loaded them, on the two axes
// skill authors test: does the description make the model load the skill
// when it should (triggering), and does the turn go better once it is
// loaded (execution). The list ranks skills by loads; a skill opens into
// its verdicts, a before/after of its last edit, a model's review of each
// load, every load, and a trigger eval set run against its description.

type insightEval struct {
	snapshot *session.Snapshot
	period   int
	agent    string
	date     string
	report   insight.SkillEvalReport
}

// skillFiles is what the eval needs from a skill's directory.
type skillFiles struct {
	extra  bool      // files beside SKILL.md: scripts, references, assets
	edited time.Time // SKILL.md's modification time
}

const evalShown = 12

func (a *App) skillEvals() insight.SkillEvalReport {
	date := time.Now().Format("2006-01-02")
	agent := a.sessionAgentFilter()
	if e := a.insights.eval; e != nil && e.snapshot == a.sessions.snapshot && e.period == a.sessions.period && e.agent == agent && e.date == date {
		return e.report
	}
	f := overviewFilter(a.sessions.period, agent, time.Now())
	r := insight.EvaluateSkills(a.sessions.snapshot, f)
	a.insights.eval = &insightEval{snapshot: a.sessions.snapshot, period: a.sessions.period, agent: agent, date: date, report: r}
	return r
}

// evalLibrary finds the installed copies of an evaluated skill: those of
// the Agents that loaded it first, else any copy in the scanned roots, such
// as a shared pool an Agent reads from.
func (a *App) evalLibrary(e insight.SkillEval) []insight.SkillHealth {
	name := e.Name
	if _, after, ok := strings.Cut(name, ":"); ok {
		name = after
	}
	var out []insight.SkillHealth
	for _, h := range a.skillInsights().Skills {
		if h.Name == name && slices.Contains(e.Agents, h.Agent) {
			out = append(out, h)
		}
	}
	if len(out) > 0 || a.snap == nil {
		return out
	}
	for _, en := range a.snap.Entries {
		if en.Name == name || en.FrontName == name {
			out = append(out, insight.SkillHealth{RootID: en.RootID, RootName: en.RootName, Name: en.Name, Path: en.Path, Description: en.Description})
		}
	}
	return out
}

func (a *App) skillFilesOf(path string) skillFiles {
	if a.insights.filesGen != a.scanGen || a.insights.files == nil {
		a.insights.files, a.insights.filesGen = map[string]skillFiles{}, a.scanGen
	}
	if f, ok := a.insights.files[path]; ok {
		return f
	}
	dir := path
	if filepath.Base(dir) == "SKILL.md" {
		dir = filepath.Dir(dir)
	}
	var f skillFiles
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if n := e.Name(); n != "SKILL.md" && !strings.HasPrefix(n, ".") && !strings.EqualFold(n, "LICENSE.txt") && !strings.EqualFold(n, "README.md") {
				f.extra = true
			}
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		f.edited = info.ModTime()
	}
	a.insights.files[path] = f
	return f
}

// evalContext gathers what the verdicts need about a skill's install.
type evalInstall struct {
	lib    []insight.SkillHealth
	files  bool
	edited time.Time
}

func (a *App) evalInstall(e insight.SkillEval) evalInstall {
	in := evalInstall{lib: a.evalLibrary(e)}
	for _, h := range in.lib {
		f := a.skillFilesOf(h.Path)
		in.files = in.files || f.extra
		if f.edited.After(in.edited) {
			in.edited = f.edited
		}
	}
	return in
}

type evalHint struct {
	warn bool
	text string
}

// evalHints are the verdicts on a skill, worst first.
func evalHints(e insight.SkillEval, base insight.Window, in evalInstall) []evalHint {
	var warn, info []evalHint
	n := len(e.Episodes)
	// Built-in and plugin commands are meant to be typed; only an installed
	// skill is expected to be picked by its description.
	if len(in.lib) > 0 && e.Manual >= 2 && e.Manual*2 >= n {
		warn = append(warn, evalHint{true, fmt.Sprintf("%d 次加载里有 %d 次是手动输入 /%s：描述可能没写清什么时候该用，模型没能自己选中它。", n, e.Manual, e.Name)})
	}
	if in.files && n >= 3 && e.Used == 0 {
		warn = append(warn, evalHint{true, fmt.Sprintf("加载了 %d 次，之后的调用从没用到技能目录里的脚本或参考文件：模型可能只读了说明，或者这些文件已经用不上。", n)})
	}
	if n >= 5 && base.Episodes >= 10 {
		switch d := e.CleanRate() - base.CleanRate(); {
		case d <= -0.15:
			warn = append(warn, evalHint{true, fmt.Sprintf("加载它的轮次有 %s 顺利，未加载技能的轮次是 %s：看看下面有失败或被纠正的加载记录。", pct(e.CleanRate()), pct(base.CleanRate()))})
		case d >= 0.1:
			info = append(info, evalHint{false, fmt.Sprintf("加载它的轮次有 %s 顺利，高于未加载技能的轮次（%s）。", pct(e.CleanRate()), pct(base.CleanRate()))})
		}
	}
	if e.Calls >= 10 && base.Calls > 0 && e.FailRate() > max(0.05, base.FailRate()*1.5) {
		warn = append(warn, evalHint{true, fmt.Sprintf("加载后工具失败率 %s，未加载技能的轮次是 %s。", pct(e.FailRate()), pct(base.FailRate()))})
	}
	if in.files && e.Used > 0 {
		info = append(info, evalHint{false, fmt.Sprintf("%d 次加载里有 %d 次用到了技能自带的文件。", n, e.Used)})
	}
	if load := e.MedianLoad(); load >= 5000 {
		info = append(info, evalHint{false, fmt.Sprintf("每次加载约 %s Token 的说明。只在部分情况下才需要的内容，可以挪到 references/ 里按需读取。", tokenNumber(load))})
	}
	if !in.edited.IsZero() {
		before, after := e.Split(in.edited)
		if before.Episodes > 0 && after.Episodes > 0 {
			info = append(info, evalHint{false, fmt.Sprintf("%s 改过 SKILL.md：改动前 %s 顺利（%d 次），改动后 %s（%d 次）。", in.edited.Local().Format("01/02"), pct(before.CleanRate()), before.Episodes, pct(after.CleanRate()), after.Episodes)})
		}
	}
	if n < 3 {
		info = append(info, evalHint{false, "加载次数少，结论仅供参考。"})
	}
	return append(warn, info...)
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", 100*v) }

func (a *App) insightEval(c *ui.Context) {
	r := a.skillEvals()
	if name := a.insights.evalSkill; name != "" {
		if e, ok := r.Lookup(name); ok {
			a.evalDetail(c, e, r.Baseline)
			return
		}
		a.insights.evalSkill = ""
	}
	a.evalList(c, r)
}

func (a *App) evalList(c *ui.Context, r insight.SkillEvalReport) {
	t := c.Theme()
	if len(r.Skills) == 0 {
		emptyState(c, "技能评测", "sparkle", "这段时间没有加载过技能",
			"Claude Code 的 Skill 调用和 /命令、Codex 读取 SKILL.md 都会算作一次加载。旧缓存要重新扫描后才能识别。", nil)
		return
	}
	loads, auto, clean, tokens := 0, 0, 0, int64(0)
	flagged := 0
	for _, e := range r.Skills {
		loads += len(e.Episodes)
		auto += e.Auto
		clean += e.Clean
		tokens += e.LoadTokens
		if hints := evalHints(e, r.Baseline, a.evalInstall(e)); len(hints) > 0 && hints[0].warn {
			flagged++
		}
	}
	figures(c, []metric{
		{label: "技能加载", value: groupDigits(loads), note: fmt.Sprintf("%d 个技能", len(r.Skills))},
		{label: "模型自己选中", value: percent(float64(auto), float64(loads)), note: "其余是手动输入 /名称"},
		{label: "加载后顺利", value: percent(float64(clean), float64(loads)), note: "未加载技能的轮次 " + pct(r.Baseline.CleanRate())},
		{label: "说明占用", value: tokenNumber(tokens), note: "加载时读入上下文 · 估算"},
		{label: "需要关注", value: groupDigits(flagged), note: "见每行的提示", warn: flagged > 0},
	})
	ui.Column(c).Label("技能评测").Children(func() {
		link := ""
		if len(r.Skills) > evalShown*2 {
			link = "全部 " + fmt.Sprint(len(r.Skills))
			if a.insights.allEvals {
				link = "收起"
			}
		}
		sectionHead(c, "按加载次数", link, func() { a.insights.allEvals = !a.insights.allEvals })
		widths := []float32{52, 64, 64, 64, 72}
		tableHead(c, "技能", []string{"加载", "自动", "顺利", "失败率", "每次加载"}, widths)
		skills := r.Skills
		if !a.insights.allEvals {
			skills = skills[:min(len(skills), evalShown*2)]
		}
		for _, e := range skills {
			in := a.evalInstall(e)
			hints := evalHints(e, r.Baseline, in)
			meta := []string{agentNames(e.Agents)}
			if len(in.lib) == 0 {
				meta = append(meta, "不在技能目录中")
			}
			tone := token(t, "ink-3")
			if len(hints) > 0 && hints[0].warn {
				meta = append(meta, firstClause(hints[0].text))
				tone = t.Warning
			}
			row := listRow(c, "eval:"+e.Name, e.Name)
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					withTip(c, ui.Text(c, e.Name).FontSize(13).SingleLine(), e.Name)
					withTip(c, ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(tone).SingleLine(), strings.Join(meta, " · "))
				})
				cell(c, groupDigits(len(e.Episodes)), widths[0], true)
				cell(c, percent(float64(e.Auto), float64(len(e.Episodes))), widths[1], true)
				cell(c, pct(e.CleanRate()), widths[2], true)
				cell(c, rateOrDash(e.Failed, e.Calls), widths[3], true)
				cell(c, tokenOrDash(e.MedianLoad()), widths[4], true)
			})
			if row.Clicked() {
				a.insights.evalSkill = e.Name
				a.insights.allEpisodes = false
			}
		}
	})
	note(c, "评测口径参考 Anthropic skill-creator 的两个维度：触发（描述能否让模型在该用时自己加载）和执行（加载后这一轮是否更顺）。这里用已发生的会话离线比较，顺利 = 加载后没有工具失败、没有被中断、下一轮开头不像纠正；对照组是同一时段没加载任何技能的轮次。这是相关性，不是因果。")
}

// firstClause shortens a verdict to its first clause for a list row.
func firstClause(s string) string {
	if i := strings.IndexAny(s, "：。"); i > 0 {
		return s[:i]
	}
	return s
}

func agentNames(agents []session.Agent) string {
	names := make([]string, len(agents))
	for i, id := range agents {
		names[i] = sessionAgentName(id)
	}
	return strings.Join(names, "、")
}

func rateOrDash(part, whole int) string {
	if whole == 0 {
		return "—"
	}
	return pct(float64(part) / float64(whole))
}

func tokenOrDash(n int64) string {
	if n == 0 {
		return "—"
	}
	return tokenNumber(n)
}

func (a *App) evalDetail(c *ui.Context, e insight.SkillEval, base insight.Window) {
	t := c.Theme()
	muted := token(t, "ink-3")
	in := a.evalInstall(e)
	ui.Column(c).Gap(6).Children(func() {
		if textButton(c, "eval-back", "arrow-left", "全部技能").Label("返回全部技能").AlignSelf(ui.Start).Clicked() {
			a.insights.evalSkill = ""
			c.Invalidate()
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, e.Name).FontSize(20).FontWeight(600).Grow(1).MinWidth(0).SingleLine()
			if len(in.lib) > 0 && outlineButton(c, "eval-open-skill", "在技能目录中打开").Clicked() {
				a.openSkill(in.lib[0])
			}
		})
		sub := agentNames(e.Agents)
		if len(in.lib) > 0 {
			sub += " · " + shortPath(a.home, in.lib[0].Path)
			if d := in.lib[0].Description; d != "" {
				withTip(c, ui.Text(c, d).FontSize(13).LineHeight(1.5).TextColor(t.TextMuted).MaxLines(3), d)
			}
		} else {
			sub += " · 不在技能目录中（内置或插件技能）"
		}
		withTip(c, ui.Text(c, sub).FontSize(12).TextColor(muted).SingleLine(), sub)
	})
	filesValue, filesNote := "—", "技能目录里只有 SKILL.md"
	if in.files {
		filesValue, filesNote = fmt.Sprintf("%d/%d", e.Used, len(e.Episodes)), "加载后调用了自带的脚本或参考"
	}
	figures(c, []metric{
		{label: "加载", value: groupDigits(len(e.Episodes)), note: fmt.Sprintf("自动 %d · 手动 %d", e.Auto, e.Manual), warn: e.Manual*2 >= len(e.Episodes) && e.Manual >= 2},
		{label: "加载后顺利", value: pct(e.CleanRate()), note: "未加载技能的轮次 " + pct(base.CleanRate())},
		{label: "工具失败率", value: rateOrDash(e.Failed, e.Calls), note: "对照 " + rateOrDash(base.Failed, base.Calls)},
		{label: "用到自带文件", value: filesValue, note: filesNote},
		{label: "每次加载", value: tokenOrDash(e.MedianLoad()), note: "说明读入上下文 · 估算"},
	})
	if hints := evalHints(e, base, in); len(hints) > 0 {
		ui.Column(c).Gap(8).Label("结论").Children(func() {
			for _, h := range hints {
				kind := feedback.Info
				if h.warn {
					kind = feedback.Warning
				}
				callout(c, kind, h.text)
			}
		})
	}
	a.evalJudge(c, e, in)
	a.evalEpisodes(c, e)
	a.evalTriggers(c, e, in)
}

func (a *App) evalEpisodes(c *ui.Context, e insight.SkillEval) {
	t := c.Theme()
	ui.Column(c).Label("加载记录").Children(func() {
		link := ""
		if len(e.Episodes) > evalShown {
			link = "全部 " + fmt.Sprint(len(e.Episodes))
			if a.insights.allEpisodes {
				link = "收起"
			}
		}
		sectionHead(c, "加载记录", link, func() { a.insights.allEpisodes = !a.insights.allEpisodes })
		widths := []float32{64, 64, 56, 72}
		tableHead(c, "会话", []string{"方式", "之后调用", "失败", "结果"}, widths)
		eps := e.Episodes
		if !a.insights.allEpisodes {
			eps = eps[:min(len(eps), evalShown)]
		}
		for i, ep := range eps {
			title := cmp.Or(strings.TrimSpace(ep.Title), sessionAgentName(ep.Agent)+" · "+cmp.Or(filepath.Base(ep.CWD), ep.SessionID))
			meta := fmt.Sprintf("%s · 第 %d 轮 · %s", ep.At.Local().Format("01/02 15:04"), ep.Turn+1, sessionAgentName(ep.Agent))
			if ep.Subagent {
				meta += " · 子 Agent"
			}
			if ep.Tokens > 0 {
				meta += " · 读入 " + tokenNumber(ep.Tokens)
			}
			row := listRow(c, fmt.Sprintf("episode:%d", i), fmt.Sprintf("%s 第 %d 轮", title, ep.Turn+1))
			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					withTip(c, ui.Text(c, title).FontSize(13).SingleLine(), title)
					ui.Text(c, meta).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
				})
				cell(c, episodeSource(ep), widths[0], true)
				calls, failed := groupDigits(ep.Calls), countOrDash(ep.Failed)
				if !session.LookupAgent(ep.Agent).ToolOutcome {
					calls, failed = "—", "—"
				}
				cell(c, calls, widths[1], true)
				cell(c, failed, widths[2], true)
				outcome := cell(c, ep.Outcome(), widths[3], true)
				if !ep.Clean() {
					outcome.TextColor(t.Warning)
				}
			})
			if row.Clicked() {
				a.openSession(string(ep.Agent) + ":" + ep.SessionID)
				a.sessions.selectedTurn = ep.TurnID
			}
		}
	})
}

func episodeSource(ep insight.Episode) string {
	switch ep.Source {
	case session.SkillByCommand:
		return "手动 /"
	case session.SkillByRead:
		return "读取"
	}
	return "自动"
}

// triggerLimit is the size skill-creator suggests for a trigger eval set.
const (
	triggerLimit    = 20
	triggerSessions = 40
)

func triggerCandidates(snap *session.Snapshot, f session.Filter, e insight.SkillEval, description string) (loaded, others []insight.TriggerCandidate, err error) {
	if snap == nil {
		return nil, nil, fmt.Errorf("没有会话")
	}
	byID := map[string]session.Session{}
	for _, s := range snap.Sessions {
		byID[string(s.Agent)+":"+s.ID] = s
	}
	// Turns that loaded the skill, newest first, by session.
	want := map[string]map[string]insight.Episode{}
	var order []string
	for _, ep := range e.Episodes {
		key := string(ep.Agent) + ":" + ep.SessionID
		if want[key] == nil {
			want[key] = map[string]insight.Episode{}
			order = append(order, key)
		}
		if _, ok := want[key][ep.TurnID]; !ok {
			want[key][ep.TurnID] = ep
		}
	}
	// Recent sessions of the same Agents supply near misses.
	var recent []session.Session
	for _, s := range snap.Sessions {
		if slices.Contains(e.Agents, s.Agent) && (f.From.IsZero() || !s.End.Before(f.From)) {
			recent = append(recent, s)
		}
	}
	slices.SortFunc(recent, func(x, y session.Session) int { return y.End.Compare(x.End) })
	for _, s := range recent {
		if len(order) >= triggerSessions {
			break
		}
		key := string(s.Agent) + ":" + s.ID
		if want[key] == nil {
			want[key] = map[string]insight.Episode{}
			order = append(order, key)
		}
	}
	read := 0
	for _, key := range order {
		s, ok := byID[key]
		if !ok || s.Path == "" || read >= triggerSessions {
			continue
		}
		read++
		tr, terr := session.ReadTranscript(context.Background(), s.Path, s.Agent)
		if terr != nil || tr == nil {
			continue
		}
		for turnID, q := range turnPrompts(s, tr) {
			cand := insight.TriggerCandidate{Query: q.text, Agent: s.Agent, SessionID: s.ID, TurnID: turnID, At: q.at}
			if ep, ok := want[key][turnID]; ok {
				cand.Loaded, cand.Manual = true, !ep.Auto()
				loaded = append(loaded, cand)
				continue
			}
			if loads := turnSkills(s, turnID); !slices.Contains(loads, e.Name) {
				if description != "" {
					cand.Score = insight.Relevance(description, q.text)
				}
				if len(loads) > 0 {
					cand.Other = loads[0]
				}
				others = append(others, cand)
			}
		}
	}
	return loaded, others, nil
}

type prompt struct {
	text string
	at   time.Time
}

// turnPrompts is each turn's first typed prompt, as an eval query.
func turnPrompts(s session.Session, tr *session.Transcript) map[string]prompt {
	out := map[string]prompt{}
	for _, e := range tr.Entries {
		if e.Kind != session.EntryUser {
			continue
		}
		i := turnAt(s, e.Start)
		if i < 0 {
			continue
		}
		id := s.Turns[i].ID
		if _, ok := out[id]; ok {
			continue
		}
		if q := insight.PromptQuery(e.Text); q != "" {
			out[id] = prompt{q, e.Start}
		}
	}
	return out
}

// turnSkills names the skills a turn loaded.
func turnSkills(s session.Session, turnID string) []string {
	var out []string
	for _, t := range s.Turns {
		if t.ID == turnID {
			for _, sk := range t.Skills {
				out = append(out, sk.Name)
			}
		}
	}
	return out
}
