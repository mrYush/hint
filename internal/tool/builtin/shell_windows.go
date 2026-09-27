//go:build windows

package builtin

import (
	"context"
	"os/exec"
)

// shellCommand builds the command that runs script through cmd.exe.
func shellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "cmd.exe", "/C", script), nil
}

// killProcessTree kills the command's process. Windows has no process
// groups in the POSIX sense; children of the shell may outlive it.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
