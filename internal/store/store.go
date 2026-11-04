// Package store remembers repositories and a display-only startup catalog.
// Fresh scans of disk remain the authority for filesystem changes.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Repo is a directory the user asked to scan in addition to the global
// skill folders.
type Repo struct {
	Path  string    `json:"path"`
	Name  string    `json:"name,omitempty"`
	Added time.Time `json:"added"`
}

type file struct {
	Repos []Repo `json:"repos"`
}

// Load reads repos. A missing file is an empty list.
func Load(path string) ([]Repo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Repos, nil
}

// Save writes repos, creating the parent directory when it is missing.
func Save(path string, repos []Repo) error {
	if repos == nil {
		repos = []Repo{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(file{Repos: repos}, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
