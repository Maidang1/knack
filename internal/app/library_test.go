package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// libraryFixture adds a pool skill linked from Claude and copied by Factory,
// and a broken link in Codex.
func libraryFixture(t *testing.T) *App {
	t.Helper()
	a := agentPageFixture(t)
	pool := filepath.Join(a.home, ".agents", "skills", "review")
	writeSkill(t, pool, "review a change")
	if err := os.WriteFile(filepath.Join(pool, "notes.md"), []byte("see [missing](gone.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(pool, filepath.Join(a.home, ".claude", "skills", "review")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(a.home, ".factory", "skills", "review"), "review a change")
	if err := os.Symlink(filepath.Join(a.home, "nowhere"), filepath.Join(a.home, ".codex", "skills", "gone")); err != nil {
		t.Fatal(err)
	}
	snap := a.sessions.snapshot
	a.startScan()
	a.sessions.snapshot, a.sessions.overview = snap, nil
	return a
}

func TestLibraryPage(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a := libraryFixture(t)
			var revealed string
			a.revealFn = func(p string) { revealed = p }
			tt := ui.NewTester(a.View, 1280, 900)
			tt.SetDark(dark)
			click(t, tt, "技能库")
			for _, s := range []string{"共享池", "Agent · Claude", "Agent · Codex", "review", "gone"} {
				if !tt.HasText(s) {
					t.Fatalf("library misses %q: %q", s, tt.Texts())
				}
			}
			// Agent entries of a pool skill are its reach, not rows of their own.
			if got := len(a.rows()); got != a.libCounts[0] {
				t.Fatalf("rows %d, counted %d", got, a.libCounts[0])
			}
			for _, r := range a.rows() {
				if r.name == "review" && r.entry.RootRole != "pool" {
					t.Fatalf("pool skill listed again from %s", r.entry.RootName)
				}
			}

			click(t, tt, "有问题")
			names := map[string]bool{}
			for _, r := range a.rows() {
				names[r.name] = true
			}
			if !names["gone"] || names["unused"] || len(names) != a.libCounts[3] {
				t.Fatalf("problem filter: %v", names)
			}
			click(t, tt, "全部")

			click(t, tt, "review")
			for _, s := range []string{"位置：共享池 / review", "挂载关系", "独立副本：Factory", "SKILL.md · 2 个文件"} {
				if _, ok := tt.Find(s); !ok && !tt.HasText(s) {
					t.Fatalf("detail misses %q: %q", s, tt.Texts())
				}
			}
			click(t, tt, "在访达中显示")
			if revealed == "" {
				t.Fatal("reveal was not called")
			}
			click(t, tt, "文件")
			if _, ok := tt.Find("打开 notes.md"); !ok {
				t.Fatal("file tab misses notes.md")
			}
			click(t, tt, "依赖")
			for i := 0; i < 20 && !tt.HasText("gone.md"); i++ {
				tt.Frame()
			}
			if !tt.HasText("gone.md") {
				t.Fatalf("deps tab misses the missing file: %q", tt.Texts())
			}
		})
	}
}

func TestSidebarSearchFindsSessions(t *testing.T) {
	a := libraryFixture(t)
	tt := ui.NewTester(a.View, 1280, 900)
	click(t, tt, "技能库")
	click(t, tt, "搜索技能")
	tt.Type("构建")
	want := 0
	for _, row := range a.sessions.snapshot.Sessions {
		if matchSession("构建", row) {
			want++
		}
	}
	if want < 2 {
		t.Fatalf("fixture has %d matching sessions", want)
	}
	link := fmt.Sprintf("在会话中搜索“构建” · %d 个", want)
	click(t, tt, link)
	if a.page != "sessions" || a.sessions.query != "构建" || a.sessions.open {
		t.Fatalf("link went to %s with %q", a.page, a.sessions.query)
	}
	if got := len(a.sessionRows()); got != want {
		t.Fatalf("session list shows %d, want %d", got, want)
	}
	// On the sessions page the sidebar's field filters sessions directly.
	click(t, tt, "搜索技能")
	tt.Type("另一项")
	if a.sessions.query != a.query || len(a.sessionRows()) != 1 {
		t.Fatalf("sidebar query %q did not reach sessions (%q)", a.query, a.sessions.query)
	}
}

func TestUsagePage(t *testing.T) {
	a := agentPageFixture(t)
	tt := ui.NewTester(a.View, 1280, 1000)
	click(t, tt, "用量与耗时")
	if a.page != usagePage {
		t.Fatalf("page %s", a.page)
	}
	d := a.overviewData()
	timing := a.usageTimingFor("", d)
	if timing.turns == 0 {
		t.Fatal("fixture has no timed turns")
	}
	if _, ok := tt.Find("耗时构成"); !ok {
		t.Fatalf("usage page misses timing: %q", tt.Texts())
	}
	if !tt.HasText(fmt.Sprintf("%d 轮", timing.turns)) && !containsPrefix(tt.Texts(), fmt.Sprintf("%d 轮 · ", timing.turns)) {
		t.Fatalf("timing summary missing: %q", tt.Texts())
	}
}

func containsPrefix(texts []string, prefix string) bool {
	for _, s := range texts {
		if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// On a tall window the totals stay put and each list scrolls on its own.
func TestUsagePageSplitScroll(t *testing.T) {
	for _, dark := range []bool{false, true} {
		a := agentPageFixture(t)
		tt := ui.NewTester(a.View, 1280, usageSplitHeight)
		tt.SetDark(dark)
		click(t, tt, "用量与耗时")
		left, okLeft := tt.Find("用量左栏")
		right, okRight := tt.Find("用量右栏")
		if !okLeft || !okRight || right.X <= left.X {
			t.Fatalf("lists not side by side: %+v %+v", left, right)
		}
		at := func(label string) float32 {
			r, ok := tt.Find(label)
			if !ok {
				t.Fatalf("missing %s: %q", label, tt.Texts())
			}
			return r.Y
		}
		head, models, skills := at("耗时构成"), at("模型输出速率"), at("Skill 调用排行")
		tt.Scroll(left.X+left.W/2, left.Y+40, 0, 120)
		if at("耗时构成") != head || at("Skill 调用排行") != skills {
			t.Fatal("scrolling the left list moved the totals or the right list")
		}
		if at("模型输出速率") >= models {
			t.Fatal("left list did not scroll")
		}
		exportSessionPreview(t, tt, fmt.Sprintf("usage-split-dark-%t.png", dark))
		// A short window scrolls as one page.
		tt = ui.NewTester(a.View, 1280, 600)
		if _, ok := tt.Find("用量左栏"); ok {
			t.Fatal("short window split the page")
		}
	}
}
