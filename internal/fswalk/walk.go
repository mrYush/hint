// Package fswalk provides bounded streaming discovery for interactive views.
package fswalk

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
)

// ErrLimit means a view must be narrowed or continued through the disk index.
var ErrLimit = errors.New("directory traversal work limit reached; narrow the path or use the paginated repository index")

// Limits bound depth, examined entries and path size independently of tree size.
type Limits struct {
	MaxDepth, MaxEntries, MaxPathBytes int
	// BeforeBatch prepares 1–32 entries before their callbacks run.
	BeforeBatch func(string, []fs.DirEntry) error
}

// WalkDir visits positive-count directory batches without a directory-wide sort.
// Ordering is lexical within each batch, not across an arbitrarily wide folder.
// The callback has fs.WalkDirFunc's skip and error semantics.
// SkipAll stops visitation; any error closing an opened directory is still
// returned, joined with a traversal error if both occur.
func WalkDir(fsys fs.FS, root string, limits Limits, fn fs.WalkDirFunc) error {
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = 128
	}
	if limits.MaxEntries <= 0 {
		limits.MaxEntries = 100000
	}
	if limits.MaxPathBytes <= 0 {
		limits.MaxPathBytes = 16 << 10
	}
	info, err := fs.Stat(fsys, root)
	if err != nil {
		err = fn(root, nil, err)
		if errors.Is(err, fs.SkipDir) || errors.Is(err, fs.SkipAll) {
			return nil
		}
		return err
	}
	examined := 0
	stopped := false
	control := func(err error, isDir bool) (bool, error) {
		if errors.Is(err, fs.SkipAll) {
			stopped = true
			return false, nil
		}
		if errors.Is(err, fs.SkipDir) {
			return !isDir, nil
		}
		return false, err
	}
	var walk func(string, fs.DirEntry, int) (bool, error)
	walk = func(name string, entry fs.DirEntry, depth int) (_ bool, resultErr error) {
		if depth > limits.MaxDepth || examined >= limits.MaxEntries || len(name) > limits.MaxPathBytes {
			return false, ErrLimit
		}
		examined++
		if err := fn(name, entry, nil); err != nil {
			return control(err, entry.IsDir())
		}
		if !entry.IsDir() {
			return false, nil
		}
		file, err := fsys.Open(name)
		if err != nil {
			return control(fn(name, entry, err), true)
		}
		defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
		dir, ok := file.(fs.ReadDirFile)
		if !ok {
			return false, fmt.Errorf("directory %q does not support bounded ReadDir", name)
		}
		for !stopped {
			batch, readErr := dir.ReadDir(32)
			sort.Slice(batch, func(i, j int) bool { return batch[i].Name() < batch[j].Name() })
			if len(batch) > 0 && limits.BeforeBatch != nil {
				if err := limits.BeforeBatch(name, batch); err != nil {
					return false, err
				}
			}
			for _, child := range batch {
				skip, err := walk(path.Join(name, child.Name()), child, depth+1)
				if err != nil || skip || stopped {
					return false, err
				}
			}
			if errors.Is(readErr, io.EOF) {
				return false, nil
			}
			if readErr != nil {
				return control(fn(name, entry, readErr), true)
			}
			if len(batch) == 0 {
				return false, io.ErrNoProgress
			}
		}
		return false, nil
	}
	_, err = walk(root, fs.FileInfoToDirEntry(info), 0)
	return err
}
