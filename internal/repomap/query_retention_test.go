package repomap

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func retentionFixture(t *testing.T, check func(string) error) (*Manager, *workspace.Registry, *workspace.Session, string, string) {
	t.Helper()
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if check != nil {
		root = root.WithAccessCheck(check)
	}
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.db")
	m, err := New(t.Context(), Options{Path: path, Store: index.Options{MaxPins: 4}, Runner: &managerTestExtractor{run: extract.Extract}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	return m, registry, registry.NewSession(), root.Dir(), path
}

func requireRetainedPins(t *testing.T, store *index.Store, expected int) {
	t.Helper()
	var probes []index.Snapshot
	defer func() {
		for _, pin := range probes {
			if err := store.Release(t.Context(), pin.ID); err != nil {
				t.Error(err)
			}
		}
	}()
	for range store.Options().MaxPins - expected {
		pin, err := store.Pin(t.Context(), "r_probe", time.Minute)
		if err != nil {
			t.Fatalf("unexpected retained pin: %v", err)
		}
		probes = append(probes, pin)
	}
	if pin, err := store.Pin(t.Context(), "r_probe", time.Minute); !errors.Is(err, index.ErrCapacity) {
		if err == nil {
			probes = append(probes, pin)
		}
		t.Fatalf("expected %d retained pins: %v", expected, err)
	}
}

func TestNewQueryFailureReleasesPin(t *testing.T) {
	for _, mode := range []string{"encoding", "cancel-after-pin"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var current *Manager
			m, _, space, _, _ := retentionFixture(t, func(string) error {
				if current != nil && mode == "cancel-after-pin" {
					current.mu.Lock()
					pinned := len(current.queries) > 0
					current.mu.Unlock()
					if pinned {
						cancel()
					}
				}
				return nil
			})
			current = m
			budget := 1
			if mode == "cancel-after-pin" {
				budget = MaxOutputBytes
			}
			attempts := 1
			if mode == "encoding" {
				attempts = 2 * m.store.Options().MaxPins
			}
			for range attempts {
				if _, err := m.query(ctx, space, Request{Path: ".", Limit: 1}, budget); err == nil {
					t.Fatal("expected post-pin query failure")
				}
			}
			requireRetainedPins(t, m.store, 0)
		})
	}
}

func TestContinuationRetentionDependsOnFailure(t *testing.T) {
	transient := errors.New("temporary metadata failure")
	var failure error
	m, registry, space, dir, _ := retentionFixture(t, func(string) error { return failure })
	indexQueryFile(t, m, space, dir, "file.go", "package p; func First() {}; func Second() {}")
	first := runMap(t, &mapTool{manager: m, space: space}, `{"limit":1}`, MaxOutputBytes)
	if first.Page.NextCursor == "" || len(first.Entries) != 1 {
		t.Fatalf("first page: %+v", first)
	}
	request := Request{Cursor: first.Page.NextCursor}
	if _, err := m.query(t.Context(), registry.NewSession(), request, MaxOutputBytes); !errors.Is(err, index.ErrStale) {
		t.Fatalf("cross-session cursor: %v", err)
	}
	requireRetainedPins(t, m.store, 1)
	failure = transient
	if _, err := m.query(t.Context(), space, request, MaxOutputBytes); !errors.Is(err, transient) {
		t.Fatalf("retryable failure became stale: %v", err)
	}
	requireRetainedPins(t, m.store, 1)
	failure = nil
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.query(canceled, space, request, MaxOutputBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled continuation: %v", err)
	}
	requireRetainedPins(t, m.store, 1)
	payload, err := m.query(t.Context(), space, request, MaxOutputBytes)
	if err != nil {
		t.Fatal(err)
	}
	var second Response
	if err := json.Unmarshal(payload, &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].ID == first.Entries[0].ID {
		t.Fatalf("retry lost or repeated a declaration: %+v", second)
	}
	failure = tool.ErrDenied
	if _, err := m.query(t.Context(), space, Request{Cursor: second.Page.NextCursor}, MaxOutputBytes); !errors.Is(err, index.ErrStale) {
		t.Fatalf("revoked cursor: %v", err)
	}
	failure = nil
	requireRetainedPins(t, m.store, 0)
}

func TestManagerCloseReleasesOnlyOwnedPins(t *testing.T) {
	m, _, space, dir, path := retentionFixture(t, nil)
	indexQueryFile(t, m, space, dir, "file.go", "package p; func First() {}; func Second() {}")
	first := runMap(t, &mapTool{manager: m, space: space}, `{"limit":1}`, MaxOutputBytes)
	if first.Page.NextCursor == "" {
		t.Fatal("fixture did not retain a cursor")
	}
	other, err := index.Open(path, index.Options{MaxPins: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	})
	foreign, err := other.Pin(t.Context(), "r_other", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Scan(t.Context(), foreign, index.Query{}, nil, 1, 8192); err != nil {
		t.Fatalf("shutdown deleted another owner's reservation: %v", err)
	}
	requireRetainedPins(t, other, 1)
}

func TestQueryAdmissionFailureAfterPinReleasesReservation(t *testing.T) {
	m, _, space, _, _ := retentionFixture(t, nil)
	if err := m.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := m.query(ctx, space, Request{Path: ".", Limit: 1}, MaxOutputBytes); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked admission: %v", err)
	}
	requireRetainedPins(t, m.store, 0)
}

func TestCloseWaitsForPostPinCleanup(t *testing.T) {
	started, resume := make(chan struct{}), make(chan struct{})
	var blockOnce, resumeOnce sync.Once
	defer resumeOnce.Do(func() { close(resume) })
	var current *Manager
	m, _, space, _, path := retentionFixture(t, func(string) error {
		if current != nil {
			current.mu.Lock()
			pinned := len(current.queries) > 0
			current.mu.Unlock()
			if pinned {
				blockOnce.Do(func() { close(started); <-resume })
			}
		}
		return nil
	})
	current = m
	other, err := index.Open(path, index.Options{MaxPins: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := other.Close(); err != nil {
			t.Error(err)
		}
	})
	queryDone := make(chan error, 1)
	go func() {
		_, err := m.query(t.Context(), space, Request{Path: ".", Limit: 1}, MaxOutputBytes)
		queryDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("query did not reach post-pin scope check")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- m.Close() }()
	<-m.ctx.Done()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before query cleanup: %v", err)
	default:
	}
	resumeOnce.Do(func() { close(resume) })
	if err := <-queryDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("query during shutdown: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	requireRetainedPins(t, other, 0)
}
