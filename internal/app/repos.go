package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"

	"knack/internal/manage"
	"knack/internal/store"
)

type repoDialogState struct {
	open, editing, picking    bool
	original, name, path, err string
}

func (a *App) repoMenu(m *menu, path string) {
	if m.Item("查看路径…").Chosen() {
		a.openRepoDialog(path, false)
	}
	if m.Item("在访达中显示").Chosen() {
		a.reveal(path)
	}
	if m.Item("复制路径").Chosen() {
		a.copyPath(path)
		a.notice = "已复制路径"
	}
	m.Separator()
	if m.Item("编辑仓库…").Disabled(a.mutating).Chosen() {
		a.openRepoDialog(path, true)
	}
	if m.Item("移除仓库…").Danger().Disabled(a.scanning || a.mutating).Chosen() {
		a.confirmRepoRemoval(path)
	}
}

func (a *App) openRepoDialog(path string, editing bool) {
	for _, r := range a.repos {
		if r.Path == path {
			a.repoDialog = &repoDialogState{open: true, editing: editing, original: path, name: r.Name, path: path}
			return
		}
	}
}

func (a *App) beginPickRepoPath() {
	d := a.repoDialog
	if d == nil || !d.open || d.picking {
		return
	}
	d.picking = true
	run := func() {
		path, err := a.chooseDirWith("更改扫描目录", "只更新扫描路径，不会移动或修改仓库文件")
		a.post(func() {
			if a.repoDialog != d || !d.open {
				return
			}
			d.picking = false
			if err != nil {
				d.err = err.Error()
			} else if path != "" {
				d.path, d.err = path, ""
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

// updateRepo changes configuration only. Save first so failed writes leave
// the current selection, scan scope and labels intact.
func (a *App) updateRepo(original, name, path string) error {
	if a.mutating {
		return fmt.Errorf("正在处理，请稍后再试")
	}
	index := slices.IndexFunc(a.repos, func(r store.Repo) bool { return r.Path == original })
	if index < 0 {
		return fmt.Errorf("仓库记录已不存在")
	}
	path = strings.TrimSpace(path)
	if path == "~" {
		path = a.home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(a.home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("请输入完整的绝对路径，或使用 ~/ 开头的路径")
	}
	path = filepath.Clean(path)
	if path != original {
		if a.isExistingRoot(path) {
			return fmt.Errorf("这个目录已经在扫描范围内")
		}
		for i, r := range a.repos {
			if i != index && filepath.Clean(r.Path) == path {
				return fmt.Errorf("这个仓库已经添加过")
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("无法读取扫描目录：%w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("扫描路径必须是目录")
		}
	}
	next := slices.Clone(a.repos)
	next[index].Name, next[index].Path = strings.TrimSpace(name), path
	if err := store.Save(a.configPath, next); err != nil {
		return err
	}
	a.repos = next
	if a.page == "repo:"+original {
		a.page = "repo:" + path
	}
	a.notice = "已更新仓库"
	if path != original {
		delete(a.pages, "repo:"+original)
		delete(a.pages, "repo:"+path)
		a.refreshRepoScan()
	}
	return nil
}

func (a *App) refreshRepoScan() {
	// A scan of the old repository paths must not replace the new scope.
	if a.scanning {
		a.scanGen++
	}
	a.startScan()
}

func (a *App) confirmRepoRemoval(path string) {
	a.confirm("不再扫描这个仓库？", "将从列表移除「"+a.repoLabel(path)+"」。只是不再扫描这个目录，仓库里的文件还在。",
		"确认不再扫描", "已不再扫描", func() ([]manage.Record, error) { return nil, a.removeRepo(path) })
	a.ask.onUI = true
}

func (a *App) repositoryDialog(c *ui.Context) {
	d := a.repoDialog
	if d == nil || !d.open {
		return
	}
	t := c.Theme()
	title := "仓库信息"
	if d.editing {
		title = "编辑仓库"
	}
	button := func(label string, variant action.ButtonVariant) ui.Element {
		return action.Button(c.Key("repo:"+label), label, action.ButtonProps{Variant: variant}).Label(label)
	}
	feedback.Modal(c, &d.open, feedback.ModalProps{Title: title, Width: 480, NoFooter: true}, func() {
		ui.Column(c).Gap(12).Children(func() {
			if d.editing {
				ui.Text(c, "显示名称").FontSize(13).TextColor(t.TextMuted)
				textField(c, &d.name, "仓库显示名称", "留空使用目录名").AutoFocus()
				ui.Text(c, "扫描路径").FontSize(13).TextColor(t.TextMuted)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					textField(c, &d.path, "仓库扫描路径", "")
					if button("选择目录…", action.Default).Disabled(d.picking || a.mutating).Clicked() {
						a.beginPickRepoPath()
					}
				})
				ui.Text(c, "只修改本应用的记录，不会重命名、移动或删除磁盘文件。").FontSize(13).TextColor(t.TextMuted)
			} else {
				ui.Text(c, a.repoLabel(d.original)).FontWeight(600)
				ui.Text(c, d.path).FontSize(13).Selectable()
			}
			if d.err != "" {
				ui.Text(c, d.err).FontSize(13).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if d.editing {
					if button("取消", action.Default).Clicked() {
						d.open = false
					}
					if button("保存", action.Primary).Disabled(d.picking || a.mutating).Clicked() {
						if err := a.updateRepo(d.original, d.name, d.path); err != nil {
							d.err = err.Error()
						} else {
							d.open = false
						}
					}
				} else {
					if button("复制路径", action.Default).Clicked() {
						a.copyPath(d.path)
						c.Toast("已复制路径")
					}
					if button("编辑…", action.Default).Disabled(a.mutating).Clicked() {
						d.editing = true
					}
					if button("完成", action.Primary).Clicked() {
						d.open = false
					}
				}
			})
		})
	})
}
