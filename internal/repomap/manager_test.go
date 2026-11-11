package repomap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
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

type managerTestExtractor struct {
	run func(context.Context, extract.Source, extract.Limits) (extract.Result, error)
}

func (f *managerTestExtractor) Extract(ctx context.Context, s extract.Source, l extract.Limits) (extract.Result, error) {
	return f.run(ctx, s, l)
}
func (*managerTestExtractor) Close() error { return nil }

func managerFixture(t *testing.T, runner Extractor) (*Manager, *workspace.Session, string) {
	t.Helper()
	return managerFixtureAt(t, runner, t.TempDir())
}

func managerFixtureAt(t *testing.T, runner Extractor, path string) (*Manager, *workspace.Session, string) {
	t.Helper()
	return managerFixtureWithStore(t, runner, path, index.Options{})
}

func managerFixtureWithStore(t *testing.T, runner Extractor, path string, store index.Options) (*Manager, *workspace.Session, string) {
	t.Helper()
	root, err := tool.NewRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(context.Background(), Options{Path: filepath.Join(t.TempDir(), "index.db"), Runner: runner, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	space := registry.NewSession()
	m.Track(space)
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	return m, space, path
}

// stopManagerTestScheduler isolates manually driven jobs from scheduler ticks.
// Join both loops before giving direct operations a fresh lifetime context;
// Close still joins the already-closed loop channels and releases the store.
func stopManagerTestScheduler(t *testing.T, m *Manager) {
	t.Helper()
	m.cancel()
	<-m.done
	<-m.sampleDone
	m.ctx, m.cancel = context.WithCancel(t.Context())
}

func managerWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManagerGlobalAdmissionBeforeSourceOpen(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	lease, err := space.Resolve("", "not-present.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = m.refresh(ctx, lease)
	m.release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("source accessed before admission: %v", err)
	}
}

func TestManagerRechecksSameStatMutationAfterParse(t *testing.T) {
	var sourcePath string
	runner := &managerTestExtractor{run: func(ctx context.Context, s extract.Source, l extract.Limits) (extract.Result, error) {
		info, err := os.Stat(sourcePath)
		if err != nil {
			return extract.Result{}, err
		}
		if err := os.WriteFile(sourcePath, []byte(strings.ReplaceAll(string(s.Content), "Old", "New")), 0600); err != nil {
			return extract.Result{}, err
		}
		if err := os.Chtimes(sourcePath, info.ModTime(), info.ModTime()); err != nil {
			return extract.Result{}, err
		}
		return extract.Extract(ctx, s, l)
	}}
	m, space, path := managerFixture(t, runner)
	sourcePath = filepath.Join(path, "x.go")
	managerWrite(t, sourcePath, "package p; func Old() {}")
	lease, err := space.Resolve("", "x.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(context.Background(), lease); !errors.Is(err, workspace.ErrChanged) {
		t.Fatalf("mutation=%v", err)
	}
	file, err := m.store.EntryLookup(context.Background(), lease.RootID, index.EntryID(lease.RootID, "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	if file.State != index.StateOmitted || file.Reason != "source_changed" {
		t.Fatalf("stale source published: %+v", file)
	}
}

func TestManagerDeclarationPublicationRevocation(t *testing.T) {
	started := make(chan struct{})
	resume := make(chan struct{})
	runner := &managerTestExtractor{run: func(ctx context.Context, s extract.Source, l extract.Limits) (extract.Result, error) {
		close(started)
		<-resume
		return extract.Extract(ctx, s, l)
	}}
	m, space, _ := managerFixture(t, runner)
	other := t.TempDir()
	managerWrite(t, filepath.Join(other, "x.go"), "package p; func Open() {}")
	info, err := space.AttachLocal(other, "other")
	if err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	lease, err := space.Resolve(info.ID, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.refresh(context.Background(), lease) }()
	<-started
	if err := space.Detach(info.ID); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-done; !errors.Is(err, workspace.ErrChanged) {
		t.Fatalf("revocation=%v", err)
	}
	if _, err := m.store.EntryLookup(context.Background(), info.ID, index.EntryID(info.ID, "x.go")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("revoked source stored: %v", err)
	}
}

func TestManagerWideDiscoveryPrioritizesFilesAndSkipsSymlinks(t *testing.T) {
	var parsed atomic.Int32
	runner := &managerTestExtractor{run: func(ctx context.Context, s extract.Source, l extract.Limits) (extract.Result, error) {
		parsed.Add(1)
		return extract.Extract(ctx, s, l)
	}}
	m, space, path := managerFixture(t, runner)
	for i := range 100 {
		managerWrite(t, filepath.Join(path, fmt.Sprintf("f%03d.go", i)), "package p; func Open() {}")
	}
	if err := os.Symlink(path, filepath.Join(path, "cycle")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	roots, _ := space.Snapshot()
	m.mu.Lock()
	work := *m.roots[roots[0].ID]
	m.mu.Unlock()
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	if err := m.step(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	if len(m.streams) != 1 {
		t.Fatalf("wide root unexpectedly completed: %d streams", len(m.streams))
	}
	if err := m.step(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	if parsed.Load() != 1 {
		t.Fatal("file waited for whole wide directory")
	}
	for range 150 {
		if err := m.step(context.Background(), work); err != nil {
			t.Fatal(err)
		}
	}
	if parsed.Load() != 100 {
		t.Fatalf("parsed %d files", parsed.Load())
	}
	if _, err := m.store.EntryLookup(context.Background(), roots[0].ID, index.EntryID(roots[0].ID, "cycle")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("directory symlink traversed: %v", err)
	}
}

func TestManagerGitIgnoreNULAndTrackedSemantics(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	m, space, path := managerFixture(t, nil)
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", path}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	run("init", "-q")
	name := "line\nbreak.go"
	managerWrite(t, filepath.Join(path, name), "package p")
	managerWrite(t, filepath.Join(path, "tracked.go"), "package p")
	run("add", "--", "tracked.go")
	managerWrite(t, filepath.Join(path, ".gitignore"), "*.go\n")
	lease, err := space.Resolve("", name)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), lease, false); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("newline ignored path=%v", err)
	}
	tracked, err := space.Resolve("", "tracked.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), tracked, false); err != nil {
		t.Fatalf("tracked file ignored: %v", err)
	}
	if err := os.Mkdir(filepath.Join(path, "ignored"), 0700); err != nil {
		t.Fatal(err)
	}
	managerWrite(t, filepath.Join(path, "ignored", "tracked.go"), "package p")
	managerWrite(t, filepath.Join(path, "ignored", "untracked.go"), "package p")
	managerWrite(t, filepath.Join(path, ".gitignore"), "*.go\nignored/\n")
	run("add", "-f", "--", "ignored/tracked.go")
	directory, err := space.Resolve("", "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), directory, true); err != nil {
		t.Fatalf("tracked descendant parent ignored: %v", err)
	}
	m.mu.Lock()
	work := *m.roots[directory.RootID]
	m.mu.Unlock()
	for range 8 {
		if err := m.step(context.Background(), work); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.store.EntryLookup(context.Background(), directory.RootID, index.EntryID(directory.RootID, "ignored/tracked.go")); err != nil {
		t.Fatalf("tracked descendant not discovered: %v", err)
	}
	if _, err := m.store.EntryLookup(context.Background(), directory.RootID, index.EntryID(directory.RootID, "ignored/untracked.go")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("untracked ignored descendant exposed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(path, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), tracked, false); err == nil {
		t.Fatal("broken previously known Git policy failed open")
	}
}

func TestManagerPrimarySubdirectoryUsesEnclosingGitPolicy(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	parent := t.TempDir()
	path := filepath.Join(parent, "subdir")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", parent}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	run("init", "-q")
	managerWrite(t, filepath.Join(parent, ".gitignore"), "*.go\n")
	managerWrite(t, filepath.Join(path, "ignored.go"), "package p")
	managerWrite(t, filepath.Join(path, "tracked.go"), "package p")
	run("add", "-f", "--", "subdir/tracked.go")
	m, space, _ := managerFixtureAt(t, nil, path)
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	ignored, err := space.Resolve("", "ignored.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), ignored, false); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("primary ignored source admitted: %v", err)
	}
	tracked, err := space.Resolve("", "tracked.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.excluded(context.Background(), tracked, false); err != nil {
		t.Fatalf("primary tracked source excluded: %v", err)
	}
	m.mu.Lock()
	work := *m.roots[ignored.RootID]
	m.mu.Unlock()
	if err := m.step(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.EntryLookup(context.Background(), ignored.RootID, index.EntryID(ignored.RootID, "ignored.go")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("discovery inherited wrong policy: %v", err)
	}
	managerWrite(t, filepath.Join(parent, ".gitignore"), "")
	if err := m.excluded(context.Background(), ignored, false); err != nil {
		t.Fatalf("current enclosing policy not observed: %v", err)
	}
	if _, err := space.Resolve("", "../.gitignore"); err == nil {
		t.Fatal("policy inheritance expanded workspace authority")
	}
}

func TestManagerTinyRootProgressesBesideWideRoot(t *testing.T) {
	m, space, path := managerFixture(t, &managerTestExtractor{run: extract.Extract})
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	tinyDir := t.TempDir()
	managerWrite(t, filepath.Join(tinyDir, "tiny.go"), "package p; func Tiny() {}")
	tinyRoot, err := space.AttachLocal(tinyDir, "tiny")
	if err != nil {
		t.Fatal(err)
	}
	m.Track(space)
	for i := range 8 {
		dir := filepath.Join(path, fmt.Sprintf("wide-%d", i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for j := range 40 {
			managerWrite(t, filepath.Join(dir, fmt.Sprintf("source-%d.go", j)), "package p; func Wide() {}")
		}
	}
	primary, err := space.Resolve("", ".")
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	wide := *m.roots[primary.RootID]
	m.mu.Unlock()
	for range 3 {
		if err := m.step(context.Background(), wide); err != nil {
			t.Fatal(err)
		}
		if len(m.streams) > 1 {
			t.Fatalf("one root monopolized %d streams", len(m.streams))
		}
	}
	if len(m.streams) != 1 {
		t.Fatal("fixture did not retain a wide stream")
	}
	m.mu.Lock()
	tiny := *m.roots[tinyRoot.ID]
	m.mu.Unlock()
	for range 2 {
		if err := m.step(context.Background(), tiny); err != nil {
			t.Fatal(err)
		}
	}
	file, err := m.store.EntryLookup(context.Background(), tinyRoot.ID, index.EntryID(tinyRoot.ID, "tiny.go"))
	if err != nil || file.State != index.StateIndexed {
		t.Fatalf("tiny root did not progress while wide root remained open: state=%s err=%v", file.State, err)
	}
	if len(m.streams) != 2 {
		t.Fatalf("retained streams=%d, want one per root", len(m.streams))
	}
}

func TestManagerWideDirectoriesAdvanceWithoutBlockedQueueChurn(t *testing.T) {
	m, space, path := managerFixture(t, nil)
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	for i := range 100 {
		if err := os.Mkdir(filepath.Join(path, fmt.Sprintf("child-%03d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	roots, _ := space.Snapshot()
	m.mu.Lock()
	work := *m.roots[roots[0].ID]
	m.mu.Unlock()
	if err := m.step(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	first, err := m.store.Take(context.Background(), work.info.ID, 1)
	if err != nil || len(first) != 1 || string(first[0].Path) == "." {
		t.Fatalf("missing blocked child fixture: %v %v", first, err)
	}
	for range 4 {
		if err := m.step(context.Background(), work); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.store.QueueLookup(context.Background(), work.info.ID, []byte(".")); !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("parent tail did not complete with current queue revision: %v", err)
	}
	current, err := m.store.QueueLookup(context.Background(), work.info.ID, first[0].Path)
	if err != nil || current.Sequence <= first[0].Sequence || current.Revision != first[0].Revision {
		t.Fatalf("blocked child not retained by one rotation: %+v %v", current, err)
	}
	page, err := m.store.ListEntries(context.Background(), work.info.ID, nil, nil, 128, 128<<10)
	if err != nil || len(page.Files) != 100 {
		t.Fatalf("parent tail incomplete: files=%d err=%v", len(page.Files), err)
	}
}

func TestManagerBlockedDirectoryDoesNotDelayQueuedFileUntilParentCompletes(t *testing.T) {
	m, space, path := managerFixture(t, &managerTestExtractor{run: extract.Extract})
	stopManagerTestScheduler(t, m)
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	for i := range 100 {
		if err := os.Mkdir(filepath.Join(path, fmt.Sprintf("child-%03d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	managerWrite(t, filepath.Join(path, "ready.go"), "package p; func Ready() {}")
	roots, _ := space.Snapshot()
	m.mu.Lock()
	work := *m.roots[roots[0].ID]
	m.mu.Unlock()
	// Choose a child from the parent's first page. Rediscovering a prequeued
	// child on a later page advances its revision and prevents that visit's
	// rotation, so a fixed child name cannot guarantee this three-step fixture.
	lease, err := space.Resolve(work.info.ID, ".")
	if err != nil {
		t.Fatal(err)
	}
	directory, err := lease.OpenDirectory()
	if err != nil {
		t.Fatal(err)
	}
	stream := &directoryStream{lease: lease, file: directory}
	m.streams[work.info.ID+"\x00."] = stream
	stream.item, err = m.store.QueueLookup(context.Background(), work.info.ID, []byte("."))
	if err != nil {
		t.Fatal(err)
	}
	stream.pending, stream.readErr = directory.ReadDir(32)
	if stream.readErr != nil {
		t.Fatal(stream.readErr)
	}
	blockedChild := ""
	for _, entry := range stream.pending {
		if entry.IsDir() {
			blockedChild = entry.Name()
			break
		}
	}
	if blockedChild == "" {
		t.Fatal("fixture has no child directory in the first page")
	}
	// The first step consumes this exact page before the blocked child visit.
	for _, path := range []string{blockedChild, "ready.go"} {
		if err := m.store.Enqueue(context.Background(), work.info.ID, []byte(path)); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		if err := m.step(context.Background(), work); err != nil {
			t.Fatal(err)
		}
	}
	file, err := m.store.EntryLookup(context.Background(), work.info.ID, index.EntryID(work.info.ID, "ready.go"))
	if err != nil || file.State != index.StateIndexed {
		t.Fatalf("ready file blocked by child directory: state=%s err=%v", file.State, err)
	}
	if m.streams[work.info.ID+"\x00."] == nil {
		t.Fatal("fixture finished parent before testing file priority")
	}
}

func TestManagerDirectoryStreamCapacityRetriesWithoutLosingTail(t *testing.T) {
	m, space, primary := managerFixture(t, nil)
	stopManagerTestScheduler(t, m)
	if err := m.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.release()
	managerWrite(t, filepath.Join(primary, "file.txt"), "text")
	for i := range 8 {
		dir := t.TempDir()
		managerWrite(t, filepath.Join(dir, "file.txt"), "text")
		if _, err := space.AttachLocal(dir, fmt.Sprintf("root-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m.Track(space)
	roots, _ := space.Snapshot()
	var work []rootWork
	m.mu.Lock()
	for _, root := range roots {
		work = append(work, *m.roots[root.ID])
	}
	m.mu.Unlock()
	for _, root := range work {
		if err := m.step(context.Background(), root); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.streams) != 8 {
		t.Fatalf("retained streams=%d", len(m.streams))
	}
	m.mu.Lock()
	status := m.roots[work[8].info.ID].status
	m.mu.Unlock()
	if status.Reason != "directory_stream_capacity" {
		t.Fatalf("capacity not reported: %+v", status)
	}
	for range 2 {
		if err := m.step(context.Background(), work[0]); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.step(context.Background(), work[8]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.store.EntryLookup(context.Background(), work[8].info.ID, index.EntryID(work[8].info.ID, "file.txt")); err != nil {
		t.Fatalf("waiting root lost durable directory task: %v", err)
	}
}

func TestManagerFocusedEmergencyPreservesCatalog(t *testing.T) {
	for _, resource := range []string{"memory", "disk"} {
		t.Run(resource, func(t *testing.T) {
			var calls atomic.Int32
			runner := &managerTestExtractor{run: func(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
				calls.Add(1)
				return extract.Extract(ctx, source, limits)
			}}
			m, space, path := managerFixture(t, runner)
			managerWrite(t, filepath.Join(path, "x.go"), "package p; func Original() {}")
			lease, err := space.Resolve("", "x.go")
			if err != nil {
				t.Fatal(err)
			}
			if err := m.refresh(context.Background(), lease); err != nil {
				t.Fatal(err)
			}
			before, err := m.store.EntryLookup(context.Background(), lease.RootID, index.EntryID(lease.RootID, "x.go"))
			if err != nil {
				t.Fatal(err)
			}
			managerWrite(t, filepath.Join(path, "x.go"), "package p; func Updated() {}")
			sample := ResourceSample{At: time.Now(), Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30}
			if resource == "memory" {
				sample.AvailableMemory = 1
			} else {
				sample.FreeDisk = 1
			}
			m.UpdateResources(sample)
			now := time.Now()
			invalid := []ResourceSample{
				{At: now},
				{At: now.Add(-time.Hour), Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30},
				{At: now.Add(time.Hour), Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30},
				{At: now, Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30, CPU: math.NaN()},
				{At: now, Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30, IO: math.NaN()},
			}
			for i := 0; i <= len(invalid); i++ {
				if err := m.refresh(context.Background(), lease); err == nil {
					t.Fatalf("known emergency admitted source work after sample %d", i)
				}
				if i < len(invalid) {
					m.UpdateResources(invalid[i])
				}
			}
			after, err := m.store.EntryLookup(context.Background(), lease.RootID, before.ID)
			if err != nil || after.Hash != before.Hash || calls.Load() != 1 {
				t.Fatalf("catalog altered or worker called during emergency: %v, calls=%d", err, calls.Load())
			}
			m.UpdateResources(ResourceSample{At: time.Now(), Known: true, AvailableMemory: 1 << 30, FreeDisk: 1 << 30})
			if err := m.refresh(context.Background(), lease); err != nil || calls.Load() != 2 {
				t.Fatalf("healthy focused recovery: %v, calls=%d", err, calls.Load())
			}
		})
	}
}

func TestManagerFocusedEmergencyCancelsActiveWorker(t *testing.T) {
	started := make(chan struct{})
	runner := &managerTestExtractor{run: func(ctx context.Context, _ extract.Source, _ extract.Limits) (extract.Result, error) {
		close(started)
		<-ctx.Done()
		return extract.Result{}, ctx.Err()
	}}
	m, space, path := managerFixture(t, runner)
	managerWrite(t, filepath.Join(path, "x.go"), "package p")
	lease, err := space.Resolve("", "x.go")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.refresh(context.Background(), lease) }()
	<-started
	m.UpdateResources(ResourceSample{At: time.Now(), Known: true, AvailableMemory: 1, FreeDisk: 1 << 30})
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("worker cancellation=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active worker ignored known emergency")
	}
}

type growingSource struct {
	*os.File
	appendPath string
	growth     int
	read       int
	grew       bool
}

func (s *growingSource) Read(p []byte) (int, error) {
	if !s.grew {
		p = p[:min(len(p), 128)]
	}
	n, err := s.File.Read(p)
	s.read += n
	if !s.grew && n > 0 {
		s.grew = true
		writer, openErr := os.OpenFile(s.appendPath, os.O_WRONLY|os.O_APPEND, 0)
		if openErr != nil {
			return n, errors.Join(err, openErr)
		}
		_, writeErr := writer.Write([]byte(strings.Repeat("x", s.growth)))
		err = errors.Join(err, writeErr, writer.Close())
	}
	return n, err
}

func TestManagerRejectsSourceGrowthDuringBoundedRead(t *testing.T) {
	const original = 1024
	limit := extract.DefaultLimits().MaxInputBytes
	for _, growth := range []int{original, 2 * limit} {
		t.Run(fmt.Sprint(growth), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "growing.go")
			managerWrite(t, path, strings.Repeat("x", original))
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Error(err)
				}
			}()
			before, err := file.Stat()
			if err != nil {
				t.Fatal(err)
			}
			source := &growingSource{File: file, appendPath: path, growth: growth}
			data, err := readObservedSource(context.Background(), source, before, limit)
			if !errors.Is(err, workspace.ErrChanged) || data != nil {
				t.Fatalf("growing source admitted: data=%d err=%v", len(data), err)
			}
			if !source.grew || source.read != min(original+growth, limit+1) {
				t.Fatalf("read=%d grew=%t; bound=%d", source.read, source.grew, limit+1)
			}
		})
	}
}

func TestManagerInferenceCancellationHoldsLeaseUntilRunnerStops(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	finish := make(chan struct{})
	runner := &managerTestExtractor{run: func(ctx context.Context, _ extract.Source, _ extract.Limits) (extract.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-finish
		return extract.Result{}, ctx.Err()
	}}
	m, space, path := managerFixture(t, runner)
	stopManagerTestScheduler(t, m)
	managerWrite(t, filepath.Join(path, "x.go"), "package p")
	lease, err := space.Resolve("", "x.go")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	m.mu.Lock()
	m.backgroundCancel = cancel
	m.mu.Unlock()
	if err := m.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		err := m.refreshHeld(ctx, lease)
		m.release()
		done <- err
	}()
	// Unblock and join the fake runner even if an assertion fails before finish.
	defer func() {
		cancel()
		select {
		case <-finish:
		default:
			close(finish)
		}
		<-stopped
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("worker exited before starting: %v", err)
	case <-ctx.Done():
		t.Fatalf("worker did not start: %v", ctx.Err())
	}
	release := m.Inference()
	defer release()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("inference did not cancel the worker")
	}
	waitCtx, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	if err := m.acquire(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		if err == nil {
			m.release()
		}
		t.Fatalf("lease reused before stop: %v", err)
	}
	close(finish)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after finish")
	}
	release()
	release()
	m.mu.Lock()
	active := m.governor.inference
	m.backgroundCancel = nil
	m.mu.Unlock()
	if active != 0 {
		t.Fatalf("inference count=%d", active)
	}
}
