//go:build !windows

package builtin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func TestShellKillFallsBackWhenGroupIsMissing(t *testing.T) {
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
		if cmd.Process != nil && cmd.ProcessState == nil {
			var exit *exec.ExitError
			if err := cmd.Wait(); err != nil && !errors.As(err, &exit) {
				t.Error(err)
			}
		}
	})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// This child inherits our group rather than leading one, so the group kill
	// returns ESRCH while the child itself is still alive and needs the fallback.
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == cmd.Process.Pid {
		t.Fatal("test child unexpectedly leads a process group")
	}
	if err := killProcessTree(cmd); err != nil {
		t.Fatalf("shell fallback: %v", err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) {
		t.Fatalf("child was not killed by the fallback: %v", err)
	}
}

func TestShellKillBeforeStart(t *testing.T) {
	if err := killProcessTree(&exec.Cmd{}); err != nil {
		t.Fatalf("unstarted shell cancellation: %v", err)
	}
}
