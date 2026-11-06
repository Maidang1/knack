package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func put(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func refresh(t *testing.T, r *Reader) *Snapshot {
	t.Helper()
	s, e := r.Refresh(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func reader(t *testing.T, home, cache string) *Reader {
	t.Helper()
	r, e := NewReader(home, cache)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestReaderRefresh(t *testing.T) {
	t.Run("Codex child metadata does not inflate sessions", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, ".codex/sessions/2026/11/01/parent.jsonl"), fixture(t, "codex.jsonl"))
		put(t, filepath.Join(home, ".codex/sessions/2026/11/01/child.jsonl"), fixture(t, "codex-child.jsonl"))
		r := reader(t, home, filepath.Join(home, "cache.json"))
		s := refresh(t, r)
		if len(s.Sessions) != 1 || len(s.UnlinkedSubagents) != 1 || s.UnlinkedSubagents[0].ParentID != "codex-one" {
			t.Fatalf("Codex child linkage %+v", s)
		}
		if sum := Aggregate(s, Filter{}); sum.Sessions != 1 || sum.Subagents != 1 || sum.Tokens.Total() != 195 {
			t.Fatalf("Codex child aggregate %+v", sum)
		}
		if cached, err := reader(t, home, filepath.Join(home, "cache.json")).Cached(); err != nil || len(cached.Sessions) != 1 || len(cached.UnlinkedSubagents) != 1 {
			t.Fatal("Codex cached child counted as root")
		}
	})
	t.Run("children, cache restart and privacy", func(t *testing.T) {
		home := t.TempDir()
		cache := filepath.Join(home, "cache", "sessions.json")
		parent := filepath.Join(home, ".claude", "projects", "project", "parent.jsonl")
		original := fixture(t, "claude.jsonl")
		put(t, parent, original)
		put(t, filepath.Join(filepath.Dir(parent), "parent", "subagents", "agent-child-a.jsonl"), fixture(t, "claude-child.jsonl"))
		r := reader(t, home, cache)
		s := refresh(t, r)
		if len(s.Sessions) != 1 || len(s.Sessions[0].Turns[0].Subagents) != 1 || len(s.UnlinkedSubagents) != 0 {
			t.Fatalf("child linkage: %+v", s)
		}
		sum := Aggregate(s, Filter{})
		if sum.Sessions != 1 || sum.Subagents != 1 || sum.Tokens.Total() != 227 || sum.OutputRate.Median != 7.75 || sum.OutputRate.P90 != 10 {
			t.Fatalf("child aggregate %+v", sum)
		}
		if s.ReadBytes == 0 {
			t.Fatal("first refresh should read logs")
		}
		if next := refresh(t, r); next.ReadBytes != 0 {
			t.Fatalf("unchanged logs read %d bytes", next.ReadBytes)
		}
		if restarted := refresh(t, reader(t, home, cache)); restarted.ReadBytes != 0 || !reflect.DeepEqual(Aggregate(restarted, Filter{}), sum) {
			t.Fatal("restart did not preserve checkpoint/aggregate")
		}
		cached, err := os.ReadFile(cache)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(cached, []byte("PRIVATE_")) {
			t.Fatal("cache contains conversation/tool content")
		}
		if runtime.GOOS != "windows" {
			info, _ := os.Stat(cache)
			if info.Mode().Perm() != 0o600 {
				t.Fatal("cache must be private")
			}
		}
		after, _ := os.ReadFile(parent)
		if !bytes.Equal(original, after) {
			t.Fatal("source log modified")
		}
		// Snapshots never share mutable slices with the next refresh.
		s.Sessions[0].Turns[0].Requests[0].Tokens.Output = 999
		if next := Aggregate(refresh(t, r), Filter{}); next.Tokens.Total() != 227 {
			t.Fatal("snapshot mutation leaked into reader")
		}
	})
	t.Run("append and incomplete final line", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, ".claude/projects/p/s.jsonl")
		lines := bytes.Split(fixture(t, "claude.jsonl"), []byte{'\n'})
		put(t, path, append(append([]byte{}, lines[0]...), '\n'))
		r := reader(t, home, filepath.Join(home, "cache.json"))
		refresh(t, r)
		f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(lines[1][:40]); e != nil {
			t.Fatal(e)
		}
		f.Close()
		partial := refresh(t, r)
		if !partial.Partial || len(partial.Sessions[0].Turns[0].Requests) != 0 {
			t.Fatal("incomplete JSONL line was consumed")
		}
		f, e = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if e != nil {
			t.Fatal(e)
		}
		_, e = f.Write(append(append([]byte{}, lines[1][40:]...), '\n'))
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		got := refresh(t, reader(t, home, filepath.Join(home, "cache.json")))
		if got.Partial || len(got.Sessions[0].Turns[0].Requests) != 1 || got.Sessions[0].Turns[0].Requests[0].Tokens.Total() != 110 {
			t.Fatalf("resume partial line %+v", got)
		}
		if got.ReadBytes != int64(len(lines[1])+1) {
			t.Fatalf("read %d, want only appended record", got.ReadBytes)
		}
	})
	t.Run("replacement truncation and deletion", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, ".claude/projects/p/s.jsonl")
		put(t, path, fixture(t, "claude.jsonl"))
		r := reader(t, home, "")
		refresh(t, r)
		put(t, path, fixture(t, "claude-child.jsonl"))
		if s := Aggregate(refresh(t, r), Filter{}); s.Requests != 1 || s.Tokens.Total() != 25 {
			t.Fatalf("truncation retained old state %+v", s)
		}
		// Replace and grow the file: head/tail fingerprints must invalidate it.
		replacement := bytes.ReplaceAll(fixture(t, "claude.jsonl"), []byte("claude-test"), []byte("new-model-x"))
		put(t, path, replacement)
		if s := Aggregate(refresh(t, r), Filter{}); s.Requests != 6 || s.Tokens.Total() != 202 {
			t.Fatalf("replacement retained old state %+v", s)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if s := refresh(t, r); len(s.Sessions) != 0 || len(r.files) != 0 {
			t.Fatal("removed file retained")
		}
	})
	t.Run("unlinked child and unsupported sources", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, ".claude/projects/p/missing/subagents/agent-child-a.jsonl"), fixture(t, "claude-child.jsonl"))
		put(t, filepath.Join(home, ".codex/sessions/2026/11/01/unknown.jsonl"), fixture(t, "unknown.jsonl"))
		s := refresh(t, reader(t, home, ""))
		if len(s.UnlinkedSubagents) != 1 || s.Sessions[0].UnsupportedReason == "" {
			t.Fatal("unknown linkage/format must remain explicit")
		}
		if sum := Aggregate(s, Filter{}); sum.Sessions != 0 || sum.Subagents != 1 || sum.Tokens.Total() != 25 || !sum.Incomplete {
			t.Fatalf("unlinked child aggregate %+v", sum)
		}
		if source := s.Sources[len(s.Sources)-1]; source.Agent != Cursor || source.Supported || !strings.Contains(source.Reason, "暂不支持") {
			t.Fatal("Cursor support notice")
		}
	})
	t.Run("malformed records and symlink boundary", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, ".factory/sessions/p/s.jsonl")
		put(t, path, append([]byte("broken\n"), fixture(t, "factory.jsonl")...))
		if runtime.GOOS != "windows" {
			if e := os.Symlink(path, filepath.Join(filepath.Dir(path), "linked.jsonl")); e != nil {
				t.Fatal(e)
			}
		}
		s := refresh(t, reader(t, home, ""))
		if len(s.Sessions) != 1 || !s.Sessions[0].Incomplete || len(s.Warnings) != 1 {
			t.Fatalf("malformed/symlink boundary %+v", s)
		}
		for _, cap := range s.Sessions[0].Capabilities {
			if (cap.Name == "tokens" || cap.Name == "output_rate") && (cap.Supported || !strings.Contains(cap.Reason, "暂不支持")) {
				t.Fatalf("Factory unsupported %+v", cap)
			}
		}
	})
	t.Run("cancelled context", func(t *testing.T) {
		r := reader(t, t.TempDir(), "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e := r.Refresh(ctx); e != context.Canceled {
			t.Fatalf("cancellation: %v", e)
		}
	})
}

func TestReadLog(t *testing.T) {
	t.Run("oversized line progresses across refreshes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "large.jsonl")
		b := append(bytes.Repeat([]byte{'x'}, 2*maxLineBytes), '\n')
		b = append(b, fixture(t, "factory.jsonl")...)
		put(t, path, b)
		file := logFile{path: path, agent: Factory}
		st := &fileState{}
		budget := int64(maxLineBytes + 1024)
		n, e := readLog(context.Background(), file, st, budget)
		if e != nil {
			t.Fatal(e)
		}
		if n != budget || st.Offset != budget || !st.Discarding || !st.Pending {
			t.Fatalf("oversized checkpoint %+v read=%d", st, n)
		}
		// Cache serialization must preserve an in-progress discarded line.
		encoded, _ := json.Marshal(st)
		st = &fileState{}
		if e := json.Unmarshal(encoded, st); e != nil {
			t.Fatal(e)
		}
		if _, e = readLog(context.Background(), file, st, int64(len(b))); e != nil {
			t.Fatal(e)
		}
		if st.LargeLines != 1 || st.Discarding || st.Pending || len(st.State.Session.Turns) != 2 {
			t.Fatalf("oversized resume %+v", st)
		}
	})
}

func TestNewReader(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"corrupt", "not JSON", true},
		{"wrong version", `{"Version":999,"Home":"ignored"}`, false},
		{"wrong home", `{"Version":1,"Home":"another-home"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cache := filepath.Join(home, "cache.json")
			put(t, cache, []byte(tc.body))
			r, e := NewReader(home, cache)
			if (e != nil) != tc.wantErr || r == nil || len(r.files) != 0 {
				t.Fatalf("reader=%v err=%v", r, e)
			}
		})
	}
}
