package repomap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func TestFrontierTerminalAndRetryOutcomes(t *testing.T) {
	for _, mode := range []string{"denied", "unreadable", "nested", "offline"} {
		t.Run(mode, func(t *testing.T) {
			root, err := tool.NewRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root.Dir(), "blocked")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			managerWrite(t, filepath.Join(path, "keep.txt"), "retained")
			blocked := false
			root = root.WithAccessCheck(func(abs string) error {
				if blocked && mode == "offline" && abs == root.Dir() {
					return os.ErrPermission
				}
				if blocked && abs == path {
					if mode == "denied" {
						return tool.ErrDenied
					}
					if mode == "unreadable" {
						return os.ErrPermission
					}
				}
				return nil
			})
			registry, err := workspace.New(root)
			if err != nil {
				t.Fatal(err)
			}
			var now atomic.Int64
			now.Store(time.Now().UnixNano())
			clock := func() time.Time { return time.Unix(0, now.Load()) }
			store, err := index.Open(filepath.Join(t.TempDir(), "index.db"), index.Options{})
			if err != nil {
				t.Fatal(err)
			}
			// Drive work synchronously so the resource governor cannot mask
			// the discovery outcome with an unrelated paused status.
			m := &Manager{store: store, ctx: t.Context(), now: clock, roots: make(map[string]*rootWork),
				streams: make(map[string]*directoryStream), slot: make(chan struct{}, 1), wake: make(chan struct{}, 1)}
			t.Cleanup(func() {
				if err := errors.Join(store.Close(), registry.Close(), root.Close()); err != nil {
					t.Error(err)
				}
			})
			space := registry.NewSession()
			m.Track(space)
			roots, _ := space.Snapshot()
			id := roots[0].ID
			items, err := m.store.Take(t.Context(), id, 1)
			if err != nil || len(items) != 1 {
				t.Fatalf("initial frontier: %v %v", items, err)
			}
			if err := m.store.Ack(t.Context(), items[0]); err != nil {
				t.Fatal(err)
			}
			file := index.File{RootID: id, Path: []byte("blocked/keep.txt"), Kind: index.KindFile, State: index.StatePending}
			if _, err := m.store.PutSource(t.Context(), file, nil); err != nil {
				t.Fatal(err)
			}
			if err := m.store.Enqueue(t.Context(), id, []byte("blocked")); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			m.roots[id].sweepAt = clock().Add(time.Hour)
			if mode == "offline" {
				m.roots[id].sweepAt = time.Time{}
			}
			m.mu.Unlock()
			item, err := m.store.QueueLookup(t.Context(), id, []byte("blocked"))
			if err != nil {
				t.Fatal(err)
			}
			lease, err := space.Resolve(id, "blocked")
			if err != nil {
				t.Fatal(err)
			}
			held, err := lease.OpenDirectory()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := held.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			})
			m.streams[id+"\x00blocked"] = &directoryStream{item: item, lease: lease, file: held}
			canceled, cancel := context.WithCancel(t.Context())
			cancel()
			if err := m.sourceFailure(canceled, *m.roots[id], item, os.ErrPermission); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled observation: %v", err)
			}
			ready, err := m.store.TakeReady(t.Context(), id, 1, clock())
			if err != nil || len(ready) != 1 || ready[0].Revision != item.Revision {
				t.Fatalf("cancellation lost or delayed work: %v %v", ready, err)
			}
			blocked = true
			if mode == "nested" {
				if err := os.Mkdir(filepath.Join(path, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			step := func() {
				t.Helper()
				m.mu.Lock()
				work := *m.roots[id]
				m.mu.Unlock()
				if err := m.acquire(t.Context()); err != nil {
					t.Fatal(err)
				}
				err := m.step(t.Context(), work)
				m.release()
				if err != nil {
					t.Fatalf("source outcome not handled: %v", err)
				}
			}
			step()
			step()
			if _, err := held.Stat(); !errors.Is(err, os.ErrClosed) || len(m.streams) != 0 {
				t.Fatalf("failed traversal retained its descriptor: streams=%d err=%v", len(m.streams), err)
			}
			status := m.roots[id].status
			wantReason := "excluded"
			if mode == "nested" {
				wantReason = "nested_root"
			}
			if mode == "unreadable" || mode == "offline" {
				wantReason = "unavailable"
				if status.State != "partial" || status.Reason != "retry_wait" {
					t.Fatalf("unreadable directory claimed complete: %+v", status)
				}
			} else if status.State != "ready" {
				t.Fatalf("terminal frontier did not drain: %+v", status)
			}
			if mode != "offline" {
				got, err := m.store.EntryLookup(t.Context(), id, index.EntryID(id, "blocked"))
				if err != nil || got.State != index.StateOmitted || got.Reason != wantReason {
					t.Fatalf("omission: %+v %v", got, err)
				}
			}
			retained, err := m.store.EntryLookup(t.Context(), id, index.EntryID(id, "blocked/keep.txt"))
			if err != nil || retained.Reason == "deleted" {
				t.Fatalf("failed enumeration inferred deletion: %+v %v", retained, err)
			}
			if mode == "unreadable" || mode == "offline" {
				blocked = false
				now.Add(int64(10 * time.Second))
				for range 20 {
					step()
				}
				if got := m.roots[id].status; got.State != "ready" {
					t.Fatalf("retry did not recover: %+v", got)
				}
				got, err := m.store.EntryLookup(t.Context(), id, index.EntryID(id, "blocked"))
				if err != nil || got.State != index.StateIndexed || got.Kind != index.KindDirectory || got.Reason != "" {
					t.Fatalf("recovered directory retained stale omission: %+v %v", got, err)
				}
			}
		})
	}
}
