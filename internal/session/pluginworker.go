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
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/klauspost/compress/zstd"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// A plugin's JavaScript runs in a worker: a copy of this program started with
// pluginWorkerFlag, one request and one response at a time as JSON over
// stdin and stdout. The engine is moejs. The app ends a worker by killing it,
// so a host call that never returns (a SQL query, for example) still cannot
// hang Knack, and whatever a script does to the Go runtime stays in the worker.

const (
	pluginWorkerFlag    = "--knack-plugin-worker"
	pluginOutOfMemory   = 3 // the worker's exit code when over its memory limit
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
	// AgentID and AgentName come from plugin.json. A module that exports list
	// and read uses them; a script that calls registerAgent must agree with them.
	AgentID   string `json:"agentId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
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
	w := &pluginWorker{items: map[string]map[string]any{}}
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

// propScript reads one property. typeofScript reports a value's typeof.
// Run once in a runtime, each returns a function.
var (
	propScript   = mustCompileFunction("prop", "obj, key", "return obj == null ? undefined : obj[key];")
	typeofScript = mustCompileFunction("kind", "value", "return typeof value;")
)

func mustCompileFunction(name, params, body string) *moejs.Script {
	s, err := moejs.CompileFunction(name, params, body, moejs.FunctionOptions{})
	if err != nil {
		panic(err)
	}
	return s
}

// pluginWorker owns one JavaScript runtime. A runtime is single-threaded, and a
// script can keep state between its calls.
type pluginWorker struct {
	vm            *moejs.Runtime
	propFn        moejs.Value
	home, file    string
	loaded        pluginRequest
	listFn        moejs.Value
	readFn        moejs.Value
	items         map[string]map[string]any // what list returned, by log key
	registered    moejs.Value
	hasRegistered bool
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
		if err = w.ensure(); err == nil {
			resp.Files, resp.Skipped, err = w.listItems()
		}
	case "read":
		if err = w.ensure(); err == nil {
			resp.Records, err = w.read(r.Session)
		}
	default:
		err = fmt.Errorf("未知请求：%s", r.Op)
	}
	if err != nil {
		return pluginResponse{Error: err.Error()}
	}
	return resp
}

// ensure reloads the script after a memory limit dropped the runtime. The
// caller holds no extra lock: the worker handles one request at a time.
func (w *pluginWorker) ensure() error {
	if w.vm != nil {
		return nil
	}
	if w.loaded.Source == "" {
		return fmt.Errorf("插件还没有加载")
	}
	_, err := w.load(w.loaded)
	return err
}

func (w *pluginWorker) drop() {
	w.vm = nil
	w.propFn = moejs.Undefined()
	w.listFn = moejs.Undefined()
	w.readFn = moejs.Undefined()
	w.registered = moejs.Undefined()
	w.hasRegistered = false
	w.items = map[string]map[string]any{}
}

func (w *pluginWorker) load(r pluginRequest) (pluginResponse, error) {
	go watchMemory(r.Memory)
	w.drop()
	w.loaded = r
	w.home, w.file = r.Home, r.File
	limit := r.Memory
	if limit <= 0 {
		limit = pluginMemoryLimit
	}
	w.vm = moejs.NewRuntime(moejs.Options{MemoryLimit: limit})
	var err error
	if w.propFn, err = w.vm.RunScript(propScript); err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	if err := w.install(); err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	compile := moejs.CompileWith
	if strings.HasSuffix(r.File, ".ts") {
		compile = func(name, source string, opts moejs.CompileOptions) (*moejs.Module, error) {
			opts.TypeScript = true
			return moejs.CompileWith(name, source, opts)
		}
	}
	mod, err := compile(r.File, r.Source, moejs.CompileOptions{})
	if err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	if err := w.vm.Load(mod); err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	resp := pluginResponse{}
	if listFn, readFn, ok, err := w.exportedFuncs(); err != nil {
		w.drop()
		return pluginResponse{}, err
	} else if ok {
		w.listFn, w.readFn = listFn, readFn
		if r.AgentID == "" || r.AgentName == "" {
			w.drop()
			return pluginResponse{}, fmt.Errorf("plugin.json 需要 id 和 name")
		}
		resp.ID, resp.Name = r.AgentID, r.AgentName
		if info, ok := w.vm.Export("info"); ok && !info.IsUndefined() && !info.IsNull() {
			parsed, err := w.infoValue(info)
			if err != nil {
				w.drop()
				return pluginResponse{}, err
			}
			resp.Info = parsed
		}
		return resp, nil
	}
	if !w.hasRegistered {
		w.drop()
		return pluginResponse{}, fmt.Errorf("没有调用 knack.registerAgent")
	}
	if err := w.funcsOf(w.registered); err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	resp.ID, err = w.stringProp(w.registered, "id")
	if err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	resp.Name, err = w.stringProp(w.registered, "name")
	if err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	if r.AgentID != "" && resp.ID != r.AgentID {
		w.drop()
		return pluginResponse{}, fmt.Errorf("脚本注册的 id 与 plugin.json 不一致")
	}
	info, err := w.prop(w.registered, "info")
	if err != nil {
		w.drop()
		return pluginResponse{}, err
	}
	if !info.IsUndefined() && !info.IsNull() {
		resp.Info, err = w.infoValue(info)
		if err != nil {
			w.drop()
			return pluginResponse{}, err
		}
	}
	return resp, nil
}

func (w *pluginWorker) exportedFuncs() (list, read moejs.Value, ok bool, err error) {
	names := map[string]bool{}
	if m := w.vm.Module(); m != nil {
		for _, name := range m.Exports() {
			names[name] = true
		}
	}
	if !names["list"] || !names["read"] {
		return moejs.Undefined(), moejs.Undefined(), false, nil
	}
	list, ok = w.vm.Export("list")
	if !ok {
		return moejs.Undefined(), moejs.Undefined(), false, nil
	}
	read, ok = w.vm.Export("read")
	if !ok {
		return moejs.Undefined(), moejs.Undefined(), false, nil
	}
	if err = w.requireFn(list, "list"); err != nil {
		return moejs.Undefined(), moejs.Undefined(), false, err
	}
	if err = w.requireFn(read, "read"); err != nil {
		return moejs.Undefined(), moejs.Undefined(), false, err
	}
	return list, read, true, nil
}

func (w *pluginWorker) funcsOf(def moejs.Value) error {
	var err error
	if w.listFn, err = w.prop(def, "list"); err != nil {
		return err
	}
	if w.readFn, err = w.prop(def, "read"); err != nil {
		return err
	}
	if err := w.requireFn(w.listFn, "list"); err != nil {
		return fmt.Errorf("registerAgent 需要 list 和 read 函数")
	}
	if err := w.requireFn(w.readFn, "read"); err != nil {
		return fmt.Errorf("registerAgent 需要 list 和 read 函数")
	}
	return nil
}

func (w *pluginWorker) requireFn(fn moejs.Value, name string) error {
	kindFn, err := w.vm.RunScript(typeofScript)
	if err != nil {
		return err
	}
	kind, err := w.vm.CallFunction(kindFn, moejs.Undefined(), fn)
	if err != nil {
		return err
	}
	if kind.String() != "function" {
		return fmt.Errorf("%s 必须是函数", name)
	}
	return nil
}

// install defines the only ways a script can reach the machine.
func (w *pluginWorker) install() error {
	rt := w.vm
	logFn := func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.String()
		}
		fmt.Fprintf(os.Stderr, "[%s] %s\n", w.file, strings.Join(parts, " "))
		return moejs.Undefined(), nil
	}
	native := func(fn func(args []moejs.Value) (any, error)) moejs.NativeFunc {
		return func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			out, err := fn(args)
			if err != nil {
				return moejs.Undefined(), err
			}
			return rt.FromGo(out)
		}
	}
	if err := rt.SetGlobal("console", map[string]any{
		"log": moejs.NativeFunc(logFn), "info": moejs.NativeFunc(logFn),
		"warn": moejs.NativeFunc(logFn), "error": moejs.NativeFunc(logFn),
	}); err != nil {
		return err
	}
	return rt.SetGlobal("knack", map[string]any{
		"home": w.home,
		"readText": native(func(args []moejs.Value) (any, error) {
			text, err := w.readText(moejs.Arg(args, 0).String())
			return text, err
		}),
		"stat": native(func(args []moejs.Value) (any, error) {
			return w.stat(moejs.Arg(args, 0).String())
		}),
		"walk": native(func(args []moejs.Value) (any, error) {
			files, err := w.walk(moejs.Arg(args, 0).String())
			return files, err
		}),
		"query": native(func(args []moejs.Value) (any, error) {
			params := make([]any, 0, len(args))
			for i := 2; i < len(args); i++ {
				v, err := rt.ToGo(args[i])
				if err != nil {
					return nil, err
				}
				params = append(params, v)
			}
			rows, err := w.query(moejs.Arg(args, 0).String(), moejs.Arg(args, 1).String(), params...)
			return rows, err
		}),
		"registerAgent": moejs.NativeFunc(func(_ *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			v := moejs.Arg(args, 0)
			if !v.IsObject() || w.hasRegistered {
				return moejs.Undefined(), errors.New("一个插件需要且只能注册一个 Agent")
			}
			w.registered = v
			w.hasRegistered = true
			return moejs.Undefined(), nil
		}),
	})
}

func (w *pluginWorker) prop(obj moejs.Value, name string) (moejs.Value, error) {
	return w.vm.CallFunction(w.propFn, moejs.Undefined(), obj, moejs.String(name))
}

func (w *pluginWorker) stringProp(obj moejs.Value, name string) (string, error) {
	v, err := w.prop(obj, name)
	if err != nil || v.IsUndefined() || v.IsNull() {
		return "", err
	}
	return v.String(), nil
}

func (w *pluginWorker) infoValue(v moejs.Value) (*AgentInfo, error) {
	raw, err := w.vm.AppendJSON(nil, v)
	if err != nil {
		return nil, err
	}
	info := &AgentInfo{}
	if err := json.Unmarshal(raw, info); err != nil {
		return nil, err
	}
	return info, nil
}

func (w *pluginWorker) callFn(fn moejs.Value, arg *moejs.Value) (moejs.Value, error) {
	if arg == nil {
		return w.vm.CallFunction(fn, moejs.Undefined())
	}
	return w.vm.CallFunction(fn, moejs.Undefined(), *arg)
}

func jsField(m map[string]any, name string) string {
	return jsString(m[name])
}

func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if !math.IsInf(x, 0) && !math.IsNaN(x) && x == math.Trunc(x) && x >= math.MinInt64 && x <= math.MaxInt64 {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
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
	v, err := w.callFn(w.listFn, nil)
	if err != nil {
		return nil, 0, w.forget(err)
	}
	got, err := w.vm.ToGo(v)
	if err != nil {
		return nil, 0, err
	}
	list, ok := got.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("list 必须返回数组")
	}
	var files []pluginSession
	skipped := 0
	items := map[string]map[string]any{}
	for _, item := range list[:min(len(list), maxFiles)] {
		o, ok := item.(map[string]any)
		if !ok {
			skipped++
			continue
		}
		path, err := w.resolve(jsField(o, "path"))
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
		f := pluginSession{ID: jsField(o, "id"), Version: jsField(o, "version"), ModTime: info.ModTime().UnixNano()}
		f.Key = logKey(path, f.ID)
		// A session that declares no version changes with its file.
		if f.Version == "" {
			f.Version = fmt.Sprintf("%d:%d", f.ModTime, info.Size())
		}
		files = append(files, f)
		items[f.Key] = o
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
	arg, err := w.vm.FromGo(item)
	if err != nil {
		return nil, err
	}
	v, err := w.callFn(w.readFn, &arg)
	if err != nil {
		return nil, w.forget(err)
	}
	if v.IsUndefined() || v.IsNull() {
		return nil, nil
	}
	b, err := w.vm.AppendJSON(nil, v)
	if err != nil {
		return nil, w.forget(err)
	}
	if !json.Valid(b) || len(b) == 0 || b[0] != '[' {
		return nil, fmt.Errorf("read 必须返回记录数组")
	}
	return b, nil
}

// scriptError is the text a plugin threw, or a memory-limit failure that
// drops the runtime so the next request starts a fresh one.
func (w *pluginWorker) forget(err error) error {
	if errors.Is(err, moejs.ErrMemoryLimit) {
		limit := w.loaded.Memory
		if limit <= 0 {
			limit = pluginMemoryLimit
		}
		w.drop()
		return fmt.Errorf("内存超过 %d MiB", limit>>20)
	}
	return scriptError(err)
}

func scriptError(err error) error {
	var ex *moejs.Exception
	if errors.As(err, &ex) {
		if msg := ex.Message(); msg != "" {
			return errors.New(msg)
		}
	}
	return err
}
