package manage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// A staged link must have the same parent as its final path: Windows infers
// directory-link type by resolving its target at creation time.
func stageLink(path, target string, create func(string, string) error) (string, error) {
	staged, err := vacantSibling(path)
	if err != nil {
		return "", err
	}
	if err := create(target, staged); err != nil {
		return "", err
	}
	return staged, nil
}

func vacantSibling(path string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".knack-link-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	closeErr := f.Close()
	removeErr := os.Remove(name)
	if closeErr != nil {
		return "", closeErr
	}
	return name, removeErr
}

// installLink prepares the new link and persists the record before moving any
// original object. Passing create explicitly lets tests simulate Windows'
// symlink privilege error without changing process-wide filesystem behavior.
func installLink(r Record, create func(string, string) error) (Record, error) {
	staged, err := stageLink(r.LinkPath, r.NewTarget, create)
	if err != nil {
		discardEmptyRecord(r)
		return Record{}, err
	}
	defer os.Remove(staged)
	if err := save(r); err != nil {
		discardEmptyRecord(r)
		return Record{}, err
	}
	if r.Payload != "" {
		if err := os.Rename(r.LinkPath, r.Payload); err != nil {
			discardEmptyRecord(r)
			return Record{}, err
		}
	} else if err := expectAbsent(r.LinkPath); err != nil {
		discardEmptyRecord(r)
		return Record{}, err
	}
	if err := os.Rename(staged, r.LinkPath); err != nil {
		if r.Payload != "" {
			if back := os.Rename(r.Payload, r.LinkPath); back != nil {
				// Keep both the object and its persisted record recoverable.
				return r, fmt.Errorf("放置软链失败（%v），原条目仍在 %s，移回失败：%w", err, r.Payload, back)
			}
		}
		discardEmptyRecord(r)
		return Record{}, err
	}
	return r, nil
}

func restoreLinkChange(r Record) error {
	if err := expectLink(r.LinkPath, r.NewTargetAbs); err != nil {
		// A failed install/rollback can leave only the persisted payload.
		if r.Payload == "" {
			return err
		}
		if absent := expectAbsent(r.LinkPath); absent != nil {
			return err
		}
		return os.Rename(r.Payload, r.LinkPath)
	}
	original := r.Payload
	if original != "" {
		// Refuse before touching the current link if the backup is unavailable.
		if _, err := os.Lstat(original); err != nil {
			return err
		}
	} else if r.OldTarget != "" {
		// Records written before link payloads stored only the raw target.
		var err error
		original, err = stageLink(r.LinkPath, r.OldTarget, os.Symlink)
		if err != nil {
			return err
		}
		defer os.Remove(original)
	} else {
		return os.Remove(r.LinkPath)
	}
	current, err := vacantSibling(r.LinkPath)
	if err != nil {
		return err
	}
	if err := os.Rename(r.LinkPath, current); err != nil {
		return err
	}
	if err := os.Rename(original, r.LinkPath); err != nil {
		if back := os.Rename(current, r.LinkPath); back != nil {
			return fmt.Errorf("恢复原条目失败（%v），当前软链仍在 %s，移回失败：%w", err, current, back)
		}
		return err
	}
	_ = os.Remove(current)
	return nil
}

func expectAbsent(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return errors.New("原位置已经有东西，没有覆盖")
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Never recursively clean up an error path that could still contain a payload.
func discardEmptyRecord(r Record) {
	if r.Payload != "" {
		if _, err := os.Lstat(r.Payload); !os.IsNotExist(err) {
			return
		}
	}
	_ = os.Remove(filepath.Join(r.Dir, "record.json"))
	_ = os.Remove(r.Dir)
}
