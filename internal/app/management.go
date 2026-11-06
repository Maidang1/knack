package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/manage"
	"knack/internal/repair"
	"knack/internal/skill"
)

func (a *App) removalButton(c *ui.Context, e skill.Entry) {
	if e.Kind == skill.KindBroken || e.Vacant {
		return
	}
	r := a.removalFor(e)
	if !r.Available {
		return
	}
	if ui.Button(c, r.Label).TextColor(c.Theme().Danger).Disabled(a.scanning || a.mutating || (r.Err != "" && !r.Unlinkable)).Clicked() {
		a.confirmRemoval(e, r)
	}
}

func (a *App) confirmRemoval(e skill.Entry, r repair.Removal) {
	if !r.Available || (r.Err != "" && !r.Unlinkable) {
		return
	}
	home, undoRoot := a.home, a.undoRoot
	repos := a.repoPaths()
	roots := append([]skill.Root(nil), a.snap.Roots...)
	run := func(policy repair.LinkPolicy) func() ([]manage.Record, error) {
		return func() ([]manage.Record, error) {
			return repair.ApplyRemovalLinks(e, home, repos, undoRoot, policy, roots...)
		}
	}
	if !r.Unlinkable {
		a.confirm(r.Title, r.Message, r.Confirm, r.Done, run(repair.LinksBlock))
		return
	}
	a.confirm(r.Title, r.Message+"\n\n还有软链指向它：\n"+linkList(r.Links), "同时移除软链并删除", r.Done+"，并移除软链", run(repair.LinksRemove))
	a.ask.alt, a.ask.altDone, a.ask.altRun = "只删除，保留软链", r.Done+"，软链已失效", run(repair.LinksKeep)
}

// linkList names the links, a few at a time so the dialog stays short.
func linkList(links []skill.Entry) string {
	var b strings.Builder
	for i, l := range links {
		if i == 6 {
			fmt.Fprintf(&b, "…还有 %d 条", len(links)-i)
			break
		}
		fmt.Fprintf(&b, "· %s / %s\n", l.RootName, l.Name)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *App) removalNote(c *ui.Context, e skill.Entry) {
	r := a.removalFor(e)
	if !r.Available || r.Err == "" {
		return
	}
	color := c.Theme().Warning
	msg := r.Err
	if r.Unlinkable {
		color = c.Theme().TextMuted
		msg = fmt.Sprintf("有 %d 条软链指向它，删除时可以选择一并移除。", len(r.Links))
	}
	ui.Text(c, msg).FontSize(13).TextColor(color)
}

func (a *App) claudeEntry(e skill.Entry) bool {
	return !e.System && e.RootRole == skill.RoleAgent && e.Usable() &&
		filepath.Clean(e.RootPath) == filepath.Join(a.home, ".claude", "skills")
}

func invocationName(e skill.Entry) string {
	if e.FrontName != "" {
		return e.FrontName
	}
	return e.Name
}

func invocationLabel(mode manage.Invocation) string {
	switch mode {
	case manage.InvocationManual:
		return "仅手动调用"
	case manage.InvocationOff:
		return "禁用"
	case manage.InvocationNameOnly:
		return "仅向模型列出名称"
	default:
		return "自动 + 手动调用"
	}
}

func (a *App) invocationSettings(c *ui.Context, e skill.Entry) {
	if !a.claudeEntry(e) {
		return
	}
	t := c.Theme()
	card(c).Padding(12, 14).Gap(8).Children(func() {
		ui.Text(c, "Claude 调用方式").FontSize(13).FontWeight(600)
		if a.invocationErr != "" {
			ui.Text(c, "无法读取 Claude 配置："+a.invocationErr).FontSize(13).TextColor(t.Warning)
			return
		}
		name := invocationName(e)
		mode := a.invocations[name]
		if mode == "" {
			mode = manage.InvocationAuto
		}
		chosen := invocationLabel(mode)
		options := []string{"自动 + 手动调用", "仅手动调用", "禁用"}
		if mode == manage.InvocationNameOnly {
			options = append(options, invocationLabel(mode))
		}
		if ui.Select(c.Key(e.Path+"|invocation"), &chosen, options).AlignSelf(ui.Start).
			Label("Claude 调用方式").Disabled(a.scanning || a.mutating || a.cached).Changed() {
			for _, next := range []manage.Invocation{manage.InvocationAuto, manage.InvocationManual, manage.InvocationOff, manage.InvocationNameOnly} {
				if invocationLabel(next) == chosen && next != mode {
					a.confirmInvocation(name, mode, next)
					break
				}
			}
		}
		ui.Text(c, "仅修改 Claude 的用户配置，不改共享 skill。SKILL.md、项目或组织设置仍可能进一步限制调用。").FontSize(13).TextColor(t.TextMuted)
	})
}

func (a *App) confirmInvocation(name string, before, next manage.Invocation) {
	message := "将「" + name + "」的 Claude 用户级调用设置改为「" + invocationLabel(next) + "」。"
	if next == manage.InvocationOff {
		message += "模型和手动命令都将不可用。"
	} else if next == manage.InvocationManual {
		message += "模型不再自动调用，仍可用 /" + name + " 手动调用。"
	}
	message += "只写入 ~/.claude/settings.json，不修改 SKILL.md。可以撤销。"
	a.confirm("更改 Claude 调用方式？", message, "确认更改", "已更改 Claude 调用方式", func() ([]manage.Record, error) {
		current, err := manage.ReadInvocation(a.home, name)
		if err != nil {
			return nil, err
		}
		if current != before {
			return nil, errors.New("调用设置已被其他程序修改，请重新扫描后再操作")
		}
		rec, err := manage.SetInvocation(a.home, name, next, a.undoRoot)
		if err != nil {
			return nil, err
		}
		return []manage.Record{rec}, nil
	})
}
