//go:build windows

package builtin

import (
	"context"
	"os/exec"
	"syscall"
)

const shellDescription = "On Windows, use cmd.exe syntax, not POSIX bash syntax. Remaining background processes are terminated when the command finishes."
const shellWaitDelayNote = "[a background child process was terminated]"

// shellCommand builds the command that runs script through cmd.exe.
func shellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	// cmd.exe does not use CommandLineToArgvW quoting. /S removes exactly
	// the outer quotes around the script, preserving its own inner quotes.
	// /D disables AutoRun commands from the user's registry.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /D /S /C "` + script + `"`}
	return cmd, nil
}
