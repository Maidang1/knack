package app

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/layout"

	"knack/internal/session"
)

func (a *App) pluginDir() string {
	return filepath.Join(a.configDir, "plugins")
}

func (a *App) reloadPlugins() []string {
	problems := session.LoadAgentConfig(filepath.Join(a.configDir, "agents.json"))
	problems = append(problems, session.LoadPlugins(a.pluginDir(), a.home)...)
	a.sessions.agentProblems = problems
	if a.sessions.loading {
		a.sessions.rescanPlugins = true
		return problems
	}
	a.startSessionScan()
	return problems
}

func (a *App) installPluginRepo(repo string) {
	if a.pluginBusy {
		return
	}
	a.pluginBusy = true
	a.pluginErr = ""
	go func() {
		pending, err := session.FetchPlugin(repo, a.pluginDir(), a.home)
		a.post(func() { a.finishPluginInstall(pending, err) })
	}()
}

func (a *App) installPluginPath(path string) {
	if a.pluginBusy || strings.TrimSpace(path) == "" {
		return
	}
	a.pluginBusy = true
	a.pluginErr = ""
	go func() {
		pending, err := session.OpenPlugin(path, a.pluginDir(), a.home)
		a.post(func() { a.finishPluginInstall(pending, err) })
	}()
}

func (a *App) finishPluginInstall(pending *session.PluginPending, err error) {
	a.pluginBusy = false
	if err != nil {
		a.pluginErr = err.Error()
		return
	}
	if err := pending.Install(a.pluginDir()); err != nil {
		pending.Discard()
		a.pluginErr = err.Error()
		return
	}
	problems := a.reloadPlugins()
	for _, problem := range problems {
		if strings.Contains(problem, "plugins/"+pending.Entry+"：") {
			_ = session.RemovePlugin(a.pluginDir(), pending.Entry)
			a.reloadPlugins()
			a.pluginErr = problem
			return
		}
	}
	a.pluginRepo = ""
	a.notice = "已安装插件 " + pending.Name
}

func (a *App) removePlugin(name string) {
	if a.pluginBusy {
		return
	}
	if err := session.RemovePlugin(a.pluginDir(), name); err != nil {
		a.pluginErr = err.Error()
		return
	}
	a.reloadPlugins()
	a.notice = "已移除插件"
}

func (a *App) choosePluginFile() (string, error) {
	if a.pickFileFn != nil {
		return a.pickFileFn()
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  a.win,
		Title:   "安装插件",
		Message: "选择一个 .js 插件或 .zip 压缩包",
		Filters: []mygo.FileFilter{{Name: "插件", Extensions: []string{"js", "zip"}}},
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func (a *App) beginPickPlugin(dir bool) {
	if a.pluginBusy || a.pluginPicking {
		return
	}
	a.pluginPicking = true
	run := func() {
		var path string
		var err error
		if dir {
			path, err = a.chooseDirWith("安装插件", "选择含有 plugin.json 的插件目录")
		} else {
			path, err = a.choosePluginFile()
		}
		a.post(func() {
			a.pluginPicking = false
			if err != nil {
				a.pluginErr = err.Error()
				return
			}
			a.installPluginPath(path)
		})
	}
	if a.win == nil {
		run()
		return
	}
	go run()
}

func (a *App) settingsPlugins(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(12).Children(func() {
		ui.Text(c, "从 GitHub 安装").FontSize(13).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			textField(c, &a.pluginRepo, "GitHub 仓库", "owner/repo 或仓库地址")
			if primaryButton(c, "plugin-github", "", "安装").Disabled(a.pluginBusy || strings.TrimSpace(a.pluginRepo) == "").Clicked() {
				a.installPluginRepo(a.pluginRepo)
			}
		})
		ui.Text(c, "或从这台电脑安装一个插件目录、.js 文件或 .zip。").FontSize(13).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if outlineButton(c, "plugin-file", "选择文件…").Disabled(a.pluginBusy || a.pluginPicking).Clicked() {
				a.beginPickPlugin(false)
			}
			if outlineButton(c, "plugin-dir", "选择目录…").Disabled(a.pluginBusy || a.pluginPicking).Clicked() {
				a.beginPickPlugin(true)
			}
			if outlineButton(c, "plugin-show", "打开插件文件夹").Clicked() {
				a.reveal(a.pluginDir())
			}
		})
		if a.pluginBusy {
			ui.Text(c, "正在安装…").FontSize(13).TextColor(t.TextMuted)
		}
		if a.pluginErr != "" {
			ui.Text(c, a.pluginErr).FontSize(13).TextColor(t.Danger)
		}
		listed := session.PluginStatuses()
		if len(listed) == 0 {
			ui.Text(c, "还没有安装插件。").FontSize(13).TextColor(t.TextMuted)
			return
		}
		ui.Column(c).Background(t.Surface).Border(1, t.Border).Radius(10).Clip().Children(func() {
			for i, p := range listed {
				if i > 0 {
					layout.Divider(c.Key("plugin-div:"+p.Name), layout.DividerProps{})
				}
				ui.Row(c.Key("plugin:"+p.Name)).AlignItems(ui.Center).Gap(12).Padding(10, 14).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						title := p.Title
						if title == "" {
							title = p.Name
						}
						ui.Text(c, title).FontSize(13).FontWeight(500).SingleLine()
						detail := p.Name
						if p.Repo != "" {
							detail = p.Repo
							if p.Version != "" {
								detail += " · " + p.Version
							}
						}
						if p.Problem != "" {
							detail = p.Problem
						}
						ui.Text(c, detail).FontSize(12).TextColor(t.TextMuted).SingleLine()
					})
					if outlineButton(c, "plugin-remove:"+p.Name, "移除").Disabled(a.pluginBusy).Clicked() {
						a.removePlugin(p.Name)
					}
				})
			}
		})
	})
}
