// Package workspace routes explicitly attached roots without widening file authority.
package workspace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mrYush/hint/internal/tool"
)

// Errors distinguish membership, lifecycle, and path-authority failures.
var (
	ErrBusy        = errors.New("workspace: session has an active turn")
	ErrUnknownRoot = errors.New("workspace: root is not attached")
	ErrPrimary     = errors.New("workspace: primary root cannot be detached")
	ErrAlias       = errors.New("workspace: alias is invalid or already assigned")
	ErrBoundary    = errors.New("workspace: path belongs to a nested root")
	ErrChanged     = errors.New("workspace: source or authority changed")
	ErrClosed      = errors.New("workspace: registry is closed")
	ErrCapacity    = errors.New("workspace: root capacity reached")
	ErrStaleEntry  = errors.New("workspace: entry handle is unavailable")
)

// MaxRoots bounds registry and per-session structural state, not tree size.
const MaxRoots = 256

// RootInfo identifies an explicitly attached local filesystem root.
type RootInfo struct {
	ID      string `json:"id"`
	Alias   string `json:"alias"`
	Path    string `json:"path"`
	Primary bool   `json:"primary"`
}

type registered struct {
	root  tool.Root
	owned bool
}

// Registry owns roots opened by AttachLocal; supplied roots remain caller-owned.
// Close requires the caller to stop workers and sessions first.
type Registry struct {
	mu       sync.RWMutex
	roots    map[string]registered
	primary  string
	closed   bool
	revision uint64
	limit    int
}

// Option configures a registry before use.
type Option func(*Registry)

// WithMaxRoots sets the finite root capacity, including retained boundaries.
func WithMaxRoots(n int) Option { return func(r *Registry) { r.limit = n } }

// New creates a registry borrowing the immutable startup primary root.
func New(primary tool.Root, options ...Option) (*Registry, error) {
	if primary.Dir() == "" {
		return nil, fmt.Errorf("workspace: primary root is uninitialized")
	}
	if _, err := primary.Stat(primary.Dir()); err != nil {
		return nil, fmt.Errorf("workspace: primary root: %w", err)
	}
	id := rootID(primary.Dir())
	r := &Registry{roots: map[string]registered{id: {root: primary}}, primary: id, revision: 1, limit: MaxRoots}
	for _, option := range options {
		option(r)
	}
	if r.limit < 1 {
		return nil, fmt.Errorf("workspace: root capacity must be positive")
	}
	return r, nil
}

// Close closes managed roots, leaving borrowed roots to their owners.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	var errs []error
	for _, entry := range r.roots {
		if entry.owned {
			errs = append(errs, entry.root.Close())
		}
	}
	return errors.Join(errs...)
}

// Session holds membership and aliases for one conversation.
// Attach and Detach are user-action APIs, never file-content or tool-output APIs.
type Session struct {
	mu       sync.RWMutex
	registry *Registry
	members  map[string]string
	aliases  map[string]string
	revision uint64
	busy     bool
	identity string
}

// NewSession attaches only the immutable primary root.
func (r *Registry) NewSession() *Session {
	return &Session{identity: rand.Text(), registry: r, members: map[string]string{r.primary: "primary"}, aliases: map[string]string{"primary": r.primary}, revision: 1}
}

// ID is an opaque identity for this live session, suitable for cursor binding.
func (s *Session) ID() string { return s.identity }

// BeginTurn prevents membership mutations until the idempotent release is called.
func (s *Session) BeginTurn() (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return nil, ErrBusy
	}
	s.registry.mu.RLock()
	closed := s.registry.closed
	s.registry.mu.RUnlock()
	if closed {
		return nil, ErrClosed
	}
	s.busy = true
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }) }, nil
}

// Attach borrows an explicitly authorized root and never closes its handle.
func (s *Session) Attach(root tool.Root, alias string) (RootInfo, error) {
	return s.attach(root, alias, false)
}

// AttachLocal opens an explicitly authorized directory, without finding parents
// or initializing submodules. Its accepted handle is owned by the registry.
func (s *Session) AttachLocal(path, alias string) (RootInfo, error) {
	root, err := tool.NewRoot(path)
	if err != nil {
		return RootInfo{}, fmt.Errorf("workspace: attach local: %w", err)
	}
	info, err := s.attach(root, alias, true)
	if err != nil {
		return info, errors.Join(err, root.Close())
	}
	return info, nil
}

func (s *Session) attach(root tool.Root, alias string, owned bool) (RootInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return RootInfo{}, ErrBusy
	}
	if root.Dir() == "" {
		return RootInfo{}, ErrUnknownRoot
	}
	if alias == "" {
		alias = filepath.Base(root.Dir())
	}
	if strings.TrimSpace(alias) != alias || alias == "" || strings.HasPrefix(alias, "r_") || strings.ContainsAny(alias, "/\\\x00") {
		return RootInfo{}, ErrAlias
	}
	r := s.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return RootInfo{}, ErrClosed
	}
	id := rootID(root.Dir())
	if previous, ok := s.aliases[alias]; ok && previous != id {
		return RootInfo{}, ErrAlias
	}
	if _, ok := r.roots[alias]; ok && alias != id {
		return RootInfo{}, ErrAlias
	}
	if _, exists := s.aliases[alias]; !exists && len(s.aliases) >= r.limit {
		return RootInfo{}, ErrCapacity
	}
	if existing, ok := r.roots[id]; ok {
		if owned {
			if err := root.Close(); err != nil {
				return RootInfo{}, fmt.Errorf("workspace: close duplicate root: %w", err)
			}
		}
		root = existing.root
	} else {
		if len(r.roots) >= r.limit || len(s.aliases) >= r.limit {
			return RootInfo{}, ErrCapacity
		}
		r.roots[id] = registered{root: root, owned: owned}
		r.revision++
	}
	if current, ok := s.members[id]; ok {
		return RootInfo{ID: id, Alias: current, Path: root.Dir(), Primary: id == r.primary}, nil
	}
	s.members[id], s.aliases[alias] = alias, id
	s.revision++
	return RootInfo{ID: id, Alias: alias, Path: root.Dir()}, nil
}

// Detach revokes membership but reserves aliases and nested ownership boundaries.
func (s *Session) Detach(repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := s.id(repo)
	if err != nil {
		return err
	}
	if s.busy {
		return ErrBusy
	}
	if id == s.registry.primary {
		return ErrPrimary
	}
	delete(s.members, id)
	s.revision++
	return nil
}

// Snapshot returns deterministic membership and its publication revision.
func (s *Session) Snapshot() ([]RootInfo, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	out := make([]RootInfo, 0, len(s.members))
	for id, alias := range s.members {
		out = append(out, RootInfo{ID: id, Alias: alias, Path: s.registry.roots[id].root.Dir(), Primary: id == s.registry.primary})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, s.revision
}

func (s *Session) id(repo string) (string, error) {
	if repo == "" {
		repo = s.registry.primary
	}
	if _, ok := s.members[repo]; ok {
		return repo, nil
	}
	id, ok := s.aliases[repo]
	if !ok {
		return "", ErrUnknownRoot
	}
	if _, ok := s.members[id]; !ok {
		return "", ErrUnknownRoot
	}
	return id, nil
}

// Lease captures membership for a resolved path; consumers must use Open/Stat
// and Publish rather than treating a previously resolved path as authorization.
type Lease struct {
	RootID             string
	MembershipRevision uint64
	Path               string
	RelativePath       string
	session            *Session
	topologyRevision   uint64
}

// Resolve authorizes a root-relative path without reading file contents.
func (s *Session) Resolve(repo, path string) (Lease, error) {
	s.mu.RLock()
	id, err := s.id(repo)
	revision := s.revision
	s.registry.mu.RLock()
	root := s.registry.roots[id].root
	topology := s.registry.revision
	closed := s.registry.closed
	s.registry.mu.RUnlock()
	s.mu.RUnlock()
	if err != nil {
		return Lease{}, err
	}
	if closed {
		return Lease{}, ErrClosed
	}
	if path == "" {
		path = "."
	}
	if filepath.IsAbs(path) {
		return Lease{}, tool.ErrOutsideRoot
	}
	abs, err := root.ResolveExact(filepath.Join(root.Dir(), path))
	if err != nil {
		return Lease{}, err
	}
	l := Lease{RootID: id, MembershipRevision: revision, Path: abs, RelativePath: root.Rel(abs), session: s, topologyRevision: topology}
	if err := l.Verify(); err != nil {
		return Lease{}, err
	}
	return l, nil
}

// EntryID gives a bounded opaque identifier over exact path bytes.
// Its disk-backed mapping belongs to the shared index, not this registry.
func EntryID(rootID, path string) string {
	sum := sha256.Sum256([]byte(rootID + "\x00" + path))
	return "e_" + hex.EncodeToString(sum[:])
}

// EntryLookup loads a root-scoped raw relative path from the shared index.
type EntryLookup func(rootID, entryID string) (string, error)

// ResolveEntry reauthorizes a persisted entry mapping and checks its identity.
func (s *Session) ResolveEntry(repo, entryID string, lookup EntryLookup) (Lease, error) {
	base, err := s.Resolve(repo, ".")
	if err != nil {
		return Lease{}, err
	}
	if lookup == nil {
		return Lease{}, ErrStaleEntry
	}
	path, err := lookup(base.RootID, entryID)
	if err != nil {
		return Lease{}, fmt.Errorf("%w: %w", ErrStaleEntry, err)
	}
	if EntryID(base.RootID, path) != entryID {
		return Lease{}, ErrStaleEntry
	}
	l, err := s.Resolve(base.RootID, path)
	if err == nil && l.MembershipRevision != base.MembershipRevision {
		return Lease{}, ErrChanged
	}
	return l, err
}

// Valid checks current membership; it does not establish source freshness.
func (s *Session) Valid(l Lease) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	_, ok := s.members[l.RootID]
	return l.session == s && ok && l.MembershipRevision == s.revision && !s.registry.closed && l.topologyRevision == s.registry.revision
}

// Publish serializes publication against membership changes. The callback must
// not call Session methods; it should only publish an already prepared result.
func (s *Session) Publish(l Lease, publish func() error) error {
	if err := l.Verify(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l.session != s || l.MembershipRevision != s.revision {
		return ErrChanged
	}
	if _, ok := s.members[l.RootID]; !ok {
		return ErrChanged
	}
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	if s.registry.closed {
		return ErrClosed
	}
	if l.topologyRevision != s.registry.revision {
		return ErrChanged
	}
	return publish()
}

func rootID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "r_" + hex.EncodeToString(sum[:16])
}

func within(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// boundary checks lexical and resolved ancestry without building an all-files
// allowlist. No registry lock is held across filesystem I/O. This is not a sandbox against
// adversarial concurrent namespace rewrites inside a root.
func (r *Registry) boundary(id, path string) error {
	r.mu.RLock()
	root := r.roots[id].root
	paths := make([]string, 0, len(r.roots))
	for other, entry := range r.roots {
		if other != id {
			paths = append(paths, entry.root.Dir())
		}
	}
	r.mu.RUnlock()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("workspace: resolve source: %w", err)
	}
	for _, other := range paths {
		if within(root.Dir(), other) && (within(other, path) || within(other, real)) {
			return ErrBoundary
		}
	}
	for _, p := range []string{path, real} {
		info, err := root.Stat(p)
		if err != nil {
			return fmt.Errorf("workspace: stat source: %w", err)
		}
		if !info.IsDir() {
			p = filepath.Dir(p)
		}
		for p != root.Dir() && within(root.Dir(), p) {
			_, err := root.Stat(filepath.Join(p, ".git"))
			if err == nil {
				return ErrBoundary
			}
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("workspace: inspect boundary: %w", err)
			}
			p = filepath.Dir(p)
		}
	}
	return nil
}
