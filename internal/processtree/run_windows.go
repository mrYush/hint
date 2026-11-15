//go:build windows

// Package processtree runs commands with cancellation of their descendants.
package processtree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Run runs a command created with exec.CommandContext in a kill-on-close job.
// The process starts suspended so no child can precede job assignment.
// Cancellation and normal return both terminate any remaining descendants.
func Run(cmd *exec.Cmd) (resultErr error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create process job: %w", err)
	}
	defer func() {
		if err := windows.CloseHandle(job); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close process job: %w", err))
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return fmt.Errorf("set process job limits: %w", err)
	}
	return runInJob(cmd, job)
}

func runInJob(cmd *exec.Cmd, job windows.Handle) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP

	// CommandContext may cancel as soon as Start returns, before assignment.
	// Hold cancellation until setup finishes so an empty job cannot miss it.
	var setup sync.Mutex
	cmd.Cancel = func() error {
		setup.Lock()
		defer setup.Unlock()
		if err := windows.TerminateJobObject(job, 1); err != nil {
			return fmt.Errorf("terminate process job: %w", err)
		}
		return nil
	}
	setup.Lock()
	err := startInJob(cmd, job)
	setup.Unlock()
	if err != nil {
		if cmd.Process == nil {
			return err
		}
		// Setup can fail after the thread was resumed (for example, closing
		// its handle). Terminate the job before waiting on inherited pipes.
		jobErr := windows.TerminateJobObject(job, 1)
		if jobErr != nil {
			jobErr = fmt.Errorf("terminate job after setup failure: %w", jobErr)
		}
		killErr := cmd.Process.Kill()
		if errors.Is(killErr, os.ErrProcessDone) {
			killErr = nil
		}
		waitErr := cmd.Wait()
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			// A killed process's exit is expected here. Including ExitError
			// would misclassify this setup failure as the command failing.
			waitErr = nil
		}
		return errors.Join(err, jobErr, killErr, waitErr)
	}
	return cmd.Wait()
}

func startInJob(cmd *exec.Cmd, job windows.Handle) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open process for job: %w", err)
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	if err := errors.Join(assignErr, windows.CloseHandle(process)); err != nil {
		return fmt.Errorf("assign process to job: %w", err)
	}
	if err := resumeThread(uint32(cmd.Process.Pid)); err != nil {
		return fmt.Errorf("resume process in job: %w", err)
	}
	return nil
}

func resumeThread(pid uint32) (resultErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for err = windows.Thread32First(snapshot, &entry); err == nil; err = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, resumeErr := windows.ResumeThread(thread)
		return errors.Join(resumeErr, windows.CloseHandle(thread))
	}
	return fmt.Errorf("suspended thread unavailable: %w", err)
}
