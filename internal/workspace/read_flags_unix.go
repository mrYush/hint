//go:build !windows

package workspace

import "syscall"

// The descriptor is checked before reading; FIFO replacement must not block open.
const regularOpenFlags = syscall.O_NONBLOCK
