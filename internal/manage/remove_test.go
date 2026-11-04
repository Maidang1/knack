package manage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveEntry(t *testing.T) {
	t.Run("directory and persisted restore", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "alpha")
		writeSkill(t, path)
		rec, err := RemoveEntry(root, path, filepath.Join(t.TempDir(), "undo"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("entry exists: %v", err)
		}
		b, err := os.ReadFile(filepath.Join(rec.Dir, "record.json"))
		if err != nil {
			t.Fatal(err)
		}
		var saved Record
		if err := json.Unmarshal(b, &saved); err != nil {
			t.Fatal(err)
		}
		if err := Restore(saved); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("link keeps its source", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(t.TempDir(), "alpha")
		writeSkill(t, source)
		path := filepath.Join(root, "alias")
		rel, err := filepath.Rel(root, source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(rel, path); err != nil {
			t.Fatal(err)
		}
		rec, err := RemoveEntry(root, path, filepath.Join(t.TempDir(), "undo"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		if err := Restore(rec); err != nil {
			t.Fatal(err)
		}
		raw, err := os.Readlink(path)
		if err != nil || raw != rel {
			t.Fatalf("restored %q: %v", raw, err)
		}
	})
	t.Run("unsafe paths", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(t.TempDir(), "alpha")
		writeSkill(t, source)
		linked := filepath.Join(root, "linked")
		if err := os.Symlink(filepath.Dir(source), linked); err != nil {
			t.Fatal(err)
		}
		system := filepath.Join(root, ".system")
		writeSkill(t, filepath.Join(system, "shipped"))
		for _, pair := range [][2]string{{root, source}, {root, system}, {system, filepath.Join(system, "shipped")}, {linked, filepath.Join(linked, "alpha")}} {
			if _, err := RemoveEntry(pair[0], pair[1], filepath.Join(t.TempDir(), "undo")); err == nil {
				t.Fatalf("allowed unsafe path %v", pair)
			}
		}
		if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(system, "shipped", "SKILL.md")); err != nil {
			t.Fatal(err)
		}
		aliasHome := t.TempDir()
		agentAlias := filepath.Join(aliasHome, ".claude")
		if err := os.Mkdir(filepath.Join(root, "skills"), 0755); err != nil {
			t.Fatal(err)
		}
		writeSkill(t, filepath.Join(root, "skills", "alpha"))
		if err := os.Symlink(root, agentAlias); err != nil {
			t.Fatal(err)
		}
		aliasRoot := filepath.Join(agentAlias, "skills")
		if _, err := RemoveEntry(aliasRoot, filepath.Join(aliasRoot, "alpha"), filepath.Join(t.TempDir(), "undo")); err == nil {
			t.Fatal("allowed redirected agent ancestor")
		}
	})
	t.Run("restore refuses redirected agent", func(t *testing.T) {
		for _, link := range []bool{false, true} {
			home := t.TempDir()
			agent := filepath.Join(home, ".claude")
			root := filepath.Join(agent, "skills")
			path := filepath.Join(root, "alpha")
			writeSkill(t, path)
			if link {
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(home, "missing-restore-target"), path); err != nil {
					t.Fatal(err)
				}
			}
			rec, err := RemoveEntry(root, path, filepath.Join(home, "undo"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(agent, filepath.Join(home, "original-agent")); err != nil {
				t.Fatal(err)
			}
			other := t.TempDir()
			if err := os.Mkdir(filepath.Join(other, "skills"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, agent); err != nil {
				t.Fatal(err)
			}
			if err := Restore(rec); err == nil {
				t.Fatal("restored into redirected agent")
			}
			if _, err := os.Lstat(filepath.Join(other, "skills", "alpha")); !os.IsNotExist(err) {
				t.Fatalf("redirected path was modified: %v", err)
			}
			if _, err := os.Stat(filepath.Join(rec.Dir, "record.json")); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("undo failure keeps entry", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "alpha")
		writeSkill(t, path)
		undo := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(undo, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, dest := range []string{"", undo} {
			if _, err := RemoveEntry(root, path, dest); err == nil {
				t.Fatal("removed without undo")
			}
			if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
		}
	})
}
