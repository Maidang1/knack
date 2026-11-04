package skill

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Document is a SKILL.md read on demand. RealPath is its resolved skill directory.
type Document struct {
	Text     string
	Size     int64
	ModTime  time.Time
	RealPath string
}

// ReadDocument resolves the current entry and reads only its SKILL.md.
// A document replaced or changed during the read is not returned as current.
func ReadDocument(e Entry) (Document, error) {
	if e.Path == "" {
		return Document{}, errors.New("skill path is empty")
	}
	real, err := filepath.EvalSymlinks(e.Path)
	if err != nil {
		return Document{}, err
	}
	if e.RealPath != "" && filepath.Clean(real) != filepath.Clean(e.RealPath) {
		return Document{}, errors.New("skill 目标已变化，请重新扫描")
	}
	path := filepath.Join(real, "SKILL.md")
	f, err := os.Open(path)
	if err != nil {
		return Document{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return Document{}, err
	}
	if !before.Mode().IsRegular() {
		return Document{}, errors.New("SKILL.md 不是普通文件")
	}
	body, err := io.ReadAll(f)
	if err != nil {
		return Document{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return Document{}, err
	}
	current, err := os.Stat(filepath.Join(e.Path, "SKILL.md"))
	if err != nil {
		return Document{}, err
	}
	if !sameFileStamp(before, after) || !sameFileStamp(after, current) {
		return Document{}, errors.New("SKILL.md 在读取期间发生变化，请重试")
	}
	return Document{Text: string(body), Size: after.Size(), ModTime: after.ModTime(), RealPath: real}, nil
}

func sameFileStamp(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime()) && a.Mode() == b.Mode()
}

// CompareCopies hashes only these two entries, using fresh metadata rather
// than the snapshot's cached values. Hashes are discarded on a failed read
// or a detected concurrent change. This is not a filesystem transaction.
func CompareCopies(e, pool Entry) (Entry, Entry, error) {
	source, sourceErr := refreshEntry(e)
	target, targetErr := refreshEntry(pool)
	fail := func(err error) (Entry, Entry, error) {
		source.SkillHash, source.TreeHash = "", ""
		target.SkillHash, target.TreeHash = "", ""
		source.HashErr, target.HashErr = err.Error(), err.Error()
		return source, target, err
	}
	if sourceErr != nil {
		return fail(sourceErr)
	}
	if targetErr != nil {
		return fail(targetErr)
	}
	if changedIdentity(e, source) || changedIdentity(pool, target) {
		return fail(errors.New("所选 skill 路径已改变，请重新扫描"))
	}
	if !source.Usable() || !target.Usable() {
		return fail(errors.New("比较需要两份可读的 SKILL.md"))
	}
	var err error
	for _, entry := range []*Entry{&source, &target} {
		entry.SkillHash, err = fileHash(filepath.Join(entry.RealPath, "SKILL.md"))
		if err != nil {
			return fail(err)
		}
		entry.TreeHash, err = TreeHash(entry.RealPath)
		if err != nil {
			return fail(err)
		}
	}
	for _, entry := range []Entry{source, target} {
		fresh, err := refreshEntry(entry)
		if err != nil {
			return fail(err)
		}
		if !sameEntryStamp(entry, fresh) {
			return fail(fmt.Errorf("%s 在比较期间发生变化，请重试", entry.Path))
		}
	}
	source.Relation = relate(source, target, source.Name == target.Name)
	target.Relation = poolRelation(target)
	return source, target, nil
}

func refreshEntry(e Entry) (Entry, error) {
	if e.Path == "" {
		return Entry{}, errors.New("skill path is empty")
	}
	root := Root{ID: e.RootID, Name: e.RootName, Role: e.RootRole, Path: e.RootPath}
	fresh, ok := scanEntry(root, e.Name, e.Path, e.System, newCache())
	if !ok {
		_, err := os.Lstat(e.Path)
		if err == nil {
			err = errors.New("skill entry is not a directory or link")
		}
		return fresh, err
	}
	return fresh, nil
}

// Previously compared entries are valid inputs: content stamps and hashes
// may be old. Only a change of the selected slot's kind or target rejects it.
func changedIdentity(selected, fresh Entry) bool {
	return selected.Kind != "" && selected.Kind != fresh.Kind ||
		selected.RealPath != "" && selected.RealPath != fresh.RealPath ||
		selected.LinkTarget != "" && selected.LinkTarget != fresh.LinkTarget
}

func sameEntryStamp(a, b Entry) bool {
	return a.Kind == b.Kind && a.RealPath == b.RealPath && a.LinkTarget == b.LinkTarget &&
		a.HasDocument == b.HasDocument && a.MetaErr == b.MetaErr &&
		a.EntryModTime.Equal(b.EntryModTime) && a.DocSize == b.DocSize && a.DocModTime.Equal(b.DocModTime)
}
