package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func usageRecord(second int, typ string, payload any) string {
	b, err := json.Marshal(map[string]any{"timestamp": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(second) * time.Second).Format(time.RFC3339Nano), "type": typ, "payload": payload})
	if err != nil {
		panic(err)
	}
	return string(b) + "\n"
}
func oldUsage(in, out, read, write int) map[string]int {
	return map[string]int{"input_tokens": in, "output_tokens": out, "cached_input_tokens": read, "cache_write_input_tokens": write}
}
func countRecord(second int, last, total any) string {
	return usageRecord(second, "event_msg", map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": last, "total_token_usage": total}})
}
func nativeRecord(second int, id string, usage any) string {
	return usageRecord(second, "token_usage_record", map[string]any{"response_id": id, "usage": usage})
}
func usagePrelude(id, parent string) string {
	meta := map[string]any{"id": id}
	if parent != "" {
		meta["parent_thread_id"] = parent
		meta["source"] = map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": parent}}}
	}
	return usageRecord(0, "session_meta", meta) + usageRecord(0, "event_msg", map[string]any{"type": "task_started", "turn_id": "t"}) + usageRecord(1, "turn_context", map[string]any{"model": "gpt-old"}) + usageRecord(1, "response_item", map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "PRIVATE_USER"}}})
}
func usageRequests(s *Snapshot) []Request {
	var requests []Request
	for _, session := range append(append([]Session{}, s.Sessions...), s.UnlinkedSubagents...) {
		for _, turn := range session.Turns {
			requests = append(requests, turn.Requests...)
		}
	}
	return requests
}
func readUsageLog(t *testing.T, log string) (*Snapshot, *Reader, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".codex/sessions/s.jsonl")
	put(t, path, []byte(log))
	r := reader(t, home, filepath.Join(home, "cache.json"))
	return refresh(t, r), r, path
}

func TestCodexLegacyTokenCount(t *testing.T) {
	first, second := oldUsage(100, 20, 60, 10), oldUsage(50, 10, 10, 0)
	log := usagePrelude("old", "") + usageRecord(2, "event_msg", map[string]any{"type": "item_completed", "item": map[string]any{"type": "Reasoning"}, "started_at_ms": 1793491201000, "completed_at_ms": 1793491202000}) + countRecord(3, first, first) + countRecord(4, first, first) + countRecord(6, second, oldUsage(150, 30, 70, 10))
	snap, r, _ := readUsageLog(t, log)
	requests := usageRequests(snap)
	if len(requests) != 2 || Aggregate(snap, Filter{}).Tokens.Total() != 180 {
		t.Fatalf("legacy counts: %+v", requests)
	}
	if requests[0].Tokens != (Tokens{30, 20, 60, 10}) || requests[0].Model != "gpt-old" || requests[0].Elapsed.Value != 2*time.Second || requests[0].Thinking.Value != time.Second || requests[0].Generation.Value != time.Second || requests[1].Elapsed.Value != 3*time.Second {
		t.Fatalf("legacy normalization/timing: %+v", requests)
	}
	if again := refresh(t, reader(t, r.home, r.cachePath)); again.ReadBytes != 0 || !reflect.DeepEqual(snap.Sessions, again.Sessions) {
		t.Fatal("legacy cache did not survive restart")
	}
	b, err := os.ReadFile(r.cachePath)
	if err != nil || strings.Contains(string(b), "PRIVATE_") {
		t.Fatal("content leaked into usage checkpoint", err)
	}
}

func TestCodexMissingZeroCumulativeFields(t *testing.T) {
	tokens := oldUsage(100, 20, 60, 0)
	omitted := oldUsage(100, 20, 60, 0)
	delete(omitted, "cache_write_input_tokens")
	log := usagePrelude("zeros", "") + countRecord(2, omitted, omitted) + countRecord(3, tokens, tokens)
	snap, _, _ := readUsageLog(t, log)
	if len(usageRequests(snap)) != 1 || Aggregate(snap, Filter{}).Tokens.Total() != 120 {
		t.Fatal("missing and explicit zero created separate cumulative positions")
	}
}

func TestCodexMixedUsageOrderAndRestart(t *testing.T) {
	for _, fallbackFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(fallbackFirst), func(t *testing.T) {
			tokens := oldUsage(100, 20, 60, 10)
			first, second := nativeRecord(3, "r", tokens), countRecord(4, tokens, tokens)
			if fallbackFirst {
				first, second = countRecord(3, tokens, tokens), nativeRecord(4, "r", tokens)
			}
			// The baseline is the previous parser's behavior: the count record only
			// updates log timestamps and cannot change request timing or model.
			baseline := usagePrelude("mixed", "") + first + second
			countAt := 4
			if fallbackFirst {
				countAt = 3
			}
			baseline = strings.Replace(baseline, countRecord(countAt, tokens, tokens), countRecord(countAt, nil, tokens), 1)
			want, _, _ := readUsageLog(t, baseline)
			home := t.TempDir()
			path := filepath.Join(home, ".codex/sessions/s.jsonl")
			cache := filepath.Join(home, "cache.json")
			put(t, path, []byte(usagePrelude("mixed", "")+first))
			refresh(t, reader(t, home, cache))
			appendToLog(t, path, second)
			got := refresh(t, reader(t, home, cache))
			if !reflect.DeepEqual(usageRequests(got), usageRequests(want)) || Aggregate(got, Filter{}).Tokens.Total() != 120 {
				t.Fatalf("mixed streams changed native requests\ngot %+v\nwant %+v", usageRequests(got), usageRequests(want))
			}
			appendToLog(t, path, countRecord(5, tokens, tokens)+nativeRecord(6, "r", tokens))
			if again := refresh(t, reader(t, home, cache)); !reflect.DeepEqual(usageRequests(again), usageRequests(want)) {
				t.Fatalf("repeated reports recounted: %+v", usageRequests(again))
			}
		})
	}
}

func appendToLog(t *testing.T, path, records string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(records); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCodexFallbackSkipsEmptyUsage(t *testing.T) {
	log := usagePrelude("empty", "") + usageRecord(2, "event_msg", map[string]any{"type": "token_count", "info": nil}) + usageRecord(3, "event_msg", map[string]any{"type": "token_count", "info": map[string]any{"total_token_usage": oldUsage(100, 20, 60, 0)}}) + countRecord(4, nil, oldUsage(100, 20, 60, 0)) + countRecord(5, oldUsage(0, 0, 0, 0), oldUsage(100, 20, 60, 0))
	snap, _, _ := readUsageLog(t, log)
	if got := usageRequests(snap); len(got) != 0 {
		t.Fatalf("empty usage became requests: %+v", got)
	}
}

func TestCodexFallbackDoesNotPairIndependentInputs(t *testing.T) {
	tokens := oldUsage(100, 20, 60, 0)
	log := usagePrelude("partial", "") + nativeRecord(2, "native", tokens) + usageRecord(3, "response_item", map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "next input"}}}) + countRecord(4, tokens, oldUsage(200, 40, 120, 0))
	snap, _, _ := readUsageLog(t, log)
	if got := usageRequests(snap); len(got) != 2 || Aggregate(snap, Filter{}).Tokens.Total() != 240 {
		t.Fatalf("independent equal-cost requests paired: %+v", got)
	}
}

func TestCodexPrimaryCountAfterInputBoundary(t *testing.T) {
	tokens := oldUsage(100, 20, 60, 0)
	output := usageRecord(3, "response_item", map[string]any{"type": "function_call_output", "call_id": "tool", "output": "PRIVATE_OUTPUT"})
	snap, _, _ := readUsageLog(t, usagePrelude("boundary", "")+nativeRecord(2, "native", tokens)+output+countRecord(4, tokens, tokens))
	if requests := usageRequests(snap); len(requests) != 1 || requests[0].ID != "native" || requests[0].Elapsed.Value != time.Second {
		t.Fatalf("tool output prevented cumulative pairing: %+v", requests)
	}
}

func TestCodexMissingPrimaryBeforeModernStream(t *testing.T) {
	tokens := oldUsage(100, 20, 60, 0)
	input := usageRecord(3, "response_item", map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "next input"}}})
	snap, _, _ := readUsageLog(t, usagePrelude("partial", "")+countRecord(2, tokens, tokens)+input+nativeRecord(4, "native", tokens)+countRecord(5, tokens, oldUsage(200, 40, 120, 0)))
	requests := usageRequests(snap)
	if len(requests) != 2 || requests[0].End.Second() != 2 || requests[1].ID != "native" || Aggregate(snap, Filter{}).Tokens.Total() != 240 {
		t.Fatalf("legacy prefix was mistaken for equal-cost native request: %+v", requests)
	}
}

func TestCodexUsageBudgetedRestart(t *testing.T) {
	saved := maxRefreshRead
	maxRefreshRead = 1024
	t.Cleanup(func() { maxRefreshRead = saved })
	home := t.TempDir()
	path := filepath.Join(home, ".codex/sessions/s.jsonl")
	cache := filepath.Join(home, "cache.json")
	tokens := oldUsage(100, 20, 60, 10)
	var log strings.Builder
	log.WriteString(usagePrelude("budget", ""))
	for i := 1; i <= 50; i++ {
		log.WriteString(countRecord(i+1, tokens, oldUsage(100*i, 20*i, 60*i, 10*i)))
	}
	for i := 1; i <= 50; i++ {
		log.WriteString(nativeRecord(i+51, fmt.Sprintf("r-%d", i), tokens))
	}
	put(t, path, []byte(log.String()))
	var previous int64
	for batch := 0; batch < 200; batch++ {
		r := reader(t, home, cache)
		snap := refresh(t, r)
		if snap.ReadBytes > maxRefreshRead || r.files[path].Offset <= previous {
			t.Fatal("no bounded progress")
		}
		previous = r.files[path].Offset
		if snap.More {
			continue
		}
		requests := usageRequests(snap)
		if len(requests) != 50 || Aggregate(snap, Filter{}).Tokens.Total() != 6000 {
			t.Fatalf("budget/restart duplicated usage: %+v", requests)
		}
		for _, request := range requests {
			if strings.HasPrefix(request.ID, "usage-") {
				t.Fatal("native request did not replace fallback")
			}
		}
		return
	}
	t.Fatal("budgeted usage never completed")
}

func TestCodexInheritedTokenHistory(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "cache.json")
	first, second, third := oldUsage(100, 20, 60, 0), oldUsage(50, 10, 10, 0), oldUsage(30, 5, 10, 0)
	parent := usagePrelude("parent", "") + countRecord(2, first, first) + countRecord(3, second, oldUsage(150, 30, 70, 0))
	// Copied history can have rewritten timestamps. The child's first metadata
	// is authoritative for identity; subsequent parent metadata identifies the
	// copied usage stream. Its cumulative values continue into new child work.
	child := usagePrelude("child", "parent") + usageRecord(0, "session_meta", map[string]any{"id": "parent"}) + countRecord(4, first, first) + countRecord(5, second, oldUsage(150, 30, 70, 0)) + countRecord(6, third, oldUsage(180, 35, 80, 0))
	put(t, filepath.Join(home, ".codex/sessions/parent.jsonl"), []byte(parent))
	put(t, filepath.Join(home, ".codex/sessions/child.jsonl"), []byte(child))
	snap := refresh(t, reader(t, home, cache))
	if len(snap.Sessions) != 1 || len(snap.UnlinkedSubagents) != 1 || snap.UnlinkedSubagents[0].ID != "child" || snap.UnlinkedSubagents[0].ParentID != "parent" || len(usageRequests(snap)) != 3 || Aggregate(snap, Filter{}).Tokens.Total() != 215 {
		t.Fatalf("inherited usage counted twice: %+v", snap)
	}
	if got := usageRequests(&Snapshot{UnlinkedSubagents: snap.UnlinkedSubagents}); len(got) != 1 || got[0].Tokens.Total() != 35 {
		t.Fatalf("child own usage discarded: %+v", got)
	}
	cached, err := reader(t, home, cache).Cached()
	if err != nil || !reflect.DeepEqual(snap.Sessions, cached.Sessions) || !reflect.DeepEqual(snap.UnlinkedSubagents, cached.UnlinkedSubagents) {
		t.Fatal("cross-file ownership changed on cached startup", err)
	}
	// With the parent unavailable, one child copy still represents the history.
	if err := os.Remove(filepath.Join(home, ".codex/sessions/parent.jsonl")); err != nil {
		t.Fatal(err)
	}
	again := refresh(t, reader(t, home, cache))
	if len(usageRequests(again)) != 3 || Aggregate(again, Filter{}).Tokens.Total() != 215 {
		t.Fatal("history lost with absent parent")
	}
}
