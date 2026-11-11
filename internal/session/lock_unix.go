//go:build !windows

package session

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f without waiting.
// Closing f releases the lock, including when the process dies.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
		return ErrLocked
	default:
		return fmt.Errorf("session: lock %s: %w", f.Name(), err)
	}
}
