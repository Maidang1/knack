package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A plugin is a JavaScript file that defines one Agent: list names its
// sessions and read turns one into normalized records, which the rules of a
// custom Agent then count. A script may run any JavaScript, but it runs in a
// worker process (see pluginworker.go) and reaches the machine only through the
// knack methods, none of which writes, opens the network or starts a process.
// See docs/agents.md.

// Variables so tests can lower them.
var (
	pluginLoadLimit   = 5 * time.Second // starting a worker and running the script's top level
	pluginCallLimit   = 30 * time.Second
	pluginMemoryLimit = int64(1 << 30)
	pluginKillGrace   = 2 * time.Second // for a killed worker to go away
	// pluginRefreshLimit is how long a plugin's reads may take in one refresh;
	// the rest wait for the next, so a slow script still shows results.
	pluginRefreshLimit = 10 * time.Second
)

const pluginStopAfter = 3 // consecutive timeouts or crashes before a plugin is disabled

// pluginFields names the normalized record a plugin returns.
var pluginFields = Fields{
	Time: "time", Role: "role", Text: "text", ID: "id", Request: "requestId", Model: "model",
	Thinking: "thinking", SessionID: "sessionId", Title: "title", CWD: "cwd", Branch: "branch",
	Usage:       Usage{Input: "usage.input", Output: "usage.output", CacheRead: "usage.cacheRead", CacheWrite: "usage.cacheWrite"},
	ToolCalls:   ToolCalls{Path: "toolCalls", ID: "id", Name: "name", Input: "input"},
	ToolResults: ToolResults{Path: "toolResults", ID: "id", Output: "output", Error: "error"},
}

var pluginRoles = Roles{User: []string{"user"}, Assistant: []string{"assistant"}, Tool: []string{"tool"}}

// plugin is the app's handle on one script. Its worker starts when needed and
// is replaced after a timeout or crash; scans and replays take turns using it.
type plugin struct {
	mu         sync.Mutex
	file, home string
	source     string // kept, so a new worker runs the script the statistics are versioned by
	worker     *pluginProcess
	// Consecutive timeouts or crashes by request kind. Kept apart because one
	// kind succeeding says nothing of the other: a quick list would otherwise
	// forgive a read that always hangs.
	failures map[string]int
	last     []logFile    // the last list that succeeded
	spent    atomic.Int64 // nanoseconds of reads in this refresh
}

type pluginProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
}

func startPluginProcess(cmd *exec.Cmd) (*pluginProcess, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pluginProcess{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// stop ends the worker and returns its exit code.
func (w *pluginProcess) stop() int {
	_ = w.cmd.Process.Kill()
	_ = w.stdin.Close()
	_ = w.cmd.Wait()
	return w.cmd.ProcessState.ExitCode()
}

type pluginFormat struct {
	customFormat
	p *plugin
}

// LoadPlugins registers the Agents defined by the .js files of dir, replacing
// any loaded before. Call it after LoadAgentConfig. A missing directory defines
// none; a broken plugin is skipped and the returned problems are also listed in
// every snapshot's warnings.
func LoadPlugins(dir, home string) []string {
	var plugins []format
	var problems []string
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		problems = append(problems, "plugins：无法读取目录")
	}
	entries = slices.DeleteFunc(entries, func(e os.DirEntry) bool { return e.IsDir() || !strings.HasSuffix(e.Name(), ".js") })
	// This runs while the app starts, so slow scripts wait side by side.
	type result struct {
		p        *plugin
		hash     string
		id, name string
		err      error
	}
	results := make([]result, len(entries))
	var wg sync.WaitGroup
	for i, e := range entries {
		wg.Go(func() {
			r := &results[i]
			r.p, r.hash, r.id, r.name, r.err = loadPlugin(filepath.Join(dir, e.Name()), home)
		})
	}
	wg.Wait()
	taken := slices.Clone(unsupportedAgents)
	for _, f := range formats() {
		if _, ok := f.(pluginFormat); !ok {
			taken = append(taken, f.Info())
		}
	}
	for i, r := range results {
		err := r.err
		if err == nil {
			r.name, err = checkIdentity(r.id, r.name, taken)
		}
		if err != nil {
			if r.p != nil {
				r.p.close()
			}
			problems = append(problems, fmt.Sprintf("plugins/%s：%s", entries[i].Name(), err))
			continue
		}
		cfg := AgentConfig{ID: r.id, Name: r.name, Fields: pluginFields, Roles: pluginRoles}
		f := pluginFormat{customFormat{cfg: cfg, hash: r.hash}, r.p}
		plugins = append(plugins, f)
		taken = append(taken, f.Info())
	}
	registry.Lock()
	previous := registry.plugins
	registry.plugins, registry.pluginWarnings = plugins, problems
	registry.Unlock()
	for _, f := range previous {
		f.(pluginFormat).p.close()
	}
	return problems
}

func loadPlugin(file, home string) (*plugin, string, string, string, error) {
	src, err := readConfig(file)
	if err != nil {
		return nil, "", "", "", err
	}
	p := &plugin{file: filepath.Base(file), home: home, source: string(src), failures: map[string]int{}}
	resp, err := p.start(context.Background())
	if err != nil {
		return nil, "", "", "", err
	}
	// The version follows the script, so editing it rebuilds this Agent's statistics.
	sum := sha256.Sum256(src)
	return p, hex.EncodeToString(sum[:6]), resp.ID, resp.Name, nil
}

func (p *plugin) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.worker != nil {
		p.worker.stop()
		p.worker = nil
	}
}

// start launches a worker and loads the script into it.
func (p *plugin) start(ctx context.Context) (pluginResponse, error) {
	exe, err := os.Executable()
	if err != nil {
		return pluginResponse{}, fmt.Errorf("无法启动插件进程")
	}
	cmd := exec.Command(exe, pluginWorkerFlag)
	cmd.Stderr = os.Stderr // where console.log goes
	if p.worker, err = startPluginProcess(cmd); err != nil {
		return pluginResponse{}, fmt.Errorf("无法启动插件进程")
	}
	resp, err := p.exchange(ctx, pluginLoadLimit, pluginRequest{Op: "load", File: p.file, Home: p.home, Source: p.source, Memory: pluginMemoryLimit})
	if err != nil && p.worker != nil {
		p.worker.stop()
		p.worker = nil
	}
	return resp, err
}

// exchange sends one request under a time limit, enforced by killing the worker.
// A killed worker has failed even if its answer arrives, so on success p.worker
// is always still running. The caller holds p.mu.
func (p *plugin) exchange(ctx context.Context, limit time.Duration, req pluginRequest) (pluginResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	type answer struct {
		resp pluginResponse
		err  error
	}
	w := p.worker
	done := make(chan answer, 1)
	go func() {
		var a answer
		b, err := encodeMessage(req)
		if err == nil {
			_, err = w.stdin.Write(b)
		}
		if err == nil {
			err = readMessage(w.stdout, &a.resp)
		}
		a.err = err
		done <- a
	}()
	var a answer
	select {
	case a = <-done:
		if a.err == nil {
			break
		}
		code := w.stop()
		p.worker = nil
		return a.resp, p.failed(ctx, limit, code, req.Op)
	case <-ctx.Done():
		_ = w.cmd.Process.Kill()
		p.worker = nil
		select {
		case <-done:
			w.stop()
		case <-time.After(pluginKillGrace):
			// Stuck in the kernel, on a dead network mount for example: not even
			// a kill ends it until the kernel lets go, so stop waiting for it.
			go w.stop()
		}
		return pluginResponse{}, p.failed(ctx, limit, 0, req.Op)
	}
	p.failures[req.Op] = 0
	if a.resp.Error != "" {
		return a.resp, errors.New(a.resp.Error)
	}
	return a.resp, nil
}

// failed explains a worker that gave no answer.
func (p *plugin) failed(ctx context.Context, limit time.Duration, code int, op string) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		p.failures[op]++
		return fmt.Errorf("执行超过 %s", limit)
	case ctx.Err() != nil:
		return ctx.Err()
	case code == pluginOutOfMemory:
		p.failures[op]++
		return fmt.Errorf("内存超过 %d MiB", pluginMemoryLimit>>20)
	}
	p.failures[op]++
	return fmt.Errorf("插件进程异常退出（代码 %d）", code)
}

// call sends a request to the worker, starting one if needed.
func (p *plugin) call(ctx context.Context, req pluginRequest) (pluginResponse, error) {
	for _, n := range p.failures {
		if n >= pluginStopAfter {
			return pluginResponse{}, fmt.Errorf("连续超时或崩溃，已停用；修复后重启 Knack")
		}
	}
	if p.worker == nil {
		if resp, err := p.start(ctx); err != nil {
			return resp, err
		}
	}
	return p.exchange(ctx, pluginCallLimit, req)
}

// LogPath returns the file behind a Session.Path.
func LogPath(path string) string {
	file, _, _ := strings.Cut(path, "\x00")
	return file
}

func (p *plugin) sessions(ctx context.Context) ([]logFile, int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	resp, err := p.call(ctx, pluginRequest{Op: "list"})
	// A failed list keeps the sessions it last named, as a failed read keeps
	// its statistics.
	if err != nil {
		return p.last, 0, err
	}
	listed := make([]logFile, len(resp.Files))
	for i, s := range resp.Files {
		listed[i] = logFile{path: s.Key, id: s.ID, version: s.Version, modTime: s.ModTime}
	}
	p.last = listed
	return listed, resp.Skipped, nil
}

// records calls the script's read and returns each record as JSON, with their
// total size.
func (p *plugin) records(ctx context.Context, key string) ([]json.RawMessage, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	resp, err := p.call(ctx, pluginRequest{Op: "read", Session: key})
	if err != nil {
		return nil, 0, err
	}
	if len(resp.Records) == 0 {
		return nil, 0, nil
	}
	var out []json.RawMessage
	if err := json.Unmarshal(resp.Records, &out); err != nil {
		return nil, 0, fmt.Errorf("read 必须返回记录数组")
	}
	return out, int64(len(resp.Records)), nil
}

func discoverPlugin(ctx context.Context, f pluginFormat, files *[]logFile) ([]string, bool) {
	agent := f.Info().ID
	f.p.spent.Store(0)
	listed, skipped, err := f.p.sessions(ctx)
	var warnings []string
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("%s：%s", agent, err))
	}
	// One log must not count under two Agents, whatever ID a plugin gives it.
	others := make(map[string]bool, len(*files))
	for _, file := range *files {
		others[LogPath(file.path)] = true
	}
	own := make(map[string]bool, len(listed))
	partial, shared := err != nil, 0
	for _, file := range listed {
		if others[LogPath(file.path)] || own[file.path] {
			shared++
			continue
		}
		if len(*files) >= maxFiles {
			partial = true
			break
		}
		own[file.path] = true
		file.agent = agent
		*files = append(*files, file)
	}
	if skipped > 0 {
		warnings = append(warnings, fmt.Sprintf("%s：跳过 %d 个没有有效 path 的会话", agent, skipped))
	}
	if shared > 0 {
		warnings = append(warnings, fmt.Sprintf("%s：%d 个会话已属于其他 Agent 或重复列出", agent, shared))
	}
	return warnings, partial
}

// readPlugin rebuilds a session whenever its version changes: only the script
// knows how its data grows, so there is no offset to resume from. A failed read
// keeps the statistics of the last good one.
func readPlugin(ctx context.Context, f pluginFormat, file logFile, st *fileState, budget int64) (int64, error) {
	known := !stale(st, file) && st.State.Session.Path != ""
	if known && st.Done && st.Version == file.version {
		st.Pending = false
		return 0, nil
	}
	begin := func() {
		reset(st, file)
		if file.id != "" {
			st.State.Session.ID = file.id
		}
	}
	if !known {
		begin()
	}
	// A failing script is not run again until its data changes.
	if st.failure != "" && st.failedVersion == file.version {
		st.Pending = true
		return 0, errors.New(st.failure)
	}
	if budget == 0 {
		st.Pending = true
		return 0, nil
	}
	// Time spends the refresh's budget as bytes do, so More continues the rest.
	if time.Duration(f.p.spent.Load()) >= pluginRefreshLimit {
		st.Pending = true
		return budget, nil
	}
	start := time.Now()
	records, size, err := f.p.records(ctx, file.path)
	f.p.spent.Add(int64(time.Since(start)))
	if err != nil {
		st.Pending = true
		if ctx.Err() == nil {
			st.failedVersion, st.failure = file.version, err.Error()
		}
		return 0, err
	}
	begin()
	p := newParser(&st.State)
	for _, r := range records {
		if p.line(r) != nil {
			st.BadLines++
		}
	}
	st.Version, st.Done = file.version, true
	return min(size, budget), nil
}

func readPluginTranscript(ctx context.Context, f pluginFormat, path string) (*Transcript, error) {
	records, _, err := f.p.records(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("插件读取失败：%w", err)
	}
	out := &Transcript{}
	tr := newTranscriptReader(out, f)
	for _, r := range records {
		if !tr.line(r) {
			out.Skipped++
		}
	}
	return out, nil
}
