package app

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestToolbarScopeAndKeyboard(t *testing.T) {
	home, work := effectiveHome(t)
	a := New(home)
	a.startScan()
	a.page = effectivePage
	a.SetWorkDir(work)
	tt := ui.NewTester(a.View, 1180, 760)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	click(t, tt, "Agent：Claude")
	tt.Key(0, ui.KeyEscape)
	if !tt.Focused("Agent：Claude") {
		t.Fatal("agent menu did not return focus to its trigger")
	}
	tt.Key(0, ui.KeyRight)
	dirLabel := "工作目录：" + shortPath(home, work)
	if !tt.Focused(dirLabel) {
		t.Fatal("directory is not reachable with the toolbar arrow keys")
	}
	picked := false
	a.pickDirFn = func() (string, error) { picked = true; return "", nil }
	tt.Key(0, ui.KeyEnter)
	if !picked {
		t.Fatal("directory picker did not open from the keyboard")
	}
	r, ok := tt.Find(dirLabel)
	if !ok || r.H != toolbarControlHeight || r.W > 180 {
		t.Fatalf("unexpected directory control bounds: %+v", r)
	}
	// A press dismisses a tooltip until both focus and pointer leave.
	click(t, tt, "搜索")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	if !tt.HasText(work) {
		t.Fatal("directory tooltip does not show the full path")
	}
	click(t, tt, "搜索")
	tt.Type("review")
	if a.query != "review" {
		t.Fatal("search no longer accepts input")
	}
}

func TestContentControlPlacement(t *testing.T) {
	home, work := effectiveHome(t)
	for _, width := range []int{1180, 880} {
		for _, collapsed := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/collapsed=%t", width, collapsed), func(t *testing.T) {
				a := New(home)
				a.startScan()
				a.page = effectivePage
				a.sideCollapsed = collapsed
				a.SetWorkDir(work)
				tt := ui.NewTester(a.View, width, 760)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				labels := []string{"Agent：Claude", "生效筛选", "重新扫描", "搜索"}
				if _, ok := tt.Find("工作目录：" + shortPath(home, work)); !ok {
					t.Fatal("short directory name unnecessarily moved into overflow")
				}
				for _, state := range []string{"ready", "scanning", "cached", "mutating"} {
					a.scanning, a.cached, a.mutating = state == "scanning", state == "cached", state == "mutating"
					tt.Frame()
					for _, label := range labels {
						r, ok := tt.Find(label)
						if !ok || r.H < 24 || r.W < 24 || r.X < 0 || r.X+r.W > float32(width) || r.Y < 0 || r.Y+r.H > 760 {
							t.Errorf("%s: control %q is clipped or too small: %+v", state, label, r)
						}
						switch label {
						case "Agent：Claude":
							if r.Y+r.H > 36 {
								t.Errorf("agent left title bar: %+v", r)
							}
						case "重新扫描":
							if a.rail {
								// The rail keeps it at its foot.
								if r.X+r.W > railWidth || r.Y < 600 {
									t.Errorf("scan is not at the rail's foot: %+v", r)
								}
								continue
							}
							if r.Y < 760-footerHeight {
								t.Errorf("scan is not in footer: %+v", r)
							}
							if !collapsed && r.X+r.W > a.sidebarWidth() {
								t.Errorf("scan left sidebar: %+v", r)
							}
						default:
							if r.Y < 36 || r.X < a.sidebarWidth() || r.X+r.W > float32(width) {
								t.Errorf("%q is not inside the page: %+v", label, r)
							}
						}
					}
				}
			})
		}
	}
}

func TestToolbarLongDirectoryRemainsVisible(t *testing.T) {
	home, _ := effectiveHome(t)
	a := New(home)
	a.startScan()
	a.page = effectivePage
	a.SetWorkDir(filepath.Join(home, "projects", "工具栏布局检查-with-a-very-long-directory-name"))
	picked := false
	a.pickDirFn = func() (string, error) { picked = true; return "", nil }
	tt := ui.NewTester(a.View, 880, 760)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	label := "工作目录：" + shortPath(home, a.eff.workDir)
	if r, ok := tt.Find(label); !ok || r.W <= 0 || r.W > 180 || r.Y+r.H > 36 {
		t.Fatalf("long directory did not stay in title bar: %+v", r)
	}
	click(t, tt, label)
	if !picked {
		t.Fatal("directory picker is not reachable")
	}
}

func TestContentTabsAndSearch(t *testing.T) {
	home, work := effectiveHome(t)
	a := New(home)
	a.startScan()
	a.page = effectivePage
	a.SetWorkDir(work)
	tt := ui.NewTester(a.View, 880, 560)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
	click(t, tt, "搜索")
	tt.Type("writing")
	v, errText := a.effective()
	if v == nil || errText != "" || len(a.effectiveRows(v)) != 1 {
		t.Fatal("list search did not filter")
	}
	click(t, tt, "全部")
	tt.Key(0, ui.KeyRight)
	if a.eff.filter != 1 || !tt.Focused("存在覆盖") || !tt.HasText("其余 2 个技能只存在于一层，切换到「全部」查看。") {
		t.Fatal("keyboard did not switch the filter")
	}
	search, ok := tt.Find("搜索")
	if !ok || search.Y < 36 || search.X < a.sidebarWidth() {
		t.Fatalf("matrix search misplaced: %+v", search)
	}
	if a.query != "writing" {
		t.Fatal("filter switch discarded query")
	}
	tt.Key(0, ui.KeyLeft)
	if a.eff.filter != 0 || !tt.Focused("全部") {
		t.Fatal("keyboard did not return to every skill")
	}
	click(t, tt, "搜索")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("no-match")
	if !tt.HasText("没有匹配的 skill") {
		t.Fatal("filtered empty state missing")
	}
	tt.Key(0, ui.KeyEscape)
	if a.query != "" {
		t.Fatal("Escape did not clear search")
	}
}

func TestFooterScanWithSidebarHidden(t *testing.T) {
	home := t.TempDir()
	a := New(home)
	a.sideCollapsed = true
	tt := ui.NewTester(a.View, 880, 560)
	click(t, tt, "重新扫描")
	if a.snap == nil {
		t.Fatal("footer scan did not run")
	}
	gen := a.scanGen
	tt.Key(ui.Cmd, ui.KeyR)
	if a.scanGen != gen+1 {
		t.Fatal("scan shortcut stopped working")
	}
	a.mutating = true
	tt.Frame()
	gen = a.scanGen
	click(t, tt, "重新扫描")
	if a.scanGen != gen {
		t.Fatal("footer scan was not disabled during mutation")
	}
}

// The sidebar's search sits under the title bar band, clear of the
// traffic lights, and the first item follows it without overlapping.
func TestSidebarSearchPlacement(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "alpha"), "alpha")
	check := func(t *testing.T, tt *ui.Tester, band float32, first string) {
		t.Helper()
		search, ok := tt.Find("搜索技能")
		if !ok || search.W <= 0 {
			t.Fatalf("missing sidebar search: %q", tt.Texts())
		}
		item, ok := tt.Find(first)
		if !ok {
			t.Fatalf("missing first sidebar item %q", first)
		}
		if search.Y < band-1 || item.Y < search.Y+search.H {
			t.Errorf("band=%.1f search=%+v %s=%+v", band, search, first, item)
		}
	}
	for _, bar := range []ui.TitleBar{{Height: 52, Left: 70}, {Height: 44, Right: 120}, {}} {
		for _, hideBrowse := range []bool{false, true} {
			t.Run(fmt.Sprintf("bar=%+v/hideBrowse=%t", bar, hideBrowse), func(t *testing.T) {
				a := New(home)
				a.startScan()
				a.page = effectivePage
				a.hidden["overview"], a.hidden["sessions"], a.hidden["issues"] = hideBrowse, hideBrowse, hideBrowse
				tt := ui.NewTester(a.View, 880, 760)
				tt.SetTitleBar(bar)
				first := "首页"
				if hideBrowse {
					first = "技能"
				}
				check(t, tt, controlsBandHeight(bar), first)
			})
		}
	}
	t.Run("large font", func(t *testing.T) {
		a := New(home)
		a.startScan()
		tt := ui.NewTester(func(c *ui.Context) {
			theme := *c.Theme()
			theme.FontSize = 18
			ui.Themed(c, &theme).Fill().Children(func() { a.View(c) })
		}, 1180, 760)
		check(t, tt, controlsBandHeight(ui.TitleBar{}), "首页")
	})
}

func TestTitleBarDragTargets(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills", "alpha"), "alpha")
	for _, collapsed := range []bool{false, true} {
		t.Run(fmt.Sprintf("collapsed=%t", collapsed), func(t *testing.T) {
			a := New(home)
			a.startScan()
			a.page = effectivePage
			a.sideCollapsed = collapsed
			unhandled := 0
			// A blank area without DragWindow falls through to this button.
			// A drag handle consumes the press before any Clicked event, just
			// as a real window routes it to the native startDrag callback.
			tt := ui.NewTester(func(c *ui.Context) {
				catch := ui.ButtonBase(c).Fill()
				catch.Children(func() { a.View(c) })
				if catch.Clicked() {
					unhandled++
				}
			}, 1180, 760)
			tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
			// Prove that the fallback observes presses outside the title bar.
			tt.ClickAt(1100, 735)
			if unhandled == 0 {
				t.Fatal("fallback did not observe content background")
			}
			points := [][2]float32{{a.sidebarWidth() + 12, 2}, {1175, 18}, {700, 18}}
			if !collapsed {
				points = append(points, [2]float32{100, 18}, [2]float32{6, 2})
			}
			for _, point := range points {
				before := unhandled
				tt.ClickAt(point[0], point[1])
				if unhandled != before {
					t.Errorf("title bar at %v did not consume drag press", point)
				}
			}
			click(t, tt, "搜索")
			tt.Type("alpha")
			if a.query != "alpha" {
				t.Fatal("drag handle intercepted search input")
			}
			click(t, tt, "存在覆盖")
			if a.eff.filter != 1 {
				t.Fatal("drag handle intercepted view switch")
			}
			toggle := "收起侧边栏"
			if collapsed {
				toggle = "展开侧边栏"
			}
			click(t, tt, toggle)
			if a.sideCollapsed == collapsed {
				t.Fatal("drag handle intercepted sidebar toggle")
			}
		})
	}
}
