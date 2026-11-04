package app

import (
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"skill-manager/internal/skill"
)

func TestSettingsGearOutline(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(ui.Hex("#ffffff")).Children(func() {
			ui.Icon(c, glyph("gear")).FontSize(96).TextColor(ui.Hex("#000000"))
		})
	}, 96, 96)
	im := tt.Image()
	seen := map[image.Point]bool{}
	components := 0
	for y := range 96 {
		for x := range 96 {
			start := image.Pt(x, y)
			if seen[start] || im.RGBAAt(x, y).R >= 128 {
				continue
			}
			seen[start] = true
			queue := []image.Point{start}
			for i := 0; i < len(queue); i++ {
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						p := queue[i].Add(image.Pt(dx, dy))
						if p.In(im.Bounds()) && !seen[p] && im.RGBAAt(p.X, p.Y).R < 128 {
							seen[p] = true
							queue = append(queue, p)
						}
					}
				}
			}
			if len(queue) > 8 {
				components++
			}
		}
	}
	// A cog has one joined toothed outline and one central ring, not
	// the eight detached rays of the old sun-shaped settings glyph.
	if components != 2 {
		t.Fatalf("gear has %d disconnected strokes, want outer cog and inner ring", components)
	}
}

func TestSettingsPaintAfterGraphZoom(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{1180, 880} {
			t.Run(fmt.Sprintf("dark=%t/width=%d", dark, width), func(t *testing.T) {
				height := 760
				if width == 880 {
					height = 560
				}
				a, tt := graphFixture(t)
				for _, name := range []string{"Adal", "Aiden", "Augment", "Bifrost", "Bob", "Botmux", "Claude", "Cline", "Coco", "Codex", "Factory"} {
					a.snap.Roots = append(a.snap.Roots, skill.Root{ID: name, Name: name, Role: skill.RoleAgent, Path: filepath.Join(a.home, "."+strings.ToLower(name), "skills")})
				}
				tt.SetSize(width, height)
				tt.SetScale(2)
				tt.SetDark(dark)
				tt.Frame()
				for _, zoom := range []float32{1, .69} {
					a.state("pool").graph.zoom = zoom
					tt.Frame()
					click(t, tt, "设置")
					if !a.settingsOpen {
						t.Fatal("settings did not open")
					}
					for _, tab := range []int{0, 1, 2, 1} {
						click(t, tt, "设置分类："+[]string{"浏览", "Agent", "自带"}[tab])
						if a.settingsTab != tab {
							t.Fatalf("category click selected %d, want %d", a.settingsTab, tab)
						}
						im := tt.Image()
						// Check pixels, not merely accessibility text presence.
						labels := []string{"设置分类", "设置标题", "完成"}
						if tab != 2 {
							group := []string{"浏览", "Agent"}[tab]
							labels = append(labels, group+"全部显示", group+"全部隐藏")
						} else {
							labels = append(labels, "没有扫描到这一类内容。")
						}
						for _, label := range labels {
							r, ok := tt.Find(label)
							if !ok || r.Y < 0 || r.Y+r.H > float32(height) {
								t.Fatalf("unreachable %s: %+v", label, r)
							}
							painted := 0
							// Ignore control borders and look for foreground ink.
							for y := int((r.Y + 5) * 2); y < int((r.Y+r.H-5)*2); y++ {
								for x := int((r.X + 5) * 2); x < int((r.X+r.W-5)*2); x++ {
									p := im.RGBAAt(x, y)
									if dark && p.R > 130 && p.G > 130 && p.B > 130 || !dark && p.R < 150 && p.G < 150 && p.B < 150 {
										painted++
									}
								}
							}
							if painted < 8 {
								t.Errorf("%s has no readable foreground at graph zoom %v", label, zoom)
							}
						}
						nav, _ := tt.Find("设置分类")
						title, _ := tt.Find("设置标题")
						list, _ := tt.Find("设置列表")
						if nav.X+nav.W >= title.X || title.Y >= list.Y || list.H < 160 {
							t.Fatalf("settings lost sidebar/content layout: nav=%+v title=%+v list=%+v", nav, title, list)
						}
					}
					click(t, tt, "完成")
					if a.settingsOpen {
						t.Fatal("settings did not close")
					}
				}
			})
		}
	}
}

func TestSettingsKeyboardAndVisibility(t *testing.T) {
	a, tt := graphFixture(t)
	// Open from focused graph and keep repeated shortcut presses idempotent.
	click(t, tt, "依赖图画布")
	tt.Key(ui.Cmd, ui.KeyComma)
	if !a.settingsOpen {
		t.Fatal("Cmd+, did not open settings")
	}
	click(t, tt, "设置分类：浏览")
	tt.Key(0, ui.KeyDown)
	if a.settingsTab != 1 || !tt.Focused("设置分类：Agent") {
		t.Fatal("Down did not move category and keyboard focus")
	}
	tt.Key(0, ui.KeyDown)
	if a.settingsTab != 2 || !tt.Focused("设置分类：自带") {
		t.Fatal("Down did not reach builtins")
	}
	tt.Key(0, ui.KeyHome)
	if a.settingsTab != 0 {
		t.Fatal("Home did not return to first category")
	}
	click(t, tt, "浏览全部隐藏")
	if !a.hidden["pool"] || !a.hidden["issues"] || a.page != effectivePage {
		t.Fatal("bulk hide no longer updates visibility and current page")
	}
	click(t, tt, "浏览全部显示")
	if a.hidden["pool"] || a.hidden["issues"] {
		t.Fatal("bulk show did not restore browse entries")
	}
	tt.Key(ui.Cmd, ui.KeyComma)
	if !a.settingsOpen || a.settingsTab != 0 {
		t.Fatal("repeated shortcut changed settings state")
	}
	tt.Key(0, ui.KeyEscape)
	if a.settingsOpen {
		t.Fatal("Escape did not close settings")
	}
	a.sideCollapsed = true
	tt.Frame()
	click(t, tt, "搜索")
	tt.Type("unchanged")
	tt.Key(ui.Cmd, ui.KeyComma)
	if !a.settingsOpen || a.query != "unchanged" {
		t.Fatal("shortcut in search failed or changed search text")
	}
	click(t, tt, "完成")
	a.ask.open = true
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if a.settingsOpen {
		t.Fatal("settings stacked over a confirmation dialog")
	}
	a.ask.open = false
	a.repoDialog = &repoDialogState{open: true}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	if a.settingsOpen {
		t.Fatal("settings stacked over repository dialog")
	}
	// Browse visibility remains configurable even before the first scan.
	empty := New(t.TempDir())
	initial := ui.NewTester(empty.View, 880, 560)
	initial.Key(ui.Cmd, ui.KeyComma)
	if !empty.settingsOpen {
		t.Fatal("shortcut unavailable before first scan")
	}
}

func TestSettingsLongListScroll(t *testing.T) {
	a, tt := graphFixture(t)
	a.sideCollapsed = true
	for i := range 18 {
		name := fmt.Sprintf("Agent %02d", i)
		a.snap.Roots = append(a.snap.Roots, skill.Root{ID: name, Name: name, Role: skill.RoleAgent, Path: filepath.Join(a.home, name)})
	}
	tt.SetSize(880, 560)
	tt.Key(ui.Cmd, ui.KeyComma)
	click(t, tt, "设置分类：Agent")
	nav, _ := tt.Find("设置分类")
	done, _ := tt.Find("完成")
	list, _ := tt.Find("设置列表")
	graph := a.state("pool").graph
	tt.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 2000)
	last, ok := tt.Find("Agent 17")
	if !ok || last.Y < list.Y || last.Y+last.H > list.Y+list.H {
		t.Fatalf("last settings row did not scroll into view: %+v in %+v", last, list)
	}
	if got, _ := tt.Find("完成"); got != done {
		t.Fatal("scroll moved the close button")
	}
	if got, _ := tt.Find("设置分类"); got != nav {
		t.Fatal("scroll moved the category navigation")
	}
	if got := a.state("pool").graph; got.x != graph.x || got.y != graph.y || got.zoom != graph.zoom {
		t.Fatal("settings scroll moved the underlying graph")
	}
	tt.ClickAt(list.X+list.W-30, last.Y+last.H/2)
	if !a.hidden["Agent 17"] || !New(a.home).hidden["Agent 17"] {
		t.Fatal("last row switch did not persist visibility")
	}
	click(t, tt, "设置分类：浏览")
	if r, ok := tt.Find("浏览全部显示"); !ok || r.Y < list.Y || r.Y+r.H > list.Y+list.H {
		t.Fatal("category switch inherited another category's scroll offset")
	}
}
