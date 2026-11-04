package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEqualPath(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{filepath.Join("skills", "child", "..", "alpha"), filepath.Join("skills", "alpha"), true},
		{filepath.Join("skills", "alpha"), filepath.Join("skills", "beta"), false},
		{filepath.Join("skills", "Alpha"), filepath.Join("SKILLS", "alpha"), runtime.GOOS == "windows"},
	} {
		if got := EqualPath(tc.a, tc.b); got != tc.want {
			t.Errorf("EqualPath(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestIsLink(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if IsLink(path, info) {
			t.Fatalf("ordinary path reported as a link: %s", path)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, dangling := range []bool{false, true} {
		if dangling {
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
		}
		info, err := os.Lstat(link)
		if err != nil {
			t.Fatal(err)
		}
		if !IsLink(link, info) {
			t.Fatalf("symlink not recognized, dangling=%v", dangling)
		}
	}
}
