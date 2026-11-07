package repomap

import (
	"context"
	"os"
	"strings"
	"testing"
)

func cgroupFixture() map[string]string {
	m := map[string]string{
		"/proc/self/cgroup":    "0::/batch/worker\n",
		"/proc/self/mountinfo": "1 0 0:1 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
	}
	for _, dir := range []string{"/sys/fs/cgroup", "/sys/fs/cgroup/batch", "/sys/fs/cgroup/batch/worker"} {
		m[dir+"/cpu.pressure"] = "some avg10=0.00 avg60=0.00 total=0\n"
		m[dir+"/io.pressure"] = "some avg10=0.00 avg60=0.00 total=0\n"
	}
	m["/sys/fs/cgroup/batch/memory.max"] = "2000\n"
	m["/sys/fs/cgroup/batch/memory.current"] = "1975\n"
	m["/sys/fs/cgroup/batch/memory.high"] = "max\n"
	m["/sys/fs/cgroup/batch/worker/memory.max"] = "1000\n"
	m["/sys/fs/cgroup/batch/worker/memory.current"] = "900\n"
	m["/sys/fs/cgroup/batch/worker/memory.high"] = "950\n"
	m["/sys/fs/cgroup/batch/worker/cpu.pressure"] = "some avg10=60.00 avg60=0.00 total=0\n"
	m["/sys/fs/cgroup/batch/io.pressure"] = "some avg10=75.00 avg60=0.00 total=0\n"
	return m
}

func fixtureReader(files map[string]string) func(string) (string, error) {
	return func(path string) (string, error) {
		value, ok := files[path]
		if !ok {
			return "", os.ErrNotExist
		}
		return value, nil
	}
}

func TestCgroupHeadroomIncludesParentsHighAndPressure(t *testing.T) {
	if memory, err := linuxAvailableMemory("MemTotal: 10000 kB\nMemAvailable: 5000 kB\n"); err != nil || memory != 5000*1024 {
		t.Fatalf("host headroom=%d,%v", memory, err)
	}
	if _, err := linuxAvailableMemory("MemAvailable: unknown kB"); err == nil {
		t.Fatal("unknown host memory became zero")
	}
	files := cgroupFixture()
	memory, cpu, ioPressure, err := linuxCgroupConstraints(context.Background(), fixtureReader(files), 5000, .1, .2)
	if err != nil || memory != 25 || cpu != .6 || ioPressure != .75 {
		t.Fatalf("constraints=%d,%f,%f,%v", memory, cpu, ioPressure, err)
	}
	files["/sys/fs/cgroup/batch/memory.max"] = "max"
	memory, _, _, err = linuxCgroupConstraints(context.Background(), fixtureReader(files), 5000, 0, 0)
	if err != nil || memory != 50 {
		t.Fatalf("memory.high was ignored: %d,%v", memory, err)
	}
	files["/sys/fs/cgroup/batch/worker/memory.current"] = "1001"
	memory, _, _, err = linuxCgroupConstraints(context.Background(), fixtureReader(files), 5000, 0, 0)
	if err != nil || memory != 0 {
		t.Fatalf("over-limit counter underflow: %d,%v", memory, err)
	}
}

func TestCgroupUnsupportedOrMalformedNeverUsesHostCapacity(t *testing.T) {
	for _, tc := range []struct{ name, path, value string }{
		{"v1", "/proc/self/cgroup", "5:memory:/batch\n"},
		{"hybrid", "/proc/self/cgroup", "0::/batch/worker\n5:cpu:/batch\n"},
		{"escape", "/proc/self/cgroup", "0::/../../outside\n"},
		{"hidden ancestors", "/proc/self/mountinfo", "1 0 0:1 /hidden /sys/fs/cgroup rw - cgroup2 cgroup rw\n"},
		{"namespace root", "/sys/fs/cgroup/memory.max", "1000"},
		{"invalid limit", "/sys/fs/cgroup/batch/worker/memory.max", "NaN"},
		{"invalid usage", "/sys/fs/cgroup/batch/worker/memory.current", "-1"},
		{"invalid pressure", "/sys/fs/cgroup/batch/worker/cpu.pressure", "some avg10=NaN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := cgroupFixture()
			files[tc.path] = tc.value
			if memory, _, _, err := linuxCgroupConstraints(context.Background(), fixtureReader(files), 5000, 0, 0); err == nil || memory != 0 {
				t.Fatalf("unknown used host capacity: %d,%v", memory, err)
			}
		})
	}
	files := cgroupFixture()
	delete(files, "/sys/fs/cgroup/batch/worker/memory.high")
	if _, _, _, err := linuxCgroupConstraints(context.Background(), fixtureReader(files), 5000, 0, 0); err == nil {
		t.Fatal("missing group constraint was ignored")
	}
}

func TestCgroupReadBudgetAndMembershipChange(t *testing.T) {
	files := cgroupFixture()
	files["/proc/self/cgroup"] = "0::/" + strings.Repeat("a/", 40) + "end\n"
	reads := 0
	reader := func(path string) (string, error) {
		reads++
		if value, ok := files[path]; ok {
			return value, nil
		}
		switch {
		case strings.HasSuffix(path, "memory.current"):
			return "0", nil
		case strings.HasSuffix(path, "memory.max"), strings.HasSuffix(path, "memory.high"):
			return "max", nil
		default:
			return "some avg10=0", nil
		}
	}
	if _, _, _, err := linuxCgroupConstraints(context.Background(), reader, 5000, 0, 0); err == nil || reads > 2+32*5 {
		t.Fatalf("unbounded hierarchy reads=%d,%v", reads, err)
	}
	files = cgroupFixture()
	groupReads := 0
	changing := func(path string) (string, error) {
		if path == "/proc/self/cgroup" {
			groupReads++
			if groupReads > 1 {
				return "0::/other\n", nil
			}
		}
		return fixtureReader(files)(path)
	}
	if _, _, _, err := linuxCgroupConstraints(context.Background(), changing, 5000, 0, 0); err == nil {
		t.Fatal("membership change was not detected")
	}
}
