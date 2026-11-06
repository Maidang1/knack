package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func compress(t *testing.T, b []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	return enc.EncodeAll(b, nil)
}

// Codex compresses older rollouts in place and in archived_sessions. They
// count exactly like plain logs, also when a small budget splits the read.
func TestCompressedCodexLogs(t *testing.T) {
	plain := t.TempDir()
	put(t, filepath.Join(plain, ".codex/sessions/2026/11/01/a.jsonl"), fixture(t, "codex.jsonl"))
	want := Aggregate(refresh(t, reader(t, plain, "")), Filter{})

	for _, budget := range []int64{maxRefreshRead, 300} {
		saved := maxRefreshRead
		maxRefreshRead = budget
		home := t.TempDir()
		z := compress(t, fixture(t, "codex.jsonl"))
		put(t, filepath.Join(home, ".codex/sessions/2026/10/01/rollout-a.jsonl.zst"), z)
		put(t, filepath.Join(home, ".codex/archived_sessions/rollout-b.jsonl.zst"), z)
		cache := filepath.Join(home, "cache.json")
		r := reader(t, home, cache)
		s := refresh(t, r)
		for range 50 {
			if !s.More {
				break
			}
			s = refresh(t, r)
		}
		maxRefreshRead = saved
		got := Aggregate(s, Filter{})
		if s.More || s.Partial || len(s.Sessions) != 2 || got.Tokens != (Tokens{want.Tokens.Input * 2, want.Tokens.Output * 2, want.Tokens.CacheRead * 2, want.Tokens.CacheWrite * 2}) || got.Requests != 2*want.Requests {
			t.Fatalf("budget %d: more=%t partial=%t sessions=%d got %+v want 2×%+v", budget, s.More, s.Partial, len(s.Sessions), got.Tokens, want.Tokens)
		}
		for _, sess := range s.Sessions {
			if sess.Incomplete || sess.ID != "codex-one" {
				t.Fatalf("compressed session %+v", sess)
			}
		}
		// A finished compressed log is not read again, also after a restart.
		if again := refresh(t, reader(t, home, cache)); again.ReadBytes != 0 || len(again.Sessions) != 2 {
			t.Fatalf("compressed log reread: %d bytes", again.ReadBytes)
		}
	}
	home := t.TempDir()
	path := filepath.Join(home, "rollout.jsonl.zst")
	put(t, path, compress(t, fixture(t, "codex.jsonl")))
	tr, err := ReadTranscript(context.Background(), path, Codex)
	if err != nil || len(entries(tr, EntryUser)) != 1 || len(entries(tr, EntryTool)) != 1 {
		t.Fatalf("compressed replay %v %+v", err, tr)
	}
}
