package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "repos.json")
	in := []Repo{{Path: "/tmp/repo", Name: "项目技能库", Added: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}}
	if err := Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Path != in[0].Path || out[0].Name != in[0].Name || !out[0].Added.Equal(in[0].Added) {
		t.Fatalf("%+v", out)
	}
	missing, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || missing != nil {
		t.Fatalf("missing file: %+v %v", missing, err)
	}
}

func TestLoadRepoWithoutDisplayName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repos.json")
	if err := os.WriteFile(path, []byte(`{"repos":[{"path":"/tmp/repo","added":"2026-11-02T00:00:00Z"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	repos, err := Load(path)
	if err != nil || len(repos) != 1 || repos[0].Name != "" || repos[0].Path != "/tmp/repo" {
		t.Fatalf("legacy repos: %+v %v", repos, err)
	}
}
