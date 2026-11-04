// Package fsutil provides shared filesystem path checks.
package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// IsLink reports whether info describes a symlink or a Windows junction.
func IsLink(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if runtime.GOOS == "windows" && info.Mode()&os.ModeIrregular != 0 {
		_, err := os.Readlink(path)
		return err == nil
	}
	return false
}

// EqualPath compares cleaned paths using the platform's case convention.
func EqualPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
