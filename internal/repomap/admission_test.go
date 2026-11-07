package repomap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
)

func TestOversizedDeclarationBecomesDurableOmission(t *testing.T) {
	var parsed atomic.Int32
	runner := &managerTestExtractor{run: func(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
		parsed.Add(1)
		return extract.Extract(ctx, source, limits)
	}}
	m, space, dir := managerFixture(t, runner)
	managerWrite(t, filepath.Join(dir, "huge.go"), largeSignatureSource())
	roots, _ := space.Snapshot()
	id := roots[0].ID
	for range 2 {
		m.mu.Lock()
		work := *m.roots[id]
		m.mu.Unlock()
		if err := m.acquire(t.Context()); err != nil {
			t.Fatal(err)
		}
		err := m.step(t.Context(), work)
		m.release()
		if err != nil {
			t.Fatalf("oversized declaration kept failing discovery: %v", err)
		}
	}
	file, err := m.store.EntryLookup(t.Context(), id, index.EntryID(id, "huge.go"))
	if err != nil || file.State != index.StateOmitted || file.Reason != "output_budget_exceeded" {
		t.Fatalf("oversized declaration omission: %+v %v", file, err)
	}
	if _, err := m.store.QueueLookup(t.Context(), id, []byte("huge.go")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("completed omission stayed queued: %v", err)
	}
	lease, err := space.Resolve(id, "huge.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if parsed.Load() != 1 {
		t.Fatalf("unchanged rejected source reparsed: %d", parsed.Load())
	}
	managerWrite(t, filepath.Join(dir, "huge.go"), "package p; func Small() {}")
	if err := m.refresh(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	file, err = m.store.EntryLookup(t.Context(), id, index.EntryID(id, "huge.go"))
	if err != nil || file.State != index.StateIndexed || parsed.Load() != 2 {
		t.Fatalf("smaller replacement did not recover: %+v %v parses=%d", file, err, parsed.Load())
	}
}

func largeSignatureSource() string {
	var source strings.Builder
	source.WriteString("package p; func Huge(")
	for i := range 7000 {
		fmt.Fprintf(&source, "p%04d int,", i)
	}
	source.WriteString(") {}")
	return source.String()
}

func TestCatalogBudgetsBoundExtractionAndCacheOmissions(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		options      index.Options
	}{
		{"declarations", "work_budget_exceeded", index.Options{MaxSourceSymbols: 1}},
		{"source-bytes", "output_budget_exceeded", index.Options{MaxRecordBytes: 1024, MaxSourceBytes: 1024}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var parsed atomic.Int32
			runner := &managerTestExtractor{run: func(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
				parsed.Add(1)
				if test.options.MaxSourceSymbols > 0 && limits.MaxDeclarations > test.options.MaxSourceSymbols || test.options.MaxSourceBytes > 0 && limits.MaxOutputBytes > test.options.MaxSourceBytes {
					return extract.Result{}, errors.New("extractor received limits above catalog budgets")
				}
				return extract.Extract(ctx, source, limits)
			}}
			m, space, dir := managerFixtureWithStore(t, runner, t.TempDir(), test.options)
			managerWrite(t, filepath.Join(dir, "file.go"), "package p; func A() {}; func B() {}; func C() {}; func D() {}")
			lease, err := space.Resolve("", "file.go")
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := m.refresh(t.Context(), lease); err != nil {
					t.Fatal(err)
				}
			}
			file, err := m.store.EntryLookup(t.Context(), lease.RootID, index.EntryID(lease.RootID, "file.go"))
			if err != nil || file.State != index.StateOmitted || file.Reason != test.reason || parsed.Load() != 1 {
				t.Fatalf("bounded omission: %+v %v parses=%d", file, err, parsed.Load())
			}
		})
	}
}

func TestIncreasedCatalogBudgetInvalidatesCachedOmission(t *testing.T) {
	m, _, space, dir, path := retentionFixture(t, nil)
	managerWrite(t, filepath.Join(dir, "huge.go"), largeSignatureSource())
	lease, err := space.Resolve("", "huge.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	oldPolicy := m.sourcePolicy("go")
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := New(t.Context(), Options{Path: path, Store: index.Options{MaxRecordBytes: 128 << 10, MaxPins: 4}, Runner: &managerTestExtractor{run: extract.Extract}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	})
	next.Track(space)
	if err := next.refresh(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	file, err := next.store.EntryLookup(t.Context(), lease.RootID, index.EntryID(lease.RootID, "huge.go"))
	if err != nil || file.State != index.StateIndexed || file.Policy == oldPolicy {
		t.Fatalf("larger catalog budget retained old omission: %+v %v", file, err)
	}
}
