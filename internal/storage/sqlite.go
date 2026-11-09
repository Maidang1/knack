package storage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Database struct {
	Agent, Root, Path                            string
	Bytes, WALBytes, FreeBytes, Pages, FreePages int64
	Error                                        string
}

func databaseFileName(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".sqlite") || strings.HasSuffix(name, ".sqlite3") || strings.HasSuffix(name, ".db")
}

// Database sidecars carry committed data or coordinate access; ordinary file
// cleanup must leave them with the database, even when it cannot inspect it.
func databaseStorageFile(name string) bool {
	name = strings.ToLower(name)
	return databaseFileName(name) || strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm") || strings.HasSuffix(name, "-journal")
}

// OpenDatabase never creates a missing database and refuses linked sidecars.
func OpenDatabase(root, path string, write bool) (*sql.DB, error) {
	if filepath.Dir(path) != filepath.Clean(root) {
		return nil, fmt.Errorf("数据库不在已知目录中")
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		st, err := ValidatePath(root, path+suffix)
		if suffix != "" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("不是常规数据库文件：%s", path+suffix)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 16)
	_, err = io.ReadFull(f, header)
	f.Close()
	if err != nil || string(header) != "SQLite format 3\x00" {
		return nil, fmt.Errorf("不是有效的 SQLite 数据库")
	}
	mode := "ro"
	if write {
		mode = "rw"
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(1000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func InspectDatabase(ctx context.Context, root, path string) Database {
	d := Database{Root: root, Path: path}
	if st, err := os.Lstat(path); err == nil {
		d.Bytes = st.Size()
	}
	if st, err := os.Lstat(path + "-wal"); err == nil {
		d.WALBytes = st.Size()
	}
	db, err := OpenDatabase(root, path, false)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	defer db.Close()
	var pageSize int64
	for _, p := range []struct {
		q string
		v *int64
	}{{"page_count", &d.Pages}, {"freelist_count", &d.FreePages}, {"page_size", &pageSize}} {
		if err = db.QueryRowContext(ctx, "PRAGMA "+p.q).Scan(p.v); err != nil {
			d.Error = strings.TrimSpace(err.Error())
			return d
		}
	}
	d.FreeBytes = d.FreePages * pageSize
	return d
}
