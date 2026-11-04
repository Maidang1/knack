// Package skill reads skill directories on disk. The shared pool and each
// agent directory are the source of truth; this package does not consult
// the Knack database.
package skill

import "time"

// Role is where a skill directory sits.
type Role string

const (
	RolePool    Role = "pool"
	RoleAgent   Role = "agent"
	RoleBuiltin Role = "builtin"
	RoleRepo    Role = "repo"
)

// Kind is what an entry is on disk.
type Kind string

const (
	KindSkill   Kind = "skill"  // real directory with SKILL.md
	KindEmpty   Kind = "empty"  // real directory without SKILL.md
	KindLink    Kind = "link"   // symlink whose target exists
	KindBroken  Kind = "broken" // symlink whose target is gone
	KindUnknown Kind = "unknown"
)

// Relation is how an entry sits against the shared pool.
type Relation string

const (
	RelPool     Relation = "pool"
	RelLinked   Relation = "linked"   // symlink to the pool entry
	RelCopy     Relation = "copy"     // real copy, not compared yet
	RelSame     Relation = "same"     // real copy, same tree hash
	RelStale    Relation = "stale"    // real copy, different tree hash
	RelLocal    Relation = "local"    // not in the pool
	RelExternal Relation = "external" // symlink that does not go through the pool
	RelBroken   Relation = "broken"
	RelEmpty    Relation = "empty"
	RelSystem   Relation = "system" // shipped with an app, or under .system
	RelUnknown  Relation = "unknown"
)

// IssueKind is a problem worth acting on in an agent or the pool.
type IssueKind string

const (
	IssueBroken    IssueKind = "broken"
	IssueStale     IssueKind = "stale"
	IssueDuplicate IssueKind = "duplicate"
	IssueEmpty     IssueKind = "empty"
	IssueOrphan    IssueKind = "orphan"
)

// Root is one skills directory.
type Root struct {
	ID   string
	Name string
	Path string
	Role Role
}

// Entry is one skill slot inside a root.
type Entry struct {
	RootID   string
	RootName string
	RootRole Role
	RootPath string

	Name         string
	Path         string
	SlotPath     string // physical parent plus basename; final link is not resolved
	Kind         Kind
	System       bool // under a .system directory, or the root is read-only
	Vacant       bool // directory has nothing but ignored junk
	LinkTarget   string
	LinkAbs      string
	RealPath     string
	FrontName    string
	Description  string
	HasDocument  bool
	DocSize      int64
	DocModTime   time.Time
	EntryModTime time.Time // Lstat timestamp of the directory or symlink
	MetaErr      string
	SkillHash    string
	TreeHash     string
	Children     []string
	// Requires are the skills the front matter lists as dependencies.
	// Scan does not populate Mentions or Children. On-demand ParseRefs and
	// BuildGraph callers can supply them explicitly.
	Requires []string
	Mentions []string
	HashErr  string
	Relation Relation
}

// Usable reports a pool entry agents can be pointed at.
func (e Entry) Usable() bool {
	if e.Kind != KindSkill && e.Kind != KindLink {
		return false
	}
	return e.HasDocument && e.MetaErr == ""
}

// Issue is one row on the problems page.
type Issue struct {
	Kind   IssueKind
	Entry  Entry
	Detail string
}

// Summary counts how one root relates to the pool.
type Summary struct {
	Root     Root
	Total    int
	Linked   int
	Copy     int
	Same     int
	Stale    int
	Broken   int
	Empty    int
	Local    int
	External int
	System   int
	Issues   int
}

// Snapshot is one finished scan.
type Snapshot struct {
	Roots     []Root
	Entries   []Entry
	Issues    []Issue
	Summaries []Summary
	PoolIndex map[string]int
	// Linked lists agent names whose symlink targets the pool entry.
	Linked   map[string][]string
	Graphs   map[string]*Graph // dependency graph of each root, by root ID
	Warnings []string
	At       time.Time
}

// GraphFor returns the dependency graph of rootID, or an empty one.
func (s *Snapshot) GraphFor(rootID string) *Graph {
	if s != nil {
		if g := s.Graphs[rootID]; g != nil {
			return g
		}
	}
	return &Graph{}
}

// PoolEntry returns the shared-pool entry named name.
func (s *Snapshot) PoolEntry(name string) (Entry, bool) {
	if s == nil || s.PoolIndex == nil {
		return Entry{}, false
	}
	i, ok := s.PoolIndex[name]
	if !ok || i < 0 || i >= len(s.Entries) {
		return Entry{}, false
	}
	return s.Entries[i], true
}

// EntriesFor returns entries that belong to rootID, in scan order.
func (s *Snapshot) EntriesFor(rootID string) []Entry {
	if s == nil {
		return nil
	}
	var out []Entry
	for _, e := range s.Entries {
		if e.RootID == rootID {
			out = append(out, e)
		}
	}
	return out
}

// IssueCount is how many issues belong to rootID. The empty rootID counts all.
func (s *Snapshot) IssueCount(rootID string) int {
	if s == nil {
		return 0
	}
	if rootID == "" {
		return len(s.Issues)
	}
	n := 0
	for _, issue := range s.Issues {
		if issue.Entry.RootID == rootID {
			n++
		}
	}
	return n
}
