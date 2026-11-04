package manage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadInvocations(t *testing.T) {
	t.Run("missing settings", func(t *testing.T) {
		home := t.TempDir()
		got, err := ReadInvocations(home)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
			t.Fatalf("read created a settings directory: %v", err)
		}
	})
	t.Run("all modes", func(t *testing.T) {
		home := t.TempDir()
		writeInvocationFixture(t, home, `{"skillOverrides":{"auto":"on","manual":"user-invocable-only","off":"off","names":"name-only"}}`, 0o640)
		got, err := ReadInvocations(home)
		want := map[string]Invocation{"auto": InvocationAuto, "manual": InvocationManual, "off": InvocationOff, "names": InvocationNameOnly}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, %v", got, err)
		}
	})
	for _, text := range []string{`{`, `null`, `[]`, `{"skillOverrides":null}`, `{"skillOverrides":[]}`, `{"skillOverrides":{"beta":"unknown"}}`, `{"skillOverrides":{"beta":true}}`} {
		t.Run(text, func(t *testing.T) {
			home := t.TempDir()
			writeInvocationFixture(t, home, text, 0o600)
			if _, err := ReadInvocations(home); err == nil {
				t.Fatal("accepted invalid settings")
			}
		})
	}
}

func TestReadInvocation(t *testing.T) {
	home := t.TempDir()
	writeInvocationFixture(t, home, `{"skillOverrides":{"beta":"off"}}`, 0o600)
	for _, tc := range []struct {
		name string
		want Invocation
	}{{"beta", InvocationOff}, {"unconfigured", InvocationAuto}} {
		got, err := ReadInvocation(home, tc.name)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s, %v", tc.name, got, err)
		}
	}
	writeInvocationFixture(t, home, `{`, 0o600)
	if _, err := ReadInvocation(home, "beta"); err == nil {
		t.Fatal("read ignored malformed settings")
	}
}

func TestSetInvocation(t *testing.T) {
	t.Run("preserves settings and undo backup", func(t *testing.T) {
		home, undo := t.TempDir(), t.TempDir()
		original := `{"large":9007199254740993,"nested":{"keep":[1,true]},"skillOverrides":{"beta":"off","other":"name-only"}}`
		path := writeInvocationFixture(t, home, original, 0o640)
		rec, err := SetInvocation(home, "beta", InvocationManual, undo)
		if err != nil {
			t.Fatal(err)
		}
		fields := readInvocationFixture(t, path)
		if string(fields["large"]) != "9007199254740993" {
			t.Fatalf("large number changed: %s", fields["large"])
		}
		modes, err := ReadInvocations(home)
		if err != nil || modes["beta"] != InvocationManual || modes["other"] != InvocationNameOnly {
			t.Fatalf("preferences %v, %v", modes, err)
		}
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0o640 {
			t.Fatalf("mode %v, %v", st, err)
		}
		backup, err := os.ReadFile(rec.Payload)
		if err != nil || string(backup) != original {
			t.Fatalf("backup %q, %v", backup, err)
		}
		raw, err := os.ReadFile(filepath.Join(rec.Dir, "record.json"))
		if err != nil {
			t.Fatal(err)
		}
		var persisted Record
		if err := json.Unmarshal(raw, &persisted); err != nil {
			t.Fatal(err)
		}
		if err := Restore(persisted); err != nil {
			t.Fatal(err)
		}
		mode, err := ReadInvocation(home, "beta")
		if err != nil || mode != InvocationOff {
			t.Fatalf("restored %s, %v", mode, err)
		}
		if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
			t.Fatalf("undo record remained: %v", err)
		}
	})
	for _, mode := range []Invocation{InvocationAuto, InvocationManual, InvocationOff, InvocationNameOnly} {
		t.Run(string(mode), func(t *testing.T) {
			home := t.TempDir()
			rec, err := SetInvocation(home, "beta", mode, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadInvocation(home, "beta")
			if err != nil || got != mode {
				t.Fatalf("got %s, %v", got, err)
			}
			st, err := os.Stat(rec.LinkPath)
			if err != nil || st.Mode().Perm() != 0o600 {
				t.Fatalf("new mode %v, %v", st, err)
			}
			if err := Restore(rec); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(rec.LinkPath); !os.IsNotExist(err) {
				t.Fatalf("new settings file not removed: %v", err)
			}
		})
	}
	t.Run("refusals do not change settings", func(t *testing.T) {
		home := t.TempDir()
		original := `{"skillOverrides":{"beta":"off"}}`
		path := writeInvocationFixture(t, home, original, 0o600)
		for _, tc := range []struct {
			name string
			mode Invocation
			undo string
		}{{"beta", InvocationOff, t.TempDir()}, {"beta", Invocation("unknown"), t.TempDir()}, {"../beta", InvocationAuto, t.TempDir()}, {"beta", InvocationManual, ""}} {
			if _, err := SetInvocation(home, tc.name, tc.mode, tc.undo); err == nil {
				t.Fatalf("accepted %+v", tc)
			}
			got, _ := os.ReadFile(path)
			if string(got) != original {
				t.Fatalf("changed original: %s", got)
			}
		}
		writeInvocationFixture(t, home, `{`, 0o600)
		if _, err := SetInvocation(home, "beta", InvocationAuto, t.TempDir()); err == nil {
			t.Fatal("overwrote malformed settings")
		}
	})
	t.Run("refuses symlinked settings", func(t *testing.T) {
		for _, directory := range []bool{false, true} {
			home, target := t.TempDir(), t.TempDir()
			targetFile := filepath.Join(target, "settings.json")
			if err := os.WriteFile(targetFile, []byte(`{"keep":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if directory {
				if err := os.Symlink(target, filepath.Join(home, ".claude")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(targetFile, filepath.Join(home, ".claude", "settings.json")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := SetInvocation(home, "beta", InvocationOff, t.TempDir()); err == nil {
				t.Fatal("wrote through symlink")
			}
			if _, err := ReadInvocations(home); err == nil {
				t.Fatal("read through symlink")
			}
			got, _ := os.ReadFile(targetFile)
			if string(got) != `{"keep":true}` {
				t.Fatalf("target changed: %s", got)
			}
		}
	})
}

func TestRestoreInvocation(t *testing.T) {
	t.Run("preserves later unrelated settings", func(t *testing.T) {
		home := t.TempDir()
		path := writeInvocationFixture(t, home, `{"skillOverrides":{"beta":"off"}}`, 0o600)
		rec, err := SetInvocation(home, "beta", InvocationManual, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		writeInvocationFixture(t, home, `{"new":9007199254740995,"skillOverrides":{"beta":"user-invocable-only","other":"name-only"}}`, 0o600)
		if err := Restore(rec); err != nil {
			t.Fatal(err)
		}
		fields := readInvocationFixture(t, path)
		if string(fields["new"]) != "9007199254740995" {
			t.Fatalf("later settings lost: %s", fields["new"])
		}
		modes, err := ReadInvocations(home)
		if err != nil || modes["beta"] != InvocationOff || modes["other"] != InvocationNameOnly {
			t.Fatalf("preferences %v, %v", modes, err)
		}
	})
	t.Run("removes only newly added key", func(t *testing.T) {
		home := t.TempDir()
		path := writeInvocationFixture(t, home, `{"keep":true}`, 0o600)
		rec, err := SetInvocation(home, "beta", InvocationOff, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := Restore(rec); err != nil {
			t.Fatal(err)
		}
		fields := readInvocationFixture(t, path)
		if _, ok := fields["skillOverrides"]; ok || string(fields["keep"]) != "true" {
			t.Fatalf("not restored: %v", fields)
		}
	})
	t.Run("keeps later settings in initially absent file", func(t *testing.T) {
		home := t.TempDir()
		rec, err := SetInvocation(home, "beta", InvocationOff, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		path := writeInvocationFixture(t, home, `{"keep":true,"skillOverrides":{"beta":"off","other":"on"}}`, 0o600)
		if err := Restore(rec); err != nil {
			t.Fatal(err)
		}
		fields := readInvocationFixture(t, path)
		modes, err := ReadInvocations(home)
		if err != nil || len(modes) != 1 || modes["other"] != InvocationAuto || string(fields["keep"]) != "true" {
			t.Fatalf("settings %v / %v, %v", fields, modes, err)
		}
	})
	t.Run("refuses changed key", func(t *testing.T) {
		home := t.TempDir()
		rec, err := SetInvocation(home, "beta", InvocationOff, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		path := writeInvocationFixture(t, home, `{"skillOverrides":{"beta":"on"}}`, 0o600)
		before, _ := os.ReadFile(path)
		if err := Restore(rec); err == nil {
			t.Fatal("overwrote later preference")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("settings changed on conflict")
		}
		if _, err := os.Stat(rec.Dir); err != nil {
			t.Fatalf("lost undo record: %v", err)
		}
	})
}

func writeInvocationFixture(t *testing.T, home, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readInvocationFixture(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}
