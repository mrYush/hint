package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// BenchmarkMetadata requires an explicit dataset size so ordinary tests and
// benchmark discovery never create a million-entry catalog accidentally.
// Example: HINT_INDEX_BENCH_ENTRIES=10000 go test ./internal/index -run '^$'
// -bench BenchmarkMetadata -benchtime=20x -benchmem.
func BenchmarkMetadata(b *testing.B) {
	value := os.Getenv("HINT_INDEX_BENCH_ENTRIES")
	if value == "" {
		b.Skip("set HINT_INDEX_BENCH_ENTRIES to 10000, 100000 or 1000000")
	}
	entries, err := strconv.Atoi(value)
	if err != nil || (entries != 10_000 && entries != 100_000 && entries != 1_000_000) {
		b.Fatal("HINT_INDEX_BENCH_ENTRIES must be 10000, 100000 or 1000000")
	}
	path := filepath.Join(b.TempDir(), "catalog.sqlite")
	s, err := Open(path, Options{MaxBytes: 8 << 30})
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	}()
	ctx := context.Background()
	started := time.Now()
	for i := range entries {
		file, symbols := testSource(fmt.Sprintf("src/file-%07d.go", i), fmt.Sprintf("Function%07d", i))
		if _, err := s.PutSource(ctx, file, symbols); err != nil {
			b.Fatalf("build entry %d: %v", i, err)
		}
		if (i+1)%(entries/10) == 0 {
			b.Logf("catalog build: %d/%d entries in %s", i+1, entries, time.Since(started).Round(time.Millisecond))
		}
	}
	buildTime := time.Since(started)
	buildNS := float64(buildTime.Nanoseconds()) / float64(entries)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	snapshot, err := s.Pin(ctx, "r_test", time.Minute)
	if err != nil {
		b.Fatal(err)
	}
	query := Query{Order: NameOrder, Name: fmt.Sprintf("Function%07d", entries-1), NameMatch: MatchPrefix}
	// Loop resets the query timer and allocations once, without rebuilding
	// the million-entry fixture during benchmark calibration.
	for b.Loop() {
		page, err := s.Scan(ctx, snapshot, query, nil, 16, 64<<10)
		if err != nil || page.Examined != 1 || len(page.Records) != 1 {
			b.Fatalf("bounded rare-prefix query: %+v, %v", page, err)
		}
	}
	b.StopTimer()
	b.ReportMetric(buildNS, "build-ns/entry")
	b.ReportMetric(buildTime.Seconds(), "build-seconds")
	b.ReportMetric(float64(info.Size()), "db-bytes")
	b.ReportMetric(float64(info.Size())/float64(entries), "db-bytes/entry")
	b.ReportMetric(float64(entries), "entries")
}
