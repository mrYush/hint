//go:build linux || darwin

package repomap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func TestSourceFIFO(t *testing.T) {
	if mode := os.Getenv("HINT_TEST_SOURCE_FIFO"); mode != "" {
		if mode == "directory" {
			testDirectoryFIFOChild(t, os.Getenv("HINT_TEST_SOURCE_DIR"))
			return
		}
		testSourceFIFOChild(t, mode, os.Getenv("HINT_TEST_SOURCE_DIR"))
		return
	}
	for _, mode := range []string{"existing", "opening", "after-parse", "validation", "directory"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSourceFIFO$", "-test.count=1")
			cmd.Env = append(os.Environ(), "HINT_TEST_SOURCE_FIFO="+mode, "HINT_TEST_SOURCE_DIR="+t.TempDir())
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil || err != nil {
				t.Fatalf("source subprocess: %v, deadline: %v\n%s", err, ctx.Err(), out)
			}
		})
	}
}

func testDirectoryFIFOChild(t *testing.T, base string) {
	t.Helper()
	dir := filepath.Join(base, "root")
	if err := os.MkdirAll(filepath.Join(dir, "scan"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root.Dir(), "scan")
	checks, trigger, replaced := 0, 0, false
	root = root.WithAccessCheck(func(abs string) error {
		if abs == path {
			checks++
			if trigger > 0 && checks == trigger {
				if err := os.Rename(path, path+".old"); err != nil {
					return err
				}
				replaced = true
				return syscall.Mkfifo(path, 0600)
			}
		}
		return nil
	})
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(t.Context(), Options{Path: filepath.Join(base, "index.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
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
	if err := m.store.Enqueue(t.Context(), id, []byte("scan")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.roots[id].sweepAt = m.now().Add(time.Hour)
	work := *m.roots[id]
	m.mu.Unlock()
	checks = 0
	lease, err := space.Resolve(id, "scan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Stat(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Verify(); err != nil {
		t.Fatal(err)
	}
	// Mirror resolution/metadata/admission before the actual directory open.
	trigger, checks = checks+2, 0
	if err := m.acquire(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = m.step(t.Context(), work)
	m.release()
	if !replaced || err != nil || len(m.streams) != 0 {
		t.Fatalf("directory replacement: replaced=%v err=%v streams=%d checks=%d trigger=%d", replaced, err, len(m.streams), checks, trigger)
	}
	if ready, err := m.store.TakeReady(t.Context(), id, 1, m.now()); err != nil || len(ready) != 0 {
		t.Fatalf("failed directory remained hot: %v %v", ready, err)
	}
	if pending, err := m.store.HasPendingWork(t.Context(), id); err != nil || !pending {
		t.Fatalf("failed directory task lost: %v %v", pending, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func testSourceFIFOChild(t *testing.T, mode, base string) {
	t.Helper()
	dir := filepath.Join(base, "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root.Dir(), "source.go")
	managerWrite(t, path, "package p; func Original() {}")
	swap := func() error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return syscall.Mkfifo(path, 0600)
	}
	checks, trigger, replaced := 0, 0, false
	root = root.WithAccessCheck(func(abs string) error {
		if abs == path {
			checks++
			if trigger > 0 && checks == trigger {
				replaced = true
				return swap()
			}
		}
		return nil
	})
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	swapDuringParse := mode == "after-parse"
	runner := &managerTestExtractor{run: func(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
		if swapDuringParse {
			if err := swap(); err != nil {
				return extract.Result{}, err
			}
		}
		return extract.Extract(ctx, source, limits)
	}}
	m, err := New(t.Context(), Options{Path: filepath.Join(base, "index.db"), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	space := registry.NewSession()
	m.Track(space)
	lease, err := space.Resolve("", "source.go")
	if err != nil {
		t.Fatal(err)
	}
	if mode == "validation" {
		if err := m.refresh(t.Context(), lease); err != nil {
			t.Fatal(err)
		}
	}
	switch mode {
	case "existing", "validation":
		if err := swap(); err != nil {
			t.Fatal(err)
		}
	case "opening":
		checks = 0
		if err := lease.Verify(); err != nil {
			t.Fatal(err)
		}
		// Verify, then the pre-open stat, then the rooted open itself.
		trigger, checks = checks+2, 0
	}
	if mode == "validation" {
		response := runMap(t, &mapTool{manager: m, space: space}, `{}`, 8192)
		if len(response.Entries) != 0 || response.Invalidations["source_changed"] != 1 {
			t.Fatalf("special file inherited cached declarations: %+v", response)
		}
	} else {
		err := m.refresh(t.Context(), lease)
		if mode == "after-parse" {
			if !errors.Is(err, workspace.ErrChanged) {
				t.Fatalf("replaced parse: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if mode == "opening" && !replaced {
			t.Fatal("open replacement hook did not run")
		}
		file, err := m.store.EntryLookup(t.Context(), lease.RootID, index.EntryID(lease.RootID, "source.go"))
		if err != nil {
			t.Fatal(err)
		}
		want := "non_regular"
		if mode == "after-parse" {
			want = "source_changed"
		}
		if file.State != index.StateOmitted || file.Reason != want {
			t.Fatalf("special source omission: %+v", file)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.refresh(ctx, lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source read: %v", err)
	}
	swapDuringParse = false
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	managerWrite(t, path, "package p; func Recovered() {}")
	if err := m.refresh(t.Context(), lease); err != nil {
		t.Fatalf("regular source recovery: %v", err)
	}
	response := runMap(t, &mapTool{manager: m, space: space}, `{}`, 8192)
	if len(response.Entries) != 1 || response.Entries[0].Name != "Recovered" {
		t.Fatalf("recovered source map: %+v", response)
	}
	// Admission must be reusable and Close must finish without a FIFO writer.
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	m.release()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
