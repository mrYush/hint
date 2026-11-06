package workspace

import (
	"path/filepath"
	"sort"
)

// Update stages membership changes while preventing a new turn, persists the
// resulting snapshot, then activates it. Callbacks run without session locks.
// Registered root handles/boundaries may survive a failed transaction, but no
// additional membership is exposed and the failed draft is never published.
func (s *Session) Update(change func(*Session) error, persist func([]RootInfo, uint64) error) error {
	s.mu.Lock()
	if s.blocked {
		s.mu.Unlock()
		return ErrChanged
	}
	if s.busy {
		s.mu.Unlock()
		return ErrBusy
	}
	s.busy = true
	draft := &Session{registry: s.registry, identity: s.identity, revision: s.revision, members: make(map[string]string, len(s.members)), aliases: make(map[string]string, len(s.aliases))}
	for k, v := range s.members {
		draft.members[k] = v
	}
	for k, v := range s.aliases {
		draft.aliases[k] = v
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.busy = false; s.mu.Unlock() }()
	if err := change(draft); err != nil {
		return err
	}
	roots, revision := draft.Snapshot()
	if persist != nil {
		if err := persist(roots, revision); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.members, s.aliases, s.revision = draft.members, draft.aliases, draft.revision
	s.mu.Unlock()
	return nil
}

// AdvanceRevision preserves the persisted revision floor when restoring a new
// session, so subsequent recorded membership changes remain monotonic.
func (s *Session) AdvanceRevision(revision uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.busy && revision > s.revision {
		s.revision = revision
	}
}

// Reserve restores a historical alias and ownership boundary without attaching
// or opening it. Removing a child root must not widen parent access on restart.
func (s *Session) Reserve(info RootInfo) error {
	if !validAlias(info.Alias) {
		return ErrAlias
	}
	var err error
	info, err = RestoreInfo(info)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	r := s.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if (info.ID == r.primary) != info.Primary {
		return ErrChanged
	}
	if id, exists := s.aliases[info.Alias]; exists && id != info.ID {
		return ErrAlias
	}
	if _, exists := s.aliases[info.Alias]; !exists && len(s.aliases) >= r.limit {
		return ErrCapacity
	}
	if _, exists := r.roots[info.ID]; !exists {
		if len(r.roots) >= r.limit {
			return ErrCapacity
		}
		r.roots[info.ID] = registered{path: filepath.Clean(info.Path)}
		r.revision++
	}
	s.aliases[info.Alias] = info.ID
	return nil
}

// Reservations returns bounded historical alias/ownership metadata, including
// detached roots, for durable session authority and fork restoration.
func (s *Session) Reservations() []RootInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.registry.mu.RLock()
	defer s.registry.mu.RUnlock()
	out := make([]RootInfo, 0, len(s.aliases))
	for alias, id := range s.aliases {
		out = append(out, RootInfo{ID: id, Alias: alias, Path: s.registry.roots[id].path, Primary: id == s.registry.primary})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}
