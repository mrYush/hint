package project

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNotRepository reports a directory that is not inside a git work tree.
var ErrNotRepository = errors.New("not a git repository")

// FindGitRoot walks up from dir to the nearest directory containing a
// `.git` entry — a directory in a normal checkout, a file in a linked
// worktree or a submodule — and returns it. ok is false when no ancestor
// has one.
func FindGitRoot(dir string) (root string, ok bool) {
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
