// Package manage changes skill directories on disk and can put them back.
// Every change is limited to a direct child of a skills directory. The old
// bytes go under an undo folder; nothing is deleted for good.
package manage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"knack/internal/fsutil"
)

// Record describes one change, enough to reverse it.
type Record struct {
	ID           string          `json:"id"`
	Op           string          `json:"op"`
	At           time.Time       `json:"at"`
	LinkPath     string          `json:"linkPath"`
	OldTarget    string          `json:"oldTarget,omitempty"`
	NewTarget    string          `json:"newTarget,omitempty"`
	NewTargetAbs string          `json:"newTargetAbs,omitempty"`
	Payload      string          `json:"payload,omitempty"`
	Dir          string          `json:"dir"`
	Invocation   *invocationUndo `json:"invocation,omitempty"`
	Cleanup      *CleanupUndo    `json:"cleanup,omitempty"`
}

const (
	opRelink   = "relink-dir"
	opRetarget = "retarget"
	opRemoveL  = "remove-link"
	opRemoveD  = "remove-dir"
	opLinkPool = "link-pool"
)

var seq atomic.Int64

// RelinkDir moves a real skill directory aside and leaves a relative
// symlink to poolSkill, which is the shared-pool entry of the same name.
func RelinkDir(skillsRoot, entryPath, poolSkill, undoRoot string) (Record, error) {
	if err := assertChild(skillsRoot, entryPath); err != nil {
		return Record{}, err
	}
	if err := sameName(entryPath, poolSkill); err != nil {
		return Record{}, err
	}
	if err := requireSkill(poolSkill); err != nil {
		return Record{}, err
	}
	info, err := os.Lstat(entryPath)
	if err != nil {
		return Record{}, err
	}
	if fsutil.IsLink(entryPath, info) || !info.IsDir() {
		return Record{}, errors.New("这不是一份实体目录，不能按副本改成软链")
	}
	rec, err := begin(undoRoot, opRelink, entryPath)
	if err != nil {
		return Record{}, err
	}
	rel, abs, err := relativeLink(entryPath, poolSkill)
	if err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	rec.Payload = filepath.Join(rec.Dir, "payload")
	rec.NewTarget = rel
	rec.NewTargetAbs = abs
	return installLink(rec, os.Symlink)
}

// Retarget points an existing symlink at poolSkill, keeping the original
// filesystem object in the undo payload.
func Retarget(skillsRoot, entryPath, poolSkill, undoRoot string) (Record, error) {
	if err := assertChild(skillsRoot, entryPath); err != nil {
		return Record{}, err
	}
	if err := sameName(entryPath, poolSkill); err != nil {
		return Record{}, err
	}
	if fsutil.EqualPath(entryPath, poolSkill) {
		return Record{}, errors.New("不能把共享池里的条目指向它自己")
	}
	if err := requireSkill(poolSkill); err != nil {
		return Record{}, err
	}
	old, err := os.Readlink(entryPath)
	if err != nil {
		return Record{}, errors.New("这不是一条软链")
	}
	rel, abs, err := relativeLink(entryPath, poolSkill)
	if err != nil {
		return Record{}, err
	}
	if sameLink(entryPath, old, poolSkill) {
		return Record{}, errors.New("这条软链已经指向共享池")
	}
	rec, err := begin(undoRoot, opRetarget, entryPath)
	if err != nil {
		return Record{}, err
	}
	rec.OldTarget = old
	rec.Payload = filepath.Join(rec.Dir, "payload")
	rec.NewTarget = rel
	rec.NewTargetAbs = abs
	return installLink(rec, os.Symlink)
}

// RemoveLink moves a symlink into its undo payload without following it.
func RemoveLink(skillsRoot, entryPath, undoRoot string) (Record, error) {
	if err := assertChild(skillsRoot, entryPath); err != nil {
		return Record{}, err
	}
	old, err := os.Readlink(entryPath)
	if err != nil {
		return Record{}, errors.New("这不是一条软链")
	}
	rec, err := begin(undoRoot, opRemoveL, entryPath)
	if err != nil {
		return Record{}, err
	}
	rec.OldTarget = old
	rec.Payload = filepath.Join(rec.Dir, "payload")
	if err := save(rec); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	if err := os.Rename(entryPath, rec.Payload); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	return rec, nil
}

// RemoveDir moves a directory aside. Callers should only use it for a
// vacant skill slot.
func RemoveDir(skillsRoot, entryPath, undoRoot string) (Record, error) {
	if err := assertChild(skillsRoot, entryPath); err != nil {
		return Record{}, err
	}
	info, err := os.Lstat(entryPath)
	if err != nil {
		return Record{}, err
	}
	if fsutil.IsLink(entryPath, info) || !info.IsDir() {
		return Record{}, errors.New("这不是一个目录")
	}
	rec, err := begin(undoRoot, opRemoveD, entryPath)
	if err != nil {
		return Record{}, err
	}
	rec.Payload = filepath.Join(rec.Dir, "payload")
	if err := save(rec); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	if err := os.Rename(entryPath, rec.Payload); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	return rec, nil
}

// LinkIntoPool creates poolRoot/name as a relative symlink to target.
// A broken symlink of that name is replaced. A real directory is left alone.
func LinkIntoPool(poolRoot, name, target, undoRoot string) (Record, error) {
	return linkInto(poolRoot, name, target, undoRoot, "共享池")
}

// LinkIntoAgent creates agentRoot/name as a relative symlink to target, a
// skill of the shared pool. It follows the rules of LinkIntoPool.
func LinkIntoAgent(agentRoot, name, target, undoRoot string) (Record, error) {
	return linkInto(agentRoot, name, target, undoRoot, "这个 agent 目录")
}

func linkInto(root, name, target, undoRoot, where string) (Record, error) {
	if err := plainName(name); err != nil {
		return Record{}, err
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	dest := filepath.Join(root, name)
	if fsutil.EqualPath(target, dest) {
		return Record{}, errors.New("不能把" + where + "条目指向它自己")
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return Record{}, errors.New("要链接的这份 skill 不存在")
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		return Record{}, errors.New("要链接的这份没有 SKILL.md")
	}
	var old string
	if info, err := os.Lstat(dest); err == nil {
		if !fsutil.IsLink(dest, info) {
			return Record{}, errors.New(where + "里已经有这个 skill")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			return Record{}, errors.New(where + "里已经有这个 skill")
		}
		old, err = os.Readlink(dest)
		if err != nil {
			return Record{}, err
		}
	} else if !os.IsNotExist(err) {
		return Record{}, err
	}
	rel, abs, err := relativeLink(dest, target)
	if err != nil {
		return Record{}, err
	}
	rec, err := begin(undoRoot, opLinkPool, dest)
	if err != nil {
		return Record{}, err
	}
	rec.OldTarget = old
	if old != "" {
		rec.Payload = filepath.Join(rec.Dir, "payload")
	}
	rec.NewTarget = rel
	rec.NewTargetAbs = abs
	return installLink(rec, os.Symlink)
}

// Restore reverses one record. It refuses when the path no longer matches
// what the change wrote.
func Restore(r Record) error {
	switch r.Op {
	case opCleanup:
		if err := restoreCleanup(r); err != nil {
			return err
		}
	case opInvocation:
		if err := restoreInvocation(r); err != nil {
			return err
		}
	case opRelink, opRetarget, opLinkPool:
		if err := restoreLinkChange(r); err != nil {
			return err
		}
	case opRemoveL:
		if err := removalRoot(filepath.Dir(r.LinkPath)); err != nil {
			return err
		}
		if err := expectAbsent(r.LinkPath); err != nil {
			return err
		}
		if r.Payload != "" {
			err := os.Rename(r.Payload, r.LinkPath)
			if err != nil {
				return err
			}
		} else if err := os.Symlink(r.OldTarget, r.LinkPath); err != nil {
			return err
		}
	case opRemoveD:
		if err := removalRoot(filepath.Dir(r.LinkPath)); err != nil {
			return err
		}
		if err := expectAbsent(r.LinkPath); err != nil {
			return err
		}
		if err := os.Rename(r.Payload, r.LinkPath); err != nil {
			return err
		}
	default:
		return errors.New("不认识的撤销记录")
	}
	_ = os.RemoveAll(r.Dir)
	return nil
}

func begin(undoRoot, op, linkPath string) (Record, error) {
	if undoRoot == "" {
		return Record{}, errors.New("没有撤销目录")
	}
	id := fmt.Sprintf("%s-%d", time.Now().Format("20060102T150405.000"), seq.Add(1))
	dir := filepath.Join(undoRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Record{}, err
	}
	return Record{ID: id, Op: op, At: time.Now(), LinkPath: linkPath, Dir: dir}, nil
}

func save(r Record) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Dir, "record.json"), append(b, '\n'), 0o644)
}

func assertChild(root, child string) error {
	root = filepath.Clean(root)
	child = filepath.Clean(child)
	if !fsutil.EqualPath(filepath.Dir(child), root) {
		return fmt.Errorf("拒绝修改 %s：它不是 %s 的直接子项", child, root)
	}
	return plainName(filepath.Base(child))
}

func plainName(name string) error {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return errors.New("拒绝修改这个路径")
	}
	if fsutil.EqualPath(name, ".system") {
		return errors.New("不会改 .system 里的自带 skill")
	}
	return nil
}

func sameName(entryPath, poolSkill string) error {
	if !fsutil.EqualPath(filepath.Base(entryPath), filepath.Base(poolSkill)) {
		return errors.New("名字和共享池里的条目不一致")
	}
	if fsutil.EqualPath(entryPath, poolSkill) {
		return errors.New("这就是共享池里的那一份")
	}
	return nil
}

func requireSkill(poolSkill string) error {
	st, err := os.Stat(poolSkill)
	if err != nil || !st.IsDir() {
		return errors.New("共享池里没有可用的这一份")
	}
	if _, err := os.Stat(filepath.Join(poolSkill, "SKILL.md")); err != nil {
		return errors.New("共享池里的这份没有可用的 SKILL.md")
	}
	return nil
}

func relativeLink(linkPath, target string) (rel, abs string, err error) {
	abs, err = physicalParentPath(target)
	if err != nil {
		return "", "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(linkPath))
	if err != nil {
		return "", "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", "", err
	}
	rel, err = relativeTarget(parent, abs)
	if err != nil {
		return "", "", err
	}
	return rel, abs, nil
}

func relativeTarget(parent, target string) (string, error) {
	rel, err := filepath.Rel(parent, target)
	if err != nil && runtime.GOOS == "windows" && !fsutil.EqualPath(filepath.VolumeName(parent), filepath.VolumeName(target)) {
		return target, nil
	}
	return rel, err
}

func sameLink(linkPath, raw, poolSkill string) bool {
	abs, err := linkTarget(linkPath, raw)
	return err == nil && equalTarget(abs, poolSkill)
}

func expectLink(linkPath, wantAbs string) error {
	raw, err := os.Readlink(linkPath)
	if err != nil {
		return errors.New("撤销时这条已经不是软链了")
	}
	abs, err := linkTarget(linkPath, raw)
	if err != nil {
		return err
	}
	if !equalTarget(abs, wantAbs) {
		return errors.New("撤销时软链已经指向别处")
	}
	return nil
}

func linkTarget(linkPath, raw string) (string, error) {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(linkPath))
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(parent, raw))
}

func equalTarget(a, b string) bool {
	if real, err := physicalParentPath(a); err == nil {
		a = real
	}
	if real, err := physicalParentPath(b); err == nil {
		b = real
	}
	return fsutil.EqualPath(a, b)
}

func physicalParentPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	// Keep the final pool slot, even when it links to a repository directory.
	return filepath.Join(parent, filepath.Base(abs)), nil
}
