package app

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestIssueHeaderLayout(t *testing.T) {
	home := t.TempDir()
	for i := range 12 {
		writeSkill(t, filepath.Join(home, ".agents", "skills", fmt.Sprintf("orphan-%02d", i)), "shared skill")
	}
	for _, width := range []int{1380, 1180, 880} {
		for _, listWidth := range []float32{440, 180} {
			t.Run(fmt.Sprintf("width=%d/list=%g", width, listWidth), func(t *testing.T) {
				a := New(home)
				a.startScan()
				a.page = "issues"
				a.listW = listWidth
				tt := ui.NewTester(a.View, width, 560)
				tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 70})
				track, ok := tt.Find("问题类别")
				if !ok {
					t.Fatal("missing issue filters")
				}
				search, _ := tt.Find("搜索")
				subtitle, _ := tt.Find(a.pageSubtitle())
				if track.X != search.X || track.X+track.W != search.X+search.W {
					t.Errorf("filters and search edges differ: filters=%+v search=%+v", track, search)
				}
				if subtitle.Y+subtitle.H > track.Y || track.Y+track.H+4 > search.Y {
					t.Errorf("header controls overlap or lack spacing: subtitle=%+v filters=%+v search=%+v", subtitle, track, search)
				}
				for _, filter := range issueFilters {
					r, ok := tt.Find(filter.label)
					if !ok || r.W <= 0 || r.H <= 0 || r.X < track.X || r.X+r.W > track.X+track.W || r.Y < track.Y || r.Y+r.H > track.Y+track.H {
						t.Errorf("filter %q is clipped: %+v in %+v", filter.label, r, track)
					}
				}
				click(t, tt, "未挂出")
				if a.filter != len(issueFilters)-1 {
					t.Fatal("last filter is not clickable")
				}
				tt.Key(0, ui.KeyHome)
				if a.filter != 0 {
					t.Fatal("filter keyboard navigation failed")
				}
			})
		}
	}
}
