package repomap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

// linuxCgroupConstraints is independent of host APIs so malformed/limited
// hierarchies can be tested on every supported development platform.
func linuxCgroupConstraints(ctx context.Context, read func(string) (string, error), memory uint64, cpu, ioPressure float64) (uint64, float64, float64, error) {
	groups, err := read("/proc/self/cgroup")
	if err != nil {
		return 0, 0, 0, err
	}
	mounts, err := read("/proc/self/mountinfo")
	if err != nil {
		return 0, 0, 0, err
	}
	group, mount, err := unifiedCgroupLocation(groups, mounts)
	if err != nil {
		return 0, 0, 0, err
	}
	location := path.Join(mount, group)
	for depth := 0; ; depth++ {
		if depth >= 32 {
			return 0, 0, 0, errors.New("index telemetry: cgroup hierarchy exceeds observation budget")
		}
		if err := ctx.Err(); err != nil {
			return 0, 0, 0, err
		}
		maxValue, maxErr := read(path.Join(location, "memory.max"))
		if location == mount && maxErr == nil {
			// memory.max exists only on non-root groups. Seeing it at the
			// apparent mount root means a namespace can hide tighter ancestors.
			return 0, 0, 0, errors.New("index telemetry: cgroup namespace hides ancestor constraints")
		}
		// The actual unified hierarchy root is not subject to memory.max.
		// Missing constraints anywhere else are unknown, never host headroom.
		if maxErr != nil && (location != mount || !errors.Is(maxErr, os.ErrNotExist)) {
			return 0, 0, 0, fmt.Errorf("index telemetry: cgroup memory maximum: %w", maxErr)
		}
		if maxErr == nil {
			current, err := read(path.Join(location, "memory.current"))
			if err != nil {
				return 0, 0, 0, err
			}
			high, err := read(path.Join(location, "memory.high"))
			if err != nil {
				return 0, 0, 0, err
			}
			used, err := strconv.ParseUint(strings.TrimSpace(current), 10, 64)
			if err != nil {
				return 0, 0, 0, errors.New("index telemetry: invalid cgroup memory usage")
			}
			for _, raw := range []string{maxValue, high} {
				raw = strings.TrimSpace(raw)
				if raw == "max" {
					continue
				}
				limit, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					return 0, 0, 0, errors.New("index telemetry: invalid cgroup memory constraint")
				}
				headroom := uint64(0)
				if used < limit {
					headroom = limit - used
				}
				memory = min(memory, headroom)
			}
		}
		cpuText, cpuErr := read(path.Join(location, "cpu.pressure"))
		ioText, ioErr := read(path.Join(location, "io.pressure"))
		if err := errors.Join(cpuErr, ioErr); err != nil {
			return 0, 0, 0, err
		}
		groupCPU, cpuErr := linuxPressure(cpuText)
		groupIO, ioErr := linuxPressure(ioText)
		if err := errors.Join(cpuErr, ioErr); err != nil {
			return 0, 0, 0, err
		}
		cpu, ioPressure = max(cpu, groupCPU), max(ioPressure, groupIO)
		if location == mount {
			currentGroups, err := read("/proc/self/cgroup")
			if err != nil || currentGroups != groups {
				return 0, 0, 0, errors.Join(err, errors.New("index telemetry: cgroup membership changed during observation"))
			}
			return memory, cpu, ioPressure, nil
		}
		location = path.Dir(location)
	}
}

func unifiedCgroupLocation(groups, mounts string) (string, string, error) {
	group := ""
	for line := range strings.SplitSeq(groups, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[0] != "0" || parts[1] != "" || group != "" {
			return "", "", errors.New("index telemetry: unsupported legacy or ambiguous cgroup hierarchy")
		}
		group = parts[2]
	}
	if !path.IsAbs(group) || path.Clean(group) != group || strings.Contains(group, "\x00") {
		return "", "", errors.New("index telemetry: invalid cgroup path")
	}
	mount := ""
	for line := range strings.SplitSeq(mounts, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok || !strings.HasPrefix(right, "cgroup2 ") {
			continue
		}
		fields := strings.Fields(left)
		if len(fields) < 6 || fields[3] != "/" || mount != "" {
			return "", "", errors.New("index telemetry: cgroup ancestors are not fully visible")
		}
		mount = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
	}
	if !path.IsAbs(mount) || path.Clean(mount) != mount || strings.Contains(mount, "\x00") {
		return "", "", errors.New("index telemetry: unified cgroup mount unavailable")
	}
	return group, mount, nil
}
