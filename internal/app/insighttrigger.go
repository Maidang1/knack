package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/feedback"

	"knack/internal/evals"
	"knack/internal/insight"
	"knack/internal/session"
)

// 触发评测 checks the other half of a skill: whether its description makes
// the Agent load it for the right requests. The set of requests comes from
// the sessions (prompts that loaded the skill and near misses), from the
// model, and from the person. A simulated run shows the model the skill list
// the Agent would see and asks what it would load for each request; the
// person can reword the description, run again, and have the model propose
// a rewording from what went wrong.

const (
	synthCount     = 8   // requests of each kind the model writes at a time
	triggerWorkers = 3   // calls in flight at once
	routeReply     = 120 // tokens allowed for a reply in the estimate
)

// routing is triggerState.busy while a run is sending.
const routing = "正在模拟"

type triggerState struct {
	skill  string
	store  evals.Store
	set    evals.Triggers
	err    string
	busy   string
	plan   *triggerPlan
	done   int
	total  int
	cancel context.CancelFunc
	run    int // bumped when work ends or is stopped, so late results are dropped
	// adding is a request the person is writing, addNo whether it should
	// not load the skill.
	adding  string
	addNo   int
	draft   string // the description to try
	why     string // the model's account of its last rewording
	written string // where the set was last exported
}

// triggerPlan is a run waiting for the go-ahead.
type triggerPlan struct {
	description string
	peers       []evals.Peer
	cases       []evals.Case
	tokens      int64
}

func (a *App) triggerFor(skill, description string) *triggerState {
	if s := a.insights.trigger; s != nil && s.skill == skill {
		return s
	}
	a.stopTriggers()
	s := &triggerState{skill: skill, store: evals.Store{Dir: evals.SkillDir(a.evalsDir(), skill)}, draft: description}
	var err error
	if s.set, err = s.store.Triggers(); err != nil {
		s.err = "触发评测集读取失败：" + err.Error()
	}
	a.insights.trigger = s
	return s
}

func (a *App) stopTriggers() {
	s := a.insights.trigger
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.run++
	s.busy, s.plan, s.cancel = "", nil, nil
}

func (a *App) saveTriggers(s *triggerState) {
	if err := s.store.SaveTriggers(s.set); err != nil {
		s.err = "保存失败：" + err.Error()
	}
}

// routeName is the skill's name on the Agent's skill list.
func routeName(e insight.SkillEval, in evalInstall) string {
	if len(in.lib) > 0 {
		return in.lib[0].Name
	}
	return e.Name
}

// skillPeers are the other skills the Agents that loaded this one list
// beside it, each once.
func (a *App) skillPeers(e insight.SkillEval, name string) []evals.Peer {
	seen := map[string]bool{name: true}
	var out []evals.Peer
	for _, h := range a.skillInsights().Skills {
		if slices.Contains(e.Agents, h.Agent) && !seen[h.Name] {
			seen[h.Name] = true
			out = append(out, evals.Peer{Name: h.Name, Description: h.Description})
		}
	}
	return out
}

// pickTriggers reads, in the background, the prompts of the turns that
// loaded the skill and of recent turns that did not, and adds the closest
// to the set.
func (a *App) pickTriggers(e insight.SkillEval, description string) {
	s := a.insights.trigger
	if s == nil || s.busy != "" {
		return
	}
	snap := a.sessions.snapshot
	f := overviewFilter(a.sessions.period, a.sessionAgentFilter(), time.Now())
	s.run++
	run := s.run
	s.busy, s.err = "正在读取会话里的提示词…", ""
	a.background(func() {
		loaded, others, err := triggerCandidates(snap, f, e, description)
		a.post(func() {
			if s.run != run {
				return
			}
			s.busy = ""
			if err != nil {
				s.err = err.Error()
				return
			}
			var cases []evals.Case
			for _, cand := range insight.TriggerSet(loaded, others, triggerLimit) {
				src := "会话里加载了这个技能"
				switch {
				case cand.Manual:
					src = "会话里手动输入了 /" + e.Name
				case !cand.Loaded && cand.Score > 0:
					src = fmt.Sprintf("会话里没有加载 · 与描述重合 %s", pct(cand.Score))
				case !cand.Loaded:
					src = "会话里加载了 " + cand.Other
				}
				cases = append(cases, evals.NewCase(cand.Query, cand.ShouldTrigger(), evals.FromSession, src+" · "+cand.At.Local().Format("01/02 15:04")))
			}
			if s.set.Add(cases...) == 0 {
				s.err = "会话里没有找到新的提示词。"
				return
			}
			a.saveTriggers(s)
		})
	})
}

// synthTriggers has the model write requests of both kinds.
func (a *App) synthTriggers(e insight.SkillEval, in evalInstall) {
	s := a.insights.trigger
	if s == nil || s.busy != "" {
		return
	}
	client, err := a.model.connect(a.model.config)
	if err != nil {
		s.err = err.Error()
		return
	}
	doc := skillDoc(e, in)
	doc.Name = routeName(e, in)
	peers := a.skillPeers(e, doc.Name)
	have := slices.Clone(s.set.Cases)
	ctx, cancel := context.WithCancel(context.Background())
	s.run++
	run := s.run
	s.busy, s.err, s.cancel = "正在让模型编写请求…", "", cancel
	a.background(func() {
		cases, _, err := evals.Synthesize(ctx, client, doc, peers, have, synthCount)
		a.post(func() {
			cancel()
			if s.run != run {
				return
			}
			s.busy, s.cancel = "", nil
			if err != nil {
				s.err = err.Error()
				return
			}
			s.set.Add(cases...)
			a.saveTriggers(s)
		})
	})
}

// planTriggers works out what a run with the draft description sends.
func (a *App) planTriggers(e insight.SkillEval, in evalInstall) {
	s := a.insights.trigger
	if s == nil || s.busy != "" {
		return
	}
	name := routeName(e, in)
	plan := &triggerPlan{description: strings.TrimSpace(s.draft), peers: a.skillPeers(e, name), cases: s.set.Active()}
	list := append(slices.Clone(plan.peers), evals.Peer{Name: name, Description: plan.description})
	for _, c := range plan.cases {
		req := evals.RouteRequest(list, c.Query)
		plan.tokens += int64(session.EstimateTokens(req.System+req.Prompt)) + routeReply
	}
	s.plan, s.err = plan, ""
}

// runTriggers sends the confirmed plan, one request per case. The first
// failure stops the run; what came back before it is kept.
func (a *App) runTriggers(name string) {
	s := a.insights.trigger
	if s == nil || s.plan == nil {
		return
	}
	plan := s.plan
	client, err := a.model.connect(a.model.config)
	if err != nil {
		s.err, s.plan = err.Error(), nil
		return
	}
	list := append(slices.Clone(plan.peers), evals.Peer{Name: name, Description: plan.description})
	result := evals.Run{Description: plan.description, Model: a.model.config.Model, At: time.Now(), Peers: len(plan.peers), Trials: map[string]evals.Trial{}}
	ctx, cancel := context.WithCancel(context.Background())
	s.run++
	run := s.run
	s.plan, s.err, s.busy, s.done, s.total, s.cancel = nil, "", routing, 0, len(plan.cases), cancel
	finish := func() {
		cancel()
		s.busy, s.cancel = "", nil
		if len(result.Trials) > 0 {
			s.set.AddRun(result)
			a.saveTriggers(s)
		}
	}
	route := func(c evals.Case) {
		trial, reply, err := evals.Route(ctx, client, list, c.Query)
		a.post(func() {
			if s.run != run {
				return
			}
			switch {
			case err == nil:
				result.Trials[c.ID] = trial
				result.Input, result.Output = result.Input+reply.Input, result.Output+reply.Output
			case s.err == "" && ctx.Err() == nil:
				s.err = "模拟停下了：" + err.Error()
				cancel()
			}
			if s.done++; s.done == s.total {
				finish()
			}
		})
	}
	if a.win == nil {
		for _, c := range plan.cases {
			route(c)
		}
		return
	}
	go func() {
		slots := make(chan struct{}, triggerWorkers)
		for _, c := range plan.cases {
			slots <- struct{}{}
			go func() {
				defer func() { <-slots }()
				route(c)
			}()
		}
	}()
}

// reviseDescription has the model reword the description run was made
// with, from the cases it got wrong.
func (a *App) reviseDescription(e insight.SkillEval, in evalInstall, run evals.Run) {
	s := a.insights.trigger
	if s == nil || s.busy != "" {
		return
	}
	client, err := a.model.connect(a.model.config)
	if err != nil {
		s.err = err.Error()
		return
	}
	doc := skillDoc(e, in)
	doc.Name = routeName(e, in)
	peers := a.skillPeers(e, doc.Name)
	misses := run.Misses(doc.Name, s.set.Cases)
	correct := run.Score(doc.Name, s.set.Cases).Correct()
	ctx, cancel := context.WithCancel(context.Background())
	s.run++
	gen := s.run
	s.busy, s.err, s.cancel = "正在让模型改写描述…", "", cancel
	a.background(func() {
		desc, why, _, err := evals.Revise(ctx, client, doc, peers, run.Description, misses, correct)
		a.post(func() {
			cancel()
			if s.run != gen {
				return
			}
			s.busy, s.cancel = "", nil
			if err != nil {
				s.err = err.Error()
				return
			}
			s.draft, s.why = desc, why
		})
	})
}

func (a *App) exportTriggers(s *triggerState) {
	var cases []insight.TriggerCase
	for _, c := range s.set.Active() {
		cases = append(cases, insight.TriggerCase{Query: c.Query, ShouldTrigger: c.Should})
	}
	path := a.evalSetPath(s.skill)
	if err := insight.WriteTriggerSet(path, cases); err != nil {
		s.err = "导出失败：" + err.Error()
		return
	}
	s.written = path
}

func (a *App) evalSetPath(skill string) string {
	return filepath.Join(evals.SkillDir(a.evalsDir(), skill), "eval_set.json")
}

func (a *App) evalTriggers(c *ui.Context, e insight.SkillEval, in evalInstall) {
	t := c.Theme()
	description := ""
	if len(in.lib) > 0 {
		description = in.lib[0].Description
	}
	s := a.triggerFor(e.Name, description)
	name := routeName(e, in)
	ready := a.model.config.Ready()
	ui.Column(c).Gap(10).Label("触发评测").Children(func() {
		sectionHead(c, "触发评测", "", nil)
		note(c, "检查描述能不能让 Agent 在该用的时候加载这个技能、不该用的时候不加载。评测集里的请求可以从会话里挑、让模型编写，或者自己写；模拟时把 Agent 能看到的技能列表和每条请求发给模型，看它会加载哪个技能。")
		if s.err != "" {
			callout(c, feedback.Warning, s.err)
		}
		last, hasRun := s.set.Last()
		if len(s.set.Cases) > 0 {
			a.triggerCases(c, s, name, last, hasRun)
		}
		a.triggerAdd(c, s)
		if s.busy != "" && s.busy != routing {
			a.triggerBusy(c, s)
		} else {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if outlineButton(c, "trigger-pick", "从会话中挑选").Disabled(s.busy != "").Clicked() {
					a.pickTriggers(e, description)
				}
				if outlineButton(c, "trigger-synth", fmt.Sprintf("让模型编写 %d 条", 2*synthCount)).Disabled(!ready || s.busy != "").Clicked() {
					a.synthTriggers(e, in)
				}
				ui.Box(c).Grow(1)
				if len(s.set.Active()) > 0 && outlineButton(c, "trigger-export", "导出 eval_set.json").Clicked() {
					a.exportTriggers(s)
				}
			})
		}
		if s.written != "" {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "已写入 "+shortPath(a.home, s.written)+"，格式与 skill-creator 的 eval_set.json 相同。").FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0)
				if outlineButton(c, "trigger-reveal", "在访达中显示").Clicked() {
					a.reveal(s.written)
				}
			})
		}
		if len(s.set.Active()) > 0 {
			a.triggerSimulate(c, s, e, in, name, description, ready)
		}
	})
}

var sourceWords = map[string]string{evals.FromSession: "会话", evals.FromModel: "模型编写", evals.FromPerson: "手写"}

// triggerCases lists the set, each request with how the last run went.
func (a *App) triggerCases(c *ui.Context, s *triggerState, name string, last evals.Run, hasRun bool) {
	t := c.Theme()
	yes, no := 0, 0
	changed := false
	ui.Column(c).Label("触发评测集").Children(func() {
		for i := range s.set.Cases {
			tc := &s.set.Cases[i]
			switch {
			case tc.Off:
			case tc.Should:
				yes++
			default:
				no++
			}
			ui.Row(c.Key("case:"+tc.ID)).Padding(8, 0).Gap(12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					q := withTip(c, ui.Text(c, tc.Query).FontSize(13).LineHeight(1.45).MaxLines(2), tc.Query)
					if tc.Off {
						q.TextColor(t.TextMuted)
					}
					meta := sourceWords[tc.Source]
					if tc.Note != "" {
						meta += " · " + tc.Note
					}
					tone := token(t, "ink-3")
					if trial, ok := last.Trials[tc.ID]; hasRun && ok && !tc.Off {
						picked := trial.Picked == name
						switch {
						case picked == tc.Should && picked:
							meta = "上次模拟加载了它 · " + meta
						case picked == tc.Should:
							meta = "上次模拟没有加载它 · " + meta
						case trial.Picked == "":
							meta, tone = "上次模拟没有加载任何技能 · "+meta, t.Warning
						case picked:
							meta, tone = "上次模拟误加载了它 · "+meta, t.Warning
						default:
							meta, tone = "上次模拟加载了 "+trial.Picked+" · "+meta, t.Warning
						}
						if trial.Reason != "" && tone == t.Warning {
							meta += " · 模型说：" + trial.Reason
						}
					}
					withTip(c, ui.Text(c, meta).FontSize(11.5).TextColor(tone).MaxLines(2), meta)
				})
				label := 0
				switch {
				case tc.Off:
					label = 2
				case !tc.Should:
					label = 1
				}
				if segmented(c, "请求 "+tc.ID+" 的标注", &label, "应触发", "不应触发", "不用") {
					tc.Should, tc.Off = label == 0, label == 2
					changed = true
				}
			})
		}
	})
	if changed {
		a.saveTriggers(s)
	}
	ui.Text(c, fmt.Sprintf("应触发 %d · 不应触发 %d", yes, no)).FontSize(12).TextColor(t.TextMuted)
}

func (a *App) triggerAdd(c *ui.Context, s *triggerState) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		textField(c, &s.adding, "新的请求", "手写一条用户会怎么说，例如：把 invoice.pdf 里的表格填一下")
		segmented(c, "新请求的标注", &s.addNo, "应触发", "不应触发")
		if outlineButton(c, "trigger-add", "添加请求").Disabled(strings.TrimSpace(s.adding) == "").Clicked() {
			if s.set.Add(evals.NewCase(s.adding, s.addNo == 0, evals.FromPerson, "")) == 0 {
				s.err = "评测集里已经有这条请求了。"
				return
			}
			s.adding, s.err = "", ""
			a.saveTriggers(s)
		}
	})
}

func (a *App) triggerBusy(c *ui.Context, s *triggerState) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		text := s.busy
		if s.busy == routing {
			text = fmt.Sprintf("正在模拟 %d/%d…", s.done, s.total)
		}
		ui.Text(c, text).FontSize(13).TextColor(c.Theme().TextMuted).Grow(1).MinWidth(0)
		if s.cancel != nil && outlineButton(c, "trigger-stop", "停止").Clicked() {
			a.stopTriggers()
		}
	})
}

// triggerSimulate is the description being tried, the run to confirm or
// watch, and the runs so far.
func (a *App) triggerSimulate(c *ui.Context, s *triggerState, e insight.SkillEval, in evalInstall, name, description string, ready bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	ui.Column(c).Gap(8).Label("模拟触发").Children(func() {
		ui.Text(c, "模拟触发").FontSize(14).FontWeight(600).Padding(8, 0, 0, 0)
		if textArea(c, &s.draft, "试用的描述", 3, 8).Disabled(s.busy != "").Changed() {
			s.plan = nil
		}
		draft := strings.TrimSpace(s.draft)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			n := len([]rune(draft))
			count := ui.Text(c, fmt.Sprintf("%d/%d 字符", n, evals.MaxDescription)).FontSize(12).TextColor(muted)
			if n > evals.MaxDescription {
				count.TextColor(t.Warning)
			}
			hint := "这是技能现在的描述"
			if draft != strings.TrimSpace(description) {
				hint = "改动只用于模拟，不会写回 SKILL.md"
			}
			ui.Text(c, hint).FontSize(12).TextColor(muted).Grow(1).MinWidth(0).SingleLine()
			if draft != strings.TrimSpace(description) {
				if outlineButton(c, "trigger-copy", "复制描述").Clicked() {
					mygo.Clipboard.WriteText(draft)
				}
				if outlineButton(c, "trigger-reset", "恢复原描述").Disabled(s.busy != "").Clicked() {
					s.draft, s.why, s.plan = description, "", nil
				}
			}
		})
		if s.why != "" {
			callout(c, feedback.Info, "模型的改写："+s.why)
		}
		switch {
		case s.busy == routing:
			a.triggerBusy(c, s)
		case s.plan != nil:
			callout(c, feedback.Info, fmt.Sprintf("将把 %d 条请求逐条发给 %s，每次附上 %d 个技能的名称和描述（含上面这份），由 %s 判断会加载哪个技能，约 %s Token（估算，技能列表能被缓存时实际计费更少）。",
				len(s.plan.cases), a.model.config.Host(), len(s.plan.peers)+1, a.model.config.Model, tokenNumber(s.plan.tokens)))
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if primaryButton(c, "trigger-run", "", "开始模拟").Clicked() {
					a.runTriggers(name)
				}
				if outlineButton(c, "trigger-cancel", "取消").Clicked() {
					s.plan = nil
				}
			})
		default:
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if primaryButton(c, "trigger-plan", "", fmt.Sprintf("模拟 %d 条", len(s.set.Active()))).Disabled(!ready || s.busy != "" || draft == "").Clicked() {
					a.planTriggers(e, in)
				}
				hint := ""
				if !ready {
					hint = "连接模型后可用"
				} else if draft == "" {
					hint = "先填写要试用的描述"
				}
				ui.Text(c, hint).FontSize(12).TextColor(muted).Grow(1).MinWidth(0).SingleLine()
				if last, ok := s.set.Last(); ok && len(last.Misses(name, s.set.Cases)) > 0 {
					if withTip(c, outlineButton(c, "trigger-revise", "让模型改写描述").Disabled(!ready || s.busy != ""), "根据最近一次模拟里判断错的请求改写描述").Clicked() {
						a.reviseDescription(e, in, last)
					}
				}
			})
		}
		a.triggerRuns(c, s, name, description)
	})
}

// triggerRuns compares the recent runs, newest first.
func (a *App) triggerRuns(c *ui.Context, s *triggerState, name, description string) {
	if len(s.set.Runs) == 0 {
		return
	}
	t := c.Theme()
	widths := []float32{72, 72, 64}
	tableHead(c, "描述", []string{"应触发", "误触发", "正确"}, widths)
	for i := len(s.set.Runs) - 1; i >= 0; i-- {
		r := s.set.Runs[i]
		score := r.Score(name, s.set.Cases)
		label := "试用的描述"
		if strings.TrimSpace(r.Description) == strings.TrimSpace(description) {
			label = "现在的描述"
		}
		meta := fmt.Sprintf("%s · %s · %s", label, r.At.Local().Format("01/02 15:04"), r.Model)
		row := listRow(c, fmt.Sprintf("trigger-run:%d", i), "使用这次的描述："+cut(r.Description, 40))
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
				withTip(c, ui.Text(c, r.Description).FontSize(13).SingleLine(), r.Description)
				ui.Text(c, meta).FontSize(11.5).TextColor(token(t, "ink-3")).SingleLine()
			})
			hit := cell(c, fmt.Sprintf("%d/%d", score.Hit, score.Should), widths[0], true)
			if score.Hit < score.Should {
				hit.TextColor(t.Warning)
			}
			wrong := cell(c, fmt.Sprintf("%d/%d", score.False, score.ShouldNot), widths[1], true)
			if score.False > 0 {
				wrong.TextColor(t.Warning)
			}
			cell(c, fmt.Sprintf("%d/%d", score.Correct(), score.Total()), widths[2], true)
		})
		if row.Clicked() && s.busy == "" {
			s.draft, s.why, s.plan = r.Description, "", nil
		}
	}
	note(c, "点一行可以把那次的描述填回上面。模拟只看技能的名称和描述，和真实 Agent 的选择会有出入，同一条请求多跑几次结果也可能不同；用它比较几种写法的高下，比看绝对数字更可靠。")
}

func cut(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}
