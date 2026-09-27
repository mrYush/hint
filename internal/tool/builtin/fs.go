package builtin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
)

// maxEditSize bounds the file size edit_file and write_file load into memory
// whole. Source files are far below it; anything above is not something a
// search/replace edit should be applied to blindly.
const maxEditSize = 10 << 20

// binaryProbe is how many leading bytes are inspected for a NUL byte to
// classify a file as binary — the heuristic git and grep use.
const binaryProbe = 8192

// rgExcludeArgs are the ripgrep flags that make its file selection match
// the Go walk under project.Basic: ignore any user config, skip hidden
// entries, and prune the same directory names. They must come after the
// caller's own --glob: ripgrep gives later globs precedence, and an
// explicit positive glob otherwise re-admits hidden files. .gitignore is
// honoured by ripgrep itself, and by the Go walk through project.Rules.
func rgExcludeArgs() []string {
	args := []string{"--no-config", "--glob", "!.*"}
	for _, name := range project.SkippedDirs() {
		args = append(args, "--glob", "!"+name)
	}
	return args
}

// isBinaryData is [isBinary] over bytes already in memory.
func isBinaryData(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binaryProbe)], 0) >= 0
}

// splitKeepNL splits s into lines that keep their terminator, the shape
// the diff package's line-level functions expect.
func splitKeepNL(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
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
//
// A symlink at abs is written through, not replaced: rename would swap the
// link for a regular file, which surprises a user who linked a file into
// the tree on purpose. Root.Resolve has already checked that the link's
// target lies inside the root, so following it here stays confined. The
// temporary file is fsynced before the rename so that a crash cannot leave
// an empty file where the old content was.
func writeAtomic(abs string, data []byte, perm os.FileMode) error {
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
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
	if err := tmp.Sync(); err != nil {
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
