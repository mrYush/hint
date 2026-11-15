//go:build !windows

package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

const shellDescription = "Use bash syntax (sh is the fallback when bash is unavailable)."
const shellWaitDelayNote = "[a background child process was detached]"

// shellCommand builds the command that runs script through the user's
// shell. bash is preferred for its ubiquity in the commands models write;
// sh is the POSIX fallback on minimal images.
func shellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	shell, err := exec.LookPath("bash")
	if err != nil {
		if shell, err = exec.LookPath("sh"); err != nil {
			return nil, fmt.Errorf("no shell found: %w", err)
		}
	}
	cmd := exec.CommandContext(ctx, shell, "-c", script)
	// A new process group lets cancellation kill pipelines and their children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd, nil
}

func runShellCommand(cmd *exec.Cmd) error {
	cmd.Cancel = func() error { return killProcessTree(cmd) }
	return cmd.Run()
}

// killProcessTree preserves the shell's fallback when its process group is gone
// or cannot be signaled. Clone's stricter cancellation is in processtree.Run.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
