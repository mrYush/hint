package tool

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot reports a path that resolves outside the working
// directory. Callers test for it with errors.Is.
var ErrOutsideRoot = errors.New("path is outside the working directory")

// ErrDenied reports a path inside the root that the file tools must not
// touch: hint's own configuration and session files (WP1.7). Callers test
// for it with errors.Is.
var ErrDenied = errors.New("path is hint's own configuration or session data, which the tools do not read")

// Root is the directory file tools are confined to. Every path a tool
// receives goes through [Root.Resolve], which rejects anything that would
// land outside — including through a symlink — and every file is then
// opened through the Root's own methods, which go through [os.Root]: the
// kernel resolves each path component under the directory, so a symlink
// swapped in between the check and the open cannot escape either (R8,
// WP1.7). The check gives the model a readable refusal; the open is what
// holds.
//
// The value is safe to copy and share: it holds the resolved directory,
// the denied paths and one shared handle.
type Root struct {
	dir string
	// denied are absolute, cleaned paths (files or directories) under
	// the root that Resolve refuses and the walks skip.
	denied []string
	// handle is the directory as os.Root opened it; nil for the zero
	// value, which no method accepts.
	handle *os.Root
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
	handle, err := os.OpenRoot(real)
	if err != nil {
		return Root{}, fmt.Errorf("tool: root %q: %w", dir, err)
	}
	return Root{dir: real, handle: handle}, nil
}

// rel turns an absolute path the Root resolved into the name os.Root
// takes: relative to the directory, "." for the directory itself.
func (r Root) rel(abs string) (string, error) {
	if r.handle == nil {
		return "", fmt.Errorf("tool: root is not initialised")
	}
	// os.Root refuses a symlink with an absolute target even when the
	// target is inside the root, and Resolve allows one. So the link is
	// resolved here first, and os.Root opens the target: a link that
	// pointed inside the root when it was looked at is what gets opened,
	// and a component that becomes a link to the outside afterwards is
	// still refused by os.Root.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		if !r.contains(real) {
			return "", fmt.Errorf("%w: %s", ErrOutsideRoot, abs)
		}
		abs = real
	}
	rel, err := filepath.Rel(r.dir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRoot, abs)
	}
	return rel, nil
}

// Open opens the file at abs for reading, through os.Root.
func (r Root) Open(abs string) (*os.File, error) {
	rel, err := r.rel(abs)
	if err != nil {
		return nil, err
	}
	return r.handle.Open(rel)
}

// OpenFile is os.OpenFile under the root.
func (r Root) OpenFile(abs string, flag int, perm os.FileMode) (*os.File, error) {
	rel, err := r.rel(abs)
	if err != nil {
		return nil, err
	}
	return r.handle.OpenFile(rel, flag, perm)
}

// Stat is os.Stat under the root.
func (r Root) Stat(abs string) (os.FileInfo, error) {
	rel, err := r.rel(abs)
	if err != nil {
		return nil, err
	}
	return r.handle.Stat(rel)
}

// ReadFile is os.ReadFile under the root.
func (r Root) ReadFile(abs string) ([]byte, error) {
	rel, err := r.rel(abs)
	if err != nil {
		return nil, err
	}
	return r.handle.ReadFile(rel)
}

// MkdirAll is os.MkdirAll under the root.
func (r Root) MkdirAll(abs string, perm os.FileMode) error {
	rel, err := r.rel(abs)
	if err != nil {
		return err
	}
	return r.handle.MkdirAll(rel, perm)
}

// Rename is os.Rename under the root; both names must be inside it.
func (r Root) Rename(oldAbs, newAbs string) error {
	from, err := r.rel(oldAbs)
	if err != nil {
		return err
	}
	to, err := r.rel(newAbs)
	if err != nil {
		return err
	}
	return r.handle.Rename(from, to)
}

// Remove is os.Remove under the root.
func (r Root) Remove(abs string) error {
	rel, err := r.rel(abs)
	if err != nil {
		return err
	}
	return r.handle.Remove(rel)
}

// CreateTemp opens a new file with a random name in the directory dir
// (inside the root), for an atomic write that renames it into place.
func (r Root) CreateTemp(dir, prefix string) (*os.File, error) {
	for range 100 {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		f, err := r.OpenFile(filepath.Join(dir, prefix+hex.EncodeToString(b[:])), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, fmt.Errorf("tool: no free temporary name in %s", r.Rel(dir))
}

// WalkDir walks the tree under abs the way filepath.WalkDir does, with
// absolute paths in the callback, reading directories through os.Root.
// Symlinks are not followed, as filepath.WalkDir does not follow them.
func (r Root) WalkDir(abs string, fn fs.WalkDirFunc) error {
	rel, err := r.rel(abs)
	if err != nil {
		return err
	}
	return fs.WalkDir(r.handle.FS(), filepath.ToSlash(rel), func(p string, d fs.DirEntry, err error) error {
		full := abs
		if p != "." && p != filepath.ToSlash(rel) {
			full = filepath.Join(r.dir, filepath.FromSlash(p))
		}
		return fn(full, d, err)
	})
}

// Dir returns the resolved root directory.
func (r Root) Dir() string { return r.dir }

// Deny returns a copy of the root that refuses paths — files or
// directories, each with everything below it. A path outside the root
// is kept too: it costs nothing and the root may be a parent of it the
// next time the same list is applied. Symlinks in a denied path are
// resolved when it exists, so a denied directory reached by another name
// is still denied.
func (r Root) Deny(paths ...string) Root {
	out := r
	out.denied = append([]string(nil), r.denied...)
	for _, p := range paths {
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		out.denied = append(out.denied, abs)
		if real, err := filepath.EvalSymlinks(abs); err == nil && real != abs {
			out.denied = append(out.denied, real)
		}
	}
	return out
}

// Denied reports whether abs (cleaned) is a denied path or below one.
func (r Root) Denied(abs string) bool {
	for _, d := range r.denied {
		if abs == d || strings.HasPrefix(abs, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// DeniedUnder returns the denied paths inside dir, relative to it with
// forward slashes: what a ripgrep run rooted at dir must exclude.
func (r Root) DeniedUnder(dir string) []string {
	var out []string
	for _, d := range r.denied {
		rel, err := filepath.Rel(dir, d)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

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
	if r.Denied(abs) {
		return "", fmt.Errorf("%w: %s", ErrDenied, p)
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
		if r.Denied(real) {
			return fmt.Errorf("%w: %s (resolves to %s)", ErrDenied, r.Rel(abs), real)
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
