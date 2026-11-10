package app

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/choice"
	lobe "github.com/lobehub/lobe-mygo/pkg/theme"

	"knack/internal/repair"
	"knack/internal/session"
	"knack/internal/skill"
)

// issueOrder is the order of the groups on the issues page: what is broken
// first, then what needs a decision, then what is merely idle.
var issueOrder = []skill.IssueKind{skill.IssueBroken, skill.IssueStale, skill.IssueDuplicate, skill.IssueOrphan, skill.IssueEmpty}

func issueGroup(k skill.IssueKind) string {
	switch k {
	case skill.IssueStale:
		return "同名副本有差异"
	case skill.IssueDuplicate:
		return "一致副本"
	case skill.IssueEmpty:
		return "空目录"
	}
	return k.Label()
}

func issueTone(t *ui.Theme, k skill.IssueKind) ui.Color {
	switch k {
	case skill.IssueBroken:
		return t.Danger
	case skill.IssueStale:
		return t.Warning
	}
	return token(t, "ink-3")
}

func (a *App) issueRows() []row {
	kind := a.issueScope()
	fixes := map[string]repair.Fix{}
	for _, f := range a.repairPlan() {
		fixes[f.Entry.Path+"\x00"+string(f.Issue)] = f
	}
	var out []row
	for _, issue := range a.snap.Issues {
		if kind != "" && string(issue.Kind) != kind {
			continue
		}
		e := issue.Entry
		if !matchQuery(a.query, e.Name, e.Description, e.RootName, e.Path, issue.Detail) {
			continue
		}
		key := e.Path + "\x00" + string(issue.Kind)
		var fix *repair.Fix
		if f, ok := fixes[key]; ok && f.Safe {
			fix = &f
		}
		out = append(out, row{
			key:      key,
			name:     e.Name,
			badge:    issue.Kind.Label(),
			tone:     e.Relation.Tone(),
			glyph:    issueGlyph(issue.Kind),
			subtitle: oneLine(issue.Detail),
			entry:    e,
			kind:     issue.Kind,
			fix:      fix,
		})
	}
	slices.SortStableFunc(out, func(x, y row) int {
		return cmp.Compare(slices.Index(issueOrder, x.kind), slices.Index(issueOrder, y.kind))
	})
	for i := 0; i < len(out); {
		j := i
		for j < len(out) && out[j].kind == out[i].kind {
			j++
		}
		out[i].section, out[i].count = issueGroup(out[i].kind), j-i
		i = j
	}
	return out
}

// issueHeading is a group's name beside a dot in its color and its size.
func issueHeading(c *ui.Context, dot ui.Color, name string, count int) {
	t := c.Theme()
	ui.Row(c).Gap(7).Padding(14, 10, 6, 10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(dot).Shrink(0)
		ui.Text(c, name).FontSize(13).FontWeight(600).SingleLine()
		number(c, fmt.Sprint(count)).FontSize(11.5).TextColor(token(t, "ink-3"))
	})
}

// rootDot is the color of the dot beside a source: the Agent's own color,
// or ink-3 for the pool and repositories.
func (a *App) rootDot(t *ui.Theme, e skill.Entry) ui.Color {
	if e.RootRole == skill.RoleAgent || e.RootRole == skill.RoleBuiltin {
		return agentColor(session.Agent(agentKey(a.home, e.RootPath)), t.Dark)
	}
	return token(t, "ink-3")
}

// paintIssueRow is the design's issue row: a checkbox, the skill in mono
// with where it lives, what is wrong, and what fixing it does.
func (a *App) paintIssueRow(c *ui.Context, r row, selected bool) {
	t := c.Theme()
	muted := token(t, "ink-3")
	if r.section != "" {
		issueHeading(c, issueTone(t, r.kind), r.section, r.count)
	}
	ps := a.state(a.page)
	ui.Row(c).Padding(8, 10).Gap(10).AlignItems(ui.Center).Children(func() {
		checked := ps.chosen.Has(r.key)
		if choice.Checkbox(c, &checked, "", choice.CheckboxProps{}).Label("选择 " + r.name).Shrink(0).Changed() {
			if checked {
				ps.chosen.Add(r.key)
			} else {
				ps.chosen.Remove(r.key)
			}
			c.Invalidate()
		}
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Text(c, r.name).FontSize(13).FontWeight(500).Shrink(1).MinWidth(0).SingleLine()
				ui.Box(c).Size(6, 6).Radius(3).Background(a.rootDot(t, r.entry)).Shrink(0)
				root := ui.Text(c, r.entry.RootName).FontSize(12).Shrink(0).SingleLine()
				if !selected {
					root.TextColor(t.TextMuted)
				}
			})
			if r.subtitle != "" {
				sub := withTip(c, ui.Text(c, r.subtitle).FontSize(12).SingleLine(), r.subtitle)
				if !selected {
					sub.TextColor(muted)
				}
			}
		})
		if r.fix == nil {
			hint := ui.Text(c, "需要你决定").FontSize(12).Shrink(0).SingleLine()
			if !selected {
				hint.TextColor(t.TextMuted)
			}
			return
		}
		fix := *r.fix
		variant := action.Text
		if selected {
			variant = action.Filled
		}
		b := action.Button(c.Key("fix:"+r.key), fix.Label, action.ButtonProps{Variant: variant, Size: lobe.Small}).
			Label(fix.Label).Shrink(0).Disabled(a.scanning || a.mutating || a.cached)
		if b.Clicked() {
			a.confirmFix(fix)
			c.Invalidate()
		}
	})
}

// anomalyGroup closes the issues list with the sessions whose timing looks
// wrong; they are fixed by reading the session, not by a file change.
func (a *App) anomalyGroup(c *ui.Context) {
	if a.filter != 0 || a.query != "" || a.sessions.snapshot == nil {
		return
	}
	d := a.overviewData()
	n := len(d.anomalies)
	if n == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c).Shrink(0).Padding(0, 8, 8, 8).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		issueHeading(c, t.Warning, "耗时异常", n)
		a.anomalyRow(c, d, n)
	})
}

func (a *App) anomalyRow(c *ui.Context, d *overviewData, n int) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key("issue-anomalies")).Label("在会话中查看耗时异常").Padding(8, 10, 8, 23).Gap(10).Radius(8).AlignItems(ui.Center)
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Textf(c, "%d 个会话有耗时异常", n).FontSize(13).FontWeight(500).SingleLine()
			ui.Text(c, "慢步骤、重试和中断，在会话中逐轮查看").FontSize(12).TextColor(token(t, "ink-3")).SingleLine()
		})
		ui.Text(c, "在会话中查看 →").FontSize(12).TextColor(t.TextMuted).Shrink(0)
	})
	if b.Clicked() {
		a.openSessionAnomalies(d)
		c.Invalidate()
	}
}

// repairBar sits at the foot of the issues list: the fixes that lose
// nothing in one click, and the ones that replace a different copy.
func (a *App) repairBar(c *ui.Context) {
	if a.snap == nil || len(a.snap.Issues) == 0 {
		return
	}
	t := c.Theme()
	kind := a.issueScope()
	safe, review := repair.Split(a.plan(kind))
	ui.Row(c).Shrink(0).Padding(10, 16).Gap(8).Wrap().AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Background(t.Background).Children(func() {
		msg := "这些问题需要你决定如何处理。"
		switch {
		case len(safe) > 0 && len(review) > 0:
			msg = fmt.Sprintf("%d 项可直接修复，%d 个副本要你确认", len(safe), len(review))
		case len(safe) > 0:
			msg = fmt.Sprintf("%d 项可安全修复，都能撤销", len(safe))
		case len(review) > 0:
			msg = fmt.Sprintf("%d 个不一致的副本要你确认", len(review))
		}
		if a.state("issues").chosen.Len() > 0 {
			msg = "已选 · " + msg
		}
		ui.Text(c, msg).FontSize(13).TextColor(t.TextMuted).Grow(1).MinWidth(120).MaxLines(2)
		if len(review) > 0 {
			label := fmt.Sprintf("替换 %d 个不一致副本", len(review))
			if outlineButton(c, "replace", label).Height(28).Disabled(a.scanning || a.mutating).Clicked() {
				a.confirmReplace(append([]repair.Fix(nil), review...))
			}
		}
		if len(safe) > 0 {
			label := fmt.Sprintf("一键修复 %d 项", len(safe))
			if kind != "" {
				label = fmt.Sprintf("修复这 %d 项", len(safe))
			}
			if primaryButton(c, "fix", "wand", label).Disabled(a.scanning || a.mutating).Clicked() {
				a.confirmBatch(append([]repair.Fix(nil), safe...), "修复")
			}
		}
	})
}
