package manage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelinkAndRestore(t *testing.T) {
	root := t.TempDir()
	pool := filepath.Join(t.TempDir(), "beta")
	undo := filepath.Join(t.TempDir(), "undo")
	writeSkill(t, pool)
	agent := filepath.Join(root, "beta")
	copyDirFile(t, pool, agent)

	rec, err := RelinkDir(root, agent, pool, undo)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "beta") {
		t.Fatalf("symlink target %s", got)
	}
	if _, err := os.Stat(filepath.Join(agent, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(agent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("restore left mode %s", info.Mode())
	}
}

func TestRetargetBrokenAndRestore(t *testing.T) {
	root := t.TempDir()
	pool := filepath.Join(t.TempDir(), "observe")
	undo := filepath.Join(t.TempDir(), "undo")
	writeSkill(t, pool)
	link := filepath.Join(root, "observe")
	if err := os.Symlink("/tmp/missing-observe", link); err != nil {
		t.Fatal(err)
	}
	rec, err := Retarget(root, link, pool, undo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(link, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/missing-observe" {
		t.Fatalf("restored target %s", got)
	}
}

func TestRefusesEscape(t *testing.T) {
	root := t.TempDir()
	pool := filepath.Join(t.TempDir(), "beta")
	writeSkill(t, pool)
	outside := filepath.Join(filepath.Dir(root), "beta")
	writeSkill(t, outside)
	_, err := RelinkDir(root, outside, pool, filepath.Join(t.TempDir(), "undo"))
	if err == nil {
		t.Fatal("relink escaped the skills directory")
	}
}

func TestLinkIntoPoolReplacesBroken(t *testing.T) {
	poolRoot := t.TempDir()
	undo := filepath.Join(t.TempDir(), "undo")
	target := filepath.Join(t.TempDir(), "gamma")
	writeSkill(t, target)
	broken := filepath.Join(poolRoot, "gamma")
	if err := os.Symlink("/tmp/missing-gamma", broken); err != nil {
		t.Fatal(err)
	}
	rec, err := LinkIntoPool(poolRoot, "gamma", target, undo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(broken, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(broken)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/missing-gamma" {
		t.Fatalf("restored %s", got)
	}
}

func TestRemoveEmptyAndRestore(t *testing.T) {
	root := t.TempDir()
	undo := filepath.Join(t.TempDir(), "undo")
	dir := filepath.Join(root, "blank")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, err := RemoveDir(root, dir, undo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still there: %v", err)
	}
	if err := Restore(rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Fatal(err)
	}
}

func writeSkill(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + filepath.Base(dir) + "\ndescription: \"x\"\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyDirFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "SKILL.md"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
