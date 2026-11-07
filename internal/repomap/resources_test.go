package repomap

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestResourceParsingRejectsMissingAndNonfiniteCounters(t *testing.T) {
	vm := "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 2.\nPages inactive: 3.\nPages speculative: 1.\n"
	if got, err := darwinAvailableMemory(vm); err != nil || got != 6*16384 {
		t.Fatalf("memory=%d,%v", got, err)
	}
	if _, err := darwinAvailableMemory(strings.ReplaceAll(vm, "Pages inactive", "Pages unknown")); err == nil {
		t.Fatal("missing memory became zero")
	}
	cpu := "cpu\nus sy id\n10 20 70\n50 40 10\n"
	if got, err := darwinCPU(cpu); err != nil || got != .9 {
		t.Fatalf("current CPU=%f,%v", got, err)
	}
	for _, bad := range []string{"us sy id\n10 20 70\n", "0 0 100\nNaN 0 100\n", "0 0 100\n-1 0 101\n", "0 0 100\n0 0 200\n"} {
		if _, err := darwinCPU(bad); err == nil {
			t.Errorf("accepted CPU %q", bad)
		}
	}
	io := "+-o Driver <class Driver, id 0x123, active>\n\"Statistics\" = {\"Total Time (Read)\"=200,\"Total Time (Write)\"=300}\n"
	got, err := darwinDiskCounters(io)
	if err != nil || got["0x123"] != 500 {
		t.Fatalf("disk=%v,%v", got, err)
	}
	if _, err := darwinDiskCounters(strings.ReplaceAll(io, "Total Time (Read)", "Latency Time (Read)")); err == nil {
		t.Fatal("missing I/O times became zero")
	}
	if _, err := diskOccupancy(nil, got, time.Second); err == nil {
		t.Fatal("first sample became known idle")
	}
	if _, err := diskOccupancy(map[string]uint64{"old": 0}, got, time.Second); err == nil {
		t.Fatal("changed device set became known")
	}
	if _, err := diskOccupancy(map[string]uint64{"0x123": 501}, got, time.Second); err == nil {
		t.Fatal("counter reset became known")
	}
	if value, err := diskOccupancy(map[string]uint64{"0x123": 0}, map[string]uint64{"0x123": 500_000_000}, time.Second); err != nil || value != .5 {
		t.Fatalf("occupancy=%f,%v", value, err)
	}
}

func TestExperimentalProfileRequiresExplicitOn(t *testing.T) {
	now := time.Now()
	g := resourceGovernor{mode: ModeAuto, profile: ExperimentalResourceProfile(), sample: ResourceSample{At: now, Known: true, AvailableMemory: 2 << 30, FreeDisk: 4 << 30}}
	if ok, why := g.admit(now); ok || why != "resource_profile_unvalidated" {
		t.Fatalf("auto=%t,%s", ok, why)
	}
	g.mode = ModeOn
	if ok, _ := g.admit(now); ok {
		t.Fatal("manual on bypassed recovery")
	}
	g.sample.At = now.Add(15 * time.Second)
	if ok, why := g.admit(g.sample.At); !ok {
		t.Fatalf("manual profile never admitted: %s", why)
	}
	g.sample.IO = math.NaN()
	if ok, why := g.admit(g.sample.At); ok || why != "telemetry_unavailable" {
		t.Fatalf("manual mode bypassed telemetry: %t,%s", ok, why)
	}
}

func TestSamplerCancellationAndDiagnosticStatus(t *testing.T) {
	started := make(chan struct{})
	m, err := New(context.Background(), Options{Path: filepath.Join(t.TempDir(), "index.db"), Mode: ModeOn,
		Sampler: func(ctx context.Context) (ResourceSample, error) {
			close(started)
			<-ctx.Done()
			return ResourceSample{}, ctx.Err()
		}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sampler did not start")
	}
	if text := m.StatusText(); !strings.Contains(text, "experimental") || !strings.Contains(text, "known=false") {
		t.Fatalf("diagnostics=%s", text)
	}
	release := m.Inference()
	release()
	if err := m.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLocalResourceSamplerSmoke(t *testing.T) {
	if os.Getenv("HINT_INDEX_TELEMETRY_SMOKE") != "1" {
		t.Skip("set HINT_INDEX_TELEMETRY_SMOKE=1 for real local platform counters")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("this platform deliberately has no background telemetry adapter")
	}
	sample := LocalResourceSampler(filepath.Join(t.TempDir(), "catalog.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// Darwin needs the first device counter observation before it can compute a delta.
	_, _ = sample(ctx)
	got, err := sample(ctx)
	if err != nil || !got.Known || got.At.IsZero() {
		t.Fatalf("local telemetry unavailable: %+v, %v", got, err)
	}
	t.Logf("memory_headroom_bytes=%d disk_headroom_bytes=%d cpu=%.4f io=%.4f", got.AvailableMemory, got.FreeDisk, got.CPU, got.IO)
}

func TestManagerMapCatalogRequiresExplicitEnable(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	for _, tc := range []struct {
		mode Mode
		want bool
	}{{ModeAuto, false}, {ModeOn, true}, {ModeOff, false}} {
		if err := m.SetMode(tc.mode); err != nil {
			t.Fatal(err)
		}
		found, list := false, false
		for _, tool := range m.Tools(space) {
			found = found || tool.Name() == "repo_map"
			list = list || tool.Name() == "repo_list"
		}
		if found != tc.want || !list {
			t.Fatalf("mode=%s map=%t list=%t", tc.mode, found, list)
		}
	}
}
