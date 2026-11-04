// Package repair turns what a scan found into changes that fix it. It plans
// fixes and applies them through package manage, so every one can be undone.
package repair

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"knack/internal/fsutil"
	"knack/internal/manage"
	"knack/internal/skill"
)

// Op is the kind of change a fix makes.
type Op string

const (
	OpRelink     Op = "relink"      // a real copy becomes a symlink to the pool
	OpRetarget   Op = "retarget"    // a symlink is pointed at the pool
	OpRemoveLink Op = "remove-link" // a broken symlink is taken away
	OpRemoveDir  Op = "remove-dir"  // an empty directory is moved aside
	OpLinkPool   Op = "link-pool"   // a repo skill is linked into the pool
	OpLinkAgent  Op = "link-agent"  // a pool skill is linked into an agent
)

// Fix is one change that repairs one entry.
type Fix struct {
	Op    Op
	Issue skill.IssueKind // set by Plan; empty for fixes that fix no issue
	Entry skill.Entry
	// Root is the directory the change happens in when it is not the
	// entry's own root, and Target what a symlink is pointed at.
	Root       string
	Target     string
	TargetHash string // pool content confirmed when planning a relink
	// Err is why the fix cannot be applied as planned.
	Err string

	home   string
	repos  []string
	roots  []skill.Root
	target skill.Entry
	// Safe fixes lose nothing and can be applied in a batch. The others
	// replace a copy whose content differs, so the user decides one by one.
	Safe bool

	Label   string // button
	Title   string // confirmation title
	Message string // confirmation text
	Confirm string // confirm button
	Done    string // toast after it ran
}

// For lists the fixes that apply to e, the best first. Entries shipped with
// an app are never changed.
func For(snap *skill.Snapshot, e skill.Entry, home string) []Fix {
	if snap == nil || e.System || e.RootRole == skill.RoleBuiltin {
		return nil
	}
	var fixes []Fix
	switch e.RootRole {
	case skill.RoleAgent:
		fixes = agentFixes(snap, e)
	case skill.RolePool:
		fixes = poolFixes(e)
	case skill.RoleRepo:
		return repoFixes(snap, e, home)
	}
	var out []Fix
	for _, f := range fixes {
		if f.Op == OpRemoveLink || f.Op == OpRemoveDir {
			r := RemovalFor(snap, e)
			if !r.Available || r.Err != "" {
				continue
			}
			f.home = home
			f.roots = append([]skill.Root(nil), snap.Roots...)
			for _, root := range snap.Roots {
				if root.Role == skill.RoleRepo {
					f.repos = append(f.repos, root.Path)
				}
			}
		}
		out = append(out, f)
	}
	return out
}

func agentFixes(snap *skill.Snapshot, e skill.Entry) []Fix {
	switch e.Relation {
	case skill.RelCopy:
		return nil
	case skill.RelSame:
		pool, ok := usablePool(snap, e.Name)
		if !ok || e.TreeHash == "" || pool.TreeHash == "" {
			return nil
		}
		return []Fix{{
			Op: OpRelink, Entry: e, Target: pool.Path, TargetHash: pool.TreeHash, target: pool, Safe: true,
			Label: "改为软链", Title: "改为指向共享池的软链？",
			Message: "现在这份和共享池内容一致。改成软链后，这里跟着共享池变。原来的目录进撤销记录。",
			Confirm: "确认改为软链", Done: "已改为软链",
		}}
	case skill.RelStale:
		pool, ok := usablePool(snap, e.Name)
		if !ok || e.TreeHash == "" || pool.TreeHash == "" {
			return nil
		}
		return []Fix{{
			Op: OpRelink, Entry: e, Target: pool.Path, TargetHash: pool.TreeHash, target: pool,
			Label: "改为软链", Title: "改成软链会换掉现在这份",
			Message: "这份和共享池同名，目录内容不一致。改成软链后，现在这份进撤销记录，这里跟着共享池。",
			Confirm: "确认改为软链", Done: "已改为软链",
		}}
	case skill.RelBroken:
		var out []Fix
		if pool, ok := usablePool(snap, e.Name); ok {
			out = append(out, Fix{
				Op: OpRetarget, Entry: e, Target: pool.Path, Safe: true,
				Label: "改指向共享池", Title: "把断链改指向共享池？",
				Message: "共享池里有同名且可用的一份。这条软链会改指向它。",
				Confirm: "确认改指向", Done: "已改指向共享池",
			})
		}
		return append(out, removeLink(e))
	case skill.RelEmpty:
		return emptyFixes(e)
	}
	return nil
}

func poolFixes(e skill.Entry) []Fix {
	switch e.Relation {
	case skill.RelBroken:
		f := removeLink(e)
		f.Title = "去掉共享池里的这条断链？"
		return []Fix{f}
	case skill.RelEmpty:
		return emptyFixes(e)
	}
	return nil
}

func removeLink(e skill.Entry) Fix {
	return Fix{
		Op: OpRemoveLink, Entry: e, Safe: true,
		Label: "去掉软链", Title: "去掉这条软链？",
		Message: "软链会去掉。原来的目标记在撤销记录里。",
		Confirm: "确认去掉", Done: "已去掉软链",
	}
}

func emptyFixes(e skill.Entry) []Fix {
	if !e.Vacant {
		return nil
	}
	return []Fix{{
		Op: OpRemoveDir, Entry: e, Safe: true,
		Label: "移走空目录", Title: "把这个空目录移到撤销记录？",
		Message: "目录是空的，没有 SKILL.md。移走后可以撤销。",
		Confirm: "确认移走", Done: "已移走空目录",
	}}
}

func repoFixes(snap *skill.Snapshot, e skill.Entry, home string) []Fix {
	pool, has := snap.PoolEntry(e.Name)
	if has && pool.Kind != skill.KindBroken {
		return nil
	}
	f := Fix{
		Op: OpLinkPool, Entry: e, Target: e.Path,
		Label: "挂到共享池", Title: "在共享池里挂上这条软链？",
		Message: "会在共享池里放一条指向仓库的软链。Grok 直接读共享池，所以能看见。其他 agent 的目录保持原样。",
		Confirm: "确认挂到共享池", Done: "已挂到共享池",
	}
	if root, ok := PoolRoot(snap, home); ok {
		f.Root = root
	} else {
		f.Err = "还没有共享池目录 ~/.agents/skills"
	}
	return []Fix{f}
}

// LinkToAgent plans linking a pool skill into an agent's skills directory.
func LinkToAgent(pool skill.Entry, agent skill.Root) Fix {
	return Fix{
		Op: OpLinkAgent, Entry: pool, Root: agent.Path, Target: pool.Path,
		Label: agent.Name, Title: "挂到 " + agent.Name + "？",
		Message: "会在 " + agent.Name + " 的 skills 目录里放一条指向共享池的软链。共享池本身不变。",
		Confirm: "确认挂出", Done: "已挂到 " + agent.Name,
	}
}

// PoolRoot is the shared pool's directory.
func PoolRoot(snap *skill.Snapshot, home string) (string, bool) {
	if snap != nil {
		for _, r := range snap.Roots {
			if r.Role == skill.RolePool {
				return r.Path, true
			}
		}
	}
	p := filepath.Join(home, ".agents", "skills")
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p, true
	}
	return "", false
}

func usablePool(snap *skill.Snapshot, name string) (skill.Entry, bool) {
	pool, ok := snap.PoolEntry(name)
	if !ok || !pool.Usable() {
		return skill.Entry{}, false
	}
	return pool, true
}

// Plan lists the best fix for every issue a scan found that can be fixed.
// Orphans have none: which agent should show a skill is the user's choice.
func Plan(snap *skill.Snapshot, home string) []Fix {
	if snap == nil {
		return nil
	}
	var out []Fix
	for _, issue := range snap.Issues {
		fixes := For(snap, issue.Entry, home)
		if len(fixes) == 0 {
			continue
		}
		f := fixes[0]
		f.Issue = issue.Kind
		out = append(out, f)
	}
	return out
}

// Split separates the fixes that lose nothing from those that replace a
// copy whose content differs from the pool's.
func Split(fixes []Fix) (safe, review []Fix) {
	for _, f := range fixes {
		if f.Safe {
			safe = append(safe, f)
		} else {
			review = append(review, f)
		}
	}
	return safe, review
}

// Summary describes fixes in words, one line for each kind of change.
func Summary(fixes []Fix) []string {
	counts := map[Op]int{}
	for _, f := range fixes {
		counts[f.Op]++
	}
	var out []string
	for _, line := range []struct {
		op   Op
		text string
	}{
		{OpRetarget, "条断链改指向共享池"},
		{OpRemoveLink, "条断链去掉"},
		{OpRelink, "个副本改为软链"},
		{OpRemoveDir, "个空目录移走"},
		{OpLinkPool, "个仓库 skill 挂到共享池"},
		{OpLinkAgent, "个 skill 挂到 agent"},
	} {
		if n := counts[line.op]; n > 0 {
			out = append(out, fmt.Sprintf("%d %s", n, line.text))
		}
	}
	return out
}

// Apply makes one fix and returns the record that undoes it.
func Apply(f Fix, undoRoot string) (manage.Record, error) {
	if f.Err != "" {
		return manage.Record{}, errors.New(f.Err)
	}
	e := f.Entry
	switch f.Op {
	case OpRelink:
		if e.TreeHash == "" || f.TargetHash == "" {
			return manage.Record{}, errors.New("请先比较这份副本和共享池的内容")
		}
		current, pool, err := skill.CompareCopies(e, f.target)
		if err != nil {
			return manage.Record{}, err
		}
		if current.TreeHash != e.TreeHash || pool.TreeHash != f.TargetHash || !fsutil.EqualPath(pool.Path, f.Target) {
			return manage.Record{}, errors.New("这份副本或共享池已变化，请重新比较后确认")
		}
		return manage.RelinkDir(e.RootPath, e.Path, f.Target, undoRoot)
	case OpRetarget:
		return manage.Retarget(e.RootPath, e.Path, f.Target, undoRoot)
	case OpRemoveLink, OpRemoveDir:
		return ApplyRemoval(e, f.home, f.repos, undoRoot, f.roots...)
	case OpLinkPool:
		return manage.LinkIntoPool(f.Root, e.Name, f.Target, undoRoot)
	case OpLinkAgent:
		return manage.LinkIntoAgent(f.Root, e.Name, f.Target, undoRoot)
	}
	return manage.Record{}, errors.New("不认识的修复")
}

// ApplyAll makes every fix it can. It returns the records of those that
// worked together with an error naming how many did not.
func ApplyAll(fixes []Fix, undoRoot string) ([]manage.Record, error) {
	var recs []manage.Record
	var first error
	failed := 0
	for _, f := range fixes {
		rec, err := Apply(f, undoRoot)
		if err != nil {
			failed++
			if first == nil {
				first = fmt.Errorf("%s：%w", f.Entry.Name, err)
			}
			continue
		}
		recs = append(recs, rec)
	}
	if failed > 0 {
		return recs, fmt.Errorf("%d 项没能修复，%w", failed, first)
	}
	return recs, nil
}
