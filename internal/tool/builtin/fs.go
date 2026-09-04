package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/mrYush/hint/internal/tool"
)

// maxEditSize bounds the file size edit_file and write_file load into memory
// whole. Source files are far below it; anything above is not something a
// search/replace edit should be applied to blindly.
const maxEditSize = 10 << 20

// binaryProbe is how many leading bytes are inspected for a NUL byte to
// classify a file as binary — the heuristic git and grep use.
const binaryProbe = 8192

// skippedDirs are directory names list_dir, glob and grep never descend
// into: dependency and build output trees that dwarf the source they
// belong to. Hidden entries (a leading dot) are skipped separately.
// .gitignore-aware filtering is WP0.8's job.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"__pycache__":  true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
}

// rgExcludeArgs are the ripgrep flags that make its file selection match
// the Go walk: ignore any user config, skip hidden entries, and prune the
// same directory names. They must come after the caller's own --glob:
// ripgrep gives later globs precedence, and an explicit positive glob
// otherwise re-admits hidden files. .gitignore is honoured by ripgrep and
// not by the walk, a difference the plan accepts.
func rgExcludeArgs() []string {
	args := []string{"--no-config", "--glob", "!.*"}
	names := make([]string, 0, len(skippedDirs))
	for name := range skippedDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		args = append(args, "--glob", "!"+name)
	}
	return args
}

// skipDir reports whether a directory entry named name should be pruned
// from a walk. The root itself is never pruned.
func skipDir(name string) bool {
	return skippedDirs[name] || isHidden(name)
}

// isHidden reports whether name is a dot-file; "." and ".." are not.
func isHidden(name string) bool {
	return len(name) > 1 && name[0] == '.' && name != ".."
}

// isBinary reports whether r's leading bytes contain a NUL. It consumes up
// to binaryProbe bytes.
func isBinary(r io.Reader) (bool, error) {
	buf := make([]byte, binaryProbe)
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return bytes.IndexByte(buf[:n], 0) >= 0, nil
}

// pathError renders a filesystem error for the model with the path shown
// relative to the root, and hides the OS-level detail behind the common
// cases.
func pathError(root tool.Root, abs string, err error) string {
	rel := root.Rel(abs)
	switch {
	case errors.Is(err, tool.ErrOutsideRoot):
		return err.Error()
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("%s: no such file or directory", rel)
	case errors.Is(err, os.ErrPermission):
		return fmt.Sprintf("%s: permission denied", rel)
	default:
		return fmt.Sprintf("%s: %v", rel, err)
	}
}

// writeAtomic writes data to abs by way of a temporary file in the same
// directory and a rename, so a reader never observes a half-written file
// and a failure mid-write leaves the original intact. perm is used for a
// new file; an existing file keeps its mode.
func writeAtomic(abs string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(abs)
	if info, err := os.Stat(abs); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".hint-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, abs); err != nil {
		cleanup()
		return err
	}
	return nil
}

// readWhole loads a regular file of at most limit bytes.
func readWhole(abs string, limit int64) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, info, fmt.Errorf("is a directory")
	}
	if info.Size() > limit {
		return nil, info, fmt.Errorf("file is too large (%d bytes, limit %d)", info.Size(), limit)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, info, err
	}
	return data, info, nil
}
