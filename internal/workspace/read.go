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
	file, err := root.Open(canonical)
	if err != nil {
		return nil, fmt.Errorf("workspace: open: %w", err)
	}
	opened, err := file.Stat()
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

// Stat uses the same authorized-open checks as Open and closes the descriptor.
func (l Lease) Stat() (os.FileInfo, error) {
	f, err := l.Open()
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	return info, errors.Join(err, f.Close())
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
