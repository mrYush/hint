//go:build windows

package processtree

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/D", "/C", "exit 0")
	if err := Run(cmd); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if cmd.Process != nil {
		t.Fatal("already-canceled command was started")
	}
}

func TestRunStartFailure(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), `Z:\hint-nonexistent\missing.exe`)
	if err := Run(cmd); err == nil || !strings.Contains(err.Error(), "start process") {
		t.Fatalf("Run = %v, want contextual startup failure", err)
	}
	if cmd.Process != nil {
		t.Fatal("missing executable started a process")
	}
}

func TestRunAssignmentFailureReapsProcess(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd.exe", "/D", "/C", "exit 0")
	// An invalid job forces the failure after Start, while cmd.exe is still
	// suspended. The runner must kill and reap it rather than leave it there.
	err := runInJob(cmd, windows.InvalidHandle)
	if err == nil || !strings.Contains(err.Error(), "assign process to job") {
		t.Fatalf("runInJob = %v, want assignment failure", err)
	}
	if cmd.Process == nil || cmd.ProcessState == nil {
		t.Fatal("failed setup left its process unreaped")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		t.Fatalf("setup failure was disguised as a command exit: %v", err)
	}
}

func TestRunCancellationDuringStartup(t *testing.T) {
	for i := range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, "cmd.exe", "/D", "/C", "ping -n 30 127.0.0.1 > NUL")
		// Vary when cancellation reaches Start/assignment/resume. The shell
		// must never wait for ping's natural exit after an early empty-job kill.
		timer := time.AfterFunc(time.Duration(i)*100*time.Microsecond, cancel)
		started := time.Now()
		err := Run(cmd)
		timer.Stop()
		cancel()
		if err == nil {
			t.Fatal("canceled command succeeded")
		}
		if time.Since(started) > 5*time.Second {
			t.Fatalf("startup cancellation missed the process: %s", time.Since(started))
		}
		if cmd.Process != nil && cmd.ProcessState == nil {
			t.Fatal("canceled process was not reaped")
		}
	}
}
