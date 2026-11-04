package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/manage"
	"knack/internal/repair"
	"knack/internal/skill"
)

// Choices only apply to the visible result of the current, fresh scan.
func (a *App) syncIssueSelection(rows []row) {
	ps := a.state("issues")
	scope := fmt.Sprintf("%d|%s", a.filter, a.query)
	if ps.choiceSnap != a.snap || ps.choiceScope != scope {
		ps.chosen.Clear()
		ps.choiceSnap, ps.choiceScope = a.snap, scope
	}
	visible := make(map[string]bool, len(rows))
	for _, r := range rows {
		visible[r.key] = true
	}
	for key := range ps.chosen.All() {
		if !visible[key] {
			ps.chosen.Remove(key)
		}
	}
}

func (a *App) chosenIssues() []skill.Entry {
	ps := a.state("issues")
	var entries []skill.Entry
	seen := map[string]bool{}
	for _, r := range a.rows() {
		if ps.chosen.Has(r.key) && !seen[r.entry.Path] {
			entries = append(entries, r.entry)
			seen[r.entry.Path] = true
		}
	}
	return entries
}

func (a *App) issueSelectionBar(c *ui.Context, rows []row) {
	ps := a.state("issues")
	ui.Row(c).Margin(0, 16, 4, 16).Gap(6).Wrap().AlignItems(ui.Center).Children(func() {
		ui.Textf(c, "已选 %d 项", ps.chosen.Len()).FontSize(12).TextColor(c.Theme().TextMuted).SingleLine()
		if ui.Button(c, "全选").Height(toolbarControlHeight).Padding(0, 6).Disabled(len(rows) == 0).
			Tooltip("选择当前列表全部项目，也可用 ⌘A；⌘单击多选，Shift 单击连续选择").Clicked() {
			for _, r := range rows {
				ps.chosen.Add(r.key)
			}
			c.Invalidate()
		}
		if ps.chosen.Len() > 0 {
			if ui.Button(c, "取消选择").Height(toolbarControlHeight).Padding(0, 6).Clicked() {
				ps.chosen.Clear()
				c.Invalidate()
			}
		}
	})
}

func (a *App) issueBatchDetail(c *ui.Context, entries []skill.Entry) {
	t := c.Theme()
	disabled := a.scanning || a.mutating || a.cached
	ui.Column(c).Padding(24, 28).Gap(14).MaxWidth(680).Children(func() {
		ui.Textf(c, "已选择 %d 项", len(entries)).FontSize(17).FontWeight(600)
		ui.Text(c, "批量操作仅作用于当前选中的 skill，可以一起撤销。").FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Wrap().Children(func() {
			a.batchLinkMenu(c, entries, disabled)
			if safe := a.chosenSafeFixes(entries); len(safe) > 0 {
				if ui.Button(c, fmt.Sprintf("修复所选 %d 项", len(safe))).Disabled(disabled).Clicked() {
					a.confirmBatch(safe, "修复所选")
				}
			}
			blocked := a.batchRemovalError(entries)
			if ui.Button(c, "删除所选").TextColor(t.Danger).Disabled(disabled || blocked != "").Clicked() {
				a.confirmBatchRemoval(entries)
			}
		})
		if reason := a.batchRemovalError(entries); reason != "" {
			ui.Text(c, reason).FontSize(12).TextColor(t.Warning)
		}
		card(c).Padding(12, 14).Gap(8).Children(func() {
			for _, e := range entries {
				ui.Text(c, e.RootName+" / "+e.Name).FontSize(12)
			}
		})
	})
}

func (a *App) chosenSafeFixes(entries []skill.Entry) []repair.Fix {
	paths := map[string]bool{}
	for _, e := range entries {
		paths[e.Path] = true
	}
	var out []repair.Fix
	for _, f := range a.repairPlan() {
		if paths[f.Entry.Path] && f.Safe {
			out = append(out, f)
		}
	}
	return out
}

func (a *App) batchLinkMenu(c *ui.Context, entries []skill.Entry, disabled bool) {
	for _, e := range entries {
		if e.RootRole != skill.RolePool || !e.Usable() {
			return
		}
	}
	agents := a.roots(skill.RoleAgent)
	ui.MenuButton(c, "挂到 Agent", func(m *ui.Menu) {
		for _, root := range agents {
			var fixes []repair.Fix
			for _, e := range entries {
				taken := false
				for _, existing := range a.entriesFor(root.ID) {
					if existing.Name == e.Name {
						taken = true
						break
					}
				}
				if !taken {
					fixes = append(fixes, repair.LinkToAgent(e, root))
				}
			}
			label := fmt.Sprintf("%s（%d 项）", root.Name, len(fixes))
			if m.Item(label).Disabled(len(fixes) == 0).Chosen() {
				a.confirmBatchLinks(fixes, root.Name, len(entries)-len(fixes))
			}
		}
	}).Disabled(disabled || len(agents) == 0)
}

func (a *App) confirmBatchLinks(fixes []repair.Fix, agent string, skipped int) {
	if len(fixes) == 0 || a.cached || a.scanning || a.mutating {
		return
	}
	kept := append([]repair.Fix(nil), fixes...)
	message := fmt.Sprintf("将在 %s 的 skills 目录创建 %d 条指向共享池的软链，可以一起撤销。", agent, len(kept))
	if skipped > 0 {
		message += fmt.Sprintf("跳过 %d 个已存在的同名项目。", skipped)
	}
	entries := make([]skill.Entry, len(kept))
	for i, f := range kept {
		entries[i] = f.Entry
	}
	message += "\n\n" + batchEntryNames(entries)
	undoRoot := a.undoRoot
	a.confirm(fmt.Sprintf("将所选 skill 挂到 %s？", agent), message, "确认批量挂出", "批量挂出已处理",
		func() ([]manage.Record, error) { return repair.ApplyAll(kept, undoRoot) })
}

func batchEntryNames(entries []skill.Entry) string {
	var names []string
	for i, e := range entries {
		if i == 6 {
			names = append(names, fmt.Sprintf("还有 %d 项", len(entries)-i))
			break
		}
		names = append(names, e.RootName+" / "+e.Name)
	}
	return strings.Join(names, "\n")
}

func (a *App) batchRemovalError(entries []skill.Entry) string {
	for _, e := range entries {
		r := a.removalFor(e)
		if !r.Available {
			return e.Name + " 不能删除。"
		}
		if r.Err != "" {
			return e.Name + "：" + r.Err + "。请在单项详情中处理。"
		}
	}
	return ""
}

func (a *App) confirmBatchRemoval(entries []skill.Entry) {
	if len(entries) == 0 || a.cached || a.scanning || a.mutating || a.batchRemovalError(entries) != "" {
		return
	}
	kept := append([]skill.Entry(nil), entries...)
	home, undoRoot, repos := a.home, a.undoRoot, a.repoPaths()
	roots := append([]skill.Root(nil), a.snap.Roots...)
	message := "目录会移到撤销记录；软链只移除链接本身，不删除目标。可以一起撤销。移除共享池中的 skill 后，Grok 将看不到它。"
	message += "\n\n" + batchEntryNames(kept)
	a.confirm(fmt.Sprintf("删除所选 %d 项？", len(kept)), message, "确认批量删除", "批量删除已处理", func() ([]manage.Record, error) {
		var recs []manage.Record
		for _, e := range kept {
			rec, err := repair.ApplyRemoval(e, home, repos, undoRoot, roots...)
			if err != nil {
				return recs, fmt.Errorf("已删除 %d/%d 项；%s：%w", len(recs), len(kept), e.Name, err)
			}
			recs = append(recs, rec)
		}
		return recs, nil
	})
}
