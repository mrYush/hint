package index

import (
	"context"
	"database/sql"
	"errors"
)

func omissionBucket(reason string) string {
	switch reason {
	case "unsupported", "oversized", "non_regular", "parse_failed", "paused", "deadline_exceeded", "work_budget_exceeded", "output_budget_exceeded", "extractor_unavailable", "source_changed", "unavailable", "excluded", "deleted":
		return reason
	default:
		return "other"
	}
}

func updateOmissionCounts(ctx context.Context, tx *sql.Tx, file, old File, oldVersion, previousGeneration uint64, omittedBefore int64) error {
	if stateCount(file, StateOmitted) == 0 && stateCount(old, StateOmitted) == 0 {
		return nil
	}
	var cutover uint64
	err := tx.QueryRowContext(ctx, "SELECT cutover FROM coverage_roots WHERE root=?", file.RootID).Scan(&cutover)
	if errors.Is(err, sql.ErrNoRows) {
		// Seed one bounded root aggregate, never scan its historical files.
		// Source versions at/before this cutover belong to unclassified until
		// an ordinary source update replaces their contribution exactly once.
		cutover = previousGeneration
		if _, err = tx.ExecContext(ctx, "INSERT INTO coverage_roots(root,cutover) VALUES (?,?)", file.RootID, cutover); err != nil {
			return storeError("initialize coverage cutover", err)
		}
		if omittedBefore > 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO omission_counts(root,reason,count) VALUES (?,'unclassified',?)", file.RootID, omittedBefore); err != nil {
				return storeError("initialize legacy omissions", err)
			}
		}
	} else if err != nil {
		return storeError("read coverage cutover", err)
	}
	if stateCount(old, StateOmitted) == 1 {
		bucket := omissionBucket(old.Reason)
		if oldVersion <= cutover {
			bucket = "unclassified"
		}
		result, err := tx.ExecContext(ctx, "UPDATE omission_counts SET count=count-1 WHERE root=? AND reason=? AND count>0", file.RootID, bucket)
		if err != nil {
			return storeError("retire omission count", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return storeError("verify retired omission count", err)
		}
		if changed != 1 {
			return errors.New("index: omission counter is missing its previous contribution")
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM omission_counts WHERE root=? AND reason=? AND count=0", file.RootID, bucket); err != nil {
			return storeError("remove empty omission counter", err)
		}
	}
	if stateCount(file, StateOmitted) == 1 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO omission_counts(root,reason,count) VALUES (?,?,1) ON CONFLICT(root,reason) DO UPDATE SET count=count+1`, file.RootID, omissionBucket(file.Reason)); err != nil {
			return storeError("record omission count", err)
		}
	}
	return nil
}

// CoverageStatus reads current root counters and bounded reason buckets in
// one transaction. Legacy counts remain explicitly unclassified until normal
// source updates classify them; no startup or query scans all source records.
func (s *Store) CoverageStatus(ctx context.Context, rootID string) (status Status, omissions map[string]int64, err error) {
	ctx, cancel := s.context(ctx)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return status, nil, storeError("begin coverage read", err)
	}
	defer rollbackTransaction(tx, &err)
	err = tx.QueryRowContext(ctx, "SELECT generation,files,pending,indexed,omitted FROM roots WHERE root=?", rootID).
		Scan(&status.Generation, &status.Files, &status.Pending, &status.Indexed, &status.Omitted)
	if errors.Is(err, sql.ErrNoRows) {
		return Status{}, nil, nil
	}
	if err != nil {
		return status, nil, storeError("read counters", err)
	}
	if status.Omitted == 0 {
		return status, nil, commitTransaction(tx)
	}
	var cutover uint64
	err = tx.QueryRowContext(ctx, "SELECT cutover FROM coverage_roots WHERE root=?", rootID).Scan(&cutover)
	if errors.Is(err, sql.ErrNoRows) {
		return status, map[string]int64{"unclassified": status.Omitted}, commitTransaction(tx)
	}
	if err != nil {
		return status, nil, storeError("read coverage state", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT reason,count FROM omission_counts WHERE root=? AND length(reason)<=32 ORDER BY reason LIMIT 32", rootID)
	if err != nil {
		return status, nil, storeError("read omission counts", err)
	}
	omissions = make(map[string]int64)
	var total int64
	for rows.Next() {
		var reason string
		var count int64
		if err = rows.Scan(&reason, &count); err != nil {
			break
		}
		if (reason != "unclassified" && reason != omissionBucket(reason)) || count <= 0 || count > status.Omitted-total {
			err = errors.New("index: inconsistent omission buckets")
			break
		}
		omissions[reason], total = count, total+count
	}
	if err = errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return status, nil, storeError("read omission buckets", err)
	}
	if total != status.Omitted {
		return status, nil, errors.New("index: omission buckets do not match aggregate count")
	}
	return status, omissions, commitTransaction(tx)
}
