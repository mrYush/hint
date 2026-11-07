//go:build linux || darwin

package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool"
)

func TestLeaseStatFIFO(t *testing.T) {
	if dir := os.Getenv("HINT_TEST_STAT_FIFO_DIR"); dir != "" {
		root, err := tool.NewRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		registry, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := registry.Close(); err != nil {
				t.Error(err)
			}
		})
		if err := syscall.Mkfifo(filepath.Join(root.Dir(), "pipe"), 0600); err != nil {
			t.Fatal(err)
		}
		lease, err := registry.NewSession().Resolve("", "pipe")
		if err != nil {
			t.Fatal(err)
		}
		info, err := lease.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("FIFO metadata lost: %v", info.Mode())
		}
		return
	}
	// No writer opens the FIFO. Kill a blocked regression in a child process
	// so the test cannot strand a goroutine or hang filesystem cleanup.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLeaseStatFIFO$", "-test.count=1")
	cmd.Env = append(os.Environ(), "HINT_TEST_STAT_FIFO_DIR="+t.TempDir())
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("FIFO metadata blocked: %v\n%s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("FIFO subprocess: %v\n%s", err, output)
	}
}
