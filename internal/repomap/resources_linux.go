//go:build linux

package repomap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func localResourceSampler(catalogPath string) ResourceSampler {
	return func(ctx context.Context) (ResourceSample, error) { return sampleLinuxResources(ctx, catalogPath) }
}

func sampleLinuxResources(ctx context.Context, catalogPath string) (ResourceSample, error) {
	if err := ctx.Err(); err != nil {
		return ResourceSample{}, err
	}
	mem, err := readResourceFile("/proc/meminfo")
	if err != nil {
		return ResourceSample{}, err
	}
	available, err := linuxAvailableMemory(mem)
	if err != nil {
		return ResourceSample{}, err
	}
	cpuData, cpuErr := readResourceFile("/proc/pressure/cpu")
	ioData, ioErr := readResourceFile("/proc/pressure/io")
	if err := errors.Join(cpuErr, ioErr); err != nil {
		return ResourceSample{}, err
	}
	cpu, cpuErr := linuxPressure(cpuData)
	ioPressure, ioErr := linuxPressure(ioData)
	if err := errors.Join(cpuErr, ioErr); err != nil {
		return ResourceSample{}, err
	}
	available, cpu, ioPressure, err = linuxCgroupConstraints(ctx, readResourceFile, available, cpu, ioPressure)
	if err != nil {
		return ResourceSample{}, err
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(catalogPath), &fs); err != nil {
		return ResourceSample{}, fmt.Errorf("index telemetry: disk: %w", err)
	}
	return ResourceSample{At: time.Now(), Known: true, AvailableMemory: available, FreeDisk: fs.Bavail * uint64(fs.Bsize), CPU: cpu, IO: ioPressure}, ctx.Err()
}

func readResourceFile(path string) (data string, resultErr error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("index telemetry: read counters: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10+1))
	if err != nil || len(b) > 64<<10 {
		return "", errors.New("index telemetry: unavailable bounded counters")
	}
	return string(b), nil
}
