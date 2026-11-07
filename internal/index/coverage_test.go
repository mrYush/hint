package index

import (
	"reflect"
	"strings"
	"testing"
)

func requireCoverageBuckets(t *testing.T, store *Store, root string, want map[string]int64) {
	t.Helper()
	status, got, err := store.CoverageStatus(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, count := range got {
		total += count
	}
	if !reflect.DeepEqual(got, want) || total != status.Omitted || status.Files != status.Pending+status.Indexed+status.Omitted {
		t.Fatalf("coverage: %+v reasons=%v; want %v", status, got, want)
	}
}

func TestCoverageBucketsFollowCurrentFileKindsAndReasons(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	a, _ := testSource("a.go", "A")
	a.State, a.Reason = StateOmitted, "unsupported"
	putTestSource(t, s, a, nil)
	b, _ := testSource("b.go", "B")
	b.State, b.Reason = StateOmitted, "parse_failed"
	putTestSource(t, s, b, nil)
	directory := File{RootID: a.RootID, Path: []byte("dir"), Kind: KindDirectory, State: StateOmitted, Reason: "unavailable"}
	putTestSource(t, s, directory, nil)
	requireCoverageBuckets(t, s, a.RootID, map[string]int64{"unsupported": 1, "parse_failed": 1})
	a.Reason = strings.Repeat("unknown-reason-", 100)
	putTestSource(t, s, a, nil)
	putTestSource(t, s, a, nil)
	requireCoverageBuckets(t, s, a.RootID, map[string]int64{"other": 1, "parse_failed": 1})
	b.State, b.Reason = StateIndexed, ""
	putTestSource(t, s, b, nil)
	requireCoverageBuckets(t, s, a.RootID, map[string]int64{"other": 1})
	a.Kind, a.Reason = KindBoundary, "nested_root"
	putTestSource(t, s, a, nil)
	requireCoverageBuckets(t, s, a.RootID, nil)
	b.RootID, b.State, b.Reason = "r_other", StateOmitted, "unsupported"
	putTestSource(t, s, b, nil)
	requireCoverageBuckets(t, s, a.RootID, nil)
	requireCoverageBuckets(t, s, b.RootID, map[string]int64{"unsupported": 1})
}

func TestLegacyCoverageClassifiesContributionsExactlyOnce(t *testing.T) {
	s, path := openTestStore(t, Options{})
	a, _ := testSource("a.go", "A")
	a.State, a.Reason = StateOmitted, "unsupported"
	b, _ := testSource("b.go", "B")
	b.State, b.Reason = StateOmitted, "parse_failed"
	putTestSource(t, s, a, nil)
	putTestSource(t, s, b, nil)
	if _, err := s.db.ExecContext(t.Context(), "DROP TABLE coverage_roots; DROP TABLE omission_counts; PRAGMA user_version=3;"); err != nil {
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
	requireCoverageBuckets(t, reopened, a.RootID, map[string]int64{"unclassified": 2})
	for range 2 {
		putTestSource(t, reopened, a, nil)
		requireCoverageBuckets(t, reopened, a.RootID, map[string]int64{"unclassified": 1, "unsupported": 1})
	}
	b.State, b.Reason = StateIndexed, ""
	putTestSource(t, reopened, b, nil)
	requireCoverageBuckets(t, reopened, a.RootID, map[string]int64{"unsupported": 1})
}
