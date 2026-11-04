package manage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"knack/internal/fsutil"
)

// RemoveEntry removes a link or moves a directory into a persisted undo record.
// It never follows an entry symlink or deletes its target.
func RemoveEntry(root, entryPath, undoRoot string) (Record, error) {
	if err := assertChild(root, entryPath); err != nil {
		return Record{}, err
	}
	if err := removalRoot(root); err != nil {
		return Record{}, err
	}
	info, err := os.Lstat(entryPath)
	if err != nil {
		return Record{}, err
	}
	op := opRemoveD
	var old string
	if fsutil.IsLink(entryPath, info) {
		op = opRemoveL
		old, err = os.Readlink(entryPath)
		if err != nil {
			return Record{}, err
		}
	} else if !info.IsDir() {
		return Record{}, errors.New("只能移除软链或 skill 目录")
	}
	rec, err := begin(undoRoot, op, entryPath)
	if err != nil {
		return Record{}, err
	}
	rec.OldTarget = old
	rec.Payload = filepath.Join(rec.Dir, "payload")
	if err := save(rec); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	err = os.Rename(entryPath, rec.Payload)
	if err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	return rec, nil
}

func removalRoot(root string) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	if !fsutil.EqualPath(real, filepath.Join(parent, filepath.Base(abs))) {
		return errors.New("skills 根目录经过软链指向其他位置，不能修改")
	}
	// Agent folders may not redirect writes into another app's installation.
	for p := filepath.Dir(abs); p != filepath.Dir(p); p = filepath.Dir(p) {
		if !strings.HasPrefix(filepath.Base(p), ".") {
			continue
		}
		for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
			info, err := os.Lstat(dir)
			if err != nil {
				return err
			}
			if fsutil.IsLink(dir, info) {
				return errors.New("agent 目录经过软链指向其他位置，不能修改")
			}
			if fsutil.EqualPath(dir, p) {
				break
			}
		}
		break
	}
	for _, path := range []string{abs, real} {
		normalized := "/" + strings.Trim(filepath.ToSlash(path), "/") + "/"
		for _, protected := range []string{"/.system/", "/builtin/", "/.builtin-skills/", "/skills-cursor/", "/.grok/bundled/", "/vendor_imports/"} {
			if strings.Contains(normalized, protected) {
				return errors.New("不会修改自带 skill 目录")
			}
		}
	}
	return nil
}
