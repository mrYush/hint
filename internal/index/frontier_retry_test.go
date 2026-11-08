package index

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFrontierDelaySurvivesRestartAndNewInvalidation(t *testing.T) {
	s, path := openTestStore(t, Options{})
	now := time.Now()
	if err := s.Enqueue(t.Context(), "r_test", []byte("retry")); err != nil {
		t.Fatal(err)
	}
	old, err := s.QueueLookup(t.Context(), "r_test", []byte("retry"))
	if err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Minute)
	if err := s.Defer(t.Context(), old, until); err != nil {
		t.Fatal(err)
	}
	if err := s.Rotate(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if pending, err := reopened.HasPendingWork(t.Context(), "r_test"); err != nil || !pending {
		t.Fatalf("delayed task lost: %v %v", pending, err)
	}
	if ready, err := reopened.TakeReady(t.Context(), "r_test", 1, now); err != nil || len(ready) != 0 {
		t.Fatalf("delayed task became hot: %v %v", ready, err)
	}
	ready, err := reopened.TakeReady(t.Context(), "r_test", 1, until)
	if err != nil || len(ready) != 1 {
		t.Fatalf("due retry unavailable: %v %v", ready, err)
	}
	stale := ready[0]
	if err := reopened.Enqueue(t.Context(), "r_test", []byte("retry")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Defer(t.Context(), stale, until.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Ack(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rotate(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	ready, err = reopened.TakeReady(t.Context(), "r_test", 1, now)
	if err != nil || len(ready) != 1 || ready[0].Revision <= stale.Revision {
		t.Fatalf("new invalidation delayed/lost: %v %v", ready, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := reopened.Defer(canceled, ready[0], until); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retry update: %v", err)
	}
	if err := reopened.Ack(t.Context(), ready[0]); err != nil {
		t.Fatal(err)
	}
	if pending, err := reopened.HasPendingWork(t.Context(), "r_test"); err != nil || pending {
		t.Fatalf("completed queue did not drain: %v %v", pending, err)
	}
}

func TestFrontierV2MigrationPreservesWork(t *testing.T) {
	s, path := openTestStore(t, Options{})
	file, symbols := testSource("keep.go", "Keep")
	putTestSource(t, s, file, symbols)
	pin := pinTestRoot(t, s)
	if err := s.Enqueue(t.Context(), file.RootID, file.Path); err != nil {
		t.Fatal(err)
	}
	// Recreate the previous frontier layout while retaining source data,
	// active reservations and queued work in the rest of the v2 catalog.
	if _, err := s.db.ExecContext(t.Context(), "DROP INDEX frontier_ready; ALTER TABLE frontier DROP COLUMN not_before; PRAGMA user_version=2;"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	if ready, err := reopened.Take(t.Context(), file.RootID, 1); err != nil || len(ready) != 1 || string(ready[0].Path) != "keep.go" {
		t.Fatalf("migration lost queued work: %v %v", ready, err)
	}
	if _, err := reopened.EntryLookup(t.Context(), file.RootID, EntryID(file.RootID, "keep.go")); err != nil {
		t.Fatal(err)
	}
	if records := visibleRecords(t, reopened, pin, Query{}); len(records) != 1 {
		t.Fatalf("migration lost pinned records: %+v", records)
	}
}

func TestFrontierRetryUsesBoundedSeek(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN SELECT sequence,root,path,revision FROM frontier INDEXED BY frontier_ready WHERE root=? AND not_before<=? ORDER BY not_before,sequence LIMIT ?", "r_test", time.Now().UnixMilli(), 1)
	if err != nil {
		t.Fatal(err)
	}
	var details string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details += detail
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "TEMP B-TREE") || !strings.Contains(details, "SEARCH frontier USING INDEX frontier_ready") {
		t.Fatalf("retry query does not use bounded index ordering: %s", details)
	}
}
