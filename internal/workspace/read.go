package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mrYush/hint/internal/tool"
)

// Valid checks whether the captured membership and ownership topology still hold.
func (l Lease) Valid() bool { return l.session != nil && l.session.Valid(l) }

// Verify rechecks current rooted authority and nested boundaries without holding
// session locks during filesystem I/O. It does not establish content freshness.
func (l Lease) Verify() error {
	if !l.Valid() {
		return ErrChanged
	}
	root := l.root()
	// The registered handle may still address an old directory after its
	// pathname is replaced. Never serve that old directory as the new root.
	openedRoot, err := root.Stat(root.Dir())
	if err != nil {
		return fmt.Errorf("workspace: stat root handle: %w", err)
	}
	currentRoot, err := os.Stat(root.Dir())
	if err != nil {
		return fmt.Errorf("workspace: stat root location: %w", err)
	}
	if !os.SameFile(openedRoot, currentRoot) {
		return ErrChanged
	}
	if _, err := root.ResolveExact(l.Path); err != nil {
		return err
	}
	if err := l.session.registry.boundary(l.RootID, l.Path); err != nil {
		return err
	}
	if !l.Valid() {
		return ErrChanged
	}
	return nil
}

func (l Lease) root() tool.Root {
	r := l.session.registry
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.roots[l.RootID].root
}

// Open opens through tool.Root and verifies identity and authority before
// returning the descriptor. Content mutation still requires caller freshness
// checks, and arbitrary in-root namespace races are not a filesystem sandbox.
func (l Lease) Open() (*os.File, error) {
	return l.open(openAny)
}

// OpenRegular admits only regular files, preserving Open's authority and
// identity checks. On Unix the open is nonblocking so replacement with a FIFO
// cannot wait for a writer before the descriptor's type is checked.
// This does not bound arbitrary filesystem or device-driver I/O latency.
func (l Lease) OpenRegular() (*os.File, error) {
	return l.open(openRegular)
}

// OpenDirectory admits only directories, preserving Open's authority and
// identity checks. On Unix replacement with a FIFO cannot wait for a writer.
// This does not bound arbitrary filesystem or device-driver I/O latency.
func (l Lease) OpenDirectory() (*os.File, error) {
	return l.open(openDirectory)
}

type openKind uint8

const (
	openAny openKind = iota
	openRegular
	openDirectory
)

func (kind openKind) check(info os.FileInfo) error {
	if kind == openRegular && !info.Mode().IsRegular() {
		return ErrNotRegular
	}
	if kind == openDirectory && !info.IsDir() {
		return ErrNotDirectory
	}
	return nil
}

func (l Lease) open(kind openKind) (*os.File, error) {
	if err := l.Verify(); err != nil {
		return nil, err
	}
	root := l.root()
	canonical, err := filepath.EvalSymlinks(l.Path)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve open: %w", err)
	}
	before, err := root.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("workspace: stat before open: %w", err)
	}
	if err := kind.check(before); err != nil {
		return nil, err
	}
	var file *os.File
	if kind != openAny {
		file, err = root.OpenFile(canonical, os.O_RDONLY|nonblockingOpenFlags, 0)
	} else {
		file, err = root.Open(canonical)
	}
	if err != nil {
		return nil, fmt.Errorf("workspace: open: %w", err)
	}
	opened, err := file.Stat()
	if err == nil {
		err = kind.check(opened)
	}
	if err == nil && !os.SameFile(before, opened) {
		err = ErrChanged
	}
	if err == nil {
		err = l.Verify()
	}
	if err == nil {
		var after os.FileInfo
		after, err = root.Stat(l.Path)
		if err == nil && !os.SameFile(opened, after) {
			err = ErrChanged
		}
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

// Stat reads rooted metadata without opening file contents, including for
// special files. Authority and identity are rechecked before returning; this
// is not an atomic snapshot or a content-freshness guarantee.
func (l Lease) Stat() (os.FileInfo, error) {
	if err := l.Verify(); err != nil {
		return nil, err
	}
	root := l.root()
	before, err := root.Stat(l.Path)
	if err != nil {
		return nil, fmt.Errorf("workspace: stat source: %w", err)
	}
	after, err := root.Stat(l.Path)
	if err != nil {
		return nil, fmt.Errorf("workspace: recheck source metadata: %w", err)
	}
	if !os.SameFile(before, after) {
		return nil, ErrChanged
	}
	if err := l.Verify(); err != nil {
		return nil, err
	}
	return after, nil
}

// Publish binds a prepared result to the session that issued this lease.
// The bounded callback must not reenter session methods.
func (l Lease) Publish(publish func() error) error {
	if l.session == nil {
		return ErrChanged
	}
	return l.session.Publish(l, publish)
}

// ToolRoot returns a borrowed root whose file operations recheck this session's
// current authority. Callers must not close it or use it as a shell sandbox.
func (s *Session) ToolRoot(repo string) (tool.Root, RootInfo, error) {
	lease, err := s.Resolve(repo, ".")
	if err != nil {
		return tool.Root{}, RootInfo{}, err
	}
	roots, _ := s.Snapshot()
	var info RootInfo
	for _, candidate := range roots {
		if candidate.ID == lease.RootID {
			info = candidate
			break
		}
	}
	if info.ID == "" {
		return tool.Root{}, RootInfo{}, ErrChanged
	}
	base := lease.root()
	checked := base.WithAccessCheck(func(abs string) error {
		rel, err := filepath.Rel(base.Dir(), abs)
		if err != nil {
			return err
		}
		_, err = s.Resolve(lease.RootID, rel)
		return err
	})
	return checked, info, nil
}
