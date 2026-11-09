package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/klauspost/compress/zstd"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// A plugin's JavaScript runs in a worker: a copy of this program started with
// pluginWorkerFlag, one request and one response at a time as JSON over
// stdin and stdout. The app ends a worker by killing it, so nothing here needs
// a timeout, and whatever a script does to the Go runtime stays in the worker.

const (
	pluginWorkerFlag    = "--knack-plugin-worker"
	pluginOutOfMemory   = 3         // the worker's exit code when over its memory limit
	pluginStackSize     = 10000     // ordinary runaway recursion fails in the script
	pluginGoStack       = 256 << 20 // the memory limit does not see stacks
	maxPluginText       = 64 << 20
	maxPluginRows       = 1 << 20
	maxPluginMessage    = 128 << 20 // decoding one costs the app about three times this
	pluginMemoryCheckMs = 100
)

type pluginRequest struct {
	Op      string `json:"op"` // load, list or read
	File    string `json:"file,omitempty"`
	Home    string `json:"home,omitempty"`
	Source  string `json:"source,omitempty"`
	Memory  int64  `json:"memory,omitempty"`
	Session string `json:"session,omitempty"`
}

type pluginSession struct {
	Key     string `json:"key"`
	ID      string `json:"id,omitempty"`
	Version string `json:"version"`
	ModTime int64  `json:"modTime"`
}

type pluginResponse struct {
	Error   string          `json:"error,omitempty"`
	ID      string          `json:"id,omitempty"`
	Info    *AgentInfo      `json:"info,omitempty"`
	Name    string          `json:"name,omitempty"`
	Files   []pluginSession `json:"files,omitempty"`
	Skipped int             `json:"skipped,omitempty"`
	Records json.RawMessage `json:"records,omitempty"`
}

// ServePluginWorker takes over the process when it was started as a plugin
// worker, and otherwise returns at once. Call it first in main.
func ServePluginWorker() {
	if len(os.Args) != 2 || os.Args[1] != pluginWorkerFlag {
		return
	}
	requests := make(chan pluginRequest)
	// Reading apart from the script means a closed stdin ends the worker even
	// in the middle of a loop, so a worker never outlives the app.
	go func() {
		for {
			var r pluginRequest
			if readMessage(os.Stdin, &r) != nil {
				os.Exit(0)
			}
			requests <- r
		}
	}()
	w := &pluginWorker{items: map[string]goja.Value{}}
	for r := range requests {
		b, err := encodeMessage(w.handle(r))
		if err == nil && len(b)-4 > maxPluginMessage {
			b, err = encodeMessage(pluginResponse{Error: fmt.Sprintf("结果超出 %d MiB", maxPluginMessage>>20)})
		}
		if err != nil {
			os.Exit(1)
		}
		if _, err := os.Stdout.Write(b); err != nil {
			os.Exit(1)
		}
	}
}

// A message is its length in 4 big-endian bytes, then JSON. Knowing the length
// lets the app refuse an oversized answer before allocating for it, and read it
// without a stream decoder's repeated copies.
func encodeMessage(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(make([]byte, 4))
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b, uint32(len(b)-4))
	return b, nil
}

func readMessage(r io.Reader, v any) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n > maxPluginMessage {
		return fmt.Errorf("消息超出上限")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// watchMemory ends the worker over limit. The runtime's own limit only makes
// the collector try harder and cannot keep up with very large allocations, so
// garbage is collected before it counts against a script.
func watchMemory(limit int64) {
	debug.SetMemoryLimit(limit * 3 / 4)
	var m runtime.MemStats
	for range time.Tick(pluginMemoryCheckMs * time.Millisecond) {
		runtime.ReadMemStats(&m)
		if int64(m.HeapAlloc) <= limit {
			continue
		}
		runtime.GC()
		runtime.ReadMemStats(&m)
		if int64(m.HeapAlloc) > limit {
			os.Exit(pluginOutOfMemory)
		}
	}
}

// pluginWorker owns one JavaScript runtime. A runtime is single-threaded, and a
// script can keep state between its calls.
type pluginWorker struct {
	vm                     *goja.Runtime
	home, file             string
	listFn, readFn, jsonFn goja.Callable
	items                  map[string]goja.Value // what list returned, by log key
}

func (w *pluginWorker) handle(r pluginRequest) (resp pluginResponse) {
	// A panic from a script's getter is the script's error, not the worker's.
	defer func() {
		if x := recover(); x != nil {
			resp = pluginResponse{Error: fmt.Sprintf("插件执行失败：%v", x)}
		}
	}()
	var err error
	switch r.Op {
	case "load":
		resp, err = w.load(r)
	case "list":
		resp.Files, resp.Skipped, err = w.listItems()
	case "read":
		resp.Records, err = w.read(r.Session)
	default:
		err = fmt.Errorf("未知请求：%s", r.Op)
	}
	if err != nil {
		return pluginResponse{Error: err.Error()}
	}
	return resp
}

func (w *pluginWorker) load(r pluginRequest) (pluginResponse, error) {
	go watchMemory(r.Memory)
	debug.SetMaxStack(pluginGoStack)
	w.vm, w.home, w.file = goja.New(), r.Home, r.File
	w.vm.SetMaxCallStackSize(pluginStackSize)
	// Taken before the script runs, which may replace the global JSON.
	w.jsonFn, _ = goja.AssertFunction(w.vm.Get("JSON").ToObject(w.vm).Get("stringify"))
	var def *goja.Object
	w.install(&def)
	program, err := goja.Compile(r.File, r.Source, false)
	if err != nil {
		return pluginResponse{}, err
	}
	if _, err := w.vm.RunProgram(program); err != nil {
		return pluginResponse{}, err
	}
	if def == nil {
		return pluginResponse{}, fmt.Errorf("没有调用 knack.registerAgent")
	}
	var ok, okRead bool
	w.listFn, ok = goja.AssertFunction(def.Get("list"))
	w.readFn, okRead = goja.AssertFunction(def.Get("read"))
	if !ok || !okRead {
		return pluginResponse{}, fmt.Errorf("registerAgent 需要 list 和 read 函数")
	}
	resp := pluginResponse{ID: field(def, "id"), Name: field(def, "name")}
	if v := def.Get("info"); v != nil && !goja.IsUndefined(v) && !goja.IsNull(v) {
		raw, err := w.jsonFn(goja.Undefined(), v)
		if err != nil {
			return pluginResponse{}, err
		}
		resp.Info = &AgentInfo{}
		if err := json.Unmarshal([]byte(raw.String()), resp.Info); err != nil {
			return pluginResponse{}, err
		}
	}
	return resp, nil
}

// install defines the only ways a script can reach the machine.
func (w *pluginWorker) install(def **goja.Object) {
	k := w.vm.NewObject()
	set := func(o *goja.Object, name string, v any) {
		if err := o.Set(name, v); err != nil {
			panic(err) // a fresh object accepts any property
		}
	}
	set(k, "home", w.home)
	set(k, "readText", w.readText)
	set(k, "stat", w.stat)
	set(k, "walk", w.walk)
	set(k, "query", w.query)
	set(k, "registerAgent", func(call goja.FunctionCall) goja.Value {
		o, ok := call.Argument(0).(*goja.Object)
		if !ok || *def != nil {
			panic(w.vm.NewTypeError("一个插件需要且只能注册一个 Agent"))
		}
		*def = o
		return goja.Undefined()
	})
	set(w.vm.GlobalObject(), "knack", k)
	console := w.vm.NewObject()
	for _, name := range []string{"log", "info", "warn", "error"} {
		set(console, name, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, a := range call.Arguments {
				parts[i] = a.String()
			}
			fmt.Fprintf(os.Stderr, "[%s] %s\n", w.file, strings.Join(parts, " "))
			return goja.Undefined()
		})
	}
	set(w.vm.GlobalObject(), "console", console)
}

func field(o *goja.Object, name string) string {
	v := o.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

// resolve expands "~/" and refuses relative paths, whose meaning would depend
// on where Knack was started.
func (w *pluginWorker) resolve(name string) (string, error) {
	slashed := filepath.ToSlash(name)
	if slashed == "~" {
		return w.home, nil
	}
	if rest, ok := strings.CutPrefix(slashed, "~/"); ok {
		return filepath.Join(w.home, filepath.FromSlash(rest)), nil
	}
	if !filepath.IsAbs(name) {
		return "", fmt.Errorf("路径需要是绝对路径或以 ~/ 开头：%s", name)
	}
	return filepath.Clean(name), nil
}

func fileValue(path string, info fs.FileInfo) map[string]any {
	return map[string]any{"path": path, "name": info.Name(), "size": info.Size(), "modTime": info.ModTime().UnixMilli(), "isDir": info.IsDir()}
}

// readText returns a regular file's text, decompressing .zst files.
func (w *pluginWorker) readText(name string) (string, error) {
	file, err := w.resolve(name)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("不是可读取的普通文件：%s", file)
	}
	f, err := os.Open(file)
	if err != nil {
		return "", fmt.Errorf("无法打开：%s", file)
	}
	defer f.Close()
	var src io.Reader = f
	if strings.HasSuffix(file, ".zst") {
		dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(64<<20))
		if err != nil {
			return "", fmt.Errorf("无法解压：%s", file)
		}
		defer dec.Close()
		src = dec
	}
	b, err := io.ReadAll(io.LimitReader(src, maxPluginText+1))
	if err != nil {
		return "", fmt.Errorf("读取失败：%s", file)
	}
	if len(b) > maxPluginText {
		return "", fmt.Errorf("超出 64 MiB：%s", file)
	}
	return string(b), nil
}

// stat returns a path's description, or null when nothing is there.
func (w *pluginWorker) stat(name string) (any, error) {
	file, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法访问：%s", file)
	}
	return fileValue(file, info), nil
}

// walk lists the regular files below a directory, without following links.
// A missing directory has none.
func (w *pluginWorker) walk(name string) ([]any, error) {
	root, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	out := []any{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("无法访问：%s", root)
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				out = append(out, fileValue(path, info))
			}
			if len(out) >= maxFiles {
				return fs.SkipAll
			}
		}
		return nil
	})
	return out, err
}

// sqliteURI opens a slash-separated path read-only. A Windows path needs its
// own leading slash, or the drive would be taken for the URI's authority.
func sqliteURI(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}}.Encode()}
	return u.String()
}

// query runs one read-only SQL statement. The connection is read-only, and
// allowing a single SELECT also rules out what escapes that mode: VACUUM INTO,
// and ATTACH followed by writes to the attached file. The database itself is
// never changed, but SQLite must create -shm and -wal files beside a WAL
// database to read it; immutable=1 would avoid them and miss recent writes.
func (w *pluginWorker) query(name, text string, args ...any) ([]any, error) {
	file, err := w.resolve(name)
	if err != nil {
		return nil, err
	}
	statement := strings.TrimRight(strings.TrimSpace(text), "; \t\r\n")
	head := strings.ToUpper(statement[:min(len(statement), 6)])
	if !strings.HasPrefix(head, "SELECT") && !strings.HasPrefix(head, "WITH") || strings.Contains(statement, ";") {
		return nil, fmt.Errorf("只支持单条 SELECT；文本值请用参数传入")
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("不是可读取的数据库：%s", file)
	}
	db, err := sql.Open("sqlite", sqliteURI(filepath.ToSlash(file)))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// SQLite allocates off the Go heap, where watchMemory cannot see, and this
	// build has no heap limit (it needs SQLITE_DEFAULT_MEMSTATUS). Capping each
	// value bounds the common case; a query holding many large values at once
	// is still ended only by the call's time limit.
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, maxPluginText); err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []any{}
	values, targets := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	for rows.Next() {
		if len(out) >= maxPluginRows {
			return nil, fmt.Errorf("结果超出 %d 行", maxPluginRows)
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, c := range columns {
			switch v := values[i].(type) {
			case []byte:
				row[c] = string(v)
			case time.Time:
				row[c] = v.Format(time.RFC3339Nano)
			default:
				row[c] = v
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// logKey identifies a listed session. A file can hold several sessions, so the
// session ID follows the path; a NUL cannot be part of a path on any system.
func logKey(path, id string) string {
	if id == "" {
		return path
	}
	return path + "\x00" + id
}

// listItems calls the script's list.
func (w *pluginWorker) listItems() ([]pluginSession, int, error) {
	v, err := w.listFn(goja.Undefined())
	if err != nil {
		return nil, 0, err
	}
	list, ok := v.(*goja.Object)
	if !ok || list.Get("length") == nil {
		return nil, 0, fmt.Errorf("list 必须返回数组")
	}
	var files []pluginSession
	skipped := 0
	items := map[string]goja.Value{}
	for i := range min(list.Get("length").ToInteger(), maxFiles) {
		item := list.Get(strconv.FormatInt(i, 10))
		o, ok := item.(*goja.Object)
		if !ok {
			skipped++
			continue
		}
		path, err := w.resolve(field(o, "path"))
		if err != nil {
			skipped++
			continue
		}
		// Replay and "show in folder" need the file to exist.
		info, err := os.Stat(path)
		if err != nil {
			skipped++
			continue
		}
		f := pluginSession{ID: field(o, "id"), Version: field(o, "version"), ModTime: info.ModTime().UnixNano()}
		f.Key = logKey(path, f.ID)
		// A session that declares no version changes with its file.
		if f.Version == "" {
			f.Version = fmt.Sprintf("%d:%d", f.ModTime, info.Size())
		}
		files = append(files, f)
		items[f.Key] = item
	}
	w.items = items
	return files, skipped, nil
}

// read calls the script's read and returns its records as a JSON array.
func (w *pluginWorker) read(key string) (json.RawMessage, error) {
	item, ok := w.items[key]
	if !ok { // a new worker, or a cached session replayed before the first scan
		if _, _, err := w.listItems(); err != nil {
			return nil, err
		}
		if item, ok = w.items[key]; !ok {
			return nil, fmt.Errorf("插件没有列出这个会话")
		}
	}
	v, err := w.readFn(goja.Undefined(), item)
	if err != nil {
		return nil, err
	}
	if goja.IsUndefined(v) || goja.IsNull(v) {
		return nil, nil
	}
	text, err := w.jsonFn(goja.Undefined(), v)
	if err != nil {
		return nil, err
	}
	b := []byte(text.String())
	if !json.Valid(b) || b[0] != '[' {
		return nil, fmt.Errorf("read 必须返回记录数组")
	}
	return b, nil
}
