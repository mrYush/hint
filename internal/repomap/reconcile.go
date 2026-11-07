package repomap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func (m *Manager) maintenance(ctx context.Context, work rootWork) error {
	m.mu.Lock()
	current := m.roots[work.info.ID]
	if current == nil || m.now().Before(current.gcAt) {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if _, err := m.store.GC(ctx, work.info.ID, 32); err != nil {
		return err
	}
	m.mu.Lock()
	if current := m.roots[work.info.ID]; current != nil {
		current.gcAt = m.now().Add(5 * time.Second)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) reconcileSlice(ctx context.Context, work rootWork) error {
	m.mu.Lock()
	current := m.roots[work.info.ID]
	if current == nil || m.now().Before(current.sweepAt) {
		m.mu.Unlock()
		return nil
	}
	after := append([]byte(nil), current.sweepAfter...)
	m.mu.Unlock()
	page, err := m.store.ScanEntries(ctx, work.info.ID, after, 8, 128<<10)
	if err != nil {
		return err
	}
	parent, err := work.space.Resolve(work.info.ID, ".")
	if err != nil {
		return err
	}
	for _, file := range page.Files {
		lease, err := work.space.Resolve(work.info.ID, string(file.Path))
		reason := ""
		switch {
		case errors.Is(err, workspace.ErrBoundary):
			reason = "nested_root"
		case errors.Is(err, tool.ErrDenied):
			reason = "excluded"
		case sourceAccessFailure(err):
			reason = "unavailable"
		case err != nil:
			return err
		default:
			info, statErr := lease.Stat()
			switch {
			case errors.Is(statErr, os.ErrNotExist):
				reason = "deleted"
			case errors.Is(statErr, workspace.ErrChanged) || ctx.Err() != nil:
				return errors.Join(statErr, ctx.Err())
			case statErr != nil:
				reason = "unavailable"
			case file.State == index.StateOmitted && file.Reason != "unsupported" && file.Reason != "oversized", info.Size() != file.Size || !info.ModTime().Equal(file.ModTime):
				if err := m.store.Enqueue(ctx, file.RootID, file.Path); err != nil {
					return err
				}
			}
		}
		if reason != "" && (file.State != index.StateOmitted || file.Reason != reason) {
			file.State, file.Reason, file.Hash, file.ObservedAt = index.StateOmitted, reason, "", m.now()
			if err := parent.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
				return err
			}
		}
		m.mu.Lock()
		if current := m.roots[work.info.ID]; current != nil {
			current.sweepAfter = append(current.sweepAfter[:0], file.Path...)
		}
		m.mu.Unlock()
	}
	if page.Exhausted {
		m.mu.Lock()
		if current := m.roots[work.info.ID]; current != nil {
			current.sweepAfter = nil
			current.sweepAt = m.now().Add(time.Minute)
		}
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) observePolicy(ctx context.Context, work rootWork, directory string) error {
	path := filepath.Join(directory, ".gitignore")
	lease, err := work.space.Resolve(work.info.ID, path)
	if err != nil {
		return err
	}
	fingerprint, err := policyFingerprint(ctx, lease)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.roots[work.info.ID]
	if current == nil {
		return workspace.ErrChanged
	}
	previous, known := current.policyFiles[path]
	if !known && fingerprint == "absent" {
		return nil
	}
	if known && previous == fingerprint {
		return nil
	}
	current.policyEpoch++
	// This is a small observation cache, never a resident all-files allowlist.
	// Additional policies still revoke cursors when encountered, without growing RAM.
	if known || len(current.policyFiles) < 128 {
		current.policyFiles[path] = fingerprint
	}
	return nil
}

func policyFingerprint(ctx context.Context, lease workspace.Lease) (value string, resultErr error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := lease.OpenRegular()
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.Join(err, errors.New("repo map: ignore source unavailable"))
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(contextReader{ctx: ctx, reader: f}, 64<<10)); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%s", info.Size(), info.ModTime().UnixNano(), hex.EncodeToString(h.Sum(nil))), nil
}
