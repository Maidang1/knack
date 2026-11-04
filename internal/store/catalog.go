package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"skill-manager/internal/skill"
)

const catalogVersion = 2

// The cached catalog is display data, never the authority for mutations.
type catalog struct {
	Version  int           `json:"version"`
	Home     string        `json:"home"`
	Repos    []string      `json:"repos"`
	Roots    []skill.Root  `json:"roots"`
	Entries  []skill.Entry `json:"entries"`
	Warnings []string      `json:"warnings,omitempty"`
	At       time.Time     `json:"at"`
}

func LoadCatalog(path, home string, repos []string) (*skill.Snapshot, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Version != catalogVersion || c.Home != home || !slices.Equal(c.Repos, repos) || c.At.IsZero() {
		return nil, nil
	}
	return skill.FromCatalog(c.Roots, c.Entries, c.Warnings, c.At), nil
}

func SaveCatalog(path, home string, repos []string, snap *skill.Snapshot) error {
	c := catalog{Version: catalogVersion, Home: home, Repos: repos, Roots: snap.Roots,
		Entries: snap.Entries, Warnings: snap.Warnings, At: snap.At}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
