package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	if s, err := LoadSettings(path); err != nil || len(s.HiddenAgents) != 0 {
		t.Fatalf("missing file: %+v %v", s, err)
	}
	want := []string{"cline", "adal"}
	if err := SaveSettings(path, Settings{HiddenAgents: want}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(path)
	if err != nil || !slices.Equal(got.HiddenAgents, want) {
		t.Fatalf("%+v %v", got, err)
	}
}
