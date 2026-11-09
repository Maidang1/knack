package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestEmbeddedStreamingCheckpoint(t *testing.T) {
	// Built-ins have neither whole-session worker results nor a worker circuit
	// breaker. Lower those limits below even a single fixture's processing time.
	oldCall, oldRefresh, oldBudget := pluginCallLimit, pluginRefreshLimit, maxRefreshRead
	pluginCallLimit, pluginRefreshLimit, maxRefreshRead = time.Nanosecond, time.Nanosecond, 2048
	t.Cleanup(func() { pluginCallLimit, pluginRefreshLimit, maxRefreshRead = oldCall, oldRefresh, oldBudget })
	for _, tc := range []struct {
		agent      Agent
		name, path string
	}{
		{Claude, "claude.jsonl", ".claude/projects/p/s.jsonl"},
		{Codex, "codex.jsonl", ".codex/archived_sessions/2026/11/01/s.jsonl"},
	} {
		t.Run(string(tc.agent), func(t *testing.T) {
			home := t.TempDir()
			path, cache := filepath.Join(home, tc.path), filepath.Join(home, "cache.json")
			original := fixture(t, tc.name)
			put(t, path, original)
			expected := fixtureState(t, tc.agent, tc.name)
			var previous int64
			batches := 0
			for {
				// Recreate the Reader every batch: no hidden JS state can be necessary
				// to finish a response or deduplicate usage after restart.
				r := reader(t, home, cache)
				snap := refresh(t, r)
				st := r.files[path]
				if st == nil || st.Offset <= previous || snap.ReadBytes > maxRefreshRead {
					t.Fatalf("no bounded progress: %+v", st)
				}
				previous = st.Offset
				batches++
				if !snap.More {
					if st.Pending || st.Offset != int64(len(original)) {
						t.Fatalf("incomplete checkpoint %+v", st)
					}
					got := st.State.Session
					got.ID, got.Path = expected.Session.ID, expected.Session.Path
					if !reflect.DeepEqual(got, expected.Session) {
						t.Fatalf("streaming changed semantics\ngot %+v\nwant %+v", got, expected.Session)
					}
					break
				}
				if batches > 50 {
					t.Fatal("streaming failed to finish")
				}
			}
			if batches < 2 {
				t.Fatal("fixture did not exercise multiple batches")
			}
			if snap := refresh(t, reader(t, home, cache)); snap.ReadBytes != 0 {
				t.Fatal("unchanged log reread")
			}
			b, err := os.ReadFile(cache)
			if err != nil || bytes.Contains(b, []byte("PRIVATE_")) {
				t.Fatal("conversation content persisted", err)
			}
			if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, original) {
				t.Fatal("source changed", err)
			}
		})
	}
}

func TestEmbeddedConcurrentTransforms(t *testing.T) {
	// Two Readers and a replay share each embedded runtime. The lock protects
	// goja, while all session-dependent state belongs to the Go checkpoint.
	var wg sync.WaitGroup
	for _, agent := range []Agent{Claude, Codex} {
		name := strings.ToLower(string(agent)) + ".jsonl"
		lines := bytes.Split(bytes.TrimSpace(fixture(t, name)), []byte{'\n'})
		want := fixtureState(t, agent, name)
		for range 4 {
			wg.Go(func() {
				st := parseState{Session: Session{Agent: agent}}
				p := newParser(&st)
				tr := newTranscriptReader(&Transcript{}, formatOf(agent))
				for _, line := range lines {
					if err := p.line(line); err != nil {
						t.Error(err)
						return
					}
					if !tr.line(line) {
						t.Error("replay failed")
						return
					}
				}
				if !reflect.DeepEqual(st, *want) {
					t.Error("session state leaked between transforms")
				}
			})
		}
	}
	wg.Wait()
}

func TestEmbeddedIgnoresInvalidContentBlocks(t *testing.T) {
	line := []byte(`{"type":"assistant","uuid":"a","timestamp":"2026-11-01T00:00:01Z","message":{"id":"r","model":"m","usage":{"input_tokens":10,"output_tokens":20},"content":[null,{"type":"text","text":123},{"type":"text","text":"valid reply"}]}}`)
	st := parseState{Session: Session{Agent: Claude}}
	if err := newParser(&st).line(line); err != nil {
		t.Fatal(err)
	}
	if len(st.Session.Turns) != 1 || len(st.Session.Turns[0].Requests) != 1 || st.Session.Turns[0].Requests[0].Tokens.Total() != 30 {
		t.Fatal("invalid block discarded valid request")
	}
	tr := newTranscriptReader(&Transcript{}, formatOf(Claude))
	if !tr.line(line) || len(tr.out.Entries) != 1 || tr.out.Entries[0].Text != "valid reply" {
		t.Fatal("invalid block discarded valid replay")
	}
}

func TestEmbeddedSkipsBlocksWithoutText(t *testing.T) {
	use := []byte(`{"type":"assistant","uuid":"a","timestamp":"2026-11-01T00:00:01Z","message":{"id":"r","model":"m","content":[{"type":"tool_use","id":"t","name":"ToolSearch","input":{}}]}}`)
	result := []byte(`{"type":"user","uuid":"u","timestamp":"2026-11-01T00:00:02Z","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"tool_reference","tool_name":"a"},{"type":"tool_reference","tool_name":"b"}]}]}}`)
	st := parseState{Session: Session{Agent: Claude}}
	p := newParser(&st)
	for _, line := range [][]byte{use, result} {
		if err := p.line(line); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.Session.Turns[0].Tools[0].OutputBytes; got != 0 {
		t.Fatalf("output bytes %d", got)
	}
	tr := newTranscriptReader(&Transcript{}, formatOf(Claude))
	if !tr.line(use) || !tr.line(result) || len(tr.out.Entries) != 1 || tr.out.Entries[0].Text != "" {
		t.Fatalf("replay %+v", tr.out.Entries)
	}
}

func TestEmbeddedVersionAndCapabilities(t *testing.T) {
	for _, agent := range []Agent{Claude, Codex} {
		f, ok := formatOf(agent).(*streamingFormat)
		if !ok || !strings.HasPrefix(f.version(), "stream/1/") {
			t.Fatal("not an embedded streaming plugin", agent)
		}
		if info := f.Info(); !info.Skills || (agent == Claude && (!info.OutputRate || !info.PermissionWait || !info.ToolOutcome)) || (agent == Codex && (info.OutputRate || info.RateNote != "无法可靠关联请求起止时间，tok/s 暂不支持" || info.TimingNote != "Codex 请求耗时按输入完成到用量记录的间隔估算；旧日志缺少逐请求用量时不计为零。")) {
			t.Fatalf("capabilities changed: %+v", info)
		}
		// Changing the parser version invalidates a checkpoint even if the log's
		// own size and mtime still match.
		home := t.TempDir()
		path := filepath.Join(home, f.layout().roots[0], "p/s.jsonl")
		if agent == Codex {
			path = filepath.Join(home, f.layout().roots[0], "s.jsonl")
		}
		put(t, path, fixture(t, strings.ToLower(string(agent))+".jsonl"))
		cache := filepath.Join(home, "cache.json")
		r := reader(t, home, cache)
		snap := refresh(t, r)
		r.files[path].Format = string(agent) + "/3"
		r.files[path].State.Session.Title = "obsolete parser"
		if err := r.save(); err != nil {
			t.Fatal(err)
		}
		rebuilt := refresh(t, reader(t, home, cache))
		if rebuilt.ReadBytes == 0 || !reflect.DeepEqual(snap.Sessions, rebuilt.Sessions) {
			t.Fatal("old parser checkpoint was not rebuilt")
		}
	}
}

func TestPluginReservedUndiscoveredRoots(t *testing.T) {
	home := t.TempDir()
	// This path is deliberately outside Claude's recognized session layout.
	log := filepath.Join(home, ".claude/projects/not-a-session.log")
	put(t, log, []byte("private"))
	alias := filepath.Join(home, "alias.log")
	if err := os.Symlink(log, alias); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`knack.registerAgent({id:'thief',name:'Thief',list(){return [{path:%q},{path:%q}];},read(){return [{time:1793491200000,role:'user',text:'stolen'}];}});`, log, alias)
	if problems := usePlugins(t, home, map[string]string{"thief.js": src}); len(problems) > 0 {
		t.Fatal(problems)
	}
	snap := refresh(t, reader(t, home, ""))
	if len(snap.Sessions) != 0 || !strings.Contains(strings.Join(snap.Warnings, " "), "已属于其他 Agent") {
		t.Fatalf("reserved log was claimed: %+v", snap)
	}
}

func TestPluginRichRecords(t *testing.T) {
	home := t.TempDir()
	log := filepath.Join(home, "rich.log")
	put(t, log, []byte("read only"))
	src := fmt.Sprintf(`knack.registerAgent({id:'rich',name:'Rich',info:{skills:true,permissionWait:true,toolOutcome:true,skillNote:'measured skills',usageNote:'usage',rateNote:'rate',timingNote:'timing'},list(){return [{path:%q}];},read(){return [
 {time:1793491200000,id:'u',stats:[{op:'known'},{op:'begin',id:'t'}],replay:[{op:'entry',kind:'context',text:'PRIVATE_CONTEXT'}]},
 {time:1793491201000,id:'a',stats:[{op:'request',id:'r',model:'model',tokens:{Input:10,Output:20},known:true,think:500},{op:'use',id:'s',name:'Skill',input:{skill:'build'}}],replay:[{op:'entry',kind:'tool',id:'s',rid:'r',name:'Skill',input:{skill:'build'}}]},
 {time:1793491203000,id:'r',stats:[{op:'result',id:'s',result:{agentId:'child',interrupted:true,timedOutAfterMs:2000},permission:{decision:'accept',source:'user_temporary'},output:'PRIVATE_OUTPUT'},{op:'event',kind:'compact',duration:1000}],replay:[{op:'result',id:'s',text:'PRIVATE_OUTPUT'}]}
 ];}});`, log)
	if problems := usePlugins(t, home, map[string]string{"rich.js": src}); len(problems) > 0 {
		t.Fatal(problems)
	}
	info := LookupAgent("rich")
	if !info.Skills || !info.PermissionWait || !info.ToolOutcome || info.SkillNote != "measured skills" || info.UsageNote != "usage" || info.RateNote != "rate" || info.TimingNote != "timing" {
		t.Fatalf("missing declarations %+v", info)
	}
	cache := filepath.Join(home, "cache.json")
	snap := refresh(t, reader(t, home, cache))
	if len(snap.Sessions) != 1 {
		t.Fatalf("rich session missing %+v", snap)
	}
	turn := snap.Sessions[0].Turns[0]
	if len(turn.Skills) != 1 || !turn.Requests[0].UsageKnown || turn.Requests[0].Thinking.Value != 500*time.Millisecond || turn.Tools[0].AgentID != "child" || !turn.Tools[0].PermissionWait || !turn.Tools[0].Interrupted || turn.Tools[0].TimedOutAfter != 2*time.Second || len(turn.Events) != 3 {
		t.Fatalf("lost rich data %+v", turn)
	}
	tr, err := ReadTranscript(context.Background(), log, "rich")
	if err != nil || len(tr.Entries) != 2 || tr.Entries[1].Text != "PRIVATE_OUTPUT" || tr.Entries[1].End.IsZero() {
		t.Fatalf("rich replay %+v: %v", tr, err)
	}
	b, err := os.ReadFile(cache)
	if err != nil || bytes.Contains(b, []byte("PRIVATE_")) || !json.Valid(b) {
		t.Fatal("invalid/private cache", err)
	}
}

// This archive's decompressed content exceeds both user-plugin whole-file and
// IPC result limits. Oversized records must advance across refresh boundaries,
// retain compaction headers and reach the usage record at the very end.
func TestEmbeddedLargeArchive(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex/archived_sessions/large.jsonl.zst")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(file, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	write := func(b []byte) {
		t.Helper()
		if _, err := enc.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write([]byte(`{"type":"session_meta","timestamp":"2026-11-01T00:00:00Z","payload":{"id":"large"}}` + "\n"))
	write([]byte(`{"type":"event_msg","timestamp":"2026-11-01T00:00:01Z","payload":{"type":"task_started","turn_id":"t"}}` + "\n"))
	chunk := bytes.Repeat([]byte("x"), 64<<10)
	for range 27 {
		write([]byte(`{"timestamp":"2026-11-01T00:00:02Z","type":"compacted","payload":{"replacement_history":["`))
		for range 80 {
			write(chunk)
		}
		write([]byte(`"]}}` + "\n"))
	}
	write([]byte(`{"type":"token_usage_record","timestamp":"2026-11-01T00:00:03Z","payload":{"response_id":"tail","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n"))
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, "cache.json")
	var total int64
	for batch := 0; batch < 30; batch++ {
		r := reader(t, home, cache)
		snap := refresh(t, r)
		total += snap.ReadBytes
		if snap.More {
			continue
		}
		st := r.files[path]
		if st == nil || st.Pending || !st.Done || st.Offset <= maxPluginMessage || total <= maxPluginMessage || len(snap.Sessions) != 1 {
			t.Fatalf("archive stopped early: %+v read=%d", st, total)
		}
		turn := snap.Sessions[0].Turns[0]
		if len(turn.Events) != 27 || len(turn.Requests) != 1 || turn.Requests[0].ID != "tail" || turn.Requests[0].Tokens.Total() != 15 || st.LargeLines != 0 || st.BadLines != 0 {
			t.Fatalf("lost archive metadata: %+v", turn)
		}
		if next := refresh(t, reader(t, home, cache)); next.ReadBytes != 0 {
			t.Fatal("finished archive reread")
		}
		return
	}
	t.Fatal("large archive never finished")
}

func BenchmarkEmbeddedRecord(b *testing.B) {
	line, err := os.ReadFile("testdata/claude.jsonl")
	if err != nil {
		b.Fatal(err)
	}
	line = bytes.Split(line, []byte{'\n'})[2]
	f := formatOf(Claude).(*streamingFormat)
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := f.normalize(line, "stats"); err != nil {
			b.Fatal(err)
		}
	}
}
