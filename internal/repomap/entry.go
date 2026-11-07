package repomap

import (
	"context"

	"github.com/mrYush/hint/internal/workspace"
)

// ResolveEntry reauthorizes an opaque entry handle using its disk-backed path.
// The handle carries no grant and never selects a different repository.
func (m *Manager) ResolveEntry(ctx context.Context, space *workspace.Session, repo, entryID string) (workspace.Lease, error) {
	return space.ResolveEntry(repo, entryID, func(root, id string) (string, error) {
		file, err := m.store.EntryLookup(ctx, root, id)
		return string(file.Path), err
	})
}
