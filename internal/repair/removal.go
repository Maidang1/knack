package repair

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"skill-manager/internal/manage"
	"skill-manager/internal/skill"
)

// Removal describes removing one slot, not deleting a symlink's source.
type Removal struct {
	Label, Title, Message, Confirm, Done, Err string
	Available                                 bool
	// Links are the agent links that still point at the entry. When they are
	// the only thing blocking removal, Unlinkable is true and the caller may
	// choose to remove them along with the entry or to leave them dangling.
	Links      []skill.Entry
	Unlinkable bool
}

// LinkPolicy says what to do with links that point at the removed entry.
type LinkPolicy int

const (
	LinksBlock  LinkPolicy = iota // refuse while any link remains
	LinksRemove                   // remove them first, as part of one undoable step
	LinksKeep                     // leave them; they become broken links
)

// RemovalFor checks both ordinary removals and the existing broken/empty fixes.
func RemovalFor(snap *skill.Snapshot, e skill.Entry) Removal {
	if snap == nil || e.System || (e.RootRole != skill.RoleAgent && e.RootRole != skill.RolePool) ||
		filepath.Dir(filepath.Clean(e.Path)) != filepath.Clean(e.RootPath) ||
		e.Name != filepath.Base(e.Path) || strings.HasPrefix(e.Name, ".") {
		return Removal{}
	}
	switch e.Kind {
	case skill.KindSkill, skill.KindLink, skill.KindBroken, skill.KindEmpty:
	default:
		return Removal{}
	}
	r := Removal{Available: true, Label: "删除 skill", Title: "删除这份 skill？",
		Message: "目录会移到撤销记录，不会永久删除。删除后可以撤销。",
		Confirm: "确认删除", Done: "已删除 skill"}
	link := e.Kind == skill.KindLink || e.Kind == skill.KindBroken
	if link {
		r.Label, r.Title, r.Confirm, r.Done = "从 "+e.RootName+" 移除", "移除这条 skill 软链？", "确认移除", "已移除软链"
		r.Message = "只移除这里的软链，不删除它的目标。原来的目标会记在撤销记录里，可以撤销。"
	}
	if e.RootRole == skill.RolePool {
		if link {
			r.Label = "从共享池移除"
		}
		r.Message += " Grok 直接读取共享池，移除后 Grok 将看不到这里的 skill。"
	}
	r.Title = "移除 " + e.RootName + " 的 " + e.Name + "？"
	if !link {
		r.Title = "删除 " + e.RootName + " 的 " + e.Name + "？"
	}
	if len(snap.Warnings) > 0 {
		r.Err = "扫描不完整，无法确认引用安全：" + strings.Join(snap.Warnings, "；")
		return r
	}
	links, refs, err := removalReferences(snap, e)
	if err != nil {
		r.Err = "无法确认引用安全：" + err.Error()
		return r
	}
	labels := slices.Clone(refs)
	for _, l := range links {
		labels = append(labels, l.RootName+" / "+l.Name+"（软链）")
	}
	slices.Sort(labels)
	labels = slices.Compact(labels)
	if len(labels) > 0 {
		r.Err = "仍被引用，不能移除：" + strings.Join(labels, "；")
	}
	if len(links) > 0 && len(refs) == 0 && e.RootRole == skill.RolePool && e.Kind != skill.KindLink && e.Kind != skill.KindBroken {
		r.Links, r.Unlinkable = links, true
		for _, l := range links {
			if l.RootRole != skill.RoleAgent && l.RootRole != skill.RolePool {
				r.Unlinkable = false
			}
		}
	}
	return r
}

// ApplyRemoval checks entry and document metadata and direct references before
// removing a slot. It does not inspect nested resources or their contents.
func ApplyRemoval(e skill.Entry, home string, repos []string, undoRoot string, expectedRoots ...skill.Root) (manage.Record, error) {
	recs, err := ApplyRemovalLinks(e, home, repos, undoRoot, LinksBlock, expectedRoots...)
	if err != nil {
		return manage.Record{}, err
	}
	return recs[0], nil
}

// ApplyRemovalLinks is ApplyRemoval with a choice for links that still point
// at the entry. With LinksRemove those links go first; if anything fails the
// ones already removed are restored. The records come back in the order made,
// the entry's last.
func ApplyRemovalLinks(e skill.Entry, home string, repos []string, undoRoot string, policy LinkPolicy, expectedRoots ...skill.Root) ([]manage.Record, error) {
	roots := skill.Discover(home)
	for _, expected := range expectedRoots {
		if expected.Role == skill.RoleRepo {
			continue
		}
		found := false
		for _, root := range roots {
			if filepath.Clean(root.Path) == filepath.Clean(expected.Path) {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, expected)
		}
	}
	found := false
	for _, root := range roots {
		if filepath.Clean(root.Path) == filepath.Clean(e.RootPath) {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("skills 根目录已变化或不可访问，请重新扫描")
	}
	snap := skill.Scan(roots, repos)
	var current skill.Entry
	present := false
	for _, candidate := range snap.Entries {
		if filepath.Clean(candidate.Path) == filepath.Clean(e.Path) {
			current, present = candidate, true
			break
		}
	}
	if !present || current.RootRole != e.RootRole || current.RootID != e.RootID || current.System != e.System ||
		current.Kind != e.Kind || current.LinkTarget != e.LinkTarget || current.LinkAbs != e.LinkAbs ||
		current.RealPath != e.RealPath || !current.EntryModTime.Equal(e.EntryModTime) ||
		current.HasDocument != e.HasDocument || current.DocSize != e.DocSize || !current.DocModTime.Equal(e.DocModTime) {
		return nil, errors.New("这份 skill 已变化，请重新扫描后确认")
	}
	r := RemovalFor(snap, current)
	if !r.Available {
		return nil, errors.New("这份 skill 不能修改")
	}
	if r.Err != "" && !(r.Unlinkable && policy != LinksBlock) {
		return nil, errors.New(r.Err)
	}
	var recs []manage.Record
	if policy == LinksRemove {
		for _, l := range r.Links {
			rec, err := manage.RemoveEntry(l.RootPath, l.Path, undoRoot)
			if err != nil {
				return nil, rollback(recs, err)
			}
			recs = append(recs, rec)
		}
	}
	rec, err := manage.RemoveEntry(current.RootPath, current.Path, undoRoot)
	if err != nil {
		return nil, rollback(recs, err)
	}
	return append(recs, rec), nil
}

// rollback puts back what a failed multi-step removal already did.
func rollback(done []manage.Record, cause error) error {
	for i := len(done) - 1; i >= 0; i-- {
		if err := manage.Restore(done[i]); err != nil {
			cause = fmt.Errorf("%w；还原软链失败：%v", cause, err)
		}
	}
	return cause
}

func removalReferences(snap *skill.Snapshot, target skill.Entry) (links []skill.Entry, deps []string, err error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(target.Path))
	if err != nil {
		return nil, nil, err
	}
	targetPath := filepath.Join(parent, filepath.Base(target.Path))
	var refs []string
	for _, e := range snap.Entries {
		if filepath.Clean(e.Path) == filepath.Clean(target.Path) {
			continue
		}
		inspect := func(path, label string) error {
			yes, err := pathTraverses(path, targetPath)
			if err != nil {
				return fmt.Errorf("%s：%w", label, err)
			}
			if yes {
				links = append(links, e)
			}
			return nil
		}
		label := e.RootName + " / " + e.Name
		if e.Kind == skill.KindLink || e.Kind == skill.KindBroken {
			if err := inspect(e.Path, label); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, e := range snap.Entries {
		if e.RootID != target.RootID || e.Name == target.Name {
			continue
		}
		for _, req := range e.Requires {
			if strings.EqualFold(req, target.Name) {
				refs = append(refs, target.RootName+" / "+e.Name+"（依赖）")
				break
			}
		}
	}
	slices.Sort(refs)
	return links, slices.Compact(refs), nil
}

// pathTraverses resolves links one component at a time, including broken
// chains. Comparing before following a link distinguishes it from aliases
// that reach the same final directory without using the removed slot.
func pathTraverses(path, target string) (bool, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	pending := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		current = filepath.Join(current, part)
		if current == target || strings.HasPrefix(current, target+string(filepath.Separator)) {
			return true, nil
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		links++
		if links > 255 {
			return false, errors.New("软链循环或层数过多")
		}
		raw, err := os.Readlink(current)
		if err != nil {
			return false, err
		}
		next := raw
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(current), next)
		}
		next = filepath.Clean(next)
		pending = append(strings.Split(strings.TrimPrefix(next, string(filepath.Separator)), string(filepath.Separator)), pending...)
		current = string(filepath.Separator)
	}
	return false, nil
}
