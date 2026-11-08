package repomap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/workspace"
)

func TestPolicyFingerprintRegularAdmission(t *testing.T) {
	_, space, path := managerFixture(t, nil)
	lease, err := space.Resolve("", ".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	got, err := policyFingerprint(t.Context(), lease)
	if err != nil || got != "absent" {
		t.Fatalf("missing policy: %q, %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(path, ".gitignore"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := policyFingerprint(t.Context(), lease); !errors.Is(err, workspace.ErrNotRegular) {
		t.Fatalf("directory policy: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := policyFingerprint(ctx, lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled policy admission: %v", err)
	}
}

func TestManagerReconciliationRetiresDeletedSubtree(t *testing.T) {
	m, space, path := managerFixture(t, &managerTestExtractor{run: extract.Extract})
	if err := os.Mkdir(filepath.Join(path, "gone"), 0700); err != nil {
		t.Fatal(err)
	}
	managerWrite(t, filepath.Join(path, "gone", "x.go"), "package p; func Before() {}")
	lease, err := space.Resolve("", "gone/x.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(context.Background(), lease); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(path, "gone")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	work := *m.roots[lease.RootID]
	m.mu.Unlock()
	if err := m.reconcileSlice(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	file, err := m.store.EntryLookup(context.Background(), lease.RootID, index.EntryID(lease.RootID, "gone/x.go"))
	if err != nil || file.State != index.StateOmitted || file.Reason != "deleted" {
		t.Fatalf("deleted source=%+v,%v", file, err)
	}
	counts, err := m.store.Status(context.Background(), lease.RootID)
	if err != nil || counts.Indexed != 0 {
		t.Fatalf("stale indexed count=%+v,%v", counts, err)
	}
}

func TestManagerPolicyRevisionChangesOnlyOnObservedPolicy(t *testing.T) {
	m, space, path := managerFixture(t, nil)
	roots, _ := space.Snapshot()
	m.mu.Lock()
	work := *m.roots[roots[0].ID]
	m.mu.Unlock()
	observe := func() {
		t.Helper()
		if err := m.observePolicy(context.Background(), work, "."); err != nil {
			t.Fatal(err)
		}
	}
	observe()
	if m.policyRevision(work.info.ID) != 0 {
		t.Fatal("absent initial policy changed revision")
	}
	managerWrite(t, filepath.Join(path, ".gitignore"), "a\n")
	observe()
	first := m.policyRevision(work.info.ID)
	observe()
	if first != 1 || m.policyRevision(work.info.ID) != first {
		t.Fatal("unchanged policy keeps invalidating cursors")
	}
	managerWrite(t, filepath.Join(path, ".gitignore"), "b\n")
	observe()
	if m.policyRevision(work.info.ID) != first+1 {
		t.Fatal("edited policy did not revoke cursor revision")
	}
	if err := os.Remove(filepath.Join(path, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	observe()
	if m.policyRevision(work.info.ID) != first+2 {
		t.Fatal("deleted policy did not revoke cursor revision")
	}
}

func TestPolicyRevisionBeyondCacheCapacityAndReattachment(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	dir := t.TempDir()
	root, err := space.AttachLocal(dir, "policies")
	if err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	m.mu.Lock()
	work := *m.roots[root.ID]
	m.mu.Unlock()
	for i := range 140 {
		name := fmt.Sprintf("p%03d", i)
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		managerWrite(t, filepath.Join(dir, name, ".gitignore"), "ignored\n")
		if err := m.observePolicy(t.Context(), work, name); err != nil {
			t.Fatal(err)
		}
	}
	before := m.policyRevision(root.ID)
	if before != 140 {
		t.Fatalf("policy observations were lost at capacity: %d", before)
	}
	for i := range 140 {
		if err := m.observePolicy(t.Context(), work, fmt.Sprintf("p%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.policyRevision(root.ID); got != before {
		t.Fatalf("unchanged policies after RAM saturation changed epoch: %d -> %d", before, got)
	}
	managerWrite(t, filepath.Join(dir, "p139", ".gitignore"), "changed\n")
	for range 2 {
		if err := m.observePolicy(t.Context(), work, "p139"); err != nil {
			t.Fatal(err)
		}
	}
	before++
	if got := m.policyRevision(root.ID); got != before {
		t.Fatalf("edited policy beyond capacity must advance exactly once: %d, want %d", got, before)
	}
	if err := space.Detach(root.ID); err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	if _, err := space.AttachLocal(dir, "policies"); err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	if got := m.policyRevision(root.ID); got != before {
		t.Fatalf("reattachment lost durable policy epoch: %d -> %d", before, got)
	}
}

func TestManagerDetachReleasesDirectoryStream(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	other := t.TempDir()
	for n := range 40 {
		managerWrite(t, filepath.Join(other, fmt.Sprintf("%02d.txt", n)), "data")
	}
	root, err := space.AttachLocal(other, "other")
	if err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	m.mu.Lock()
	work := *m.roots[root.ID]
	m.mu.Unlock()
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.step(context.Background(), work); err != nil {
		m.release()
		t.Fatal(err)
	}
	stream := m.streams[root.ID+"\x00."]
	m.release()
	if stream == nil {
		t.Fatal("directory stream missing")
	}
	if err := space.Detach(root.ID); err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	m.closeDetachedStreams()
	if _, err := stream.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("detached stream remained open: %v", err)
	}
	m.mu.Lock()
	retained := m.roots[root.ID]
	m.mu.Unlock()
	if retained != nil {
		t.Fatal("detached root retained scheduler capacity")
	}
}

func TestManagerMaintenanceRetainsPinsThenCollects(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	roots, _ := space.Snapshot()
	root := roots[0].ID
	file := index.File{RootID: root, Path: []byte("x.go"), Kind: index.KindFile, State: index.StateIndexed, Size: 1}
	symbols := []index.Symbol{{Name: "Before", Kind: "function", EndByte: 1, StartLine: 1, EndLine: 1}}
	if _, err := m.store.PutSource(context.Background(), file, symbols); err != nil {
		t.Fatal(err)
	}
	pin, err := m.store.Pin(context.Background(), root, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	symbols[0].Name = "After"
	if _, err := m.store.PutSource(context.Background(), file, symbols); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	work := *m.roots[root]
	m.mu.Unlock()
	if err := m.maintenance(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	page, err := m.store.Scan(context.Background(), pin, index.Query{Order: index.PathOrder}, nil, 10, 64<<10)
	if err != nil || len(page.Records) != 2 {
		t.Fatalf("active pin versions=%d,%v", len(page.Records), err)
	}
	if err := m.store.Release(context.Background(), pin.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.roots[root].gcAt = time.Time{}
	m.mu.Unlock()
	if err := m.maintenance(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	current, err := m.store.Pin(context.Background(), root, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	page, err = m.store.Scan(context.Background(), current, index.Query{Order: index.PathOrder}, nil, 10, 64<<10)
	if err != nil || len(page.Records) != 1 || page.Records[0].Symbol.Name != "After" {
		t.Fatalf("collected versions=%+v,%v", page.Records, err)
	}
}
