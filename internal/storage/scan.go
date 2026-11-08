// Package storage inventories known Agent data without reading conversation bodies.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"knack/internal/fsutil"
)

type Category struct {
	ID, Agent, Name, Root, Description string
	History                            bool
	// Extensions restrict session cleanup to transcripts, preserving project memory.
	Extensions []string
	OnlyDir    string
}

type File struct {
	Path     string
	Size     int64
	Modified time.Time
	info     os.FileInfo
}

// ageBandDays bounds Group.Ages and matches the retention choices.
var ageBandDays = []int{7, 30, 90, 180}

// AgeBands counts the bands of Group.Ages; the last one has no upper bound.
const AgeBands = 5

type Group struct {
	Category
	Ages, ProtectedAges                  [AgeBands]int64
	Bytes, EligibleBytes, ProtectedBytes int64
	Files, Eligible, Protected           int
	Candidates                           []File
}

type Snapshot struct {
	At        time.Time
	Groups    []Group
	Databases []Database
	Warnings  []string
}

// Locations are explicit, so tests and alternate homes never read the live home.
type Locations struct{ Claude, Codex, SQLite string }

func DefaultLocations(home string) Locations {
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	return Locations{filepath.Join(home, ".claude"), filepath.Join(home, ".codex"), ""}
}

func (l Locations) Categories() []Category {
	var out []Category
	add := func(id, agent, name, root, sub, desc string, history bool, ext ...string) {
		out = append(out, Category{ID: id, Agent: agent, Name: name, Root: filepath.Join(root, sub), Description: desc, History: history, Extensions: ext})
	}
	add("claude-debug", "Claude Code", "调试日志", l.Claude, "debug", "诊断日志；清理后无法查看对应的旧调试记录。", false)
	add("claude-hook-logs", "Claude Code", "Hook 调试日志", l.Claude, "hook-debug-logs", "旧 Hook 运行日志。", false)
	add("claude-cache", "Claude Code", "缓存", l.Claude, "cache", "可重新生成的本地缓存。", false)
	add("claude-paste", "Claude Code", "粘贴缓存", l.Claude, "paste-cache", "旧粘贴内容；可能被历史会话引用。", true)
	add("claude-shell", "Claude Code", "Shell 快照", l.Claude, "shell-snapshots", "旧终端环境快照。", false)
	add("claude-history", "Claude Code", "文件回退快照", l.Claude, "file-history", "清理后会影响旧会话的 /rewind 文件恢复。", true)
	add("claude-projects", "Claude Code", "历史会话", l.Claude, "projects", "仅清理 JSONL 会话文件及其子 Agent 元数据；会影响恢复会话和用量统计，保留项目记忆文件。", true, ".jsonl", ".meta.json")
	add("claude-tool-results", "Claude Code", "工具输出附件", l.Claude, "projects", "旧工具输出、图片等附件；清理后历史会话引用的附件将不可用。", true)
	out[len(out)-1].OnlyDir = "tool-results"
	add("claude-plans", "Claude Code", "历史计划", l.Claude, "plans", "旧计划文档；清理后无法再从磁盘查看。", true)
	add("claude-telemetry", "Claude Code", "遥测暂存", l.Claude, "telemetry", "发送失败后暂存的遥测事件；清理后不再重试发送。", false)
	add("claude-todos", "Claude Code", "会话待办", l.Claude, "todos", "每个会话的待办列表；可能影响恢复旧会话时的任务状态。", true)
	add("claude-transcripts", "Claude Code", "转录文件", l.Claude, "transcripts", "旧会话转录；清理后无法再从磁盘查看。", true)
	add("codex-log", "Codex", "运行日志", l.Codex, "log", "诊断日志；清理后无法查看对应的旧运行记录。", false)
	add("codex-cache", "Codex", "缓存", l.Codex, "cache", "可重新生成的本地缓存。", false)
	add("codex-shell", "Codex", "Shell 快照", l.Codex, "shell_snapshots", "旧终端环境快照。", false)
	add("codex-sessions", "Codex", "历史会话", l.Codex, "sessions", "清理会影响恢复会话和用量统计；数据库中的会话索引可能暂时保留。", true, ".jsonl", ".jsonl.zst")
	add("codex-archived", "Codex", "已归档会话", l.Codex, "archived_sessions", "归档会话仍包含对话内容；清理会影响后续恢复。", true, ".jsonl", ".jsonl.zst")
	// tmp/path and tmp/arg0 contain executable shims for running Agents, not caches.
	return out
}

// ValidatePath rejects links and junctions in every component below a known root.
func ValidatePath(root, path string) (os.FileInfo, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("路径超出清理范围：%s", path)
	}
	// Validate ancestors too; a symlinked cache root must never reach external data.
	for p := root; ; p = filepath.Dir(p) {
		st, e := os.Lstat(p)
		if e != nil {
			return nil, e
		}
		if fsutil.IsLink(p, st) {
			return nil, fmt.Errorf("跳过链接路径：%s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return st, nil
	}
	p := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		p = filepath.Join(p, part)
		st, err = os.Lstat(p)
		if err != nil {
			return nil, err
		}
		if fsutil.IsLink(p, st) {
			return nil, fmt.Errorf("跳过链接路径：%s", p)
		}
	}
	return st, nil
}

// sessionOf names the Claude session a stored file belongs to, if any.
func sessionOf(categoryID, rel string) string {
	parts := strings.Split(rel, string(filepath.Separator))
	switch categoryID {
	case "claude-projects", "claude-tool-results":
		if len(parts) >= 3 {
			return parts[1]
		}
	case "claude-history":
		if len(parts) >= 2 {
			return parts[0]
		}
	case "claude-todos":
		if id, _, ok := strings.Cut(parts[len(parts)-1], "-agent-"); ok {
			return id
		}
	}
	return ""
}

// ActiveSessions holds Claude sessions whose transcript changed within retention.
// A resumed session keeps old attachments that its live transcript still needs.
type ActiveSessions struct {
	ids map[string]bool
	// partial means the projects tree could not be read in full, so any
	// session-owned file might belong to an active session.
	partial bool
}

// Holds fails closed: a file is only released when its session is known inactive.
func (a ActiveSessions) Holds(cat Category, path string) bool {
	rel, err := filepath.Rel(cat.Root, path)
	if err != nil {
		return true
	}
	id := sessionOf(cat.ID, rel)
	return id != "" && (a.partial || a.ids[id])
}

// ageBand uses the retention comparison, so a band edge never disagrees with it.
func ageBand(now, modified time.Time) int {
	for i, days := range ageBandDays {
		if !modified.Before(retentionCutoff(now, days)) {
			return i
		}
	}
	return AgeBands - 1
}

func retentionCutoff(now time.Time, days int) time.Time {
	return now.Add(-time.Duration(max(1, days)) * 24 * time.Hour)
}

// FindActiveSessions reads only transcript metadata, directly below each project.
func FindActiveSessions(ctx context.Context, l Locations, now time.Time, days int) ActiveSessions {
	active := ActiveSessions{ids: map[string]bool{}}
	root := filepath.Join(l.Claude, "projects")
	cutoff := retentionCutoff(now, days)
	if _, err := ValidatePath(root, root); err != nil {
		active.partial = !os.IsNotExist(err)
		return active
	}
	projects, err := os.ReadDir(root)
	if err != nil {
		active.partial = true
		return active
	}
	for _, project := range projects {
		if ctx.Err() != nil {
			active.partial = true
			return active
		}
		dir := filepath.Join(root, project.Name())
		st, err := project.Info()
		if err != nil || fsutil.IsLink(dir, st) {
			active.partial = true
			continue
		}
		if !st.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			active.partial = true
			continue
		}
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".jsonl")
			if !ok || id == "" || entry.IsDir() {
				continue
			}
			st, err := entry.Info()
			if err != nil {
				active.partial = true
			} else if !st.ModTime().Before(cutoff) {
				active.ids[id] = true
			}
		}
	}
	return active
}

// skipUnreadable keeps one vanished or denied entry from ending a whole category.
func skipUnreadable(d fs.DirEntry, err error, unreadable *int) error {
	if !os.IsNotExist(err) {
		*unreadable++
	}
	if d != nil && d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

func (f File) Unchanged(root string) error {
	st, err := ValidatePath(root, f.Path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() != f.Size || !st.ModTime().Equal(f.Modified) || f.info == nil || !os.SameFile(st, f.info) {
		return fmt.Errorf("文件已变化，请重新扫描：%s", f.Path)
	}
	return nil
}

// Scan bounds traversal and excludes files touched in the last retention period.
// At least one day is always retained, including when a caller passes zero.
func Scan(ctx context.Context, l Locations, now time.Time, days int) Snapshot {
	out := Snapshot{At: now}
	cutoff := retentionCutoff(now, days)
	remaining := 100000
	active := FindActiveSessions(ctx, l, now, days)
	if active.partial {
		out.Warnings = append(out.Warnings, "无法完整读取 Claude 项目目录，已保护全部会话附件")
	}
	for _, cat := range l.Categories() {
		g := Group{Category: cat}
		unreadable := 0
		_, err := ValidatePath(cat.Root, cat.Root)
		if os.IsNotExist(err) {
			out.Groups = append(out.Groups, g)
			continue
		}
		if err != nil {
			out.Warnings = append(out.Warnings, err.Error())
			out.Groups = append(out.Groups, g)
			continue
		}
		err = filepath.WalkDir(cat.Root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return skipUnreadable(d, err, &unreadable)
			}
			remaining--
			if remaining < 0 {
				return fmt.Errorf("文件超过 100000 个，扫描结果不完整")
			}
			st, err := d.Info()
			if err != nil {
				return skipUnreadable(d, err, &unreadable)
			}
			if fsutil.IsLink(path, st) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() && (cat.ID == "claude-projects" || cat.ID == "claude-tool-results") && d.Name() == "memory" {
				return fs.SkipDir
			}
			if d.IsDir() && cat.ID == "claude-projects" && d.Name() == "tool-results" {
				return fs.SkipDir
			}
			if !st.Mode().IsRegular() {
				return nil
			}
			if databaseStorageFile(d.Name()) {
				return nil
			}
			rel, _ := filepath.Rel(cat.Root, path)
			if cat.OnlyDir != "" {
				if !slices.Contains(strings.Split(filepath.Dir(rel), string(filepath.Separator)), cat.OnlyDir) {
					return nil
				}
			}
			if len(cat.Extensions) > 0 && !slices.ContainsFunc(cat.Extensions, func(ext string) bool { return strings.HasSuffix(path, ext) }) {
				return nil
			}
			g.Files++
			g.Bytes += st.Size()
			band := ageBand(now, st.ModTime())
			g.Ages[band] += st.Size()
			if st.ModTime().Before(cutoff) {
				if active.Holds(cat, path) {
					g.Protected++
					g.ProtectedBytes += st.Size()
					g.ProtectedAges[band] += st.Size()
					return nil
				}
				g.Eligible++
				g.EligibleBytes += st.Size()
				g.Candidates = append(g.Candidates, File{path, st.Size(), st.ModTime(), st})
			}
			return nil
		})
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("扫描超时，结果不完整")
		}
		if err != nil {
			out.Warnings = append(out.Warnings, cat.Name+"："+err.Error())
		}
		if unreadable > 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s %s：%d 个条目无法读取，已跳过", cat.Agent, cat.Name, unreadable))
		}
		out.Groups = append(out.Groups, g)
		if remaining < 0 || ctx.Err() != nil {
			break
		}
	}
	// Claude Code currently stores sessions as JSONL. Probe only its own data
	// locations for SQLite used by other versions, excluding plugins and projects.
	roots := []struct{ agent, path string }{
		{"Claude Code", l.Claude},
		{"Claude Code", filepath.Join(l.Claude, "state")},
		{"Claude Code", filepath.Join(l.Claude, "sqlite")},
		{"Claude Code", filepath.Join(l.Claude, "cache")},
		{"Claude Code", filepath.Join(l.Claude, "daemon")},
		{"Claude Code", filepath.Join(l.Claude, "sessions")},
		{"Codex", l.Codex},
		{"Codex", filepath.Join(l.Codex, "sqlite")},
	}
	if l.SQLite != "" {
		roots = append(roots, struct{ agent, path string }{"Codex", l.SQLite})
	}
	seen := map[string]bool{}
	for _, location := range roots {
		root := location.path
		if _, err := ValidatePath(root, root); err != nil {
			if !os.IsNotExist(err) {
				out.Warnings = append(out.Warnings, err.Error())
			}
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			out.Warnings = append(out.Warnings, err.Error())
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return out
			}
			name := entry.Name()
			if entry.IsDir() || !databaseFileName(name) {
				continue
			}
			path := filepath.Join(root, name)
			if seen[path] {
				continue
			}
			seen[path] = true
			if len(out.Databases) >= 100 {
				out.Warnings = append(out.Warnings, "数据库超过 100 个，扫描结果不完整")
				return out
			}
			db := InspectDatabase(ctx, root, path)
			db.Agent = location.agent
			out.Databases = append(out.Databases, db)
		}
	}
	return out
}
