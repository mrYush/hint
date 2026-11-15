package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestWriteSummaryCacheConcurrent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("an open reader can prevent replacing a cache entry on Windows")
	}
	path := filepath.Join(t.TempDir(), "summary.md")
	first, second := strings.Repeat("a", 64<<10), strings.Repeat("b", 64<<10)
	if err := writeSummaryCache(path, first); err != nil {
		t.Fatal(err)
	}

	var writers sync.WaitGroup
	for _, summary := range []string{first, second} {
		writers.Go(func() {
			for range 10 {
				if err := writeSummaryCache(path, summary); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	defer writers.Wait()
	for range 50 {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := string(data); got != first && got != second {
			t.Fatalf("read a partial or mixed summary (%d bytes)", len(data))
		}
	}
}

func TestWriteSummaryCacheCleansUpOnRenameFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.md")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeSummaryCache(path, "summary"); err == nil {
		t.Fatal("renaming over a directory succeeded")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "summary.md" || !files[0].IsDir() {
		t.Fatalf("cache directory contains %v, want only the original directory", files)
	}
}
