//go:build darwin

package repomap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func localResourceSampler(catalogPath string) ResourceSampler {
	var mu sync.Mutex
	var previous map[string]uint64
	var previousAt time.Time
	return func(ctx context.Context) (ResourceSample, error) {
		mu.Lock()
		defer mu.Unlock()
		cpuData, err := resourceCommand(ctx, "/usr/sbin/iostat", "-dC", "-n", "0", "-w", "1", "-c", "2")
		if err != nil {
			return ResourceSample{}, err
		}
		cpu, err := darwinCPU(cpuData)
		if err != nil {
			return ResourceSample{}, err
		}
		vmData, err := resourceCommand(ctx, "/usr/bin/vm_stat")
		if err != nil {
			return ResourceSample{}, err
		}
		available, err := darwinAvailableMemory(vmData)
		if err != nil {
			return ResourceSample{}, err
		}
		pressure, err := unix.SysctlUint32("kern.memorystatus_vm_pressure_level")
		if err != nil {
			return ResourceSample{}, fmt.Errorf("index telemetry: memory pressure: %w", err)
		}
		if pressure != 1 {
			available = 0
		}
		ioData, err := resourceCommand(ctx, "/usr/sbin/ioreg", "-r", "-c", "IOBlockStorageDriver", "-d", "1", "-l")
		if err != nil {
			return ResourceSample{}, err
		}
		counters, err := darwinDiskCounters(ioData)
		if err != nil {
			return ResourceSample{}, err
		}
		now := time.Now()
		busy, err := diskOccupancy(previous, counters, now.Sub(previousAt))
		previous, previousAt = counters, now
		if err != nil {
			return ResourceSample{}, err
		}
		var fs unix.Statfs_t
		if err := unix.Statfs(filepath.Dir(catalogPath), &fs); err != nil {
			return ResourceSample{}, fmt.Errorf("index telemetry: disk: %w", err)
		}
		return ResourceSample{At: now, Known: true, AvailableMemory: available, FreeDisk: fs.Bavail * uint64(fs.Bsize), CPU: cpu, IO: busy}, ctx.Err()
	}
}

func resourceCommand(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "PATH=/usr/bin:/usr/sbin:/bin:/sbin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = 100 * time.Millisecond
	out := &resourceOutput{}
	cmd.Stdout, cmd.Stderr = out, io.Discard
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("index telemetry: platform counter command: %w", err)
	}
	return string(out.data), nil
}

type resourceOutput struct{ data []byte }

func (b *resourceOutput) Write(p []byte) (int, error) {
	if len(p) > (256<<10)-len(b.data) {
		return 0, os.ErrInvalid
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
