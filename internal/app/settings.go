package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"skill-manager/internal/skill"
	"skill-manager/internal/store"
)

// agentRoots lists every discovered agent, shown or not.
func (a *App) agentRoots() []skill.Root {
	if a.snap == nil {
		return nil
	}
	var out []skill.Root
	for _, r := range a.snap.Roots {
		if r.Role == skill.RoleAgent {
			out = append(out, r)
		}
	}
	return out
}

// setAgentShown shows or hides one agent in the sidebar and persists it.
func (a *App) setAgentShown(id string, shown bool) {
	a.setAgentsShown([]string{id}, shown)
}

func (a *App) setAgentsShown(ids []string, shown bool) {
	old := a.hidden
	next := make(map[string]bool, len(old))
	for k := range old {
		next[k] = true
	}
	for _, id := range ids {
		if shown {
			delete(next, id)
		} else {
			next[id] = true
		}
	}
	list := make([]string, 0, len(next))
	for k := range next {
		list = append(list, k)
	}
	slices.Sort(list)
	if err := store.SaveSettings(a.settingsPath, store.Settings{HiddenAgents: list}); err != nil {
		a.notice = err.Error()
		return
	}
	a.hidden = next
	if a.pageHidden() {
		a.page = a.homePage()
	}
	a.cache = viewCache{}
}

// pageHidden reports that the current page was switched off in settings.
func (a *App) pageHidden() bool {
	if id, ok := strings.CutPrefix(a.page, "root:"); ok {
		return a.hidden[id]
	}
	return a.hidden[a.page] && (a.page == "issues" || a.page == "pool")
}

// homePage is the first page still switched on.
func (a *App) homePage() string {
	for _, id := range []string{"issues", "pool"} {
		if !a.hidden[id] {
			return id
		}
	}
	return effectivePage
}

type settingsRow struct {
	id, name string
	look     look
}

func (a *App) openSettings() {
	if a.ask.open || a.repoDialog != nil && a.repoDialog.open {
		return
	}
	a.settingsOpen = true
}

// settingsDialog keeps category navigation beside an independently scrolling
// content pane. Display settings apply immediately, without a save step.
func (a *App) settingsDialog(c *ui.Context) {
	if !a.settingsOpen {
		return
	}
	t := c.Theme()
	browse := []settingsRow{
		{"issues", "问题", look{"issues", ui.Hex("#f59e0b")}},
		{"pool", "共享池", look{"pool", ui.Hex("#2563eb")}},
	}
	var agents, builtins []settingsRow
	if a.snap != nil {
		for _, r := range a.snap.Roots {
			row := settingsRow{r.ID, r.Name, lookOf(a.home, r)}
			switch r.Role {
			case skill.RoleAgent:
				agents = append(agents, row)
			case skill.RoleBuiltin:
				builtins = append(builtins, row)
			}
		}
	}
	categories := []struct{ name, icon, description string }{
		{"浏览", "sidebar", "选择侧边栏中的浏览入口。"},
		{"Agent", "bot", "选择在侧边栏显示的 Agent。"},
		{"自带", "package", "选择在侧边栏显示的自带内容。"},
	}
	a.settingsTab = max(0, min(a.settingsTab, len(categories)-1))
	width, height := c.Size()
	ui.DialogBase(c, &a.settingsOpen, func(backdrop ui.Element, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.25)).Justify(ui.Center).AlignItems(ui.Center)
		panel.Size(min(780, width-48), min(500, height-48)).Padding(0).Gap(0).Radius(12).
			Background(t.Background).Border(1, t.Border).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.35)).
			Label("设置")
		ui.Row(c).Fill().AlignItems(ui.Stretch).Radius(11).Clip().Children(func() {
			ui.Column(c).Width(156).Shrink(0).Padding(24, 12, 16, 12).Gap(16).
				Background(t.SurfaceHover).Children(func() {
				ui.Text(c, "设置").FontSize(12).FontWeight(600).TextColor(t.TextMuted).Padding(0, 8)
				tabs := ui.TabsBase(c, &a.settingsTab, len(categories))
				tabs.List.Column().AlignItems(ui.Stretch).FocusGroup(ui.Vertical).Gap(4).Label("设置分类").Children(func() {
					for i, category := range categories {
						tab := tabs.Tab(i).Height(34).Padding(0, 10).Gap(8).Radius(6).
							AlignItems(ui.Center).Label("设置分类：" + category.name)
						if i == a.settingsTab {
							tab.Background(t.Selection)
						} else if tab.Hovered() {
							tab.Background(t.SurfacePressed)
						}
						tab.Children(func() {
							ui.Icon(c, glyph(category.icon)).FontSize(15).TextColor(t.TextMuted)
							label := ui.Text(c, category.name).FontSize(13)
							if i == a.settingsTab {
								label.FontWeight(600)
							}
						})
					}
				})
				if tabs.List.Changed() {
					c.Invalidate()
				}
			})
			ui.Column(c).Grow(1).MinWidth(0).Padding(28, 28, 20, 28).Gap(20).Label("设置内容").Children(func() {
				category := categories[a.settingsTab]
				ui.Column(c).Gap(6).Shrink(0).Children(func() {
					ui.Text(c, category.name).FontSize(20).FontWeight(600).Label("设置标题")
					ui.Text(c, category.description).FontSize(12).TextColor(t.TextMuted)
				})
				ui.Scroll(c.Key("settings:" + category.name)).Grow(1).MinHeight(0).Label("设置列表").Children(func() {
					rows := [][]settingsRow{browse, agents, builtins}[a.settingsTab]
					a.settingsGroup(c, category.name, rows)
				})
				ui.Column(c).Gap(12).Shrink(0).Children(func() {
					ui.Text(c, "隐藏只影响侧边栏显示，不会修改磁盘上的文件。").FontSize(11).TextColor(t.TextMuted)
					ui.Row(c).Justify(ui.End).Children(func() {
						if ui.Button(c, "完成").AutoFocus().Clicked() {
							a.settingsOpen = false
						}
					})
				})
			})
		})
	})
}

// settingsGroup is one titled card. Groups with several rows get bulk
// buttons; an empty group is left out.
func (a *App) settingsGroup(c *ui.Context, title string, rows []settingsRow) {
	t := c.Theme()
	if len(rows) == 0 {
		ui.Text(c, "没有扫描到这一类内容。").FontSize(12).TextColor(t.TextMuted).Padding(0, 4, 0, 4)
		return
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	ui.Column(c.Key("group:" + title)).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(0, 4, 0, 4).Children(func() {
			ui.Text(c, "在侧边栏显示").FontSize(12).FontWeight(600).TextColor(t.TextMuted).Grow(1)
			if len(rows) > 1 {
				if ui.Button(c, "全部显示").Label(title + "全部显示").Clicked() {
					a.setAgentsShown(ids, true)
				}
				if ui.Button(c, "全部隐藏").Label(title+"全部隐藏").Margin(0, 0, 0, 6).Clicked() {
					a.setAgentsShown(ids, false)
				}
			}
		})
		ui.Column(c).Background(t.Surface).Border(1, t.Border).Radius(10).Children(func() {
			for i, r := range rows {
				if i > 0 {
					ui.Divider(c.Key("div:"+r.id)).Margin(0, 0, 0, 46)
				}
				ui.Row(c.Key(r.id)).AlignItems(ui.Center).Gap(10).Padding(6, 12, 6, 10).Children(func() {
					tile(c, r.look, 24)
					ui.Text(c, r.name).FontSize(13).Grow(1).SingleLine()
					shown := !a.hidden[r.id]
					if ui.Switch(c, &shown).Label(r.name).Changed() {
						a.setAgentShown(r.id, shown)
					}
				})
			}
		})
	})
}

func rootIDs(rs []skill.Root) []string {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}
