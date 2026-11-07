package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// PolicyRevision returns the durable revision of observed ignore policies.
// It is not a filesystem watcher or proof of an unobserved policy's freshness.
func (s *Store) PolicyRevision(ctx context.Context, rootID string) (uint64, error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	var epoch uint64
	err := s.db.QueryRowContext(ctx, "SELECT epoch FROM policy_epochs WHERE root=?", rootID).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, storeError("read policy revision", err)
	}
	return epoch, nil
}

// ObservePolicy atomically stores one bounded fingerprint and increments the
// root revision only on change. A never-observed absent file needs no row.
// Observations consume the shared disk allowance, not a resident path map.
func (s *Store) ObservePolicy(ctx context.Context, rootID string, path []byte, fingerprint string) (epoch uint64, err error) {
	if len(path)+len(fingerprint) > s.options.MaxRecordBytes || len(fingerprint) > 256 {
		return 0, ErrTooLarge
	}
	if !validRoot(rootID) || !validPath(path) || fingerprint == "" {
		return 0, fmt.Errorf("index: invalid policy observation")
	}
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, storeError("begin policy observation", err)
	}
	defer rollbackTransaction(tx, &err)
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM policy_files WHERE root=? AND path=?", rootID, path).Scan(&previous)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return 0, storeError("read policy fingerprint", err)
	}
	if previous == fingerprint || missing && fingerprint == "absent" {
		err = tx.QueryRowContext(ctx, "SELECT epoch FROM policy_epochs WHERE root=?", rootID).Scan(&epoch)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, storeError("read policy revision", err)
		}
		return epoch, commitTransaction(tx)
	}
	if fingerprint == "absent" {
		_, err = tx.ExecContext(ctx, "DELETE FROM policy_files WHERE root=? AND path=?", rootID, path)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO policy_files(root,path,fingerprint) VALUES (?,?,?) ON CONFLICT(root,path) DO UPDATE SET fingerprint=excluded.fingerprint`, rootID, path, fingerprint)
	}
	if err != nil {
		return 0, storeError("store policy fingerprint", err)
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO policy_epochs(root,epoch) VALUES (?,1) ON CONFLICT(root) DO UPDATE SET epoch=epoch+1 RETURNING epoch`, rootID).Scan(&epoch)
	if err != nil {
		return 0, storeError("advance policy revision", err)
	}
	return epoch, commitTransaction(tx)
}
