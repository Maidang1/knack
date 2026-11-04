package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"skill-manager/internal/repair"
	"skill-manager/internal/skill"
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
	card(c).Padding(12, 14).Gap(8).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "SKILL.md 内容").FontSize(13).FontWeight(600).Grow(1)
			if ui.Button(c, "重新读取").Disabled(state.loading).Clicked() {
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
			ui.Text(c, state.document.Text).FontSize(12).LineHeight(1.5).Selectable()
		}
	})
}

func (a *App) compareButton(c *ui.Context, e skill.Entry) {
	if e.Relation != skill.RelCopy || a.cached {
		return
	}
	state := a.comparisons[e.Path]
	label := "比较共享池内容"
	if state != nil && state.loading {
		label = "正在比较…"
	}
	if ui.Button(c, label).AlignSelf(ui.Start).Disabled(a.scanning || a.mutating || (state != nil && state.loading)).Clicked() {
		a.beginCompare(e)
	}
	if state == nil {
		return
	}
	if state.err != "" {
		ui.Text(c, state.err).FontSize(12).TextColor(c.Theme().Warning)
		return
	}
	if state.loading {
		return
	}
	ui.Text(c, state.entry.Relation.Label()).FontSize(12).TextColor(c.Theme().TextMuted)
	// Only this explicitly compared pair is used to plan replacement.
	for _, f := range state.fixes {
		a.fixButton(c, f, false)
	}
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
