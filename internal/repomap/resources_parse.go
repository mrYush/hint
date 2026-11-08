package repomap

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	darwinPageSize = regexp.MustCompile(`page size of ([0-9]+) bytes`)
	darwinDiskID   = regexp.MustCompile(`, id (0x[0-9a-fA-F]+),`)
	darwinReadNS   = regexp.MustCompile(`"Total Time \(Read\)"=([0-9]+)`)
	darwinWriteNS  = regexp.MustCompile(`"Total Time \(Write\)"=([0-9]+)`)
)

func darwinAvailableMemory(data string) (uint64, error) {
	size := darwinPageSize.FindStringSubmatch(data)
	if len(size) != 2 {
		return 0, errors.New("index telemetry: page size unavailable")
	}
	pageSize, err := strconv.ParseUint(size[1], 10, 32)
	if err != nil || pageSize == 0 {
		return 0, errors.New("index telemetry: invalid page size")
	}
	values := make(map[string]uint64, 3)
	for line := range strings.SplitSeq(data, "\n") {
		name, raw, ok := strings.Cut(line, ":")
		if !ok || (name != "Pages free" && name != "Pages inactive" && name != "Pages speculative") {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(raw), "."), 10, 48)
		if err != nil {
			return 0, errors.New("index telemetry: invalid memory counter")
		}
		values[name] = value
	}
	if len(values) != 3 {
		return 0, errors.New("index telemetry: incomplete memory counters")
	}
	pages := values["Pages free"] + values["Pages inactive"] + values["Pages speculative"]
	if pages > math.MaxUint64/pageSize {
		return 0, errors.New("index telemetry: memory counter overflow")
	}
	return pages * pageSize, nil
}

func darwinCPU(data string) (float64, error) {
	var samples []float64
	for line := range strings.SplitSeq(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		user, ue := strconv.ParseFloat(parts[0], 64)
		system, se := strconv.ParseFloat(parts[1], 64)
		idle, ie := strconv.ParseFloat(parts[2], 64)
		if ue != nil || se != nil || ie != nil {
			continue
		}
		if !(user >= 0 && user <= 100 && system >= 0 && system <= 100 && idle >= 0 && idle <= 100) || math.Abs(user+system+idle-100) > 2 {
			return 0, errors.New("index telemetry: invalid CPU counters")
		}
		samples = append(samples, (100-idle)/100)
	}
	if len(samples) != 2 {
		return 0, errors.New("index telemetry: current CPU sample unavailable")
	}
	// The first iostat row is the uptime average, never current pressure.
	return samples[1], nil
}

func darwinDiskCounters(data string) (map[string]uint64, error) {
	result := make(map[string]uint64)
	id := ""
	for line := range strings.SplitSeq(data, "\n") {
		if match := darwinDiskID.FindStringSubmatch(line); len(match) == 2 {
			if id != "" {
				return nil, errors.New("index telemetry: missing device statistics")
			}
			id = match[1]
		}
		if !strings.Contains(line, `"Statistics"`) {
			continue
		}
		r, w := darwinReadNS.FindStringSubmatch(line), darwinWriteNS.FindStringSubmatch(line)
		if id == "" || len(r) != 2 || len(w) != 2 || len(result) >= 64 {
			return nil, errors.New("index telemetry: unavailable bounded device counters")
		}
		read, re := strconv.ParseUint(r[1], 10, 64)
		write, we := strconv.ParseUint(w[1], 10, 64)
		if re != nil || we != nil || read > math.MaxUint64-write {
			return nil, errors.New("index telemetry: invalid device counters")
		}
		if _, duplicate := result[id]; duplicate {
			return nil, errors.New("index telemetry: duplicate device")
		}
		result[id] = read + write
		id = ""
	}
	if id != "" || len(result) == 0 {
		return nil, errors.New("index telemetry: incomplete device counters")
	}
	return result, nil
}

func diskOccupancy(previous, current map[string]uint64, interval time.Duration) (float64, error) {
	if len(previous) == 0 || len(previous) != len(current) || interval <= 0 || interval > 15*time.Second {
		return 0, errors.New("index telemetry: device observation is not current")
	}
	var occupancy float64
	for id, count := range current {
		before, ok := previous[id]
		if !ok || count < before {
			return 0, errors.New("index telemetry: device set or counters changed")
		}
		occupancy += float64(count-before) / float64(interval)
	}
	// Parallel devices or overlapping requests can exceed elapsed wall time.
	// Saturating their sum is deliberately conservative and is not a device utilization claim.
	return min(1, occupancy), nil
}
