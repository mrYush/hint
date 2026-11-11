//go:build !windows

// Package processtree runs commands with cancellation of their descendants.
package processtree

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Run runs a command created with exec.CommandContext in its own process group.
// Cancellation kills that group; background processes may survive normal exit.
func Run(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	return cmd.Run()
}
