package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"knack/internal/skill"
)

func TestCatalogRoundTripAndScope(t *testing.T) {
	home := t.TempDir()
	pool := filepath.Join(home, ".agents", "skills")
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(pool, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		md := "---\nname: " + name + "\ndescription: example\n---\n"
		if name == "beta" {
			md = "---\nname: beta\nrequires: [alpha]\n---\n"
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := skill.Scan(skill.Discover(home), nil)
	path := filepath.Join(home, "cache", "catalog.json")
	if cached, err := LoadCatalog(path, home, nil); err != nil || cached != nil {
		t.Fatalf("absent cache: %v", err)
	}
	if err := SaveCatalog(path, home, nil, snap); err != nil {
		t.Fatal(err)
	}
	cached, err := LoadCatalog(path, home, nil)
	if err != nil || cached == nil {
		t.Fatalf("load cache: %v", err)
	}
	if !cached.At.Equal(snap.At) || len(cached.Entries) != len(snap.Entries) || len(cached.GraphFor("pool").Edges) != 1 {
		t.Fatal("derived catalog data was not restored")
	}
	for _, scope := range []struct {
		home  string
		repos []string
	}{{home + "-other", nil}, {home, []string{"new-repo"}}} {
		if cached, err := LoadCatalog(path, scope.home, scope.repos); err != nil || cached != nil {
			t.Fatal("used cache for other scope")
		}
	}
	wantPerm := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		wantPerm = 0o666
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != wantPerm {
		t.Fatal("cache permissions")
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCatalog(path, home, nil); err == nil {
		t.Fatal("accepted corrupt cache")
	}
}
