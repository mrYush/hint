package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCloneValidationNeverTouchesExistingDestination(t *testing.T) {
	for _, source := range []string{"https://token@example.com/a.git", "https://example.com/a?token=secret", "ssh://git:secret@example.com/a.git", "ext::evil", "--upload-pack=evil", "/local/path"} {
		destination := filepath.Join(t.TempDir(), "new")
		if err := cloneRepo(context.Background(), source, destination, os.Environ()); err == nil {
			t.Fatalf("unsafe URL accepted: %q", source)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid URL created destination: %v", err)
		}
	}
	destination := t.TempDir()
	sentinel := filepath.Join(destination, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cloneRepo(context.Background(), "https://example.invalid/repo.git", destination, os.Environ()); err == nil {
		t.Fatal("existing destination accepted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("existing data changed: %q %v", data, err)
	}
}

func TestCloneArgvEnvironmentAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable is a POSIX script")
	}
	bin := t.TempDir()
	capture := filepath.Join(bin, "args")
	executable := filepath.Join(bin, "git")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE\"\nif [ -n \"$GIT_CONFIG_COUNT\" ]; then exit 99; fi\nexit 7\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	source := "https://example.invalid/a;echo-untrusted.git"
	destination := filepath.Join(t.TempDir(), "new")
	err := cloneRepo(context.Background(), source, destination, append(os.Environ(), "CAPTURE="+capture, "GIT_CONFIG_COUNT=1"))
	if err == nil || strings.Contains(err.Error(), source) {
		t.Fatalf("clone diagnostic leaked source or succeeded: %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "--no-recurse-submodules\n--\n"+source+"\n"+destination) {
		t.Fatalf("unsafe argv: %q", data)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatalf("failed destination removed: %v", err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	err = cloneRepo(ctx, "ssh://git@example.invalid/repo.git", filepath.Join(t.TempDir(), "new"), os.Environ())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("clone cancellation: %v", err)
	}
}
