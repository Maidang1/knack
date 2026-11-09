package session

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A plugin's worker is a copy of the running program, here the test binary.
func TestMain(m *testing.M) {
	ServePluginWorker()
	os.Exit(m.Run())
}

// skipUnderRace skips tests that fill hundreds of MiB byte by byte, which the
// race detector slows past the call limit.
func skipUnderRace(t *testing.T) {
	t.Helper()
	if raceEnabled {
		t.Skip("too slow under the race detector")
	}
}

// usePlugins installs scripts by file name for one test and removes them after.
func usePlugins(t *testing.T, home string, scripts map[string]string) []string {
	t.Helper()
	dir := filepath.Join(home, "plugins")
	for name, src := range scripts {
		put(t, filepath.Join(dir, name), []byte(src))
	}
	t.Cleanup(func() { LoadPlugins(filepath.Join(home, "missing"), home) })
	return LoadPlugins(dir, home)
}

// textPlugin reads a format no field mapping could: lines of "ms|role|payload".
const textPlugin = `
var limits = { input: 100, output: 10 };
knack.registerAgent({
	id: "text-agent",
	name: "Text Agent",
	list: function () {
		return knack.walk("~/.text-agent").filter(function (f) { return f.name.endsWith(".log") });
	},
	read: function (session) {
		return knack.readText(session.path).trim().split("\n").map(function (line) {
			var parts = line.split("|");
			var r = { time: Number(parts[0]), role: parts[1], id: parts[0] };
			if (parts[1] === "user") r.text = parts[2];
			if (parts[1] === "assistant") {
				r.text = parts[2];
				r.model = "m1";
				r.usage = limits;
				if (parts[3]) r.toolCalls = [{ id: parts[3], name: "shell", input: { cmd: "ls" } }];
			}
			if (parts[1] === "tool") r.toolResults = [{ id: parts[2], output: parts[3], error: parts[4] === "fail" }];
			return r;
		});
	},
});`

const textLog = "1793491200000|user|列出文件\n" +
	"1793491203000|assistant||c1\n" +
	"1793491205000|tool|c1|README.md\n" +
	"1793491208000|assistant|完成\n"

func TestPluginReadsAnyFormat(t *testing.T) {
	home := t.TempDir()
	if problems := usePlugins(t, home, map[string]string{"text.js": textPlugin}); len(problems) != 0 {
		t.Fatal(problems)
	}
	if info := LookupAgent("text-agent"); info.Name != "Text Agent" || info.Unsupported != "" {
		t.Fatalf("registered info %+v", info)
	}
	log := filepath.Join(home, ".text-agent/2026/chat-1.log")
	put(t, log, []byte(textLog))
	cache := filepath.Join(home, "cache.json")
	r := reader(t, home, cache)
	snap := refresh(t, r)
	if len(snap.Sessions) != 1 || len(snap.Warnings) != 0 {
		t.Fatalf("snapshot %+v warnings %q", snap.Sessions, snap.Warnings)
	}
	s := snap.Sessions[0]
	if s.Agent != "text-agent" || s.ID != "chat-1.log" || s.Incomplete || len(s.Turns) != 1 || len(s.Turns[0].Requests) != 2 || len(s.Turns[0].Tools) != 1 {
		t.Fatalf("session %+v", s)
	}
	if tool := s.Turns[0].Tools[0]; tool.Name != "shell" || tool.Elapsed.Value != 2*time.Second {
		t.Fatalf("tool %+v", tool)
	}
	if sum := Aggregate(snap, Filter{}); sum.Tokens != (Tokens{Input: 200, Output: 20}) {
		t.Fatalf("tokens %+v", sum.Tokens)
	}

	tr, err := ReadTranscript(context.Background(), log, "text-agent")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range tr.Entries {
		kinds = append(kinds, string(e.Kind))
		if e.Kind == EntryTool && (e.Text != "README.md" || !strings.Contains(e.Input, `"cmd": "ls"`)) {
			t.Fatalf("tool entry %+v", e)
		}
	}
	if got := strings.Join(kinds, ","); got != "user,tool,assistant" {
		t.Fatalf("replay kinds %s", got)
	}

	t.Run("a new worker can still replay", func(t *testing.T) {
		formatOf("text-agent").(pluginFormat).p.close()
		if tr, err := ReadTranscript(context.Background(), log, "text-agent"); err != nil || len(tr.Entries) != 3 {
			t.Fatalf("transcript %+v %v", tr, err)
		}
	})
	t.Run("an unchanged session is not read again, even by a new reader", func(t *testing.T) {
		if again := refresh(t, reader(t, home, cache)); again.ReadBytes != 0 || len(again.Sessions) != 1 {
			t.Fatalf("read %d bytes, sessions %d", again.ReadBytes, len(again.Sessions))
		}
	})
	t.Run("a changed log is rebuilt, not appended to", func(t *testing.T) {
		put(t, log, []byte(textLog+"1793491300000|user|谢谢\n"))
		s := refresh(t, r).Sessions[0]
		if len(s.Turns) != 2 || len(s.Turns[0].Requests) != 2 {
			t.Fatalf("turns %+v", s.Turns)
		}
	})
	t.Run("an edited script rebuilds its statistics", func(t *testing.T) {
		usePlugins(t, home, map[string]string{"text.js": strings.Replace(textPlugin, "input: 100", "input: 1", 1)})
		if sum := Aggregate(refresh(t, r), Filter{}); sum.Tokens.Input != 2 {
			t.Fatalf("not rebuilt %+v", sum.Tokens)
		}
	})
}

func TestPluginSessionsInOneDatabase(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".db-agent/state.db")
	put(t, path, nil)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`create table messages (chat text, at integer, role text, body text)`,
		`insert into messages values ('a', 1793491200000, 'user', '你好'), ('a', 1793491201000, 'assistant', '嗨'), ('b', 1793491300000, 'user', '第二个；带分号')`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	problems := usePlugins(t, home, map[string]string{"db.js": `
	var file = "~/.db-agent/state.db";
	knack.registerAgent({
		id: "db-agent", name: "DB Agent",
		list: function () {
			return knack.query(file, "SELECT chat, max(at) AS last FROM messages GROUP BY chat;").map(function (c) {
				return { path: file, id: c.chat, version: c.last };
			});
		},
		read: function (s) {
			return knack.query(file, "SELECT at, role, body FROM messages WHERE chat = ? ORDER BY at", s.id).map(function (m) {
				return { time: m.at, role: m.role, text: m.body };
			});
		},
	});`})
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	snap := refresh(t, reader(t, home, ""))
	if len(snap.Sessions) != 2 || len(snap.Warnings) != 0 {
		t.Fatalf("sessions %+v warnings %q", snap.Sessions, snap.Warnings)
	}
	a := snap.Sessions[0]
	if a.ID != "a" || LogPath(a.Path) != path || len(a.Turns) != 1 {
		t.Fatalf("session %+v", a)
	}
	tr, err := ReadTranscript(context.Background(), snap.Sessions[1].Path, "db-agent")
	if err != nil || len(tr.Entries) != 1 || tr.Entries[0].Text != "第二个；带分号" {
		t.Fatalf("transcript %+v %v", tr, err)
	}
}

// probe runs body inside list, loaded with the host methods, and returns what
// the refresh warned about. Its sessions are never read.
func probe(t *testing.T, home, body string) string {
	t.Helper()
	problems := usePlugins(t, home, map[string]string{"probe.js": `knack.registerAgent({id: "probe", name: "Probe",
		list: function () { ` + body + `; return [] }, read: function () { return [] }})`})
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	return strings.Join(refresh(t, reader(t, home, "")).Warnings, "\n")
}

func TestPluginBoundaries(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "x.db")
	put(t, path, nil)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`create table t (v integer); insert into t values (1)`); err != nil {
		t.Fatal(err)
	}
	t.Run("nothing but knack reaches the machine", func(t *testing.T) {
		for _, name := range []string{"require", "process", "fetch", "XMLHttpRequest"} {
			if w := probe(t, home, `if (typeof `+name+` !== "undefined") throw new Error("exposed `+name+`")`); w != "" {
				t.Fatal(w)
			}
		}
		if w := probe(t, home, `var names = Object.keys(knack).sort().join(); if (names !== "home,query,readText,registerAgent,stat,walk") throw new Error(names)`); w != "" {
			t.Fatal(w)
		}
	})
	t.Run("SQL cannot write", func(t *testing.T) {
		for _, statement := range []string{
			"DELETE FROM t",
			"VACUUM INTO '" + filepath.Join(home, "copy.db") + "'",
			"ATTACH '" + filepath.Join(home, "new.db") + "' AS n",
			"SELECT 1; DELETE FROM t",
			"WITH c AS (SELECT 1) DELETE FROM t",
		} {
			src := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(statement)
			if w := probe(t, home, `knack.query("~/x.db", "`+src+`")`); w == "" {
				t.Fatalf("%q was allowed", statement)
			}
		}
		var n int
		if err := db.QueryRow(`select count(*) from t`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rows %d %v", n, err)
		}
		for _, name := range []string{"copy.db", "new.db"} {
			if w := probe(t, home, `if (knack.stat("~/`+name+`")) throw new Error("created")`); w != "" {
				t.Fatal(w)
			}
		}
	})
	t.Run("paths must be absolute", func(t *testing.T) {
		if w := probe(t, home, `knack.readText("x.db")`); !strings.Contains(w, "绝对路径") {
			t.Fatalf("warnings %q", w)
		}
		if w := probe(t, home, `if (knack.stat("~/nothing") !== null) throw new Error("found"); if (knack.walk("~/nothing").length) throw new Error("listed")`); w != "" {
			t.Fatal(w)
		}
	})
	t.Run("runaway recursion fails in the script", func(t *testing.T) {
		if w := probe(t, home, `function f() { [1].map(f) }; f()`); w == "" {
			t.Fatal("recursion went unnoticed")
		}
	})
}

func TestPluginFailures(t *testing.T) {
	slowLimits(t)
	home := t.TempDir()
	valid := func(id string) string {
		return `knack.registerAgent({id: "` + id + `", name: "` + id + `", list: function () { return [] }, read: function () { return [] }})`
	}
	problems := usePlugins(t, home, map[string]string{
		"a-syntax.js":   "function (",
		"b-throws.js":   `throw new Error("boom")`,
		"c-silent.js":   `var x = 1`,
		"d-loop.js":     `while (true) {}`,
		"e-noread.js":   `knack.registerAgent({id: "noread", name: "No Read", list: function () { return [] }})`,
		"f-builtin.js":  valid("claude"),
		"g-twice.js":    valid("twice") + ";" + valid("twice2"),
		"h-good.js":     valid("good"),
		"i-dup.js":      valid("good"),
		"not-script.md": valid("ignored"),
	})
	if len(problems) != 8 {
		t.Fatalf("problems %q", problems)
	}
	for _, want := range []string{"a-syntax.js", "boom", "没有调用", "超过", "list 和 read", "重复", "只能注册一个"} {
		if !strings.Contains(strings.Join(problems, "\n"), want) {
			t.Fatalf("missing %q in %q", want, problems)
		}
	}
	if LookupAgent("good").Unsupported != "" || LookupAgent("ignored").Unsupported == "" {
		t.Fatal("only valid plugins load")
	}
	if !strings.Contains(strings.Join(refresh(t, reader(t, home, "")).Warnings, "\n"), "plugins/a-syntax.js") {
		t.Fatal("problems must be in warnings")
	}

	t.Run("a failed read keeps the last statistics", func(t *testing.T) {
		src := `knack.registerAgent({id: "flaky", name: "Flaky",
			list: function () { return [{path: "~/log", version: knack.readText("~/version")}] },
			read: function () { if (knack.readText("~/version") === "2") throw new Error("bad read"); return [{time: 1793491200000, role: "user", text: "hi"}] }})`
		if problems := usePlugins(t, home, map[string]string{"z.js": src}); len(problems) != 8 {
			t.Fatalf("problems %q", problems)
		}
		put(t, filepath.Join(home, "version"), []byte("1"))
		put(t, filepath.Join(home, "log"), nil)
		r := reader(t, home, "")
		if s := refresh(t, r).Sessions; len(s) != 1 || len(s[0].Turns) != 1 {
			t.Fatalf("sessions %+v", s)
		}
		put(t, filepath.Join(home, "version"), []byte("2"))
		snap := refresh(t, r)
		if len(snap.Sessions) != 1 || len(snap.Sessions[0].Turns) != 1 || !snap.Sessions[0].Incomplete || !strings.Contains(strings.Join(snap.Warnings, "\n"), "bad read") {
			t.Fatalf("sessions %+v warnings %q", snap.Sessions, snap.Warnings)
		}
	})

	t.Run("repeated timeouts disable a plugin", func(t *testing.T) {
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "stuck", name: "Stuck",
			list: function () { while (true) {} }, read: function () { return [] }})`})
		r := reader(t, home, "")
		for range pluginStopAfter {
			if w := strings.Join(refresh(t, r).Warnings, "\n"); !strings.Contains(w, "执行超过") {
				t.Fatalf("warnings %q", w)
			}
		}
		start := time.Now()
		if w := strings.Join(refresh(t, r).Warnings, "\n"); !strings.Contains(w, "已停用") {
			t.Fatalf("warnings %q", w)
		}
		if elapsed := time.Since(start); elapsed > pluginCallLimit {
			t.Fatalf("disabled plugin still ran for %s", elapsed)
		}
	})

	t.Run("native work that ignores interrupts cannot hang a scan", func(t *testing.T) {
		// A backreference forces the backtracking engine, which never checks.
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "regex", name: "Regex",
			list: function () { return [/^(a+)+\1b$/.test("a".repeat(34))] }, read: function () { return [] }})`})
		if w := strings.Join(refresh(t, reader(t, home, "")).Warnings, "\n"); !strings.Contains(w, "执行超过") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a timeout does not stop the next call", func(t *testing.T) {
		put(t, filepath.Join(home, "hang"), nil)
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "once", name: "Once",
			list: function () { if (knack.stat("~/hang")) while (true) {} return [] }, read: function () { return [] }})`})
		r := reader(t, home, "")
		if w := strings.Join(refresh(t, r).Warnings, "\n"); !strings.Contains(w, "执行超过") {
			t.Fatalf("warnings %q", w)
		}
		if err := os.Remove(filepath.Join(home, "hang")); err != nil {
			t.Fatal(err)
		}
		if w := strings.Join(refresh(t, r).Warnings, "\n"); strings.Contains(w, "once") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("invalid records are counted", func(t *testing.T) {
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "partly", name: "Partly",
			list: function () { return [{path: "~/log"}, {path: "relative"}, {path: "~/missing"}, 5] },
			read: function () { return [{time: 1793491200000, role: "user", text: "hi"}, 7, {nothing: true}] }})`})
		snap := refresh(t, reader(t, home, ""))
		w := strings.Join(snap.Warnings, "\n")
		if !strings.Contains(w, "跳过 2 条无效记录") || !strings.Contains(w, "跳过 3 个") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a log of another Agent is not counted twice", func(t *testing.T) {
		claude := filepath.Join(home, ".claude/projects/p/one.jsonl")
		put(t, claude, fixture(t, "claude.jsonl"))
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "thief", name: "Thief",
			list: function () { return [{path: "~/.claude/projects/p/one.jsonl"}] }, read: function () { return [] }})`})
		snap := refresh(t, reader(t, home, ""))
		for _, s := range snap.Sessions {
			if s.Agent == "thief" {
				t.Fatalf("stolen %+v", s)
			}
		}
		if !strings.Contains(strings.Join(snap.Warnings, "\n"), "已属于其他 Agent") {
			t.Fatalf("warnings %q", snap.Warnings)
		}
	})
}

func TestSqliteURI(t *testing.T) {
	for path, want := range map[string]string{
		"/a/b.db":    "file:///a/b.db?",
		"C:/a/b.db":  "file:///C:/a/b.db?",
		"/a/b#c?.db": "file:///a/b%23c%3F.db?",
	} {
		if got := sqliteURI(path); !strings.HasPrefix(got, want) {
			t.Fatalf("%s: %s", path, got)
		}
	}
}

// slowLimits makes plugin timeouts quick for one test.
func slowLimits(t *testing.T) {
	t.Helper()
	load, call := pluginLoadLimit, pluginCallLimit
	pluginLoadLimit, pluginCallLimit = 2*time.Second, 300*time.Millisecond
	t.Cleanup(func() { pluginLoadLimit, pluginCallLimit = load, call })
}

func TestPluginReviewedFailures(t *testing.T) {
	warnings := func(s *Snapshot) string { return strings.Join(s.Warnings, "\n") }
	script := func(list, read string) map[string]string {
		return map[string]string{"z.js": `var n = 0; knack.registerAgent({id: "z", name: "Z",
			list: function () { ` + list + ` }, read: function (s) { ` + read + ` }})`}
	}
	record := `return [{time: 1793491200000, role: "user", text: "hi"}]`

	t.Run("compiling is inside the load limit", func(t *testing.T) {
		slowLimits(t)
		home := t.TempDir()
		start := time.Now()
		// A long expression used to stall the previous engine. It has to finish
		// inside the load limit; the script is then refused for not registering.
		problems := usePlugins(t, home, map[string]string{"z.js": "var x = " + strings.Repeat("1+", 40000) + "1"})
		if len(problems) != 1 || !strings.Contains(problems[0], "没有调用 knack.registerAgent") {
			t.Fatalf("problems %q", problems)
		}
		if elapsed := time.Since(start); elapsed > pluginLoadLimit {
			t.Fatalf("load blocked for %s", elapsed)
		}
	})

	t.Run("a timeout inside a host method counts", func(t *testing.T) {
		slowLimits(t)
		home := t.TempDir()
		path := filepath.Join(home, "x.db")
		put(t, path, nil)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`create table t (v integer)`); err != nil {
			t.Fatal(err)
		}
		usePlugins(t, home, script(`knack.query("~/x.db", "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c"); return []`, `return []`))
		r := reader(t, home, "")
		for range pluginStopAfter {
			if w := warnings(refresh(t, r)); !strings.Contains(w, "执行超过") {
				t.Fatalf("warnings %q", w)
			}
		}
		if w := warnings(refresh(t, r)); !strings.Contains(w, "已停用") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a failing read is not run again until its data changes", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		put(t, filepath.Join(home, "version"), []byte("1"))
		usePlugins(t, home, script(`return [{path: "~/log", version: knack.readText("~/version")}]`, `throw new Error("attempt " + (++n))`))
		r := reader(t, home, "")
		for range 2 {
			if w := warnings(refresh(t, r)); !strings.Contains(w, "attempt 1") {
				t.Fatalf("warnings %q", w)
			}
		}
		put(t, filepath.Join(home, "version"), []byte("2"))
		if w := warnings(refresh(t, r)); !strings.Contains(w, "attempt 2") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a failed list keeps its sessions", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, script(`if (n++) throw new Error("list failed"); return [{path: "~/log"}]`, record))
		r := reader(t, home, "")
		if s := refresh(t, r).Sessions; len(s) != 1 {
			t.Fatalf("sessions %+v", s)
		}
		snap := refresh(t, r)
		if len(snap.Sessions) != 1 || len(snap.Sessions[0].Turns) != 1 || !strings.Contains(warnings(snap), "list failed") {
			t.Fatalf("sessions %+v warnings %q", snap.Sessions, snap.Warnings)
		}
	})

	t.Run("an ID does not claim another Agent's log", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, ".claude/projects/p/one.jsonl"), fixture(t, "claude.jsonl"))
		usePlugins(t, home, script(`return [{path: "~/.claude/projects/p/one.jsonl", id: "x"}]`, record))
		snap := refresh(t, reader(t, home, ""))
		for _, s := range snap.Sessions {
			if s.Agent == "z" {
				t.Fatalf("claimed %+v", s)
			}
		}
		if !strings.Contains(warnings(snap), "已属于其他 Agent") {
			t.Fatalf("warnings %q", snap.Warnings)
		}
	})

	t.Run("sessions over the budget are never blank", func(t *testing.T) {
		saved := maxRefreshRead
		maxRefreshRead = 1
		t.Cleanup(func() { maxRefreshRead = saved })
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, script(`return [{path: "~/log", id: "a"}, {path: "~/log", id: "b"}, {path: "~/log", id: "c"}]`, record))
		snap := refresh(t, reader(t, home, ""))
		if len(snap.Sessions) != 3 || !snap.More {
			t.Fatalf("sessions %+v more %v", snap.Sessions, snap.More)
		}
		for _, s := range snap.Sessions {
			if s.Agent != "z" || s.ID == "" || s.Path == "" {
				t.Fatalf("blank session %+v", s)
			}
		}
	})

	t.Run("slow reads spread over refreshes", func(t *testing.T) {
		saved := pluginRefreshLimit
		pluginRefreshLimit = 5 * time.Millisecond
		t.Cleanup(func() { pluginRefreshLimit = saved })
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, script(`return [{path: "~/log", id: "a"}, {path: "~/log", id: "b"}]`, `const until = Date.now() + 10; while (Date.now() < until) {} n++; `+record))
		r := reader(t, home, "")
		snap := refresh(t, r)
		turns := func(s *Snapshot) int {
			total := 0
			for _, x := range s.Sessions {
				total += len(x.Turns)
			}
			return total
		}
		if turns(snap) != 1 || !snap.More || snap.ReadBytes == 0 {
			t.Fatalf("first refresh: turns %d more %v read %d", turns(snap), snap.More, snap.ReadBytes)
		}
		if snap = refresh(t, r); turns(snap) != 2 || snap.More {
			t.Fatalf("second refresh: turns %d more %v", turns(snap), snap.More)
		}
	})
}

func TestPluginWorkerIsolation(t *testing.T) {
	warnings := func(s *Snapshot) string { return strings.Join(s.Warnings, "\n") }

	t.Run("a dynamic compile error stays in the plugin", func(t *testing.T) {
		home := t.TempDir()
		crash := filepath.Join(home, "crash")
		put(t, crash, nil)
		// Longer than the engine's run-time source cap. It throws in the
		// script; the app and the worker keep running.
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { if (knack.stat("~/crash")) new Function("return " + "1+".repeat(3000000) + "1"); return [] },
			read: function () { return [] }})`})
		r := reader(t, home, "")
		if w := warnings(refresh(t, r)); !strings.Contains(w, "MaxDynamicSource") {
			t.Fatalf("warnings %q", w)
		}
		if err := os.Remove(crash); err != nil {
			t.Fatal(err)
		}
		if w := warnings(refresh(t, r)); w != "" {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a script over the memory limit is ended", func(t *testing.T) {
		skipUnderRace(t)
		saved := pluginMemoryLimit
		pluginMemoryLimit = 64 << 20
		t.Cleanup(func() { pluginMemoryLimit = saved })
		home := t.TempDir()
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { var a = []; while (true) a.push("x".repeat(1 << 20)) },
			read: function () { return [] }})`})
		if w := warnings(refresh(t, reader(t, home, ""))); !strings.Contains(w, "内存超过 64 MiB") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a script keeps its state in a healthy worker", func(t *testing.T) {
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, map[string]string{"z.js": `var n = 0; knack.registerAgent({id: "z", name: "Z",
			list: function () { n++; return [{path: "~/log", version: n}] }, read: function () { throw new Error("n=" + n) }})`})
		r := reader(t, home, "")
		refresh(t, r)
		if w := warnings(refresh(t, r)); !strings.Contains(w, "n=2") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("garbage does not count against a script", func(t *testing.T) {
		skipUnderRace(t)
		saved := pluginMemoryLimit
		pluginMemoryLimit = 64 << 20
		t.Cleanup(func() { pluginMemoryLimit = saved })
		home := t.TempDir()
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { for (var i = 0; i < 12; i++) { var s = "x".repeat(4 << 20); s = null } return [] },
			read: function () { return [] }})`})
		if w := warnings(refresh(t, reader(t, home, ""))); w != "" {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("one SQLite value is limited", func(t *testing.T) {
		home := t.TempDir()
		path := filepath.Join(home, "x.db")
		put(t, path, nil)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if _, err := db.Exec(`create table t (v integer)`); err != nil {
			t.Fatal(err)
		}
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { knack.query("~/x.db", "SELECT length(printf('%900000000c', 'x'))"); return [] },
			read: function () { return [] }})`})
		if w := warnings(refresh(t, reader(t, home, ""))); !strings.Contains(w, "too big") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("an oversized answer is refused by the worker", func(t *testing.T) {
		skipUnderRace(t)
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { return [{path: "~/log"}] },
			read: function () { var a = []; for (var i = 0; i < 130; i++) a.push({time: 1793491200000, role: "user", text: "x".repeat(1 << 20)}); return a }})`})
		if w := warnings(refresh(t, reader(t, home, ""))); !strings.Contains(w, "结果超出 128 MiB") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a read that always hangs is disabled despite a healthy list", func(t *testing.T) {
		slowLimits(t)
		saved := pluginRefreshLimit
		pluginRefreshLimit = time.Nanosecond
		t.Cleanup(func() { pluginRefreshLimit = saved })
		home := t.TempDir()
		put(t, filepath.Join(home, "log"), nil)
		usePlugins(t, home, map[string]string{"z.js": `knack.registerAgent({id: "z", name: "Z",
			list: function () { return [{path: "~/log", id: "a"}, {path: "~/log", id: "b"}, {path: "~/log", id: "c"}, {path: "~/log", id: "d"}] },
			read: function () { while (true) {} }})`})
		r := reader(t, home, "")
		for range pluginStopAfter {
			if w := warnings(refresh(t, r)); !strings.Contains(w, "执行超过") {
				t.Fatalf("warnings %q", w)
			}
		}
		if w := warnings(refresh(t, r)); !strings.Contains(w, "已停用") {
			t.Fatalf("warnings %q", w)
		}
	})

	t.Run("a worker a kill cannot end is abandoned", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("needs a shell")
		}
		saved := pluginKillGrace
		pluginKillGrace = 100 * time.Millisecond
		t.Cleanup(func() { pluginKillGrace = saved })
		// The killed shell's child keeps the pipe open, as a process stuck in
		// the kernel would.
		w, err := startPluginProcess(exec.Command("sh", "-c", "sleep 5 & wait"))
		if err != nil {
			t.Fatal(err)
		}
		p := &plugin{worker: w, failures: map[string]int{}}
		start := time.Now()
		_, err = p.exchange(context.Background(), 200*time.Millisecond, pluginRequest{Op: "list"})
		if err == nil || !strings.Contains(err.Error(), "执行超过") || p.worker != nil {
			t.Fatalf("err %v worker %v", err, p.worker)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("waited %s", elapsed)
		}
	})

	t.Run("a worker never outlives the app", func(t *testing.T) {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, pluginWorkerFlag)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		send := func(r pluginRequest) error {
			b, err := encodeMessage(r)
			if err == nil {
				_, err = stdin.Write(b)
			}
			return err
		}
		src := `knack.registerAgent({id: "z", name: "Z", list: function () { while (true) {} }, read: function () { return [] }})`
		var resp pluginResponse
		if err := send(pluginRequest{Op: "load", File: "z.js", Home: t.TempDir(), Source: src, Memory: 1 << 30}); err != nil {
			t.Fatal(err)
		}
		if err := readMessage(stdout, &resp); err != nil || resp.Error != "" || resp.ID != "z" {
			t.Fatalf("load %+v %v", resp, err)
		}
		if err := send(pluginRequest{Op: "list"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond) // the script is now spinning
		stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the worker kept running")
		}
	})
}

func TestPluginDocExamplesLoad(t *testing.T) {
	doc, err := os.ReadFile("../../docs/agents.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```js\r?\n(.*?)```").FindAllSubmatch(doc, -1)
	if len(blocks) != 2 {
		t.Fatalf("found %d examples", len(blocks))
	}
	home := t.TempDir()
	scripts := map[string]string{}
	for i, b := range blocks {
		scripts[fmt.Sprintf("doc-%d.js", i)] = string(b[1])
	}
	if problems := usePlugins(t, home, scripts); len(problems) != 0 {
		t.Fatal(problems)
	}
	put(t, filepath.Join(home, ".my-agent/logs/a.log"), []byte("1793491200000|user|你好\n"))
	if s := refresh(t, reader(t, home, "")).Sessions; len(s) != 1 || len(s[0].Turns) != 1 {
		t.Fatalf("sessions %+v", s)
	}
}

func TestPluginConcurrentReplay(t *testing.T) {
	home := t.TempDir()
	usePlugins(t, home, map[string]string{"text.js": textPlugin})
	log := filepath.Join(home, ".text-agent/chat.log")
	put(t, log, []byte(textLog))
	r := reader(t, home, "")
	done := make(chan error, 8)
	for range 8 {
		go func() {
			_, err := ReadTranscript(context.Background(), log, "text-agent")
			done <- err
		}()
	}
	refresh(t, r)
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
