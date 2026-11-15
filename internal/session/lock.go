package session

import (
	"errors"
	"fmt"
	"os"
)

// lockPath is the file whose lock guards path for writing. The lock sits
// beside the session instead of on it so that a reader can still read a
// session another process is recording: a Windows byte-range lock on the
// session file itself would block the reader, and a copy taken for a fork
// has to see the records as they stand.
func lockPath(path string) string { return path + ".lock" }

// lockSession opens path's lock file and takes an exclusive lock on it
// without waiting. The returned file holds the lock until it is closed.
// A session another hint process has open is [ErrLocked].
func lockSession(path string) (*os.File, error) {
	f, err := os.OpenFile(lockPath(path), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open lock for %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrLocked) {
			return nil, fmt.Errorf("%w: %s", ErrLocked, path)
		}
		return nil, err
	}
	return f, nil
}
