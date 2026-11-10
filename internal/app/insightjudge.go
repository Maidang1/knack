package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/feedback"

	"knack/internal/evals"
	"knack/internal/insight"
	"knack/internal/session"
)

// LLM 评测 judges each load of a skill against an evaluator: criteria that a
// model drafts from the SKILL.md and the person edits. The model reads the
// turn that loaded the skill and answers every criterion with the words it
// rests on; the person marks the answers right or wrong, and the wrong ones
// are shown to the model the next time. Nothing is sent until the connection
// is switched on, and each run states what it will send first.

const (
	judgeBatch   = 20  // loads one run judges at most
	judgeWorkers = 3   // calls in flight at once
	judgeReply   = 800 // tokens allowed for a reply in the estimate
	corrections  = 8   // earlier mistakes shown to the judge
)

// judging is judgeState.busy while a run is sending.
const judging = "正在评审"

type judgeState struct {
	skill    string
	store    evals.Store
	ev       *evals.Evaluator
	verdicts map[string]evals.Verdict
	err      string
	note     string
	busy     string // what is running, in words
	plan     *judgePlan
	done     int
	total    int
	cancel   context.CancelFunc
	// run is bumped when a run ends or is stopped, so results that arrive
	// late are dropped.
	run      int
	open     map[string]bool // loads whose verdicts are shown
	adding   string
	analysis *evals.Analysis // the causes found in the failures
	causes   *causePlan      // an analysis waiting for the go-ahead
}

type causePlan struct {
	doc      evals.SkillDoc
	failures []evals.Failure
	tokens   int64
}

// judgePlan is a run that has been prepared and waits for the go-ahead.
type judgePlan struct {
	doc         evals.SkillDoc
	criteria    []evals.Criterion
	corrections []evals.Result
	items       []judgeItem
	tokens      int64
	unread      int // loads whose log could not be read
}

type judgeItem struct {
	key     string
	excerpt evals.Excerpt
}

func (a *App) judgeFor(skill string) *judgeState {
	if j := a.insights.judge; j != nil && j.skill == skill {
		return j
	}
	a.stopJudging()
	j := &judgeState{skill: skill, store: evals.Store{Dir: evals.SkillDir(a.evalsDir(), skill)}, open: map[string]bool{}}
	var err error
	if j.ev, err = j.store.Evaluator(); err != nil {
		j.err = "评估器读取失败：" + err.Error()
	}
	if j.verdicts, err = j.store.Verdicts(); err != nil {
		j.err = "评审结论读取失败：" + err.Error()
	}
	if j.analysis, err = j.store.Analysis(); err != nil {
		j.err = "失败归因读取失败：" + err.Error()
	}
	a.insights.judge = j
	return j
}

// stopJudging abandons whatever the open skill's evaluation is doing.
func (a *App) stopJudging() {
	j := a.insights.judge
	if j == nil {
		return
	}
	if j.cancel != nil {
		j.cancel()
	}
	j.run++
	j.busy, j.plan, j.causes, j.cancel = "", nil, nil, nil
}

// background runs work off the UI goroutine; without a window, as in
// tests, it runs in place.
func (a *App) background(work func()) {
	if a.win == nil {
		work()
		return
	}
	go work()
}

// current is a load's verdict against the evaluator as it is now worded.
func (j *judgeState) current(key string) (evals.Verdict, bool) {
	v, ok := j.verdicts[key]
	return v, ok && j.ev != nil && v.Evaluator == j.ev.Hash()
}

// judgeLoads are the loads a model can judge, newest first: one per turn,
// and not those inside a sub-Agent, whose steps the turn's log does not hold.
func judgeLoads(e insight.SkillEval) []insight.Episode {
	seen := map[string]bool{}
	var out []insight.Episode
	for _, ep := range e.Episodes {
		if key := evals.Key(ep); !ep.Subagent && !seen[key] {
			seen[key] = true
			out = append(out, ep)
		}
	}
	return out
}

func episodeTitle(ep insight.Episode) string {
	return cmp.Or(strings.TrimSpace(ep.Title), sessionAgentName(ep.Agent)+" · "+cmp.Or(filepath.Base(ep.CWD), ep.SessionID))
}

// skillDoc reads what a model is told about the skill.
func skillDoc(e insight.SkillEval, in evalInstall) evals.SkillDoc {
	doc := evals.SkillDoc{Name: e.Name}
	if len(in.lib) == 0 {
		return doc
	}
	doc.Description = in.lib[0].Description
	dir := in.lib[0].Path
	if filepath.Base(dir) == "SKILL.md" {
		dir = filepath.Dir(dir)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "SKILL.md")); err == nil {
		r := []rune(string(b))
		if len(r) > evals.MaxBody {
			r, doc.BodyCut = r[:evals.MaxBody], true
		}
		doc.Body = string(r)
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		rel, rerr := filepath.Rel(dir, path)
		if err != nil || rerr != nil || rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || strings.Count(rel, string(filepath.Separator)) > 2 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && rel != "SKILL.md" && len(doc.Files) < 100 {
			doc.Files = append(doc.Files, filepath.ToSlash(rel))
		}
		return nil
	})
	return doc
}

// turnAt is the index of the turn a log timestamp falls in, -1 before the
// first.
func turnAt(s session.Session, at time.Time) int {
	i, _ := slices.BinarySearchFunc(s.Turns, at, func(t session.Turn, at time.Time) int { return t.Start.Compare(at) })
	if i == len(s.Turns) || !s.Turns[i].Start.Equal(at) {
		i--
	}
	return i
}

// episodeExcerpt reads, from the raw log, the turn that loaded the skill
// and the prompt that followed it.
func episodeExcerpt(snap *session.Snapshot, ep insight.Episode) (evals.Excerpt, error) {
	if snap == nil {
		return evals.Excerpt{}, errors.New("没有会话")
	}
	for _, s := range snap.Sessions {
		if s.Agent != ep.Agent || s.ID != ep.SessionID {
			continue
		}
		turn := slices.IndexFunc(s.Turns, func(t session.Turn) bool { return t.ID == ep.TurnID })
		if s.Path == "" || turn < 0 {
			break
		}
		tr, err := session.ReadTranscript(context.Background(), s.Path, s.Agent)
		if err != nil || tr == nil {
			return evals.Excerpt{}, cmp.Or(err, errors.New("无法读取日志"))
		}
		var entries []session.Entry
		next := ""
		for _, en := range tr.Entries {
			switch i := turnAt(s, en.Start); {
			case i == turn:
				entries = append(entries, en)
			case i == turn+1 && next == "" && en.Kind == session.EntryUser:
				next = insight.PromptQuery(en.Text)
			}
		}
		if len(entries) == 0 {
			break
		}
		return evals.NewExcerpt(entries, next), nil
	}
	return evals.Excerpt{}, errors.New("日志里找不到这一轮")
}

// draftEvaluator has the model write criteria from the SKILL.md and, when
// there are any, one load that went well and one that did not.
func (a *App) draftEvaluator(e insight.SkillEval, in evalInstall) {
	j := a.judgeFor(e.Name)
	if j.busy != "" {
		return
	}
	doc := skillDoc(e, in)
	if doc.Body == "" {
		j.err = "读不到这个技能的 SKILL.md，可以先手动添加标准。"
		return
	}
	client, err := a.model.connect(a.model.config)
	if err != nil {
		j.err = err.Error()
		return
	}
	loads := judgeLoads(e)
	var picks []insight.Episode
	for _, clean := range []bool{false, true} {
		if i := slices.IndexFunc(loads, func(ep insight.Episode) bool { return ep.Clean() == clean }); i >= 0 {
			picks = append(picks, loads[i])
		}
	}
	rules := a.judgeRules(e, in)
	snap, model := a.sessions.snapshot, a.model.config.Model
	ctx, cancel := context.WithCancel(context.Background())
	j.run++
	run := j.run
	j.busy, j.err, j.note, j.plan, j.cancel = "正在阅读 SKILL.md 并起草评估标准…", "", "", nil, cancel
	a.background(func() {
		var samples []evals.Sample
		for _, ep := range picks {
			if x, err := episodeExcerpt(snap, ep); err == nil {
				samples = append(samples, evals.Sample{Outcome: ep.Outcome(), Excerpt: x})
			}
		}
		criteria, _, err := evals.Draft(ctx, client, doc, samples)
		a.post(func() {
			cancel()
			if j.run != run {
				return
			}
			j.busy, j.cancel = "", nil
			if err != nil {
				j.err = err.Error()
				return
			}
			a.setEvaluator(j, evals.Evaluator{Skill: e.Name, Created: time.Now(), Model: model, Criteria: append(rules, criteria...)})
			if doc.BodyCut {
				j.note = fmt.Sprintf("SKILL.md 很长，起草时只发送了前 %d 字。", evals.MaxBody)
			}
		})
	})
}

// judgeRules are the criteria Knack checks itself for this skill.
func (a *App) judgeRules(e insight.SkillEval, in evalInstall) []evals.Criterion {
	outcomes := slices.ContainsFunc(e.Agents, func(id session.Agent) bool { return session.LookupAgent(id).ToolOutcome })
	return evals.Rules(outcomes, in.files)
}

func (a *App) setEvaluator(j *judgeState, ev evals.Evaluator) {
	if err := j.store.SaveEvaluator(ev); err != nil {
		j.err = "保存失败：" + err.Error()
		return
	}
	j.ev = &ev
}

// planJudging reads the turns that still lack a verdict and works out what
// judging them would send, for the person to confirm.
func (a *App) planJudging(e insight.SkillEval, in evalInstall) {
	j := a.judgeFor(e.Name)
	if j.ev == nil || j.busy != "" {
		return
	}
	plan := &judgePlan{doc: evals.SkillDoc{Name: e.Name}, criteria: j.ev.Judged(), corrections: evals.Corrections(j.verdicts, corrections)}
	if len(in.lib) > 0 {
		plan.doc.Description = in.lib[0].Description
	}
	var pending []insight.Episode
	for _, ep := range judgeLoads(e) {
		if _, ok := j.current(evals.Key(ep)); !ok && len(pending) < judgeBatch {
			pending = append(pending, ep)
		}
	}
	snap := a.sessions.snapshot
	j.run++
	run := j.run
	j.busy, j.err, j.plan = "正在读取会话记录…", "", nil
	a.background(func() {
		for _, ep := range pending {
			x, err := episodeExcerpt(snap, ep)
			if err != nil {
				plan.unread++
				continue
			}
			req := evals.JudgeRequest(plan.doc, plan.criteria, plan.corrections, x)
			plan.tokens += int64(session.EstimateTokens(req.System+req.Prompt)) + judgeReply
			plan.items = append(plan.items, judgeItem{evals.Key(ep), x})
		}
		a.post(func() {
			if j.run != run {
				return
			}
			j.busy = ""
			if len(plan.items) == 0 {
				j.err = "这些加载的会话日志已经读不到了。"
				return
			}
			j.plan = plan
		})
	})
}

// runJudging sends the confirmed plan. The first failure stops the run:
// a wrong key or an exhausted quota would fail every call after it too.
func (a *App) runJudging() {
	j := a.insights.judge
	if j == nil || j.plan == nil || j.ev == nil {
		return
	}
	plan := j.plan
	client, err := a.model.connect(a.model.config)
	if err != nil {
		j.err, j.plan = err.Error(), nil
		return
	}
	hash, model := j.ev.Hash(), a.model.config.Model
	ctx, cancel := context.WithCancel(context.Background())
	j.run++
	run := j.run
	j.plan, j.err, j.busy, j.done, j.total, j.cancel = nil, "", judging, 0, len(plan.items), cancel
	judge := func(it judgeItem) {
		results, reply, err := evals.Judge(ctx, client, plan.doc, plan.criteria, plan.corrections, it.excerpt)
		a.post(func() {
			if j.run != run {
				return
			}
			switch {
			case err == nil:
				j.verdicts[it.key] = evals.Verdict{Evaluator: hash, Model: model, At: time.Now(), Results: results,
					Truncated: it.excerpt.Truncated, Input: reply.Input, Output: reply.Output}
				if err := j.store.SaveVerdicts(j.verdicts); err != nil && j.err == "" {
					j.err = "保存失败：" + err.Error()
				}
			case j.err == "" && ctx.Err() == nil:
				j.err = "评审停下了：" + err.Error()
				cancel()
			}
			if j.done++; j.done == j.total {
				cancel()
				j.busy, j.cancel = "", nil
			}
		})
	}
	if a.win == nil {
		for _, it := range plan.items {
			judge(it)
		}
		return
	}
	go func() {
		slots := make(chan struct{}, judgeWorkers)
		for _, it := range plan.items {
			slots <- struct{}{}
			go func() {
				defer func() { <-slots }()
				judge(it)
			}()
		}
	}()
}

func (a *App) saveVerdicts(j *judgeState) {
	if err := j.store.SaveVerdicts(j.verdicts); err != nil {
		j.err = "保存失败：" + err.Error()
	}
}

// judgeSummary is the open skill's standing against its evaluator.
type judgeSummary struct {
	loads, judged int
	stale         int // judged against an earlier wording
	passed        int // loads with no failed criterion
	tally         evals.Tally
	byCriterion   map[string]*evals.Tally
}

func (j *judgeState) summary(e insight.SkillEval) judgeSummary {
	s := judgeSummary{byCriterion: map[string]*evals.Tally{}}
	if j.ev == nil {
		return s
	}
	count := func(id, verdict string) {
		t := s.byCriterion[id]
		if t == nil {
			t = &evals.Tally{}
			s.byCriterion[id] = t
		}
		switch verdict {
		case evals.Pass:
			t.Pass++
		case evals.Fail:
			t.Fail++
		default:
			t.NA++
		}
	}
	for _, ep := range judgeLoads(e) {
		s.loads++
		failed := false
		for _, c := range j.ev.Criteria {
			if c.Kind == evals.KindRule {
				v := evals.RuleVerdict(c, ep)
				count(c.ID, v)
				failed = failed || v == evals.Fail
			}
		}
		v, ok := j.current(evals.Key(ep))
		if !ok {
			if _, old := j.verdicts[evals.Key(ep)]; old {
				s.stale++
			}
			continue
		}
		s.judged++
		t := v.Count()
		s.tally.Pass, s.tally.Fail, s.tally.NA = s.tally.Pass+t.Pass, s.tally.Fail+t.Fail, s.tally.NA+t.NA
		s.tally.Reviewed, s.tally.Agreed = s.tally.Reviewed+t.Reviewed, s.tally.Agreed+t.Agreed
		for _, r := range v.Results {
			count(r.ID, r.Final())
		}
		if !failed && t.Fail == 0 {
			s.passed++
		}
	}
	return s
}

func (a *App) evalJudge(c *ui.Context, e insight.SkillEval, in evalInstall) {
	j := a.judgeFor(e.Name)
	ready := a.model.config.Ready()
	ui.Column(c).Gap(10).Label("LLM 评测").Children(func() {
		sectionHead(c, "LLM 评测", "", nil)
		if j.ev == nil {
			note(c, "让模型对照评估标准阅读每一次加载的会话记录，逐条给出通过或未通过，并摘出依据的原文。标准由模型根据 SKILL.md 起草，你可以改写、删除或添加。")
		}
		if !ready {
			callout(c, feedback.Info, "还没有连接模型。连接并开启发送之前，不会向任何服务发送内容。")
			if outlineButton(c, "judge-connect", "连接模型").Clicked() {
				a.settingsTab = settingsModelTab
				a.openSettings()
			}
		}
		if j.err != "" {
			callout(c, feedback.Warning, j.err)
		}
		if j.note != "" {
			callout(c, feedback.Info, j.note)
		}
		if j.ev == nil {
			a.judgeStart(c, j, e, in, ready)
			return
		}
		s := j.summary(e)
		agree, agreeNote := "—", "逐条核对结论后显示"
		if s.tally.Reviewed > 0 {
			agree, agreeNote = fmt.Sprintf("%d/%d", s.tally.Agreed, s.tally.Reviewed), "你核对过的结论里判对的"
		}
		passed, passedNote := "—", "评审后显示"
		if s.judged > 0 {
			passed, passedNote = fmt.Sprintf("%d/%d", s.passed, s.judged), "没有一条标准未通过的加载"
		}
		figures(c, []metric{
			{label: "评估器", value: fmt.Sprintf("%d 条", len(j.ev.Criteria)), note: fmt.Sprintf("模型判断 %d · 规则 %d", len(j.ev.Judged()), len(j.ev.Criteria)-len(j.ev.Judged()))},
			{label: "已评审", value: fmt.Sprintf("%d/%d", s.judged, s.loads), note: "可评审的加载", warn: s.stale > 0},
			{label: "全部通过", value: passed, note: passedNote},
			{label: "与你一致", value: agree, note: agreeNote, warn: s.tally.Reviewed >= 5 && s.tally.Agreed*5 < s.tally.Reviewed*4},
		})
		a.judgeCriteria(c, j, s)
		a.judgeActions(c, j, e, in, s, ready)
		a.judgeResults(c, j, e)
		a.judgeCauses(c, j, e, in, ready)
	})
}

// judgeStart is the section before there is an evaluator.
func (a *App) judgeStart(c *ui.Context, j *judgeState, e insight.SkillEval, in evalInstall, ready bool) {
	t := c.Theme()
	if j.busy != "" {
		a.judgeBusy(c, j)
		return
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if len(in.lib) > 0 {
			if primaryButton(c, "judge-draft", "sparkle", "生成评估器").Disabled(!ready).Clicked() {
				a.draftEvaluator(e, in)
			}
			if ready {
				ui.Text(c, "会把 SKILL.md 和最多 2 段这个技能的真实会话发给 "+a.model.config.Host()).FontSize(12).TextColor(token(t, "ink-3")).Grow(1).MinWidth(0).SingleLine()
			}
		} else {
			ui.Text(c, "这个技能不在技能目录中，读不到 SKILL.md，不能自动起草。").FontSize(12.5).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		}
		if outlineButton(c, "judge-blank", "手动建立").Clicked() {
			a.setEvaluator(j, evals.Evaluator{Skill: e.Name, Created: time.Now(), Criteria: a.judgeRules(e, in)})
		}
	})
}

func (a *App) judgeBusy(c *ui.Context, j *judgeState) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		text := j.busy
		if j.busy == judging {
			text = fmt.Sprintf("正在评审 %d/%d…", j.done, j.total)
		}
		ui.Text(c, text).FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(0)
		if j.cancel != nil && outlineButton(c, "judge-stop", "停止").Clicked() {
			a.stopJudging()
		}
	})
}

// judgeCriteria lists the evaluator for editing, each criterion with how
// the loads fared against it.
func (a *App) judgeCriteria(c *ui.Context, j *judgeState, s judgeSummary) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Column(c).Label("评估标准").Children(func() {
		tableHead(c, "评估标准", []string{"通过", ""}, []float32{64, 52})
		remove := -1
		for i := range j.ev.Criteria {
			crit := &j.ev.Criteria[i]
			ui.Row(c.Key("criterion:"+crit.ID)).Padding(6, 0).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				kind := "模型"
				if crit.Kind == evals.KindRule {
					kind = "规则"
				}
				ui.Text(c, kind).FontSize(11.5).TextColor(muted).Width(28).Shrink(0)
				if crit.Kind == evals.KindRule {
					withTip(c, ui.Text(c, crit.Text).FontSize(13).Grow(1).MinWidth(0).SingleLine(), crit.Text)
				} else if textField(c, &crit.Text, "标准 "+crit.ID, "").Disabled(j.busy != "").Changed() {
					// Rewording a criterion makes its verdicts stale.
					j.plan = nil
					a.setEvaluator(j, *j.ev)
				}
				rate := "—"
				if tl := s.byCriterion[crit.ID]; tl != nil && tl.Pass+tl.Fail > 0 {
					rate = fmt.Sprintf("%d/%d", tl.Pass, tl.Pass+tl.Fail)
				}
				cell(c, rate, 64, true)
				if outlineButton(c, "criterion-remove", "删除").Label("删除标准 " + crit.ID).Width(52).Disabled(j.busy != "").Clicked() {
					remove = i
				}
			})
		}
		if remove >= 0 {
			ev := *j.ev
			ev.Criteria = slices.Delete(slices.Clone(ev.Criteria), remove, remove+1)
			j.plan = nil
			a.setEvaluator(j, ev)
		}
		ui.Row(c).Padding(8, 0, 0, 0).Gap(10).AlignItems(ui.Center).Children(func() {
			textField(c, &j.adding, "新的评估标准", "添加一条标准，写成做对时的样子，例如：修改之前先读取了原文件")
			if outlineButton(c, "criterion-add", "添加").Disabled(strings.TrimSpace(j.adding) == "" || j.busy != "").Clicked() {
				ev := *j.ev
				ev.Criteria = append(slices.Clone(ev.Criteria), evals.Criterion{ID: evals.NewID(ev.Criteria), Text: strings.TrimSpace(j.adding), Kind: evals.KindLLM})
				j.adding, j.plan = "", nil
				a.setEvaluator(j, ev)
			}
		})
	})
}

// judgeActions is the one next step: plan a run, confirm it, or watch it.
func (a *App) judgeActions(c *ui.Context, j *judgeState, e insight.SkillEval, in evalInstall, s judgeSummary, ready bool) {
	t := c.Theme()
	switch {
	case j.busy != "":
		a.judgeBusy(c, j)
		return
	case j.plan != nil:
		text := fmt.Sprintf("将把 %d 次加载的会话记录发给 %s，由 %s 评审，约 %s Token（估算）。", len(j.plan.items), a.model.config.Host(), a.model.config.Model, tokenNumber(j.plan.tokens))
		if j.plan.unread > 0 {
			text += fmt.Sprintf("另有 %d 次的日志已经读不到，不在其中。", j.plan.unread)
		}
		callout(c, feedback.Info, text)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if primaryButton(c, "judge-run", "", "开始评审").Clicked() {
				a.runJudging()
			}
			if outlineButton(c, "judge-cancel", "取消").Clicked() {
				j.plan = nil
			}
		})
		return
	}
	pending := min(s.loads-s.judged, judgeBatch)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		label := fmt.Sprintf("评审 %d 次加载", pending)
		if primaryButton(c, "judge-plan", "", label).Disabled(!ready || pending == 0 || len(j.ev.Judged()) == 0).Clicked() {
			a.planJudging(e, in)
		}
		var hint string
		switch {
		case len(j.ev.Judged()) == 0:
			hint = "还没有需要模型判断的标准"
		case s.stale > 0:
			hint = fmt.Sprintf("标准改动过，%d 次加载需要重新评审", s.stale)
		case pending == 0 && s.loads > 0:
			hint = "都评审过了"
		case s.loads-s.judged > judgeBatch:
			hint = fmt.Sprintf("每次最多 %d 次，从最近的开始", judgeBatch)
		}
		ui.Text(c, hint).FontSize(12).TextColor(token(t, "ink-3")).Grow(1).MinWidth(0).SingleLine()
		if len(in.lib) > 0 && withTip(c, outlineButton(c, "judge-redraft", "重新起草").Disabled(!ready), "让模型重写评估标准，替换现有的").Clicked() {
			a.draftEvaluator(e, in)
		}
	})
}

// judgeResults lists the judged loads; a load opens into its verdicts.
func (a *App) judgeResults(c *ui.Context, j *judgeState, e insight.SkillEval) {
	t := c.Theme()
	var judged []insight.Episode
	for _, ep := range judgeLoads(e) {
		if _, ok := j.current(evals.Key(ep)); ok {
			judged = append(judged, ep)
		}
	}
	if len(judged) == 0 {
		return
	}
	ui.Column(c).Label("评审结论").Children(func() {
		widths := []float32{72, 72}
		tableHead(c, "评审结论", []string{"通过", "核对"}, widths)
		for _, ep := range judged {
			key := evals.Key(ep)
			v, _ := j.current(key)
			tally := v.Count()
			pass, total := tally.Pass, tally.Pass+tally.Fail
			for _, crit := range j.ev.Criteria {
				if crit.Kind != evals.KindRule {
					continue
				}
				switch evals.RuleVerdict(crit, ep) {
				case evals.Pass:
					pass, total = pass+1, total+1
				case evals.Fail:
					total++
				}
			}
			title := episodeTitle(ep)
			meta := fmt.Sprintf("%s · 第 %d 轮 · %s", ep.At.Local().Format("01/02 15:04"), ep.Turn+1, sessionAgentName(ep.Agent))
			if v.Truncated {
				meta += " · 这一轮过长，发送时有省略"
			}
			row := listRow(c, "verdict:"+key, fmt.Sprintf("%s 第 %d 轮的评审结论", title, ep.Turn+1))
			row.Children(func() {
				icon := "chevron-right"
				if j.open[key] {
					icon = "chevron-down"
				}
				ui.Icon(c, glyph(icon)).FontSize(12).TextColor(token(t, "ink-3")).Shrink(0)
				ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
					withTip(c, ui.Text(c, title).FontSize(13).SingleLine(), title)
					ui.Text(c, meta).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
				})
				score := cell(c, fmt.Sprintf("%d/%d", pass, total), widths[0], true)
				if pass < total {
					score.TextColor(t.Warning)
				}
				cell(c, fmt.Sprintf("%d/%d", tally.Reviewed, len(v.Results)), widths[1], true)
			})
			if row.Clicked() {
				j.open[key] = !j.open[key]
			}
			if j.open[key] {
				a.judgeVerdict(c, j, ep, key)
			}
		}
	})
	note(c, "引文由 Knack 在会话记录里核对过；对不上的会标明，这样的结论不要直接采信。你标为判错的结论会在下次评审时告诉模型。")
}

var verdictWords = map[string]string{evals.Pass: "通过", evals.Fail: "未通过", evals.NA: "不适用"}

// judgeVerdict shows one load criterion by criterion, with what each
// judgement rests on and the person's word on it.
func (a *App) judgeVerdict(c *ui.Context, j *judgeState, ep insight.Episode, key string) {
	t := c.Theme()
	muted := token(t, "ink-3")
	v := j.verdicts[key]
	line := func(verdict, text string, body func(), side func()) {
		icon, tone := "skip", muted
		switch verdict {
		case evals.Pass:
			icon, tone = "check", t.TextMuted
		case evals.Fail:
			icon, tone = "alert", t.Warning
		}
		ui.Row(c).Padding(8, 0, 8, 22).Gap(10).AlignItems(ui.Start).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Icon(c, glyph(icon)).FontSize(13).TextColor(tone).Shrink(0).Margin(2, 0, 0, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, verdictWords[verdict]+" · "+text).FontSize(13).LineHeight(1.45)
				body()
			})
			side()
		})
	}
	changed := false
	ui.Column(c.Key("verdict-body:" + key)).Children(func() {
		for _, crit := range j.ev.Criteria {
			if crit.Kind == evals.KindRule {
				ui.Column(c.Key("rule:" + crit.ID)).Children(func() {
					line(evals.RuleVerdict(crit, ep), crit.Text, func() {}, func() {
						ui.Text(c, "规则").FontSize(11.5).TextColor(muted).Shrink(0)
					})
				})
				continue
			}
			i := slices.IndexFunc(v.Results, func(r evals.Result) bool { return r.ID == crit.ID })
			if i < 0 {
				continue
			}
			r := &v.Results[i]
			ui.Column(c.Key("result:" + crit.ID)).Children(func() {
				line(r.Final(), crit.Text, func() {
					reason := r.Reason
					if r.Review == evals.ReviewWrong {
						reason = "模型判为" + verdictWords[r.Verdict] + "，你标为判错。" + reason
					}
					if reason != "" {
						ui.Text(c, reason).FontSize(12).LineHeight(1.5).TextColor(t.TextMuted)
					}
					switch {
					case r.Quote != "" && r.Verified:
						withTip(c, ui.Text(c, fmt.Sprintf("第 %d 步：“%s”", r.Step, r.Quote)).FontSize(12).LineHeight(1.5).TextColor(muted).MaxLines(3), r.Quote)
					case r.Quote != "":
						withTip(c, ui.Text(c, "引文在这一轮的记录里没有找到：“"+r.Quote+"”").FontSize(12).LineHeight(1.5).TextColor(t.Warning).MaxLines(3), r.Quote)
					}
				}, func() {
					review := slices.Index([]string{"", evals.ReviewOK, evals.ReviewWrong}, r.Review)
					if segmented(c, "核对 "+crit.ID, &review, "未核对", "判对了", "判错了") {
						r.Review = []string{"", evals.ReviewOK, evals.ReviewWrong}[review]
						changed = true
					}
				})
			})
		}
		ui.Row(c).Padding(8, 0, 8, 22).Gap(10).AlignItems(ui.Center).Children(func() {
			if outlineButton(c, "verdict-open", "打开这一轮").Clicked() {
				a.openSession(string(ep.Agent) + ":" + ep.SessionID)
				a.sessions.selectedTurn = ep.TurnID
			}
			if outlineButton(c, "verdict-all-ok", "其余都判对了").Clicked() {
				for i := range v.Results {
					if v.Results[i].Review == "" {
						v.Results[i].Review, changed = evals.ReviewOK, true
					}
				}
			}
		})
	})
	if changed {
		j.verdicts[key] = v
		a.saveVerdicts(j)
	}
}

// planCauses gathers the failed verdicts and works out what asking for
// their causes sends, for the person to confirm.
func (a *App) planCauses(e insight.SkillEval, in evalInstall) {
	j := a.judgeFor(e.Name)
	if j.ev == nil || j.busy != "" {
		return
	}
	fails := evals.Failures(*j.ev, j.verdicts)
	if len(fails) == 0 {
		return
	}
	fails = fails[:min(len(fails), evals.MaxFailures)]
	doc := skillDoc(e, in)
	req := evals.CausesRequest(doc, fails)
	j.causes, j.err = &causePlan{doc: doc, failures: fails, tokens: int64(session.EstimateTokens(req.System+req.Prompt)) + 2000}, ""
}

// runCauses sends the confirmed analysis.
func (a *App) runCauses() {
	j := a.insights.judge
	if j == nil || j.causes == nil || j.ev == nil {
		return
	}
	plan := j.causes
	client, err := a.model.connect(a.model.config)
	if err != nil {
		j.err, j.causes = err.Error(), nil
		return
	}
	hash, model := j.ev.Hash(), a.model.config.Model
	ctx, cancel := context.WithCancel(context.Background())
	j.run++
	run := j.run
	j.causes, j.err, j.busy, j.cancel = nil, "", "正在归纳失败原因…", cancel
	a.background(func() {
		causes, reply, err := evals.Analyze(ctx, client, plan.doc, plan.failures)
		a.post(func() {
			cancel()
			if j.run != run {
				return
			}
			j.busy, j.cancel = "", nil
			if err != nil {
				j.err = err.Error()
				return
			}
			an := evals.Analysis{Evaluator: hash, Failures: len(plan.failures), Model: model, At: time.Now(), Causes: causes, Input: reply.Input, Output: reply.Output}
			if err := j.store.SaveAnalysis(an); err != nil {
				j.err = "保存失败：" + err.Error()
			}
			j.analysis = &an
		})
	})
}

// judgeCauses groups the failed verdicts into causes, each with what to
// change in the SKILL.md.
func (a *App) judgeCauses(c *ui.Context, j *judgeState, e insight.SkillEval, in evalInstall, ready bool) {
	fails := evals.Failures(*j.ev, j.verdicts)
	if len(fails) == 0 && j.analysis == nil {
		return
	}
	t := c.Theme()
	muted := token(t, "ink-3")
	n := min(len(fails), evals.MaxFailures)
	ui.Column(c).Gap(8).Label("失败归因").Children(func() {
		sectionHead(c, "失败归因", "", nil)
		switch {
		case j.causes != nil:
			callout(c, feedback.Info, fmt.Sprintf("将把 SKILL.md 和 %d 条未通过结论的理由、引文发给 %s，由 %s 归纳原因，约 %s Token（估算）。",
				len(j.causes.failures), a.model.config.Host(), a.model.config.Model, tokenNumber(j.causes.tokens)))
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if primaryButton(c, "causes-run", "", "开始归纳").Clicked() {
					a.runCauses()
				}
				if outlineButton(c, "causes-cancel", "取消").Clicked() {
					j.causes = nil
				}
			})
		case j.busy == "" && n > 0:
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				label := fmt.Sprintf("归纳 %d 条未通过的原因", n)
				if j.analysis != nil {
					label = "重新归纳"
				}
				if outlineButton(c, "causes-plan", label).Disabled(!ready).Clicked() {
					a.planCauses(e, in)
				}
				hint := "把同一原因造成的失败归到一起，并给出修改 SKILL.md 的建议"
				if an := j.analysis; an != nil && (an.Evaluator != j.ev.Hash() || an.Failures != n) {
					hint = "评审结论有变化，下面的归因可能已经过时"
				}
				ui.Text(c, hint).FontSize(12).TextColor(muted).Grow(1).MinWidth(0).SingleLine()
			})
		}
		if j.analysis == nil {
			return
		}
		if n == 0 {
			ui.Text(c, "现在没有未通过的结论，下面是之前的归因").FontSize(12).TextColor(muted)
		}
		titles := map[string]string{}
		for _, ep := range judgeLoads(e) {
			titles[evals.Key(ep)] = fmt.Sprintf("%s 第 %d 轮", episodeTitle(ep), ep.Turn+1)
		}
		for i, cause := range j.analysis.Causes {
			ui.Column(c.Key(fmt.Sprintf("cause:%d", i))).Padding(10, 12).Gap(4).Radius(8).Border(1, t.Border).Children(func() {
				ui.Text(c, cause.Title).FontSize(13.5).FontWeight(600)
				var loads []string
				for _, key := range cause.Loads {
					if title, ok := titles[key]; ok && len(loads) < 3 {
						loads = append(loads, title)
					}
				}
				meta := fmt.Sprintf("%d 次加载 · %d 条结论", len(cause.Loads), cause.Count)
				if len(loads) > 0 {
					meta += " · " + strings.Join(loads, "、")
				}
				withTip(c, ui.Text(c, meta).FontSize(11.5).TextColor(muted).SingleLine(), meta)
				if cause.Detail != "" {
					ui.Text(c, cause.Detail).FontSize(12.5).LineHeight(1.5).TextColor(t.TextMuted)
				}
				if cause.Fix != "" {
					ui.Text(c, "建议："+cause.Fix).FontSize(12.5).LineHeight(1.5)
				}
			})
		}
		note(c, fmt.Sprintf("%s 由 %s 归纳。建议只是起点，改完 SKILL.md 后可以重新评审最近的加载，看未通过的是否变少。", j.analysis.At.Local().Format("01/02 15:04"), j.analysis.Model))
	})
}
