package manage

import (
	"context"
	"errors"
	"fmt"

	"knack/internal/storage"
)

// MaintainDatabase uses SQLite's own locks and transactions, preserving rows.
// checkpointOnly never vacuums; busy readers/writers produce an actionable error.
func MaintainDatabase(ctx context.Context, d storage.Database, checkpointOnly bool) (int64, error) {
	before := storage.InspectDatabase(ctx, d.Root, d.Path)
	if before.Error != "" {
		return 0, errors.New(before.Error)
	}
	db, err := storage.OpenDatabase(d.Root, d.Path, true)
	if err != nil {
		return 0, err
	}
	check := func() error {
		var result string
		if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return fmt.Errorf("数据库完整性检查失败：%s", result)
		}
		return nil
	}
	checkpoint := func() error {
		var busy, log, done int
		if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("数据库正在使用，请退出 Claude Code / Codex 后重试")
		}
		return nil
	}
	work := func() error {
		if err := check(); err != nil {
			return err
		}
		if err := checkpoint(); err != nil {
			return err
		}
		if !checkpointOnly {
			if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
				return fmt.Errorf("整理失败，请关闭使用数据库的应用并检查可用空间：%w", err)
			}
		}
		if err := checkpoint(); err != nil {
			return err
		}
		return check()
	}
	err = work()
	closeErr := db.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	after := storage.InspectDatabase(ctx, d.Root, d.Path)
	if after.Error != "" {
		return 0, errors.New(after.Error)
	}
	return max(0, before.Bytes+before.WALBytes-after.Bytes-after.WALBytes), nil
}
