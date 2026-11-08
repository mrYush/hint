package project

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIgnoreRejectsEscapingPathsBeforeAccess(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	paths := []string{"..", "../outside", "sub/../../outside", filepath.ToSlash(parent), "bad\x00path"}
	if runtime.GOOS == "windows" {
		paths = append(paths, `..\outside`, `C:\outside`, `C:relative`, `\\server\share`)
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			if !Basic.Ignored(path, true) {
				t.Errorf("Basic admitted escaping path %q", path)
			}
			for _, cached := range []bool{false, true} {
				calls := 0
				g := &lazyGitIgnorer{ctx: t.Context(), git: "must-not-execute", dir: dir, cache: make(map[string]bool), environ: func() []string {
					calls++
					return nil
				}}
				if cached {
					g.cache[path] = false
				}
				if !g.Ignored(path, true) || calls != 0 || g.err != nil {
					t.Errorf("escaping path accessed policy/filesystem: cached=%v commands=%d err=%v", cached, calls, g.err)
				}
			}
		})
	}
}

func TestPrepareIgnoreRejectsEscapingParentBeforeGit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	g := &lazyGitIgnorer{ctx: context.Background(), git: "must-not-execute", dir: dir, cache: make(map[string]bool), environ: func() []string {
		calls++
		return nil
	}}
	if err := PrepareIgnore(g, "../outside", entries); err == nil || calls != 0 {
		t.Fatalf("escaping parent: commands=%d err=%v", calls, err)
	}
}
