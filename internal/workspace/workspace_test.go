package workspace

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func newWorkspace(t *testing.T) (*Registry, *Session, tool.Root) {
	t.Helper()
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	r, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, r.NewSession(), root
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMembershipAndTurnGuard(t *testing.T) {
	r, s, root := newWorkspace(t)
	before, rev := s.Snapshot()
	if len(before) != 1 || !before[0].Primary || rev != 1 {
		t.Fatalf("initial snapshot: %v %d", before, rev)
	}
	if s.ID() == r.NewSession().ID() {
		t.Fatal("session identities collide")
	}
	lease, err := s.Resolve("", ".")
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.BeginTurn()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginTurn(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second turn: %v", err)
	}
	if _, err := s.AttachLocal(t.TempDir(), "other"); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy attach: %v", err)
	}
	release()
	release()
	child, err := s.AttachLocal(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Valid() {
		t.Fatal("old membership remains valid")
	}
	called := false
	if err := s.Publish(lease, func() error { called = true; return nil }); !errors.Is(err, ErrChanged) || called {
		t.Fatalf("stale publication: %v called=%v", err, called)
	}
	if err := s.Detach("primary"); !errors.Is(err, ErrPrimary) {
		t.Fatalf("detach primary: %v", err)
	}
	other, err := s.Resolve("other", ".")
	if err != nil {
		t.Fatal(err)
	}
	if other.RootID != child.ID {
		t.Fatal("alias resolved incorrectly")
	}
	if err := s.Detach("other"); err != nil {
		t.Fatal(err)
	}
	if other.Valid() {
		t.Fatal("detached lease valid")
	}
	if _, err := s.Resolve(child.ID, "."); !errors.Is(err, ErrUnknownRoot) {
		t.Fatalf("detached resolve: %v", err)
	}
	if _, err := s.AttachLocal(t.TempDir(), "other"); !errors.Is(err, ErrAlias) {
		t.Fatalf("alias rebound: %v", err)
	}
	primary, err := s.Resolve("", ".")
	if err != nil || primary.Path != root.Dir() {
		t.Fatalf("primary changed: %v %v", primary, err)
	}
}

func TestNestedOwnershipAndSymlinkAliases(t *testing.T) {
	r, s, root := newWorkspace(t)
	for _, kind := range []string{"repository", "worktree"} {
		dir := filepath.Join(root.Dir(), kind)
		write(t, filepath.Join(dir, "file.go"), "package child")
		if kind == "repository" {
			if err := os.Mkdir(filepath.Join(dir, ".git"), 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			write(t, filepath.Join(dir, ".git"), "gitdir: elsewhere")
		}
		if _, err := s.Resolve("", kind+"/file.go"); !errors.Is(err, ErrBoundary) {
			t.Fatalf("%s boundary: %v", kind, err)
		}
		child, err := s.AttachLocal(dir, kind)
		if err != nil {
			t.Fatal(err)
		}
		l, err := s.Resolve(child.ID, "file.go")
		if err != nil {
			t.Fatal(err)
		}
		f, err := l.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if string(b) != "package child" {
			t.Fatal("wrong child source")
		}
	}
	ordinary := filepath.Join(root.Dir(), "ordinary")
	write(t, filepath.Join(ordinary, "file"), "child")
	old, err := s.Resolve("", "ordinary/file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.NewSession().AttachLocal(ordinary, "ordinary"); err != nil {
		t.Fatal(err)
	}
	if old.Valid() {
		t.Fatal("new nested ownership did not invalidate lease")
	}
	if _, err := s.Resolve("", "ordinary/file"); !errors.Is(err, ErrBoundary) {
		t.Fatalf("ordinary boundary: %v", err)
	}
	alias := filepath.Join(root.Dir(), "shortcut")
	if err := os.Symlink(ordinary, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := s.Resolve("", "shortcut/file"); !errors.Is(err, ErrBoundary) {
		t.Fatalf("symlink bypass: %v", err)
	}
}

func TestDeniedEscapeAndFreshOpen(t *testing.T) {
	_, s, root := newWorkspace(t)
	write(t, filepath.Join(root.Dir(), "file"), "ok")
	l, err := s.Resolve("", "file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve("", "../outside"); !errors.Is(err, tool.ErrOutsideRoot) {
		t.Fatalf("escape: %v", err)
	}
	if _, err := s.Resolve("", root.Dir()); !errors.Is(err, tool.ErrOutsideRoot) {
		t.Fatalf("absolute: %v", err)
	}
	if err := os.Remove(filepath.Join(root.Dir(), "file")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted source: %v", err)
	}
	write(t, filepath.Join(root.Dir(), "secret"), "secret")
	denied, err := New(root.Deny(filepath.Join(root.Dir(), "secret")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := denied.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := denied.NewSession().Resolve("", "secret"); !errors.Is(err, tool.ErrDenied) {
		t.Fatalf("denial lost: %v", err)
	}
}

func TestEntryHandlesPreserveBytesAndReauthorize(t *testing.T) {
	_, s, root := newWorkspace(t)
	path := " spaced name "
	write(t, filepath.Join(root.Dir(), path), "exact")
	l, err := s.Resolve("", path)
	if err != nil {
		t.Fatal(err)
	}
	if l.RelativePath != path {
		t.Fatalf("path bytes changed: %q", l.RelativePath)
	}
	id := EntryID(l.RootID, path)
	if len(EntryID(l.RootID, strings.Repeat("x", 100000))) != len(id) {
		t.Fatal("handle grows with path")
	}
	lookup := func(rootID, entryID string) (string, error) {
		if rootID != l.RootID || entryID != id {
			t.Fatal("lookup scope")
		}
		return path, nil
	}
	resolved, err := s.ResolveEntry("", id, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolved.Stat(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveEntry("", id, func(string, string) (string, error) { return "another", nil }); !errors.Is(err, ErrStaleEntry) {
		t.Fatalf("mismatched lookup: %v", err)
	}
	if _, err := s.ResolveEntry("", id, nil); !errors.Is(err, ErrStaleEntry) {
		t.Fatalf("missing lookup: %v", err)
	}
}

func TestStableRootsAndManagedLifecycle(t *testing.T) {
	r, s, primary := newWorkspace(t)
	dir := t.TempDir()
	first, err := s.AttachLocal(dir, "first")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.AttachLocal(dir, "unused")
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != first.ID || duplicate.Alias != "first" {
		t.Fatal("duplicate changed identity")
	}
	otherRoot, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := otherRoot.Close(); err != nil {
			t.Error(err)
		}
	}()
	fresh, err := New(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Error(err)
		}
	}()
	info, _ := fresh.NewSession().Snapshot()
	if info[0].ID != first.ID {
		t.Fatal("root ID changed across registries")
	}
	managed := r.roots[first.ID].root
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := managed.Stat(managed.Dir()); err == nil {
		t.Fatal("managed root not closed")
	}
	if _, err := primary.Stat(primary.Dir()); err != nil {
		t.Fatalf("borrowed primary closed: %v", err)
	}
}

func TestCapacityAndPublication(t *testing.T) {
	_, _, primary := newWorkspace(t)
	r, err := New(primary, WithMaxRoots(2))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	s := r.NewSession()
	root, err := s.AttachLocal(t.TempDir(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachLocal(t.TempDir(), "third"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	if err := s.Detach(root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachLocal(root.Path, "second"); err != nil {
		t.Fatalf("reattach at capacity: %v", err)
	}
	l, err := s.Resolve(root.ID, ".")
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("publication failed")
	if err := s.Publish(l, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error: %v", err)
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = s.Snapshot(); _ = l.Valid() }()
	}
	wg.Wait()
}

func TestRootPathReplacementAndRawNames(t *testing.T) {
	_, s, root := newWorkspace(t)
	write(t, filepath.Join(root.Dir(), "file"), "old")
	l, err := s.Resolve("", "file")
	if err != nil {
		t.Fatal(err)
	}
	renamed := root.Dir() + "-old"
	if err := os.Rename(root.Dir(), renamed); err != nil {
		t.Skipf("cannot rename open root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(renamed); err != nil {
			t.Error(err)
		}
	})
	write(t, filepath.Join(root.Dir(), "file"), "new")
	if _, err := l.Open(); !errors.Is(err, ErrChanged) {
		t.Fatalf("root replacement served stale handle: %v", err)
	}
}

func TestResolveEntryRejectsMidLookupRevocation(t *testing.T) {
	_, s, _ := newWorkspace(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "file"), "data")
	attached, err := s.AttachLocal(dir, "other")
	if err != nil {
		t.Fatal(err)
	}
	id := EntryID(attached.ID, "file")
	_, err = s.ResolveEntry(attached.ID, id, func(string, string) (string, error) {
		if err := s.Detach(attached.ID); err != nil {
			t.Fatal(err)
		}
		return "file", nil
	})
	if !errors.Is(err, ErrUnknownRoot) && !errors.Is(err, ErrChanged) {
		t.Fatalf("revocation ignored: %v", err)
	}
}

func TestSymlinkSwapRechecksOwnership(t *testing.T) {
	_, s, root := newWorkspace(t)
	write(t, filepath.Join(root.Dir(), "safe", "file"), "safe")
	write(t, filepath.Join(root.Dir(), "child", "file"), "child")
	write(t, filepath.Join(root.Dir(), "child", ".git"), "gitdir: outside")
	link := filepath.Join(root.Dir(), "link")
	if err := os.Symlink("safe", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	l, err := s.Resolve("", "link/file")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("child", link); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(); !errors.Is(err, ErrBoundary) {
		t.Fatalf("swapped alias crossed nested boundary: %v", err)
	}
}
