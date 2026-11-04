package manage

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsCrossVolumeLinkTarget(t *testing.T) {
	target := `D:\skills\alpha`
	got, err := relativeTarget(`C:\Users\user\pool`, target)
	if err != nil || got != target {
		t.Fatalf("cross-volume target = %q, %v", got, err)
	}
	got, err = relativeTarget(`C:\Users\user\pool`, `C:\skills\alpha`)
	if err != nil || filepath.IsAbs(got) {
		t.Fatalf("same-volume target = %q, %v", got, err)
	}
}

func TestWindowsReplacementPrivilegeFailureKeepsJunction(t *testing.T) {
	for _, retarget := range []bool{false, true} {
		root := t.TempDir()
		target := filepath.Join(t.TempDir(), "alpha")
		writeSkill(t, target)
		probe := filepath.Join(root, "probe")
		if err := os.Symlink(target, probe); err == nil {
			_ = os.Remove(probe)
			t.Skip("symlink creation is permitted; denied staging is covered by injection")
		}
		source := filepath.Join(t.TempDir(), "original")
		writeSkill(t, source)
		path := filepath.Join(root, "alpha")
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", path, source).CombinedOutput(); err != nil {
			t.Fatalf("junction fixture: %v (%s)", err, out)
		}
		before, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(source, source+"-away"); err != nil {
			t.Fatal(err)
		}
		if retarget {
			_, err = Retarget(root, path, target, t.TempDir())
		} else {
			_, err = LinkIntoPool(root, "alpha", target, t.TempDir())
		}
		if err == nil {
			t.Fatal("replacement unexpectedly succeeded without symlink privilege")
		}
		assertSameObject(t, path, before)
	}
}
