package app

import (
	"knack/internal/repair"
	"knack/internal/skill"
)

// viewCache lasts for one immutable scan result. Mutations still validate
// fresh disk state; no cached plan authorizes a filesystem change.
type viewCache struct {
	snap     *skill.Snapshot
	entries  map[string][]skill.Entry
	removals map[string]repair.Removal
	fixes    map[string][]repair.Fix
	plan     []repair.Fix
	planRead bool

	page, query string
	filter      int
	rows        []row
	rowsRead    bool
}

func (a *App) viewCache() *viewCache {
	if a.cache.snap != a.snap {
		a.cache = viewCache{
			snap:     a.snap,
			entries:  make(map[string][]skill.Entry),
			removals: make(map[string]repair.Removal),
			fixes:    make(map[string][]repair.Fix),
		}
	}
	return &a.cache
}

func (a *App) removalFor(e skill.Entry) repair.Removal {
	if a.cached {
		return repair.Removal{}
	}
	cache := a.viewCache()
	if r, ok := cache.removals[e.Path]; ok {
		return r
	}
	r := repair.RemovalFor(a.snap, e)
	cache.removals[e.Path] = r
	return r
}

func (a *App) fixesFor(e skill.Entry) []repair.Fix {
	if a.cached {
		return nil
	}
	cache := a.viewCache()
	if fixes, ok := cache.fixes[e.Path]; ok {
		return fixes
	}
	fixes := repair.For(a.snap, e, a.home)
	cache.fixes[e.Path] = fixes
	return fixes
}

func (a *App) repairPlan() []repair.Fix {
	if a.cached {
		return nil
	}
	cache := a.viewCache()
	if !cache.planRead {
		cache.plan = repair.Plan(a.snap, a.home)
		cache.planRead = true
	}
	return cache.plan
}

func (a *App) entriesFor(rootID string) []skill.Entry {
	cache := a.viewCache()
	if entries, ok := cache.entries[rootID]; ok {
		return entries
	}
	entries := a.snap.EntriesFor(rootID)
	cache.entries[rootID] = entries
	return entries
}

func (a *App) rows() []row {
	if a.snap == nil {
		return nil
	}
	cache := a.viewCache()
	if !cache.rowsRead || cache.page != a.page || cache.query != a.query || cache.filter != a.filter {
		cache.rows = a.buildRows()
		cache.page, cache.query, cache.filter = a.page, a.query, a.filter
		cache.rowsRead = true
	}
	return cache.rows
}
