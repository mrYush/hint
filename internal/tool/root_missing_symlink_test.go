package tool_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestRootMissingChildBelowAbsoluteInRootSymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	path, err := root.Resolve("alias/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing child misclassified: %v", err)
	}
	file, err := root.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "real/new.txt")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.OpenFile(filepath.Join(root.Dir(), "outside/new.txt"), os.O_CREATE|os.O_WRONLY, 0600); !errors.Is(err, tool.ErrOutsideRoot) {
		t.Fatalf("outside creation allowed: %v", err)
	}
}
