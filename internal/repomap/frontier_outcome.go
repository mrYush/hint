package repomap

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

const sourceRetryDelay = 5 * time.Second

func sourceAccessFailure(err error) bool {
	var pathError *os.PathError
	return definitiveAccess(err) || errors.Is(err, workspace.ErrNotDirectory) || errors.Is(err, workspace.ErrNotRegular) || errors.As(err, &pathError)
}

func (m *Manager) sourceFailure(ctx context.Context, work rootWork, item index.QueueItem, cause error) error {
	reason, retry := "unavailable", true
	switch {
	case errors.Is(cause, workspace.ErrBoundary):
		reason, retry = "nested_root", false
	case errors.Is(cause, tool.ErrDenied), errors.Is(cause, tool.ErrOutsideRoot):
		reason, retry = "excluded", false
	case errors.Is(cause, os.ErrNotExist):
		reason, retry = "deleted", false
	}
	return m.recordSourceFailure(ctx, work, item, cause, reason, retry)
}

func (m *Manager) retrySource(ctx context.Context, work rootWork, item index.QueueItem, cause error) error {
	return m.recordSourceFailure(ctx, work, item, cause, "unavailable", true)
}

func (m *Manager) recordSourceFailure(ctx context.Context, work rootWork, item index.QueueItem, cause error, reason string, retry bool) error {
	if ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, workspace.ErrChanged) || errors.Is(cause, workspace.ErrClosed) || errors.Is(cause, workspace.ErrUnknownRoot) || errors.Is(cause, index.ErrCapacity) || errors.Is(cause, index.ErrTooLarge) {
		return errors.Join(cause, ctx.Err())
	}
	// A failed or newly forbidden traversal must not retain a descriptor that
	// could later resume through a parent or a formerly authorized child.
	var closeErr error
	for key, stream := range m.streams {
		path, scope := string(stream.item.Path), string(item.Path)
		if stream.lease.RootID == item.RootID && (scope == "." || path == scope || strings.HasPrefix(path, scope+"/")) {
			delete(m.streams, key)
			closeErr = errors.Join(closeErr, stream.file.Close())
		}
	}
	if closeErr != nil {
		return errors.Join(cause, closeErr)
	}
	parent, err := work.space.Resolve(work.info.ID, ".")
	if err != nil {
		if errors.Is(err, workspace.ErrChanged) || errors.Is(err, workspace.ErrClosed) || errors.Is(err, workspace.ErrUnknownRoot) {
			return err
		}
		// No current root authority means no metadata publication. Retain the
		// durable task for recovery instead of declaring the root empty.
		retry = true
	} else if string(item.Path) != "." {
		file, lookupErr := m.store.EntryLookup(ctx, item.RootID, index.EntryID(item.RootID, string(item.Path)))
		if lookupErr != nil && !errors.Is(lookupErr, index.ErrNotFound) {
			return lookupErr
		}
		if errors.Is(lookupErr, index.ErrNotFound) {
			file = index.File{RootID: item.RootID, Path: item.Path, Kind: index.KindFile}
		}
		if reason == "nested_root" {
			file.Kind = index.KindBoundary
		}
		if file.State != index.StateOmitted || file.Reason != reason {
			file.State, file.Reason, file.Hash, file.ObservedAt = index.StateOmitted, reason, "", m.now()
			if err := parent.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
				return err
			}
		}
	} else {
		retry = true
	}
	if retry {
		if err := m.store.Defer(ctx, item, m.now().Add(sourceRetryDelay)); err != nil {
			return err
		}
		m.setStatus(item.RootID, Status{State: "partial", Reason: "retry_wait"})
		return nil
	}
	return m.store.Ack(ctx, item)
}

func (m *Manager) deferReconciliation(ctx context.Context, work rootWork, cause error) error {
	if ctx.Err() != nil {
		return errors.Join(cause, ctx.Err())
	}
	if err := m.store.Enqueue(ctx, work.info.ID, []byte(".")); err != nil {
		return err
	}
	item, err := m.store.QueueLookup(ctx, work.info.ID, []byte("."))
	if err != nil {
		return err
	}
	if err := m.retrySource(ctx, work, item, cause); err != nil {
		return err
	}
	m.mu.Lock()
	if current := m.roots[work.info.ID]; current != nil {
		current.sweepAt = m.now().Add(sourceRetryDelay)
	}
	m.mu.Unlock()
	return nil
}
