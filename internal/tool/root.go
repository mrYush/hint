package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot reports a path that resolves outside the working
// directory. Callers test for it with errors.Is.
var ErrOutsideRoot = errors.New("path is outside the working directory")

// Root is the directory file tools are confined to. Every path a tool
// receives goes through [Root.Resolve], which rejects anything that would
// land outside — including through a symlink — so the confinement holds by
// construction rather than by each tool remembering to check.
//
// The value is safe to copy and share: it holds only the resolved
// directory.
type Root struct {
	dir string
}

// NewRoot resolves dir to an absolute, symlink-free directory.
func NewRoot(dir string) (Root, error) {
	if dir == "" {
		return Root{}, fmt.Errorf("tool: root directory is empty")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Root{}, fmt.Errorf("tool: root %q: %w", dir, err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Root{}, fmt.Errorf("tool: root %q: %w", dir, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return Root{}, fmt.Errorf("tool: root %q: %w", dir, err)
	}
	if !info.IsDir() {
		return Root{}, fmt.Errorf("tool: root %q is not a directory", dir)
	}
	return Root{dir: real}, nil
}

// Dir returns the resolved root directory.
func (r Root) Dir() string { return r.dir }

// Resolve turns p — relative to the root, or absolute — into a cleaned
// absolute path inside the root. An empty p means the root itself.
//
// Two checks are needed. The lexical one (after Clean, the path must have
// the root as a prefix) catches "../.." escapes. The symlink one resolves the
// deepest existing ancestor of the path and repeats the prefix test, so a
// link inside the tree that points at /etc or ~/.ssh is caught even though
// its own path looks fine. The target itself need not exist: write_file
// creates files, so only the existing prefix can be checked.
func (r Root) Resolve(p string) (string, error) {
	if r.dir == "" {
		return "", fmt.Errorf("tool: root is not initialised")
	}
	p = strings.TrimSpace(p)
	var abs string
	switch {
	case p == "":
		abs = r.dir
	case filepath.IsAbs(p):
		abs = filepath.Clean(p)
	default:
		abs = filepath.Join(r.dir, p)
	}
	if !r.contains(abs) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, p)
	}

	if err := r.checkReal(abs, 0); err != nil {
		return "", err
	}
	return abs, nil
}

// checkReal verifies that the deepest existing ancestor of abs, with its
// symlinks resolved, still lies inside the root.
//
// filepath.EvalSymlinks fails on a dangling link, so that case is resolved
// by hand: the link's target is checked lexically and then recursively, up
// to a small depth, so a chain of dangling links cannot loop forever.
func (r Root) checkReal(abs string, depth int) error {
	if depth > 8 {
		return fmt.Errorf("tool: resolve %q: too many levels of symbolic links", abs)
	}
	existing := abs
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err == nil {
		if !r.contains(real) {
			return fmt.Errorf("%w: %s (resolves to %s)", ErrOutsideRoot, r.Rel(abs), real)
		}
		return nil
	}
	target, rerr := os.Readlink(existing)
	if rerr != nil {
		return fmt.Errorf("tool: resolve %q: %w", abs, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(existing), target)
	}
	target = filepath.Clean(target)
	if !r.contains(target) {
		return fmt.Errorf("%w: %s (links to %s)", ErrOutsideRoot, r.Rel(abs), target)
	}
	return r.checkReal(target, depth+1)
}

// Rel returns abs relative to the root for display, or abs unchanged when
// it is not inside the root.
func (r Root) Rel(abs string) string {
	rel, err := filepath.Rel(r.dir, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return abs
	}
	return rel
}

// contains reports whether abs (already cleaned) is the root or below it.
func (r Root) contains(abs string) bool {
	return abs == r.dir || strings.HasPrefix(abs, r.dir+string(filepath.Separator))
}
