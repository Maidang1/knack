package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TreeHash is a sha256 of the skill directory: every regular file's
// contents, plus symlink targets and directory names. .git, node_modules,
// .DS_Store and AppleDouble files are left out so two copies of the same
// skill compare equal.
func TreeHash(root string) (string, error) {
	var lines []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		if path != root && skipTreeName(base) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			tgt, readErr := os.Readlink(path)
			if readErr != nil {
				return readErr
			}
			lines = append(lines, "link\x00"+rel+"\x00"+tgt)
			if d.IsDir() {
				return filepath.SkipDir
			}
		case d.IsDir():
			lines = append(lines, "dir\x00"+rel)
		case info.Mode().IsRegular():
			sum, hashErr := fileHash(path)
			if hashErr != nil {
				return hashErr
			}
			lines = append(lines, "file\x00"+rel+"\x00"+sum)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	after, err := f.Stat()
	if err != nil {
		return "", err
	}
	current, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !sameFileStamp(before, after) || !sameFileStamp(after, current) {
		return "", fmt.Errorf("%s changed while hashing", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func skipTreeName(name string) bool {
	switch name {
	case ".git", "node_modules", ".DS_Store":
		return true
	}
	return strings.HasPrefix(name, "._")
}

// ShortHash returns the first 12 hex characters, or an em dash when empty.
func ShortHash(h string) string {
	if h == "" {
		return "—"
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
