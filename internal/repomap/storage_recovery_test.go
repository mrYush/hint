package repomap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func TestDiscoveryQuotaPreservesWorkAndCursorThenRecovers(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(t.TempDir(), "index.db")
	store, err := index.Open(catalogPath, index.Options{MaxBytes: 512 << 10})
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	var parses atomic.Int32
	runner := &managerTestExtractor{run: func(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
		parses.Add(1)
		return extract.Extract(ctx, source, limits)
	}}
	m := &Manager{ctx: t.Context(), store: store, runner: runner, now: func() time.Time { return time.Unix(0, now.Load()) },
		slot: make(chan struct{}, 1), wake: make(chan struct{}, 1), roots: make(map[string]*rootWork), queries: make(map[string]*savedQuery), streams: make(map[string]*directoryStream)}
	t.Cleanup(func() {
		for _, stream := range m.streams {
			if err := stream.file.Close(); err != nil {
				t.Error(err)
			}
		}
		if err := errors.Join(m.releaseOwnedQueries(), store.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	space := registry.NewSession()
	m.Track(space)
	indexQueryFile(t, m, space, root.Dir(), "keep.go", "package p; func A() {}; func B() {}")
	indexQueryFile(t, m, space, root.Dir(), "change.go", "package p; func Small() {}")
	roots, _ := space.Snapshot()
	id := roots[0].ID
	// A different connection holds the actual SQLite writer lock; cooldown
	// must skip database work, then recover after the lock is released.
	other, err := sql.Open("sqlite", catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	}()
	connection, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := connection.ExecContext(t.Context(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			if _, err := connection.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Error(err)
			}
		}
	}()
	err = m.backgroundSlice(t.Context(), *m.roots[id])
	if !errors.Is(err, index.ErrBusy) {
		t.Fatalf("contention: %v", err)
	}
	m.recordWorkFailure(id, err)
	if m.roots[id].status.Reason != "storage_busy" {
		t.Fatal(m.roots[id].status)
	}
	for range 20 {
		if err := m.backgroundSlice(t.Context(), *m.roots[id]); err != nil {
			t.Fatalf("cooldown touched locked store: %v", err)
		}
	}
	if _, err := connection.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	locked = false
	now.Add(int64(2 * time.Second))
	initial, err := store.QueueLookup(t.Context(), id, []byte("."))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Ack(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	target := tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: 2048})
	first := runMap(t, target, `{"path":"keep.go","limit":1}`, 2048)
	if first.Page.NextCursor == "" {
		t.Fatal("missing retained cursor")
	}
	var source strings.Builder
	source.WriteString("package p; func Large(")
	for i := range 1400 {
		if i > 0 {
			source.WriteString(",")
		}
		fmt.Fprintf(&source, "p%d int", i)
	}
	source.WriteString(") {}")
	managerWrite(t, filepath.Join(root.Dir(), "change.go"), source.String())
	if err := store.Enqueue(t.Context(), id, []byte("change.go")); err != nil {
		t.Fatal(err)
	}
	filled := 0
	for i := range 2000 {
		err := store.Enqueue(t.Context(), "r_discovery", []byte(fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 512))))
		if errors.Is(err, index.ErrCapacity) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		filled++
	}
	if filled == 0 || filled == 2000 {
		t.Fatalf("frontier did not exhaust quota: %d", filled)
	}
	m.roots[id].sweepAt = m.now().Add(time.Hour)
	work := *m.roots[id]
	err = m.backgroundSlice(t.Context(), work)
	if !errors.Is(err, index.ErrCapacity) {
		t.Fatalf("expected pressure from discovery: %v", err)
	}
	m.recordWorkFailure(id, err)
	if got := m.roots[id].status; got.State != "paused" || got.Reason != "storage_capacity" {
		t.Fatalf("false completion: %+v", got)
	}
	if _, err := store.QueueLookup(t.Context(), id, []byte("change.go")); err != nil {
		t.Fatalf("lost task: %v", err)
	}
	before := parses.Load()
	for range 20 {
		if err := m.backgroundSlice(t.Context(), work); err != nil {
			t.Fatal(err)
		}
	}
	if parses.Load() != before {
		t.Fatalf("quota retry loop: %d extra parses", parses.Load()-before)
	}
	args, _ := json.Marshal(Request{Cursor: first.Page.NextCursor})
	next := runMap(t, target, string(args), 2048)
	if len(next.Entries) != 1 || next.Entries[0].Name != "B" {
		t.Fatalf("retained cursor damaged: %+v", next)
	}
	// Acknowledge completed discovery tasks from the competing root to make
	// actual pages reusable; no cursor/source records are evicted for progress.
	for {
		page, err := store.Take(t.Context(), "r_discovery", 16)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			if err := store.Ack(t.Context(), item); err != nil {
				t.Fatal(err)
			}
		}
	}
	now.Add(int64(6 * time.Second))
	for range 10 {
		if err := m.backgroundSlice(t.Context(), work); err != nil {
			t.Fatal(err)
		}
	}
	file, err := store.EntryLookup(t.Context(), id, index.EntryID(id, "change.go"))
	if err != nil || file.State != index.StateIndexed || file.Hash == "" {
		t.Fatalf("did not recover: %+v %v", file, err)
	}
	if _, err := store.QueueLookup(t.Context(), id, []byte("change.go")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("work not completed: %v", err)
	}
	if m.roots[id].status.State != "ready" {
		t.Fatalf("not ready after recovery: %+v", m.roots[id].status)
	}
	if _, err := os.Stat(filepath.Join(root.Dir(), "keep.go")); err != nil {
		t.Fatal(err)
	}
}
