package manage

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"knack/internal/fsutil"
)

func TestFailedReplacementPreservesOriginalLink(t *testing.T) {
	for _, op := range []string{opRetarget, opLinkPool} {
		t.Run(op, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(t.TempDir(), "original")
			writeSkill(t, source)
			path := filepath.Join(root, "alpha")
			directoryLinkForTest(t, source, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			old, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := begin(t.TempDir(), op, path)
			if err != nil {
				t.Fatal(err)
			}
			rec.Payload = filepath.Join(rec.Dir, "payload")
			rec.OldTarget = old
			rec.NewTarget = filepath.Join("..", "replacement")
			_, err = installLink(rec, func(target, staged string) error {
				gotParent, err := filepath.EvalSymlinks(filepath.Dir(staged))
				if err != nil {
					t.Fatal(err)
				}
				wantParent, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				if !fsutil.EqualPath(gotParent, wantParent) || target != rec.NewTarget {
					t.Fatalf("staged link %q -> %q", staged, target)
				}
				return os.ErrPermission
			})
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("got %v, want symlink privilege failure", err)
			}
			assertSameObject(t, path, before)
			if raw, err := os.Readlink(path); err != nil || raw != old {
				t.Fatalf("original link changed: %q, %v", raw, err)
			}
			if _, err := os.Lstat(rec.Payload); !os.IsNotExist(err) {
				t.Fatalf("original moved despite failed staging: %v", err)
			}
		})
	}
}

func TestDirectoryLinkRestoreWhileTargetAbsent(t *testing.T) {
	operations := map[string]func(string, string, string, string) (Record, error){
		"RemoveEntry": func(root, path, target, undo string) (Record, error) {
			return RemoveEntry(root, path, undo)
		},
		"RemoveLink": func(root, path, target, undo string) (Record, error) {
			return RemoveLink(root, path, undo)
		},
		"Retarget": Retarget,
		"LinkIntoPool": func(root, path, target, undo string) (Record, error) {
			return LinkIntoPool(root, filepath.Base(path), target, undo)
		},
		"LinkIntoAgent": func(root, path, target, undo string) (Record, error) {
			return LinkIntoAgent(root, filepath.Base(path), target, undo)
		},
	}
	for name, apply := range operations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "alpha")
			source := filepath.Join(t.TempDir(), "source")
			writeSkill(t, source)
			directoryLinkForTest(t, source, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			old, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			// Create the directory link while the target exists, then break it.
			away := source + "-away"
			if err := os.Rename(source, away); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "alpha")
			writeSkill(t, target)
			rec, err := apply(root, path, target, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			assertSameObject(t, rec.Payload, before)
			saved := persistedRecord(t, rec)
			if err := Restore(saved); err != nil {
				t.Fatal(err)
			}
			assertSameObject(t, path, before)
			if raw, err := os.Readlink(path); err != nil || raw != old {
				t.Fatalf("restored target %q, %v", raw, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("target should still be absent: %v", err)
			}
			if err := os.Rename(away, source); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
				t.Fatalf("restored directory link cannot follow returning target: %v", err)
			}
		})
	}
}

func TestRestoreReplacementFailureKeepsCurrentLink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha")
	writeSkill(t, path)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "alpha")
	writeSkill(t, target)
	rec, err := RelinkDir(root, path, target, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	away := rec.Payload + "-away"
	if err := os.Rename(rec.Payload, away); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err == nil {
		t.Fatal("restore succeeded without its original payload")
	}
	assertSameObject(t, path, current)
	_ = persistedRecord(t, rec)
	if err := os.Rename(away, rec.Payload); err != nil {
		t.Fatal(err)
	}
	if err := Restore(rec); err != nil {
		t.Fatal(err)
	}
	assertSameObject(t, path, before)
}

func TestRestoreRemovedLinkConflictKeepsPayload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha")
	source := t.TempDir()
	directoryLinkForTest(t, source, path)
	rec, err := RemoveLink(root, path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(rec.Payload)
	if err != nil {
		t.Fatal(err)
	}
	writeSkill(t, path)
	if err := Restore(rec); err == nil {
		t.Fatal("restore overwrote an occupied slot")
	}
	assertSameObject(t, rec.Payload, before)
	_ = persistedRecord(t, rec)
	if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestLinksUnderRedirectedParentsKeepPoolSlot(t *testing.T) {
	for _, op := range []string{opRelink, opRetarget, opLinkPool} {
		t.Run(op, func(t *testing.T) {
			home := t.TempDir()
			physicalAgent := filepath.Join(t.TempDir(), "deep", "agent")
			physicalRoot := filepath.Join(physicalAgent, "skills")
			if err := os.MkdirAll(physicalRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			directoryLinkForTest(t, physicalAgent, filepath.Join(home, ".claude"))
			root := filepath.Join(home, ".claude", "skills")
			path := filepath.Join(root, "alpha")
			repo := filepath.Join(t.TempDir(), "alpha")
			writeSkill(t, repo)
			pool := filepath.Join(t.TempDir(), "alpha")
			directoryLinkForTest(t, repo, pool)
			var rec Record
			var err error
			switch op {
			case opRelink:
				writeSkill(t, path)
				rec, err = RelinkDir(root, path, pool, t.TempDir())
			case opRetarget:
				// A direct repository link must still be retargeted to the pool slot.
				directoryLinkForTest(t, repo, path)
				rec, err = Retarget(root, path, pool, t.TempDir())
			case opLinkPool:
				rec, err = LinkIntoAgent(root, "alpha", pool, t.TempDir())
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.Readlink(path)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.IsAbs(raw) {
				t.Fatalf("same-volume link is absolute: %s", raw)
			}
			want, err := physicalParentPath(pool)
			if err != nil {
				t.Fatal(err)
			}
			got, err := linkTarget(path, raw)
			if err != nil || !fsutil.EqualPath(got, want) || !fsutil.EqualPath(rec.NewTargetAbs, want) {
				t.Fatalf("link = %q, recorded = %q, want pool slot %q, err = %v", got, rec.NewTargetAbs, want, err)
			}
			if !sameLink(path, raw, pool) || sameLink(path, raw, repo) {
				t.Fatal("comparison lost physical-parent or pool-slot identity")
			}
			if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			// Undo must still recognize the slot when its repository disappears.
			if err := os.Rename(repo, repo+"-away"); err != nil {
				t.Fatal(err)
			}
			if err := Restore(rec); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRestoreLegacyLinkRecords(t *testing.T) {
	for _, op := range []string{opRetarget, opRemoveL, opLinkPool} {
		t.Run(op, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "alpha")
			target := filepath.Join(t.TempDir(), "alpha")
			writeSkill(t, target)
			rec, err := begin(t.TempDir(), op, path)
			if err != nil {
				t.Fatal(err)
			}
			rec.OldTarget = filepath.Join(t.TempDir(), "missing")
			rec.NewTargetAbs = target
			if op != opRemoveL {
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := save(rec); err != nil {
				t.Fatal(err)
			}
			if err := Restore(persistedRecord(t, rec)); err != nil {
				t.Fatal(err)
			}
			if raw, err := os.Readlink(path); err != nil || raw != rec.OldTarget {
				t.Fatalf("legacy target %q, %v", raw, err)
			}
		})
	}
}

func TestRestoreIncompleteReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "alpha")
	source := t.TempDir()
	directoryLinkForTest(t, source, path)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := begin(t.TempDir(), opRetarget, path)
	if err != nil {
		t.Fatal(err)
	}
	rec.Payload = filepath.Join(rec.Dir, "payload")
	rec.NewTargetAbs = filepath.Join(t.TempDir(), "alpha")
	if err := save(rec); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, rec.Payload); err != nil {
		t.Fatal(err)
	}
	if err := Restore(persistedRecord(t, rec)); err != nil {
		t.Fatal(err)
	}
	assertSameObject(t, path, before)
}

func persistedRecord(t *testing.T, rec Record) Record {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(rec.Dir, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Record
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func assertSameObject(t *testing.T, path string, before os.FileInfo) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("filesystem object at %s was recreated", path)
	}
}

func directoryLinkForTest(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		// Junction fixtures work for ordinary Windows users without symlink
		// privileges and also exercise preservation of the reparse-point type.
		if out, junctionErr := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput(); junctionErr != nil {
			t.Fatalf("symlink: %v; junction: %v (%s)", err, junctionErr, out)
		}
	}
}
