package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Settings are display preferences. They never change what is scanned.
type Settings struct {
	// HiddenAgents holds root IDs of agents left out of the sidebar.
	HiddenAgents []string `json:"hiddenAgents"`
}

// LoadSettings reads settings. A missing file is the zero value.
func LoadSettings(path string) (Settings, error) {
	var s Settings
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(b, &s)
	return s, err
}

// SaveSettings writes settings, creating the parent directory when needed.
func SaveSettings(path string, s Settings) error {
	if s.HiddenAgents == nil {
		s.HiddenAgents = []string{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
