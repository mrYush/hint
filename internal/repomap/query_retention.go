package repomap

import (
	"context"
	"errors"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/workspace"
)

func invalidReservation(err error) bool {
	return errors.Is(err, index.ErrStale) || errors.Is(err, workspace.ErrChanged) || errors.Is(err, workspace.ErrClosed) || errors.Is(err, workspace.ErrUnknownRoot) || definitiveAccess(err)
}

func (m *Manager) releaseQuery(q *savedQuery, retire bool) error {
	if retire {
		m.mu.Lock()
		q.retired = true
		m.mu.Unlock()
	}
	// Cleanup must survive cancellation of both the request and the manager.
	// Failed terminal cleanup stays owned and is retried before new work.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.store.Release(ctx, q.snapshot.ID); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.queries, q.snapshot.ID)
	m.mu.Unlock()
	return nil
}

func (m *Manager) retryRetiredQueries(parent context.Context) error {
	m.mu.Lock()
	var ids []string
	for id, q := range m.queries {
		if q.retired {
			ids = append(ids, id)
			if len(ids) == 8 {
				break
			}
		}
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 100*time.Millisecond)
	defer cancel()
	if err := m.store.ReleaseMany(ctx, ids); err != nil {
		return err
	}
	m.mu.Lock()
	for _, id := range ids {
		delete(m.queries, id)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) releaseOwnedQueries() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.queries))
	for id := range m.queries {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.store.ReleaseMany(ctx, ids); err != nil {
		return err
	}
	m.mu.Lock()
	clear(m.queries)
	m.mu.Unlock()
	return nil
}
