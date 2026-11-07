package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// QueueItem is a durable discovery or reindex task. Revision makes Ack safe
// when a new invalidation arrives while an earlier observation is running.
type QueueItem struct {
	Sequence int64
	RootID   string
	Path     []byte
	Revision int64
}

// Enqueue durably coalesces work for one raw root-relative path. The special
// path "." denotes initial discovery of the attached root itself.
func (s *Store) Enqueue(ctx context.Context, rootID string, path []byte) error {
	if len(path) > s.options.MaxRecordBytes {
		return ErrTooLarge
	}
	if !validRoot(rootID) || (!validPath(path) && string(path) != ".") {
		return fmt.Errorf("index: invalid frontier root or path")
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO frontier(root,path) VALUES (?,?) ON CONFLICT(root,path) DO UPDATE SET revision=revision+1,not_before=0`, rootID, path); err != nil {
		return storeError("enqueue discovery", err)
	}
	return nil
}

// Take returns a bounded durable queue prefix without removing it. A caller
// admits one worker per root; a crash before Ack leaves work available again.
func (s *Store) Take(ctx context.Context, rootID string, limit int) ([]QueueItem, error) {
	return s.TakeReady(ctx, rootID, limit, time.Now())
}

// TakeReady seeks eligible work in retry-time/sequence order without scanning
// delayed rows. It retains every item until an explicit Ack or Defer.
func (s *Store) TakeReady(ctx context.Context, rootID string, limit int, now time.Time) ([]QueueItem, error) {
	if limit < 1 || limit > maxScanRecords {
		return nil, ErrTooLarge
	}
	// Even maximum-size paths must fit the finite in-memory work batch.
	limit = min(limit, max(1, s.options.MaxSourceBytes/(s.options.MaxRecordBytes+maxIDBytes+24)))
	ctx, cancel := s.context(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,root,path,revision FROM frontier INDEXED BY frontier_ready WHERE root=? AND not_before<=? ORDER BY not_before,sequence LIMIT ?", rootID, now.UnixMilli(), limit)
	if err != nil {
		return nil, storeError("read frontier", err)
	}
	items := make([]QueueItem, 0, limit)
	for rows.Next() {
		var item QueueItem
		if err = rows.Scan(&item.Sequence, &item.RootID, &item.Path, &item.Revision); err != nil {
			break
		}
		items = append(items, item)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return nil, storeError("read frontier", err)
	}
	return items, nil
}

// HasPendingWork includes delayed tasks, so an empty ready page cannot claim
// completed discovery while unreadable directories still await a retry.
func (s *Store) HasPendingWork(ctx context.Context, rootID string) (bool, error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM frontier WHERE root=? LIMIT 1", rootID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, storeError("inspect unfinished frontier", err)
	}
	return true, nil
}

// Defer retains an unfinished task until a bounded retry time. A newer Enqueue
// revision is never delayed by a failed older observation.
func (s *Store) Defer(ctx context.Context, item QueueItem, until time.Time) error {
	if until.UnixMilli() <= 0 {
		return fmt.Errorf("index: invalid frontier retry time")
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, "UPDATE frontier SET not_before=? WHERE sequence=? AND root=? AND revision=?", until.UnixMilli(), item.Sequence, item.RootID, item.Revision); err != nil {
		return storeError("defer frontier retry", err)
	}
	return nil
}

// QueueLookup seeks the current task by its unique root/path key. Retained
// directory streams use it after rotation changes the durable sequence.
func (s *Store) QueueLookup(ctx context.Context, rootID string, path []byte) (QueueItem, error) {
	if len(path) > s.options.MaxRecordBytes {
		return QueueItem{}, ErrTooLarge
	}
	if !validRoot(rootID) || (!validPath(path) && string(path) != ".") {
		return QueueItem{}, fmt.Errorf("index: invalid frontier root or path")
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	item := QueueItem{RootID: rootID, Path: append([]byte(nil), path...)}
	err := s.db.QueryRowContext(ctx, "SELECT sequence,revision FROM frontier WHERE root=? AND path=?", rootID, path).Scan(&item.Sequence, &item.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return QueueItem{}, ErrNotFound
	}
	if err != nil {
		return QueueItem{}, storeError("lookup frontier", err)
	}
	return item, nil
}

// Ack removes only the exact queued revision that the caller completed.
// A concurrent Enqueue therefore remains visible for another worker slice.
func (s *Store) Ack(ctx context.Context, item QueueItem) error {
	ctx, cancel := s.context(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, "DELETE FROM frontier WHERE sequence=? AND root=? AND revision=?", item.Sequence, item.RootID, item.Revision); err != nil {
		return storeError("acknowledge frontier", err)
	}
	return nil
}

// Rotate atomically moves an unfinished task behind the current frontier.
// If Enqueue changed its revision, the newer work stays untouched. A crash
// or quota failure rolls back both deletion and insertion together.
func (s *Store) Rotate(ctx context.Context, item QueueItem) (err error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeError("begin frontier rotation", err)
	}
	defer rollbackTransaction(tx, &err)
	var path []byte
	var notBefore int64
	err = tx.QueryRowContext(ctx, "SELECT path,not_before FROM frontier WHERE sequence=? AND root=? AND revision=?", item.Sequence, item.RootID, item.Revision).Scan(&path, &notBefore)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return storeError("read rotating task", err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM frontier WHERE sequence=? AND root=? AND revision=?", item.Sequence, item.RootID, item.Revision); err != nil {
		return storeError("remove rotating task", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO frontier(root,path,revision,not_before) VALUES (?,?,?,?)", item.RootID, path, item.Revision, notBefore); err != nil {
		return storeError("append rotating task", err)
	}
	return commitTransaction(tx)
}

// EntryPage is a bounded listing of current direct children, independent of
// declaration snapshots. Each file still requires a current authority check.
type EntryPage struct {
	Files     []File
	Next      []byte
	ReadBytes int64
	Exhausted bool
}

// ListEntries seeks a direct-parent index and reads a bounded page of current
// metadata. Empty parent lists top-level entries; after is an exclusive raw path.
func (s *Store) ListEntries(ctx context.Context, rootID string, parent, after []byte, limit int, byteBudget int64) (EntryPage, error) {
	return s.listEntries(ctx, rootID, parent, after, limit, byteBudget, false)
}

// ScanEntries seeks current metadata across a root for bounded reconciliation.
// It does not scan the filesystem or retain a complete in-memory file list.
func (s *Store) ScanEntries(ctx context.Context, rootID string, after []byte, limit int, byteBudget int64) (EntryPage, error) {
	return s.listEntries(ctx, rootID, nil, after, limit, byteBudget, true)
}

func (s *Store) listEntries(ctx context.Context, rootID string, parent, after []byte, limit int, byteBudget int64, all bool) (page EntryPage, err error) {
	if limit < 1 || limit > maxScanRecords || byteBudget < maxIDBytes+16 || byteBudget > 16<<20 {
		return EntryPage{}, ErrTooLarge
	}
	limit = min(limit, int(byteBudget/(maxIDBytes+16)))
	if parent == nil {
		parent = []byte{}
	}
	if after == nil {
		after = []byte{}
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return EntryPage{}, storeError("begin child listing", err)
	}
	defer rollbackTransaction(tx, &err)
	statement := `SELECT h.id,h.version,length(f.data) FROM heads h INDEXED BY heads_parent JOIN files f ON f.root=h.root AND f.id=h.id AND f.version=h.version WHERE h.root=? AND h.parent=? AND h.path>? ORDER BY h.path,h.id LIMIT ?`
	args := []any{rootID, parent, after, limit}
	if all {
		statement = `SELECT h.id,h.version,length(f.data) FROM heads h INDEXED BY heads_path JOIN files f ON f.root=h.root AND f.id=h.id AND f.version=h.version WHERE h.root=? AND h.path>? ORDER BY h.path LIMIT ?`
		args = []any{rootID, after, limit}
	}
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return EntryPage{}, storeError("read child headers", err)
	}
	headers := make([]recordHeader, 0, limit)
	for rows.Next() {
		var header recordHeader
		if err = rows.Scan(&header.id, &header.version, &header.size); err != nil {
			break
		}
		if header.size < 0 || header.size > int64(s.options.MaxRecordBytes) {
			err = ErrTooLarge
			break
		}
		page.ReadBytes += int64(len(header.id)) + 16
		headers = append(headers, header)
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return EntryPage{}, storeError("read child headers", err)
	}
	page.Exhausted = len(headers) < limit
	for _, header := range headers {
		if header.size > byteBudget-page.ReadBytes {
			page.Exhausted = false
			break
		}
		var data []byte
		if err = tx.QueryRowContext(ctx, "SELECT data FROM files WHERE root=? AND id=? AND version=?", rootID, header.id, header.version).Scan(&data); err != nil {
			return EntryPage{}, storeError("read child", err)
		}
		page.ReadBytes += int64(len(data))
		var file File
		if err = json.Unmarshal(data, &file); err != nil {
			return EntryPage{}, fmt.Errorf("index: decode child: %w", err)
		}
		page.Files = append(page.Files, file)
		page.Next = file.Path
	}
	return page, commitTransaction(tx)
}
