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
// A group-kill failure is returned without falling back to the main process.
func Run(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		return cancelGroup(cmd, syscall.Kill)
	}
	return cmd.Run()
}

func cancelGroup(cmd *exec.Cmd, kill func(int, syscall.Signal) error) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
