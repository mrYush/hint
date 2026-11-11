//go:build !windows

package processtree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestCancelGroupPreservesError(t *testing.T) {
	// A live child makes a fallback observable: killing only this process must
	// not turn a failed group kill into success for callers such as git clone.
	ctx, cancel := context.WithCancel(t.Context())
	cmd := exec.CommandContext(ctx, "sh", "-c", "read -r line")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
		if cmd.Process != nil {
			var exit *exec.ExitError
			if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
				t.Error(err)
			}
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	wrappedPermission := fmt.Errorf("group kill: %w", syscall.EPERM)
	for _, tt := range []struct {
		name    string
		killErr error
		want    error
	}{
		{"success", nil, nil},
		{"permission", syscall.EPERM, syscall.EPERM},
		{"wrapped permission", wrappedPermission, wrappedPermission},
		{"missing group", syscall.ESRCH, os.ErrProcessDone},
		{"wrapped missing group", fmt.Errorf("group kill: %w", syscall.ESRCH), os.ErrProcessDone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := cancelGroup(cmd, func(pid int, signal syscall.Signal) error {
				calls++
				if pid != -cmd.Process.Pid || signal != syscall.SIGKILL {
					t.Fatalf("kill(%d, %v), want group %d with SIGKILL", pid, signal, -cmd.Process.Pid)
				}
				return tt.killErr
			})
			if !errors.Is(err, tt.want) || calls != 1 {
				t.Fatalf("cancelGroup = %v, calls %d; want %v, one call", err, calls, tt.want)
			}
			if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatalf("group cancellation killed the main process: %v", err)
			}
		})
	}
}

func TestCancelGroupBeforeStart(t *testing.T) {
	err := cancelGroup(&exec.Cmd{}, func(int, syscall.Signal) error {
		t.Fatal("group kill called before the process started")
		return nil
	})
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("cancelGroup = %v, want os.ErrProcessDone", err)
	}
}
