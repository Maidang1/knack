package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"knack/internal/repair"
	"knack/internal/skill"
)

type documentState struct {
	loading  bool
	document skill.Document
	err      string
}

type comparisonState struct {
	loading     bool
	entry, pool skill.Entry
	err         string
	fixes       []repair.Fix
}

type graphState struct {
	loading bool
	graph   *skill.Graph
	err     string
}

func documentKey(e skill.Entry) string {
	return fmt.Sprintf("%s|%s|%d|%d", e.Path, e.RealPath, e.DocSize, e.DocModTime.UnixNano())
}

func (a *App) documentFor(e skill.Entry) *documentState {
	if !e.HasDocument {
		return nil
	}
	key := documentKey(e)
	if state := a.documents[key]; state != nil {
		return state
	}
	state := &documentState{loading: true}
	a.documents[key] = state
	run := func() {
		doc, err := skill.ReadDocument(e)
		a.post(func() {
			if a.documents[key] != state {
				return
			}
			state.loading = false
			state.document = doc
			if err != nil {
				state.err = err.Error()
			}
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
	return state
}

func (a *App) documentPane(c *ui.Context, e skill.Entry) {
	state := a.documentFor(e)
	if state == nil {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "SKILL.md").FontSize(14).FontWeight(600).Grow(1)
			if outlineButton(c, "reload-document", "重新读取").Disabled(state.loading).Clicked() {
				delete(a.documents, documentKey(e))
				c.Invalidate()
			}
		})
		switch {
		case state.loading:
			ui.Text(c, "正在读取正文…").FontSize(12).TextColor(t.TextMuted)
		case state.err != "":
			ui.Text(c, state.err).FontSize(12).TextColor(t.Warning)
		default:
			// Numbers and text share a size and line height and never wrap,
			// so each number stays beside its line.
			text := strings.TrimRight(state.document.Text, "\n")
			n := strings.Count(text, "\n") + 1
			numbers := make([]string, n)
			for i := range numbers {
				numbers[i] = strconv.Itoa(i + 1)
			}
			ui.ScrollHorizontal(c).Label("SKILL.md 正文").Radius(8).Border(1, t.Border).Background(t.Surface).Children(func() {
				ui.Row(c).Padding(12, 0).AlignItems(ui.Start).Children(func() {
					ui.Text(c, strings.Join(numbers, "\n")).Font(monoFont).FontSize(12).LineHeight(1.7).NoWrap().
						TextColor(token(t, "ink-3")).TextAlign(ui.End).MinWidth(28).Padding(0, 14, 0, 10).Shrink(0)
					ui.Text(c, text).Font(monoFont).FontSize(12).LineHeight(1.7).NoWrap().Selectable().Padding(0, 18, 0, 4)
				})
			})
		}
	})
}

func (a *App) compareButton(c *ui.Context, e skill.Entry) {
	if e.Relation != skill.RelCopy || a.cached {
		return
	}
	state := a.comparisons[e.Path]
	t := c.Theme()
	label := "比较共享池内容"
	if state != nil && state.loading {
		label = "正在比较…"
	}
	ink := token(t, "warn-ink")
	ui.Column(c).Gap(8).Padding(10, 14).Radius(8).Background(token(t, "warn-bg")).Label("独立副本").Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, glyph("copy")).FontSize(14).TextColor(ink)
			ui.Text(c, "这是一份独立副本，内容还没有和共享池比较。").FontSize(12.5).TextColor(ink).Grow(1).MinWidth(0)
			if outlineButton(c, "compare", label).Disabled(a.scanning || a.mutating || (state != nil && state.loading)).Clicked() {
				a.beginCompare(e)
			}
		})
		if state == nil || state.loading {
			return
		}
		if state.err != "" {
			ui.Text(c, state.err).FontSize(12).TextColor(t.Warning)
			return
		}
		ui.Text(c, state.entry.Relation.Label()).FontSize(12).TextColor(ink)
		// Only this explicitly compared pair is used to plan replacement.
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, f := range state.fixes {
				a.fixButton(c, f, false)
			}
		})
	})
}

func (a *App) comparisonSnapshot(state *comparisonState) *skill.Snapshot {
	entries := append([]skill.Entry(nil), a.snap.Entries...)
	for i := range entries {
		if entries[i].Path == state.entry.Path {
			entries[i] = state.entry
		}
		if entries[i].Path == state.pool.Path {
			entries[i] = state.pool
		}
	}
	return skill.FromCatalog(a.snap.Roots, entries, a.snap.Warnings, a.snap.At)
}

func (a *App) beginCompare(e skill.Entry) {
	pool, ok := a.snap.PoolEntry(e.Name)
	if !ok || !pool.Usable() {
		return
	}
	state := &comparisonState{loading: true}
	a.comparisons[e.Path] = state
	run := func() {
		entry, target, err := skill.CompareCopies(e, pool)
		a.post(func() {
			if a.comparisons[e.Path] != state {
				return
			}
			state.loading = false
			state.entry, state.pool = entry, target
			if err != nil {
				state.err = err.Error()
				return
			}
			state.entry.Relation = skill.RelStale
			if entry.TreeHash == target.TreeHash {
				state.entry.Relation = skill.RelSame
			}
			state.fixes = repair.For(a.comparisonSnapshot(state), state.entry, a.home)
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
}

// Body-based dependency inference is requested only when opening a graph.
func (a *App) graphFor(rootID string) *skill.Graph {
	state := a.graphs[rootID]
	if state != nil {
		if state.graph != nil {
			return state.graph
		}
		return a.snap.GraphFor(rootID)
	}
	entries := append([]skill.Entry(nil), a.snap.EntriesFor(rootID)...)
	state = &graphState{loading: true}
	a.graphs[rootID] = state
	run := func() {
		var errs []string
		for i := range entries {
			if !entries[i].HasDocument {
				continue
			}
			doc, err := skill.ReadDocument(entries[i])
			if err != nil {
				errs = append(errs, entries[i].Name+"："+err.Error())
				continue
			}
			_, entries[i].Mentions = skill.ParseRefs(doc.Text)
		}
		graph := skill.BuildGraph(entries)
		a.post(func() {
			if a.graphs[rootID] != state {
				return
			}
			state.loading = false
			state.graph = graph
			state.err = strings.Join(errs, "；")
		})
	}
	if a.win == nil {
		run()
	} else {
		go run()
	}
	if state.graph != nil {
		return state.graph
	}
	return a.snap.GraphFor(rootID)
}
