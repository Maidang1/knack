package manage

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// RebaseRecords points every record under undoRoot at the directory it now
// sits in. A record names its own directory and payload, and a cleanup
// backup is only listed or restored when both match, so records that were
// moved with their undo root must be rewritten. Records that do not look
// like this package's own are left alone.
func RebaseRecords(undoRoot string) error {
	if real, err := filepath.EvalSymlinks(undoRoot); err == nil {
		undoRoot = real
	}
	entries, err := os.ReadDir(undoRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failed error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(undoRoot, entry.Name())
		path := filepath.Join(dir, "record.json")
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		var rec Record
		if json.Unmarshal(data, &rec) != nil || rec.ID != entry.Name() || filepath.Base(rec.Dir) != rec.ID || rec.Dir == dir {
			continue
		}
		if rec.Payload != "" {
			if filepath.Dir(rec.Payload) != rec.Dir {
				continue
			}
			rec.Payload = filepath.Join(dir, filepath.Base(rec.Payload))
		}
		rec.Dir = dir
		b, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, append(b, '\n'), st.Mode().Perm()); err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			failed = errors.Join(failed, err)
		}
	}
	return failed
}
