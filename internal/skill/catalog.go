package skill

import "time"

// FromCatalog rebuilds derived display data from a saved catalog. It does not
// read disk and must not be used to authorize filesystem changes.
func FromCatalog(roots []Root, entries []Entry, warnings []string, at time.Time) *Snapshot {
	s := &Snapshot{Roots: roots, Entries: entries, Warnings: warnings, At: at}
	analyze(s)
	return s
}
