package app

import (
	"errors"
	"os"
	"path/filepath"

	"knack/internal/manage"
)

// Knack keeps its own files in ~/.config/knack and ~/.cache/knack. They
// were named skill-manager before the app was; the first launch moves what
// is there across.

const (
	dataName   = "knack"
	legacyName = "skill-manager"
)

// dataDirs returns the config and cache directories beneath home, after
// moving the legacy ones into place. A directory that could not be moved
// at all keeps being used where it is, so nothing looks lost.
func dataDirs(home string) (config, cache string, err error) {
	config, moved, err := adoptLegacy(filepath.Join(home, ".config", legacyName), filepath.Join(home, ".config", dataName))
	if moved {
		// Undo records name their own directory, which has just changed.
		err = errors.Join(err, manage.RebaseRecords(filepath.Join(config, "undo")))
	}
	cache, _, cacheErr := adoptLegacy(filepath.Join(home, ".cache", legacyName), filepath.Join(home, ".cache", dataName))
	return config, cache, errors.Join(err, cacheErr)
}

// adoptLegacy moves old to dir and returns the directory to use. When dir
// already exists, as it may when something else created it first, the
// entries it lacks are moved in one by one and those it has are left in old.
func adoptLegacy(old, dir string) (use string, moved bool, err error) {
	if _, err := os.Lstat(old); err != nil {
		return dir, false, nil
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(old, dir); err != nil {
			return old, false, err
		}
		return dir, true, nil
	}
	entries, err := os.ReadDir(old)
	if err != nil {
		return dir, false, err
	}
	for _, e := range entries {
		to := filepath.Join(dir, e.Name())
		if _, lerr := os.Lstat(to); !errors.Is(lerr, os.ErrNotExist) {
			continue
		}
		if rerr := os.Rename(filepath.Join(old, e.Name()), to); rerr != nil {
			err = errors.Join(err, rerr)
			continue
		}
		moved = true
	}
	// Only an emptied directory goes; anything left stays where it was.
	_ = os.Remove(old)
	return dir, moved, err
}
