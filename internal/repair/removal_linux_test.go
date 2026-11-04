package repair

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathTraversesDistinctHardlinkedSymlink(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(dir, "selected")
	makeSymlink(t, source, selected)
	bypass := filepath.Join(dir, "bypass")
	if err := os.Link(selected, bypass); err != nil {
		t.Fatal(err)
	}
	throughBypass := filepath.Join(dir, "through-bypass")
	makeSymlink(t, bypass, throughBypass)
	for _, path := range []string{bypass, throughBypass} {
		if yes, err := pathTraverses(path, selected); err != nil || yes {
			t.Fatalf("independent hardlink %q traversed selected slot: %v, %v", path, yes, err)
		}
	}
	if err := os.Remove(selected); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bypass); err != nil {
		t.Fatalf("removal affected independent hardlink: %v", err)
	}
}
