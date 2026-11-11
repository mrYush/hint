//go:build windows

package session

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on f without waiting. Closing the
// handle releases the lock, including when the process dies. The whole
// range is locked; f is the lock file, never a session file, so the lock
// does not hide the records from a reader.
func lockFile(f *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, ^uint32(0), ^uint32(0), &windows.Overlapped{},
	)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return ErrLocked
	default:
		return fmt.Errorf("session: lock %s: %w", f.Name(), err)
	}
}
