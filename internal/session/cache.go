package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const cacheVersion = 2 // 2: Codex request timing and large compaction headers
const maxCacheBytes = 128 << 20

// Cached returns the last checkpoint for immediate display, without discovering
// or opening any source logs. Refresh replaces it after background validation.
func (r *Reader) Cached() (*Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.files) == 0 {
		return nil, nil
	}
	logs := make([]logFile, 0, len(r.files))
	for path, st := range r.files {
		if formatOf(st.Agent) == nil {
			continue // a user-defined Agent removed from agents.json
		}
		logs = append(logs, logFile{path: path, agent: st.Agent, parent: st.State.Session.ParentID, child: st.State.Session.AgentID})
	}
	slices.SortFunc(logs, func(a, b logFile) int { return strings.Compare(a.path, b.path) })
	out := &Snapshot{}
	for _, st := range r.files {
		if st.Pending {
			out.Partial = true
		}
	}
	if err := r.snapshot(out, logs); err != nil {
		return nil, err
	}
	return out, nil
}

type diskCache struct {
	Version int
	Home    string
	Files   map[string]*fileState
}

func (r *Reader) load() error {
	f, err := os.Open(r.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxCacheBytes {
		return fmt.Errorf("会话缓存超出 128 MiB，请重新扫描")
	}
	var cache diskCache
	if err := json.NewDecoder(io.LimitReader(f, maxCacheBytes)).Decode(&cache); err != nil {
		return err
	}
	if cache.Version != cacheVersion || cache.Home != r.home {
		return nil
	}
	if len(cache.Files) > maxFiles {
		return fmt.Errorf("会话缓存超出文件上限")
	}
	for path, st := range cache.Files {
		// Compressed logs checkpoint a decompressed offset, which may exceed
		// the file size.
		if st == nil || st.Offset < 0 || st.Size < st.Offset && !strings.HasSuffix(path, ".zst") {
			return fmt.Errorf("会话缓存检查点无效")
		}
	}
	if cache.Files != nil {
		r.files = cache.Files
	}
	return nil
}

func (r *Reader) save() error {
	b, err := json.Marshal(diskCache{Version: cacheVersion, Home: r.home, Files: r.files})
	if err != nil {
		return err
	}
	if len(b) > maxCacheBytes {
		return fmt.Errorf("会话缓存超出 128 MiB")
	}
	if err := os.MkdirAll(filepath.Dir(r.cachePath), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(r.cachePath), ".sessions-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(b)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), r.cachePath)
}

func (r *Reader) snapshot(out *Snapshot, logs []logFile) error {
	byPath := map[string]int{}
	for _, f := range logs {
		if r.files[f.path].State.Session.ParentID != "" {
			continue
		}
		st := r.files[f.path]
		if st == nil {
			continue
		}
		s, err := copySession(st)
		if err != nil {
			return err
		}
		byPath[f.path] = len(out.Sessions)
		out.Sessions = append(out.Sessions, s)
	}
	for _, f := range logs {
		if r.files[f.path].State.Session.ParentID == "" {
			continue
		}
		child, err := copySession(r.files[f.path])
		if err != nil {
			return err
		}
		// Codex source.subagent.thread_spawn identifies the parent thread, but
		// not its originating turn/tool. Keep such associations explicit without
		// attributing work to an arbitrary parent turn.
		parentPath := ""
		if fm := formatOf(child.Agent); fm != nil {
			parentPath = fm.parentLog(f)
		}
		if parentPath == "" {
			out.UnlinkedSubagents = append(out.UnlinkedSubagents, child)
			continue
		}
		index, ok := byPath[parentPath]
		linked := false
		if ok {
			parent := &out.Sessions[index]
			for ti := range parent.Turns {
				for _, tool := range parent.Turns[ti].Tools {
					if tool.AgentID == child.AgentID && tool.AgentID != "" {
						parent.Turns[ti].Subagents = append(parent.Turns[ti].Subagents, Subagent{ID: child.AgentID, ToolID: tool.ID, Session: child})
						linked = true
						break
					}
				}
				if linked {
					break
				}
			}
		}
		if !linked {
			out.UnlinkedSubagents = append(out.UnlinkedSubagents, child)
		}
	}
	return nil
}

func copySession(st *fileState) (Session, error) {
	// Detach slices from the mutable reader checkpoint; callers can hold older
	// snapshots while the next refresh runs without observing mutation or races.
	b, err := json.Marshal(st.State.Session)
	if err != nil {
		return Session{}, err
	}
	var s Session
	if err := json.Unmarshal(b, &s); err != nil {
		return Session{}, err
	}
	s.Incomplete = st.Pending || st.BadLines > 0 || st.LargeLines > 0
	if !st.State.FormatKnown {
		s.UnsupportedReason = "无法识别本地日志格式，暂不支持"
	}
	requests, known := 0, 0
	for _, t := range s.Turns {
		for _, req := range t.Requests {
			requests++
			if req.UsageKnown {
				known++
			}
		}
	}
	usageReason := ""
	if known == 0 {
		usageReason = "日志没有受支持的逐请求用量字段，暂不支持"
	} else if known < requests {
		usageReason = "部分请求缺少用量，统计不完整"
	}
	info := LookupAgent(s.Agent)
	s.Capabilities = []Capability{
		{Name: "tokens", Supported: known > 0, Reason: usageReason},
		{Name: "output_rate", Supported: info.OutputRate && known > 0, Reason: info.RateNote},
		{Name: "tools", Supported: true},
		{Name: "skills", Supported: info.Skills, Reason: info.SkillNote},
		{Name: "permission_wait", Supported: info.PermissionWait},
	}
	for i := range s.Capabilities {
		if !s.Capabilities[i].Supported && s.Capabilities[i].Reason == "" {
			s.Capabilities[i].Reason = "该日志格式暂不支持"
		}
	}
	return s, nil
}
