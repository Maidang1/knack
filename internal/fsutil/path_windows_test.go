package fsutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIsLinkWindowsJunction(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "junction directory")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("create junction: %v\n%s", err, out)
	}
	for _, dangling := range []bool{false, true} {
		if dangling {
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if !IsLink(link, info) {
			t.Fatalf("junction not recognized: mode=%v dangling=%v", info.Mode(), dangling)
		}
	}
}
