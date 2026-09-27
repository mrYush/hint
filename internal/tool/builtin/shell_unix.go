//go:build !windows

package builtin

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

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
	// A new process group, so that a timeout kills the pipeline the shell
	// spawned and not only the shell itself.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd, nil
}

// killProcessTree kills the command's whole process group.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
