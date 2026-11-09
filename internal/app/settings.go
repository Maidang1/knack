package app

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/skill"
	"knack/internal/store"
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
	if err := store.SaveSettings(a.settingsPath, store.Settings{HiddenAgents: list, FoldLongSteps: a.foldLongSteps}); err != nil {
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
	return a.hidden[a.page] && (a.page == storagePage || slices.Contains(browsePages, a.page))
}

// homePage is the first page still switched on.
func (a *App) homePage() string {
	for _, id := range browsePages {
		if !a.hidden[id] {
			return id
		}
	}
	return effectivePage
}

// browsePages are the sidebar's places that settings can switch off.
var browsePages = []string{"overview", "sessions", "issues", libraryPage, graphPage, insightsPage, "pool"}

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
		{"overview", "首页", look{"overview", ui.Hex("#8E8E93")}},
		{"sessions", "全部会话", look{"sessions", ui.Hex("#8E8E93")}},
		{"issues", "待处理", look{"issues", ui.Hex("#f59e0b")}},
		{libraryPage, "技能库", look{"pool", ui.Hex("#8E8E93")}},
		{graphPage, "依赖图", look{"graph", ui.Hex("#8E8E93")}},
		{insightsPage, "诊断", look{"wave", ui.Hex("#8E8E93")}},
		{"pool", "共享池", look{"package", ui.Hex("#2563eb")}},
		{storagePage, "磁盘清理", look{"disk", ui.Hex("#8E8E93")}},
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
	categories := []struct{ name, icon, title, description string }{
		{"浏览", "sidebar", "侧边栏浏览入口", "选择侧边栏中的浏览入口。"},
		{"Agent", "bot", "在侧边栏显示的 Agent", "选择在侧边栏显示的 Agent。"},
		{"自带", "package", "Agent 自带的技能", "选择在对应 Agent 技能列表中显示的自带内容。"},
		{"插件", "braces", "Agent 插件", "用 JavaScript 接入其他 Agent 的会话。可从 GitHub、本机目录、.js 文件或 ZIP 安装。"},
	}
	a.settingsTab = max(0, min(a.settingsTab, len(categories)-1))
	width, height := c.Size()
	ui.DialogBase(c, &a.settingsOpen, func(backdrop ui.Element, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.25)).Justify(ui.Center).AlignItems(ui.Center)
		panel.Size(min(680, width-48), min(540, height-48)).Padding(0).Gap(0).Radius(12).
			Background(t.Surface).Border(1, t.Border).Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.3)).
			Label("设置")
		ui.Column(c).Fill().Radius(11).Clip().Children(func() {
			// The toolbar of a macOS settings window: the pane's name over
			// a row of icon tabs.
			ui.Column(c).Shrink(0).Padding(12, 16, 8, 16).Gap(8).AlignItems(ui.Center).
				Background(token(t, "sidebar")).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, categories[a.settingsTab].name).FontSize(13).FontWeight(600)
				tabs := ui.TabsBase(c, &a.settingsTab, len(categories))
				tabs.List.FocusGroup(ui.Horizontal).Gap(4).Label("设置分类").Children(func() {
					for i, category := range categories {
						on := i == a.settingsTab
						tab := tabs.Tab(i).MinWidth(64).Padding(6, 12).Gap(4).Radius(7).Column().
							AlignItems(ui.Center).Label("设置分类：" + category.name)
						if on {
							tab.Background(token(t, "hover"))
						} else if tab.Hovered() {
							tab.Background(t.SurfaceHover)
						}
						tab.Children(func() {
							icon := ui.Icon(c, glyph(category.icon)).FontSize(17)
							label := ui.Text(c, category.name).FontSize(12)
							if on {
								label.FontWeight(600)
							} else {
								icon.TextColor(t.TextMuted)
								label.TextColor(t.TextMuted)
							}
						})
					}
				})
				if tabs.List.Changed() {
					c.Invalidate()
				}
			})
			ui.Column(c).Grow(1).MinHeight(0).Padding(22, 28, 18, 28).Gap(16).Label("设置内容").Children(func() {
				category := categories[a.settingsTab]
				ui.Column(c).Gap(4).Shrink(0).Children(func() {
					ui.Text(c, category.title).FontSize(15).FontWeight(600).Label("设置标题")
					ui.Text(c, category.description).FontSize(13).TextColor(t.TextMuted)
				})
				ui.Scroll(c.Key("settings:" + category.name)).Grow(1).MinHeight(0).Label("设置列表").Children(func() {
					if a.settingsTab == len(categories)-1 {
						a.settingsPlugins(c)
						return
					}
					rows := [][]settingsRow{browse, agents, builtins}[a.settingsTab]
					a.settingsGroup(c, category.name, rows)
				})
				ui.Row(c).Gap(12).Shrink(0).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, glyph("info")).FontSize(13).TextColor(token(t, "ink-3")).Shrink(0)
					note := "隐藏只影响侧边栏显示，不会修改磁盘上的文件。"
					if a.settingsTab == len(categories)-1 {
						note = "插件只通过只读方法读取本机文件，安装前请先看一遍它的代码。"
					}
					ui.Text(c, note).FontSize(12).TextColor(token(t, "ink-3")).Grow(1).MinWidth(0)
					if primaryButton(c, "settings-done", "", "完成").AutoFocus().Clicked() {
						a.settingsOpen = false
					}
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
		ui.Text(c, "没有扫描到这一类内容。").FontSize(13).TextColor(t.TextMuted).Padding(0, 4, 0, 4)
		return
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.id
	}
	ui.Column(c.Key("group:" + title)).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(0, 4, 0, 4).Children(func() {
			label := "在侧边栏显示"
			if title == "自带" {
				label = "在 Agent 技能列表中显示"
			}
			ui.Text(c, label).FontSize(13).TextColor(t.TextMuted).Grow(1)
			if len(rows) > 1 {
				if outlineButton(c, "show:"+title, "全部显示").Label(title + "全部显示").Clicked() {
					a.setAgentsShown(ids, true)
				}
				if outlineButton(c, "hide:"+title, "全部隐藏").Label(title+"全部隐藏").Margin(0, 0, 0, 6).Clicked() {
					a.setAgentsShown(ids, false)
				}
			}
		})
		ui.Column(c).Background(t.Surface).Border(1, t.Border).Radius(10).Clip().Children(func() {
			for i, r := range rows {
				if i > 0 {
					ui.Divider(c.Key("div:"+r.id)).Margin(0, 0, 0, 48)
				}
				ui.Row(c.Key(r.id)).AlignItems(ui.Center).Gap(12).Padding(10, 14, 10, 12).Children(func() {
					tile(c, r.look, 24)
					ui.Text(c, r.name).FontSize(13).FontWeight(500).Grow(1).SingleLine()
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

// Persist before changing the visible preference; failed writes retain the old
// scale and both settings mutations preserve the other display preferences.
func (a *App) setFoldLongSteps(fold bool) {
	ids := make([]string, 0, len(a.hidden))
	for id, hidden := range a.hidden {
		if hidden {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if err := store.SaveSettings(a.settingsPath, store.Settings{HiddenAgents: ids, FoldLongSteps: fold}); err != nil {
		a.notice = err.Error()
		return
	}
	a.foldLongSteps = fold
}
