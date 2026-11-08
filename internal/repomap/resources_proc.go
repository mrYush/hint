package repomap

import (
	"errors"
	"strconv"
	"strings"
)

func linuxAvailableMemory(data string) (uint64, error) {
	for line := range strings.SplitSeq(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) == 3 && parts[0] == "MemAvailable:" && parts[2] == "kB" {
			v, err := strconv.ParseUint(parts[1], 10, 54)
			if err == nil {
				return v * 1024, nil
			}
		}
	}
	return 0, errors.New("index telemetry: memory headroom unavailable")
}

func linuxPressure(data string) (float64, error) {
	for line := range strings.SplitSeq(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 || parts[0] != "some" {
			continue
		}
		for _, part := range parts[1:] {
			if raw, ok := strings.CutPrefix(part, "avg10="); ok {
				v, err := strconv.ParseFloat(raw, 64)
				if err == nil && v >= 0 && v <= 100 {
					return v / 100, nil
				}
			}
		}
	}
	return 0, errors.New("index telemetry: pressure unavailable")
}
