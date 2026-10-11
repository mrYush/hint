package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// gitListFiles returns the paths git considers part of the work tree
// under dir — tracked files plus untracked ones that no ignore rule
// excludes — relative to dir, slash-separated. It runs
// `git ls-files --cached --others --exclude-standard` in dir, which is
// git's own reading of every .gitignore, the repository's exclude file
// and the user's global one, at the cost of a process per call.
func gitListFiles(ctx context.Context, git, dir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, git, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 128 && strings.Contains(msg, "not a git repository") {
			return nil, ErrNotRepository
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git ls-files in %s: %s", dir, msg)
	}
	var out []string
	for _, p := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(p) == 0 {
			continue
		}
		out = append(out, string(p))
	}
	return out, nil
}
