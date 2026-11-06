package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

const (
	maxFiles       = 20000
	maxDepth       = 3 // directory levels below a log root
	maxLineBytes   = 4 << 20
	maxFileRead    = 16 << 20
	maxWarnings    = 200
	largeHeadBytes = 4 << 10
)

// maxRefreshRead bounds one refresh so results appear progressively; the app
// refreshes again while Snapshot.More is set. A variable so tests can lower it.
var maxRefreshRead int64 = 64 << 20

// Codex compaction records embed the whole replacement history and can exceed
// maxLineBytes. Their statistics only need the leading timestamp and type, so
// an oversized record of a known metadata-only type is replayed as its header.
var largeHeaderTypes = map[string]bool{"compacted": true}

func largeRecordHeader(head []byte) []byte {
	field := func(name string) string {
		key := []byte(`"` + name + `":"`)
		i := bytes.Index(head, key)
		if i < 0 {
			return ""
		}
		rest := head[i+len(key):]
		j := bytes.IndexByte(rest, '"')
		if j < 0 || bytes.IndexByte(rest[:j], '\\') >= 0 {
			return ""
		}
		return string(rest[:j])
	}
	kind, at := field("type"), field("timestamp")
	if at == "" {
		return nil
	}
	// Messages carrying large attachments (images) keep their role, which is
	// all the statistics read from a message; the replay shows them as skipped.
	if kind == "response_item" && bytes.Contains(head, []byte(`"payload":{"type":"message"`)) {
		if role := field("role"); role != "" {
			return []byte(`{"timestamp":"` + at + `","type":"response_item","payload":{"type":"message","role":"` + role + `"}}`)
		}
	}
	if !largeHeaderTypes[kind] {
		return nil
	}
	return []byte(`{"timestamp":"` + at + `","type":"` + kind + `"}`)
}

type fileState struct {
	Agent                 Agent
	Format                string // format version the statistics were parsed with
	Offset, Size, ModTime int64
	HeadHash, TailHash    string
	State                 parseState
	BadLines, LargeLines  int
	Pending               bool
	Discarding            bool // oversized line continued across bounded refreshes
	Done                  bool // compressed log read to its end
	// LargeHeader is the synthesized metadata-only record (timestamp, type,
	// role) of an oversized record still being discarded; never its content.
	LargeHeader string `json:",omitempty"`
}

// Reader serializes refreshes. Call Refresh in a background worker, and replace
// the UI snapshot atomically when it completes. Returned snapshots are detached.
type Reader struct {
	mu              sync.Mutex
	home, cachePath string
	files           map[string]*fileState
}

func NewReader(home, cachePath string) (*Reader, error) {
	r := &Reader{home: filepath.Clean(home), cachePath: cachePath, files: map[string]*fileState{}}
	if cachePath != "" {
		if err := r.load(); err != nil {
			return r, err
		}
	}
	return r, nil
}

type logFile struct {
	path          string
	agent         Agent
	parent, child string
	modTime       int64
}

func discover(ctx context.Context, home string) ([]logFile, []string, bool) {
	var files []logFile
	var warnings []string
	partial := false
	for _, f := range formats() {
		l := f.layout()
		for _, rel := range l.roots {
			w, p := discoverRoot(ctx, home, f, rel, l.suffixes, &files)
			warnings = append(warnings, w[:min(len(w), max(0, maxWarnings-len(warnings)))]...)
			partial = partial || p
		}
	}
	slices.SortFunc(files, func(a, b logFile) int { return strings.Compare(a.path, b.path) })
	if partial && len(warnings) < maxWarnings {
		warnings = append(warnings, "日志发现未完成或达到文件上限；当前统计不完整")
	}
	return files, warnings, partial
}

func discoverRoot(ctx context.Context, home string, f format, rel string, suffixes []string, files *[]logFile) ([]string, bool) {
	var warnings []string
	partial := false
	agent := f.Info().ID
	root := filepath.Join(home, filepath.FromSlash(rel))
	if info, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil, false
	} else if err != nil {
		return []string{fmt.Sprintf("%s：无法访问日志目录", agent)}, true
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return []string{fmt.Sprintf("%s：日志目录不是普通目录，暂不支持", agent)}, true
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if len(warnings) < maxWarnings {
				warnings = append(warnings, fmt.Sprintf("%s：无法读取部分日志路径", agent))
			}
			partial = true
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() {
			if _, _, ok := f.visit(parts, true); len(parts) > maxDepth || !ok {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !slices.ContainsFunc(suffixes, func(s string) bool { return strings.HasSuffix(d.Name(), s) }) {
			return nil
		}
		parent, child, ok := f.visit(parts, false)
		if !ok {
			return nil
		}
		file := logFile{path: path, agent: agent, parent: parent, child: child}
		if info, err := d.Info(); err == nil {
			file.modTime = info.ModTime().UnixNano()
		}
		if len(*files) >= maxFiles {
			partial = true
			return fs.SkipAll
		}
		*files = append(*files, file)
		return nil
	})
	if err != nil {
		partial = true
		if len(warnings) < maxWarnings {
			warnings = append(warnings, fmt.Sprintf("%s：日志发现未完成", agent))
		}
	}
	return warnings, partial
}

func (r *Reader) Refresh(ctx context.Context) (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	logs, warnings, partial := discover(ctx, r.home)
	out := &Snapshot{At: time.Now(), Warnings: append(configWarnings(), warnings...), Partial: partial}
	active := map[string]bool{}
	budget := maxRefreshRead
	// The read budget goes to the most recently written logs first, across all
	// agents. Path order would let one agent's backlog starve the others.
	order := slices.Clone(logs)
	slices.SortStableFunc(order, func(a, b logFile) int {
		if a.modTime != b.modTime {
			if a.modTime > b.modTime {
				return -1
			}
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	for _, file := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		active[file.path] = true
		st := r.files[file.path]
		if st == nil {
			st = &fileState{}
			r.files[file.path] = st
		}
		read, err := readLog(ctx, file, st, min(int64(maxFileRead), budget))
		budget -= read
		if st.Pending && (budget <= 0 || read == maxFileRead) {
			out.More = true
		}
		out.ReadBytes += read
		if err != nil && len(out.Warnings) < maxWarnings {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s：%s", filepath.Base(file.path), err))
		}
		if st.Pending {
			out.Partial = true
		}
		if st.BadLines+st.LargeLines > 0 && len(out.Warnings) < maxWarnings {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s：跳过 %d 条无效记录、%d 条超出 4 MiB 的记录", filepath.Base(file.path), st.BadLines, st.LargeLines))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// A failed discovery must not invalidate caches for temporarily inaccessible
	// paths. They are nevertheless excluded from this snapshot's current totals.
	if !partial {
		for path := range r.files {
			if !active[path] {
				delete(r.files, path)
			}
		}
	}
	for _, info := range Agents() {
		n, recognized := 0, 0
		for _, f := range logs {
			if f.agent == info.ID {
				n++
				if r.files[f.path].State.FormatKnown {
					recognized++
				}
			}
		}
		s := Source{Agent: info.ID, Files: n, Supported: info.Unsupported == ""}
		if !s.Supported {
			s.Reason = info.Unsupported + "，暂不支持"
		} else if n == 0 {
			s.Reason = "未发现本地会话日志"
		} else if recognized == 0 {
			s.Supported = false
			s.Reason = "没有可识别的会话记录，暂不支持"
		}
		out.Sources = append(out.Sources, s)
	}
	if err := r.snapshot(out, logs); err != nil {
		return nil, err
	}
	if r.cachePath != "" {
		if err := r.save(); err != nil {
			return out, fmt.Errorf("保存会话缓存：%w", err)
		}
	}
	return out, nil
}

func reset(st *fileState, file logFile) {
	*st = fileState{Agent: file.agent, Format: formatOf(file.agent).version(), State: parseState{Session: Session{ID: strings.TrimSuffix(strings.TrimSuffix(filepath.Base(file.path), ".zst"), ".jsonl"), Agent: file.agent, Path: file.path, ParentID: file.parent, AgentID: file.child}}}
}

// stale reports a checkpoint made for another Agent or by other parsing rules.
func stale(st *fileState, file logFile) bool {
	return st.Agent != file.agent || st.Format != formatOf(file.agent).version()
}

func readLog(ctx context.Context, file logFile, st *fileState, budget int64) (int64, error) {
	info, err := os.Lstat(file.path)
	if err != nil {
		st.Pending = true
		return 0, fmt.Errorf("无法读取日志元数据")
	}
	if !info.Mode().IsRegular() {
		st.Pending = true
		return 0, fmt.Errorf("仅支持普通日志文件")
	}
	if strings.HasSuffix(file.path, ".zst") {
		return readCompressed(ctx, file, st, budget, info)
	}
	if stale(st, file) || info.Size() < st.Offset || (info.ModTime().UnixNano() != st.ModTime && info.Size() <= st.Size) {
		reset(st, file)
	}
	if st.State.Session.Path == "" {
		reset(st, file)
	}
	if st.Offset == info.Size() && st.ModTime == info.ModTime().UnixNano() {
		st.Pending = st.Discarding
		return 0, nil
	}
	if budget == 0 {
		st.Pending = true
		return 0, nil
	}
	f, err := os.Open(file.path)
	if err != nil {
		st.Pending = true
		return 0, fmt.Errorf("无法打开日志")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		st.Pending = true
		return 0, fmt.Errorf("日志已替换，请重新扫描")
	}
	if st.Offset > 0 {
		head, tail, err := fingerprints(f, st.Offset)
		if err != nil {
			st.Pending = true
			return 0, err
		}
		if head != st.HeadHash || tail != st.TailHash {
			reset(st, file)
		}
	}
	if _, err = f.Seek(st.Offset, io.SeekStart); err != nil {
		return 0, err
	}
	limit := min(budget, info.Size()-st.Offset)
	read, err := consumeLines(ctx, io.LimitReader(f, limit), st)
	if err != nil {
		return read, err
	}
	st.Size = info.Size()
	st.ModTime = info.ModTime().UnixNano()
	st.Pending = st.Offset < info.Size() || st.Discarding
	st.HeadHash, st.TailHash, err = fingerprints(f, st.Offset)
	if err != nil {
		st.Pending = true
		return read, err
	}
	return read, nil
}

// readCompressed reads a zstd-compressed log. Codex only compresses finished
// rollouts, so the file does not grow: the checkpoint is the decompressed
// offset, and a bounded refresh resumes by skipping what it already parsed.
// Any change of size or time rebuilds the file's statistics.
func readCompressed(ctx context.Context, file logFile, st *fileState, budget int64, info os.FileInfo) (int64, error) {
	if stale(st, file) || st.State.Session.Path == "" || st.Size != info.Size() || st.ModTime != info.ModTime().UnixNano() {
		reset(st, file)
	}
	if st.Done {
		st.Pending = false
		return 0, nil
	}
	if budget == 0 {
		st.Pending = true
		return 0, nil
	}
	f, err := os.Open(file.path)
	if err != nil {
		st.Pending = true
		return 0, fmt.Errorf("无法打开日志")
	}
	defer f.Close()
	dec, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(64<<20))
	if err != nil {
		st.Pending = true
		return 0, fmt.Errorf("无法解压日志")
	}
	defer dec.Close()
	if _, err := io.CopyN(io.Discard, dec, st.Offset); err != nil {
		reset(st, file)
		st.Pending = true
		return 0, fmt.Errorf("解压日志失败")
	}
	limited := &io.LimitedReader{R: dec, N: budget}
	read, err := consumeLines(ctx, limited, st)
	st.Size, st.ModTime = info.Size(), info.ModTime().UnixNano()
	if err != nil {
		return read, fmt.Errorf("解压日志失败")
	}
	// The limit was not reached: the decoder hit the end of the file.
	st.Done = limited.N > 0 && ctx.Err() == nil && !st.Discarding
	st.Pending = !st.Done
	return read, nil
}

// consumeLines parses complete records from r, advancing st.Offset past each
// one. A trailing partial record is left for the next read.
func consumeLines(ctx context.Context, r io.Reader, st *fileState) (int64, error) {
	reader := bufio.NewReaderSize(r, 64<<10)
	p := newParser(&st.State)
	var read, consumed int64
	var buf, head []byte
	large := st.Discarding
	for {
		if err := ctx.Err(); err != nil {
			st.Pending = true
			break
		}
		fragment, readErr := reader.ReadSlice('\n')
		read += int64(len(fragment))
		consumed += int64(len(fragment))
		if len(buf)+len(fragment) <= maxLineBytes && !large {
			buf = append(buf, fragment...)
		} else {
			if !large && !st.Discarding {
				head = append(head[:0], buf[:min(len(buf), largeHeadBytes)]...)
			}
			large = true
			buf = nil
		}
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			if large {
				record := largeRecordHeader(head)
				if record == nil && st.LargeHeader != "" {
					record = []byte(st.LargeHeader)
				}
				if record != nil {
					_ = p.line(record)
				} else {
					st.LargeLines++
				}
				head = head[:0]
				st.LargeHeader = ""
			} else if len(bytes.TrimSpace(buf)) > 0 {
				if err := p.line(buf); err != nil {
					st.BadLines++
				}
			}
			st.Offset += consumed
			st.Discarding = false
			consumed = 0
			buf = buf[:0]
			large = false
		}
		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr != nil {
			if readErr != io.EOF {
				st.Pending = true
				return read, fmt.Errorf("读取日志失败")
			}
			break
		}
	}
	if large && consumed > 0 {
		// A single huge line must not pin the checkpoint forever. Continue
		// discarding it next time without retaining any of its contents.
		if record := largeRecordHeader(head); record != nil {
			st.LargeHeader = string(record)
		}
		st.Offset += consumed
		st.Discarding = true
	}
	return read, nil
}

func fingerprints(f *os.File, offset int64) (string, string, error) {
	hash := func(start, n int64) (string, error) {
		b := make([]byte, n)
		if _, err := f.ReadAt(b, start); err != nil {
			return "", fmt.Errorf("日志检查点失效")
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), nil
	}
	n := min(offset, int64(4096))
	head, err := hash(0, n)
	if err != nil {
		return "", "", err
	}
	tail, err := hash(offset-n, n)
	return head, tail, err
}
