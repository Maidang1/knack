package manage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"knack/internal/fsutil"
	"knack/internal/storage"
)

const opCleanup = "cleanup-file"

type CleanupUndo struct {
	Root     string      `json:"root"`
	Category string      `json:"category,omitempty"`
	Size     int64       `json:"size"`
	Mode     os.FileMode `json:"mode"`
	Modified time.Time   `json:"modified"`
	Digest   string      `json:"digest"`
	// Stored is the backup's size on disk, filled in when records are listed.
	Stored int64 `json:"-"`
}

type cleanupReader struct {
	ctx context.Context
	src io.Reader
}

func (r cleanupReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}

// CleanupFile compresses the original into a private undo record before removal.
// A stale scan, replaced file, or linked path never authorizes removal.
func CleanupFile(ctx context.Context, cat storage.Category, f storage.File, undoRoot string) (Record, error) {
	if ctx.Err() != nil {
		return Record{}, ctx.Err()
	}
	if err := f.Unchanged(cat.Root); err != nil {
		return Record{}, err
	}
	if err := os.MkdirAll(undoRoot, 0700); err != nil {
		return Record{}, err
	}
	var resolveErr error
	undoRoot, resolveErr = filepath.EvalSymlinks(undoRoot)
	if resolveErr != nil {
		return Record{}, resolveErr
	}
	st, err := os.Lstat(f.Path)
	if err != nil {
		return Record{}, err
	}
	rec, err := begin(undoRoot, opCleanup, f.Path)
	if err != nil {
		return Record{}, err
	}
	// Conversation content must not inherit the older skill undo directory modes.
	if err = os.Chmod(rec.Dir, 0700); err != nil {
		discardEmptyRecord(rec)
		return Record{}, err
	}
	rec.Payload = filepath.Join(rec.Dir, "payload.zst")
	rec.Cleanup = &CleanupUndo{Root: cat.Root, Category: cat.ID, Size: f.Size, Mode: st.Mode().Perm(), Modified: f.Modified}
	fail := func(err error) (Record, error) { _ = os.RemoveAll(rec.Dir); return Record{}, err }
	src, err := os.Open(f.Path)
	if err != nil {
		return fail(err)
	}
	defer src.Close()
	opened, err := src.Stat()
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(st, opened) {
		return fail(errors.New("文件已被替换，请重新扫描"))
	}
	dst, err := os.OpenFile(rec.Payload, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fail(err)
	}
	enc, err := zstd.NewWriter(dst, zstd.WithEncoderConcurrency(1))
	if err != nil {
		dst.Close()
		return fail(err)
	}
	hash := sha256.New()
	n, copyErr := io.Copy(enc, io.TeeReader(cleanupReader{ctx, src}, hash))
	closeErr := enc.Close()
	syncErr := dst.Sync()
	dstErr := dst.Close()
	if err = errors.Join(copyErr, closeErr, syncErr, dstErr); err != nil {
		return fail(err)
	}
	if n != f.Size {
		return fail(errors.New("文件在备份时发生变化，请重新扫描"))
	}
	if err = f.Unchanged(cat.Root); err != nil {
		return fail(err)
	}
	rec.Cleanup.Digest = hex.EncodeToString(hash.Sum(nil))
	// Re-read content to detect in-place writes that preserve file size and mtime.
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	hash.Reset()
	if _, err = io.Copy(hash, cleanupReader{ctx, src}); err != nil {
		return fail(err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != rec.Cleanup.Digest {
		return fail(errors.New("文件内容已变化，请重新扫描"))
	}
	src.Close()
	if err = save(rec); err != nil {
		return fail(err)
	}
	if err = os.Chmod(filepath.Join(rec.Dir, "record.json"), 0600); err != nil {
		return fail(err)
	}
	if err = f.Unchanged(cat.Root); err != nil {
		return fail(err)
	}
	if err = os.Remove(f.Path); err != nil {
		return fail(err)
	}
	pruneEmptyDirs(cat.Root, filepath.Dir(f.Path))
	return rec, nil
}

// pruneEmptyDirs removes directories emptied by cleanup, stopping below root.
func pruneEmptyDirs(root, dir string) {
	for strings.HasPrefix(dir, root+string(filepath.Separator)) {
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() || fsutil.IsLink(dir, st) || os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ensureCleanupDir recreates pruned parents below the deepest existing,
// link-free ancestor, so a refused path never gains directories.
func ensureCleanupDir(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("路径超出清理范围：%s", dir)
	}
	var missing []string
	p := dir
	for {
		_, err := os.Lstat(p)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) || filepath.Dir(p) == p {
			return err
		}
		missing = append(missing, p)
		p = filepath.Dir(p)
	}
	if _, err := storage.ValidatePath(p, p); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0700); err != nil && !os.IsExist(err) {
			return err
		}
		st, err := os.Lstat(missing[i])
		if err != nil {
			return err
		}
		if fsutil.IsLink(missing[i], st) {
			return fmt.Errorf("跳过链接路径：%s", missing[i])
		}
	}
	return nil
}

func restoreCleanup(rec Record) error {
	u := rec.Cleanup
	if u == nil {
		return errors.New("清理备份记录不完整")
	}
	if err := expectAbsent(rec.LinkPath); err != nil {
		return err
	}
	if _, err := storage.ValidatePath(rec.Dir, rec.Payload); err != nil {
		return err
	}
	src, err := os.Open(rec.Payload)
	if err != nil {
		return err
	}
	defer src.Close()
	dec, err := zstd.NewReader(src, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		return err
	}
	defer dec.Close()
	if err = ensureCleanupDir(u.Root, filepath.Dir(rec.LinkPath)); err != nil {
		return err
	}
	dst, err := os.OpenFile(rec.LinkPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(dst, hash), io.LimitReader(dec, u.Size+1))
	err = errors.Join(copyErr, dst.Sync(), dst.Close())
	if err != nil || n != u.Size || hex.EncodeToString(hash.Sum(nil)) != u.Digest {
		_ = os.Remove(rec.LinkPath)
		pruneEmptyDirs(u.Root, filepath.Dir(rec.LinkPath))
		return fmt.Errorf("备份校验失败，未恢复：%s", rec.LinkPath)
	}
	if err = os.Chmod(rec.LinkPath, u.Mode); err != nil {
		return err
	}
	return os.Chtimes(rec.LinkPath, u.Modified, u.Modified)
}

// CleanupRecords returns only this feature's records; skill undo is excluded.
func CleanupRecords(undoRoot string) ([]Record, int64, error) {
	if real, err := filepath.EvalSymlinks(undoRoot); err == nil {
		undoRoot = real
	}
	entries, err := os.ReadDir(undoRoot)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var records []Record
	var bytes int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(undoRoot, entry.Name())
		path := filepath.Join(dir, "record.json")
		if _, err := storage.ValidatePath(undoRoot, path); err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return records, bytes, err
		}
		var rec Record
		if json.Unmarshal(data, &rec) != nil || rec.Op != opCleanup {
			continue
		}
		if rec.ID != entry.Name() || rec.Dir != dir || rec.Payload != filepath.Join(dir, "payload.zst") || rec.Cleanup == nil {
			continue
		}
		st, err := storage.ValidatePath(undoRoot, rec.Payload)
		if err != nil {
			continue
		}
		rec.Cleanup.Stored = st.Size() + int64(len(data))
		bytes += rec.Cleanup.Stored
		records = append(records, rec)
	}
	return records, bytes, nil
}

// PurgeCleanupBackups permanently removes only the confirmed cleanup backups.
func PurgeCleanupBackups(undoRoot string, records []Record) error {
	if real, err := filepath.EvalSymlinks(undoRoot); err == nil {
		undoRoot = real
	}
	for _, rec := range records {
		if rec.Op != opCleanup || rec.Dir != filepath.Join(undoRoot, rec.ID) || filepath.Base(rec.ID) != rec.ID || rec.Payload != filepath.Join(rec.Dir, "payload.zst") {
			return errors.New("无效的清理备份路径")
		}
		if _, err := storage.ValidatePath(undoRoot, rec.Payload); err != nil {
			return err
		}
		// Remove known files only, never recursively delete an unexpected directory.
		if err := os.Remove(rec.Payload); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(rec.Dir, "record.json")); err != nil {
			return err
		}
		if err := os.Remove(rec.Dir); err != nil {
			return err
		}
	}
	return nil
}
