package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T, options Options) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.sqlite")
	s, err := Open(path, options)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s, path
}

func testSource(path, name string) (File, []Symbol) {
	file := File{RootID: "r_test", Path: []byte(path), Hash: "hash:" + name, Policy: "go-v1",
		Size: 100, ModTime: time.Unix(1000, 0), ObservedAt: time.Unix(2000, 0), Kind: KindFile, State: StateIndexed}
	return file, []Symbol{{Name: name, Kind: "function", Signature: "func " + name + "()", StartByte: 1, EndByte: 30, StartLine: 1, EndLine: 3}}
}

func putTestSource(t *testing.T, s *Store, file File, symbols []Symbol) uint64 {
	t.Helper()
	gen, err := s.PutSource(t.Context(), file, symbols)
	if err != nil {
		t.Fatalf("put %q: %v", file.Path, err)
	}
	return gen
}

func pinTestRoot(t *testing.T, s *Store) Snapshot {
	t.Helper()
	snapshot, err := s.Pin(t.Context(), "r_test", time.Minute)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	return snapshot
}

func TestCatalogRejectsOldSchemaAndNoncanonicalSymbolIDs(t *testing.T) {
	s, path := openTestStore(t, Options{})
	file, symbols := testSource("a.go", "Open")
	symbols[0].ID = "s_invalid"
	if _, err := s.PutSource(t.Context(), file, symbols); err == nil {
		t.Fatal("noncanonical symbol ID accepted")
	}
	if _, err := s.db.ExecContext(t.Context(), "PRAGMA user_version=1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path, Options{})
	if err == nil {
		if closeErr := other.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("old incompatible cache accepted")
	}
}

func TestScanEntriesReconcilesNestedMetadataInBoundedPages(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	for _, path := range []string{"a.go", "deep/a.go", "deep/other/z.go", "z.go"} {
		file, symbols := testSource(path, "Open")
		putTestSource(t, s, file, symbols)
	}
	var after []byte
	var paths []string
	for range 5 {
		page, err := s.ScanEntries(t.Context(), "r_test", after, 2, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Files) > 2 || page.ReadBytes > 64<<10 {
			t.Fatalf("unbounded page: %+v", page)
		}
		for _, file := range page.Files {
			paths = append(paths, string(file.Path))
		}
		if page.Exhausted {
			break
		}
		if len(page.Next) == 0 {
			t.Fatal("no reconciliation progress")
		}
		after = page.Next
	}
	want := []string{"a.go", "deep/a.go", "deep/other/z.go", "z.go"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths=%q, want=%q", paths, want)
	}
}

func TestFileCoverageCountersExcludeDirectoriesAndBoundaries(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	file, _ := testSource("changing", "Open")
	for i, change := range []struct {
		kind  EntryKind
		state SourceState
		want  Status
	}{
		{KindDirectory, StateIndexed, Status{}},
		{KindBoundary, StateOmitted, Status{}},
		{KindFile, StatePending, Status{Files: 1, Pending: 1}},
		{KindFile, StateIndexed, Status{Files: 1, Indexed: 1}},
		{KindFile, StateOmitted, Status{Files: 1, Omitted: 1}},
		{KindDirectory, StatePending, Status{}},
	} {
		file.Kind, file.State = change.kind, change.state
		putTestSource(t, s, file, nil)
		got, err := s.Status(t.Context(), file.RootID)
		change.want.Generation = uint64(i + 1)
		if err != nil || got != change.want {
			t.Fatalf("step %d: %+v %v, want %+v", i, got, err, change.want)
		}
	}
}

func visibleRecords(t *testing.T, s *Store, snapshot Snapshot, query Query) []Record {
	t.Helper()
	var records []Record
	var after *Key
	for range 1000 {
		page, err := s.Scan(t.Context(), snapshot, query, after, 3, 64<<10)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		if page.Examined > 3 || page.ReadBytes > 64<<10 {
			t.Fatalf("unbounded page: %+v", page)
		}
		for _, record := range page.Records {
			if record.Visible(snapshot.Generation) {
				records = append(records, record)
			}
		}
		if page.Exhausted {
			return records
		}
		if page.Next == nil {
			t.Fatal("scan made no progress with sufficient byte budget")
		}
		after = page.Next
	}
	t.Fatal("scan did not terminate")
	return nil
}

func TestVersionedSourcesRetainSnapshotsAcrossReopen(t *testing.T) {
	s, path := openTestStore(t, Options{})
	file, symbols := testSource("src/a.go", "Before")
	if gen := putTestSource(t, s, file, symbols); gen != 1 {
		t.Fatalf("first generation = %d", gen)
	}
	old := pinTestRoot(t, s)
	file.Hash = "new-hash"
	symbols[0].Name = "After"
	if gen := putTestSource(t, s, file, symbols); gen != 2 {
		t.Fatalf("second generation = %d", gen)
	}
	current := pinTestRoot(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	oldRecords := visibleRecords(t, reopened, old, Query{})
	newRecords := visibleRecords(t, reopened, current, Query{})
	if len(oldRecords) != 1 || oldRecords[0].Symbol.Name != "Before" || len(newRecords) != 1 || newRecords[0].Symbol.Name != "After" {
		t.Fatalf("snapshot versions changed: old=%+v, new=%+v", oldRecords, newRecords)
	}
	file.State = StatePending
	putTestSource(t, reopened, file, nil)
	pending := pinTestRoot(t, reopened)
	if got := visibleRecords(t, reopened, pending, Query{}); len(got) != 0 {
		t.Fatalf("pending replacement exposed old declarations: %+v", got)
	}
	if n, err := reopened.GC(t.Context(), "r_test", 1); err != nil || n != 0 {
		t.Fatalf("GC with active pins = %d, %v", n, err)
	}
	// An obsolete raw record can still be the last consumed position of a
	// newer snapshot, so retention protects its key as well as visible data.
	if _, err := reopened.LookupKey(t.Context(), pending, oldRecords[0].Symbol.ID, 1); err != nil {
		t.Fatalf("obsolete cursor key was evicted: %v", err)
	}
	status, err := reopened.Status(t.Context(), "r_test")
	if err != nil || status != (Status{Generation: 3, Files: 1, Pending: 1}) {
		t.Fatalf("status = %+v, %v", status, err)
	}
	for _, snapshot := range []Snapshot{old, current, pending} {
		if err := reopened.Release(t.Context(), snapshot.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := reopened.GC(t.Context(), "r_test", 1); err != nil || n != 2 {
		t.Fatalf("bounded GC = %d, %v, want one symbol and one source", n, err)
	}
	got, err := reopened.EntryLookup(t.Context(), file.RootID, EntryID(file.RootID, string(file.Path)))
	if err != nil || got.State != StatePending || !bytes.Equal(got.Path, file.Path) {
		t.Fatalf("current source lost after collection: %+v, %v", got, err)
	}
}

func TestReservationsEnforceCapacityExpiryAndRootBinding(t *testing.T) {
	s, _ := openTestStore(t, Options{MaxPins: 1})
	snapshot := pinTestRoot(t, s)
	if snapshot.Generation != 0 {
		t.Fatalf("empty root generation = %d", snapshot.Generation)
	}
	if _, err := s.Pin(t.Context(), "r_other", time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("second reservation = %v, want capacity", err)
	}
	forged := snapshot
	forged.RootID = "r_other"
	if _, err := s.Scan(t.Context(), forged, Query{}, nil, 1, 1024); !errors.Is(err, ErrStale) {
		t.Fatalf("cross-root reservation accepted: %v", err)
	}
	snapshot.ExpiresAt = time.Now().Add(-time.Second)
	if _, err := s.db.Exec("UPDATE pins SET expires=? WHERE id=?", snapshot.ExpiresAt.UnixNano(), snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(t.Context(), snapshot, Query{}, nil, 1, 1024); !errors.Is(err, ErrStale) {
		t.Fatalf("expired reservation accepted: %v", err)
	}
	if _, err := s.Pin(t.Context(), "r_other", time.Minute); err != nil {
		t.Fatalf("expired capacity was not reclaimed: %v", err)
	}
}

func TestConcurrentGenerationAndPinAdmissionPreservesQuotaAndRoots(t *testing.T) {
	const maxPins, callers, updates = 4, 24, 6
	s, _ := openTestStore(t, Options{MaxPins: maxPins})
	roots := []string{"r_first", "r_second"}
	for _, root := range roots {
		file, symbols := testSource("shared.go", root+"Gen00")
		file.RootID = root
		putTestSource(t, s, file, symbols)
	}
	type admission struct {
		snapshot Snapshot
		err      error
	}
	start := make(chan struct{})
	admitted := make(chan admission, callers)
	writes := make(chan error, len(roots))
	for i := range callers {
		go func(root string) {
			<-start
			snapshot, err := s.Pin(t.Context(), root, time.Minute)
			admitted <- admission{snapshot, err}
		}(roots[i%len(roots)])
	}
	for _, root := range roots {
		go func() {
			<-start
			for i := 1; i <= updates; i++ {
				file, symbols := testSource("shared.go", fmt.Sprintf("%sGen%02d", root, i))
				file.RootID = root
				if _, err := s.PutSource(t.Context(), file, symbols); err != nil {
					writes <- err
					return
				}
			}
			writes <- nil
		}()
	}
	close(start)
	var snapshots []Snapshot
	denied := 0
	for range callers {
		result := <-admitted
		if errors.Is(result.err, ErrCapacity) {
			denied++
		} else if result.err != nil {
			t.Errorf("pin admission: %v", result.err)
		} else {
			snapshots = append(snapshots, result.snapshot)
		}
	}
	for range roots {
		if err := <-writes; err != nil {
			t.Errorf("concurrent source update: %v", err)
		}
	}
	if len(snapshots) != maxPins || denied != callers-maxPins {
		t.Fatalf("global quota: admitted=%d denied=%d", len(snapshots), denied)
	}
	seen := map[string]bool{}
	for _, snapshot := range snapshots {
		if seen[snapshot.ID] || snapshot.Generation < 1 || snapshot.Generation > updates+1 {
			t.Fatalf("invalid admitted snapshot: %+v", snapshot)
		}
		seen[snapshot.ID] = true
		records := visibleRecords(t, s, snapshot, Query{})
		want := fmt.Sprintf("%sGen%02d", snapshot.RootID, snapshot.Generation-1)
		if len(records) != 1 || records[0].File.RootID != snapshot.RootID || records[0].Symbol.Name != want || records[0].File.Hash != "hash:"+want {
			t.Fatalf("non-atomic retained generation %+v: %+v", snapshot, records)
		}
		forged := snapshot
		forged.RootID = roots[0]
		if forged.RootID == snapshot.RootID {
			forged.RootID = roots[1]
		}
		if _, err := s.Scan(t.Context(), forged, Query{}, nil, 1, 1024); !errors.Is(err, ErrStale) {
			t.Fatalf("cross-root pin accepted: %v", err)
		}
	}
	if _, err := s.Pin(t.Context(), roots[0], time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatalf("quota after concurrent writes: %v", err)
	}
	if err := s.Release(t.Context(), snapshots[0].ID); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.Pin(t.Context(), roots[1], time.Minute)
	if err != nil || replacement.Generation != updates+1 {
		t.Fatalf("released capacity did not admit current generation: %+v, %v", replacement, err)
	}
}

func TestOrderedQueriesAndBoundedContinuation(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	inputs := []struct{ path, name string }{
		{"z.go", "Alpha"}, {"a.go", "Alphabet"}, {"b.go", "Alpha"},
		{"other/c.go", "Beta"}, {"invalid-\xff.go", "Alpha"},
	}
	for _, input := range inputs {
		file, symbols := testSource(input.path, input.name)
		putTestSource(t, s, file, symbols)
	}
	snapshot := pinTestRoot(t, s)
	for _, tt := range []struct {
		name  string
		query Query
		paths []string
	}{
		{name: "path byte order", query: Query{}, paths: []string{"a.go", "b.go", "invalid-\xff.go", "other/c.go", "z.go"}},
		{name: "exact name in path order", query: Query{Name: "Alpha", NameMatch: MatchExact}, paths: []string{"b.go", "invalid-\xff.go", "z.go"}},
		{name: "name prefix", query: Query{Order: NameOrder, Name: "Al", NameMatch: MatchPrefix}, paths: []string{"b.go", "invalid-\xff.go", "z.go", "a.go"}},
		{name: "path prefix", query: Query{Path: []byte("other/"), PathMatch: MatchPrefix}, paths: []string{"other/c.go"}},
		{name: "exact path", query: Query{Path: []byte("z.go"), PathMatch: MatchExact}, paths: []string{"z.go"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			records := visibleRecords(t, s, snapshot, tt.query)
			var paths []string
			for _, record := range records {
				paths = append(paths, string(record.File.Path))
			}
			if !reflect.DeepEqual(paths, tt.paths) {
				t.Errorf("paths = %q, want %q", paths, tt.paths)
			}
		})
	}
	page, err := s.Scan(t.Context(), snapshot, Query{}, nil, 1, 200)
	if err != nil || len(page.Records) != 0 || page.Examined != 1 || page.Next != nil || page.Exhausted || page.ReadBytes > 200 {
		t.Fatalf("insufficient byte budget consumed a candidate: %+v, %v", page, err)
	}
	page, err = s.Scan(t.Context(), snapshot, Query{}, nil, 1, 64<<10)
	if err != nil || len(page.Records) != 1 || string(page.Records[0].File.Path) != "a.go" {
		t.Fatalf("retained candidate = %+v, %v", page, err)
	}
	key, err := s.LookupKey(t.Context(), snapshot, page.Next.SymbolID, page.Next.Version)
	if err != nil || !reflect.DeepEqual(key, *page.Next) {
		t.Fatalf("handle lookup = %+v, %v, want %+v", key, err, page.Next)
	}
}

func TestQueryPlansSeekWithoutGlobalSort(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	key := &Key{Path: []byte("a.go"), Name: "Alpha", StartByte: 1, Kind: "function", SymbolID: "s_test", Version: 1}
	queries := []Query{
		{}, {Name: "Alpha", NameMatch: MatchExact},
		{Order: NameOrder, Name: "Al", NameMatch: MatchPrefix},
		{Path: []byte("src/"), PathMatch: MatchPrefix},
		{Path: []byte("src/a.go"), PathMatch: MatchExact},
	}
	for _, query := range queries {
		for _, after := range []*Key{nil, key} {
			if after != nil && query.PathMatch != MatchAny {
				copyKey := *after
				copyKey.Path = append(append([]byte(nil), query.Path...), 'z')
				if query.PathMatch == MatchExact {
					copyKey.Path = query.Path
				}
				after = &copyKey
			}
			statement, args, err := scanSQL("r_test", query, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+statement, args...)
			if err != nil {
				t.Fatal(err)
			}
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				t.Fatal(err)
			}
			plan := strings.Join(details, "\n")
			t.Logf("query=%+v continuation=%t: %s", query, after != nil, plan)
			if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "SEARCH symbols USING INDEX symbols_") {
				t.Errorf("query %+v requires sorting or scanning: %s", query, plan)
			}
			if after != nil && !strings.Contains(plan, ")>(") {
				t.Errorf("continuation %+v scans earlier matches instead of seeking the full tuple: %s", query, plan)
			}
		}
	}
}

func TestSparseNameQueryDoesNotExamineUnrelatedRecords(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	for i := range 100 {
		file, symbols := testSource(fmt.Sprintf("file-%03d.go", i), "Common")
		putTestSource(t, s, file, symbols)
	}
	file, symbols := testSource("rare.go", "RareName")
	putTestSource(t, s, file, symbols)
	page, err := s.Scan(t.Context(), pinTestRoot(t, s), Query{Order: NameOrder, Name: "Rare", NameMatch: MatchPrefix}, nil, 2, 64<<10)
	if err != nil || page.Examined != 1 || len(page.Records) != 1 || !page.Exhausted {
		t.Fatalf("rare-prefix scan = %+v, %v", page, err)
	}
}

func TestSourceAdmissionIsAtomicAndRootScoped(t *testing.T) {
	s, _ := openTestStore(t, Options{MaxRecordBytes: 1024})
	file, symbols := testSource("a.go", "Before")
	putTestSource(t, s, file, symbols)
	symbols[0].Signature = strings.Repeat("x", 2000)
	if _, err := s.PutSource(t.Context(), file, symbols); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized source = %v", err)
	}
	if status, err := s.Status(t.Context(), file.RootID); err != nil || status.Generation != 1 {
		t.Fatalf("failed update advanced generation: %+v, %v", status, err)
	}
	if _, err := s.EntryLookup(t.Context(), "r_other", EntryID(file.RootID, "a.go")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-root lookup = %v", err)
	}
	for _, path := range []string{"../secret", "/absolute", "a/../secret", "a//b", "a\x00b"} {
		file.Path = []byte(path)
		if _, err := s.PutSource(t.Context(), file, nil); err == nil {
			t.Errorf("invalid relative path accepted: %q", path)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Status(ctx, "r_test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled operation = %v", err)
	}
}

func TestDiskQuotaPreservesExistingGenerationAndPin(t *testing.T) {
	s, path := openTestStore(t, Options{MaxBytes: 512 << 10})
	file, symbols := testSource("a.go", "Original")
	putTestSource(t, s, file, symbols)
	snapshot := pinTestRoot(t, s)
	var lastGeneration uint64 = 1
	full := false
	for i := range 300 {
		file, symbols = testSource(fmt.Sprintf("large-%d.go", i), "Large")
		symbols[0].Signature = strings.Repeat("x", 4096)
		gen, err := s.PutSource(t.Context(), file, symbols)
		if errors.Is(err, ErrCapacity) {
			full = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		lastGeneration = gen
	}
	if !full {
		t.Fatal("finite database page quota was not enforced")
	}
	status, err := s.Status(t.Context(), "r_test")
	if err != nil || status.Generation != lastGeneration {
		t.Fatalf("quota failure did not roll back generation: %+v, %v", status, err)
	}
	got := visibleRecords(t, s, snapshot, Query{})
	if len(got) != 1 || got[0].Symbol.Name != "Original" {
		t.Fatalf("quota evicted retained data: %+v", got)
	}
	var diskBytes int64
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err == nil {
			diskBytes += info.Size()
		}
	}
	if diskBytes > 512<<10 {
		t.Fatalf("catalog uses %d bytes, above total quota", diskBytes)
	}
}

func TestFrontierRevisionAndChildListing(t *testing.T) {
	s, path := openTestStore(t, Options{})
	if err := s.Enqueue(t.Context(), "r_test", []byte(".")); err != nil {
		t.Fatal(err)
	}
	items, err := s.Take(t.Context(), "r_test", 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("take = %+v, %v", items, err)
	}
	if err := s.Enqueue(t.Context(), "r_test", []byte(".")); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(t.Context(), items[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	items, err = reopened.Take(t.Context(), "r_test", 1)
	if err != nil || len(items) != 1 || items[0].Revision != 2 {
		t.Fatalf("in-flight invalidation lost on reopen: %+v, %v", items, err)
	}
	if err := reopened.Ack(t.Context(), items[0]); err != nil {
		t.Fatal(err)
	}
	if items, err := reopened.Take(t.Context(), "r_test", 1); err != nil || len(items) != 0 {
		t.Fatalf("ack left work: %+v, %v", items, err)
	}
	for _, path := range []string{"a.go", "src/b.go", "src/c.go", "src/deeper/d.go"} {
		file, symbols := testSource(path, "Function")
		putTestSource(t, reopened, file, symbols)
	}
	page, err := reopened.ListEntries(t.Context(), "r_test", []byte("src"), nil, 1, 64<<10)
	if err != nil || len(page.Files) != 1 || string(page.Files[0].Path) != "src/b.go" {
		t.Fatalf("first child page = %+v, %v", page, err)
	}
	page, err = reopened.ListEntries(t.Context(), "r_test", []byte("src"), page.Next, 2, 64<<10)
	if err != nil || len(page.Files) != 1 || string(page.Files[0].Path) != "src/c.go" || !page.Exhausted {
		t.Fatalf("second child page = %+v, %v", page, err)
	}
}

func TestFrontierLookupReturnsCurrentRotatedAndInvalidatedTask(t *testing.T) {
	s, _ := openTestStore(t, Options{})
	path := []byte("retained/directory")
	if err := s.Enqueue(t.Context(), "r_test", path); err != nil {
		t.Fatal(err)
	}
	before, err := s.QueueLookup(t.Context(), "r_test", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rotate(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	rotated, err := s.QueueLookup(t.Context(), "r_test", path)
	if err != nil || rotated.Sequence <= before.Sequence || rotated.Revision != before.Revision {
		t.Fatalf("rotated lookup: before=%+v after=%+v err=%v", before, rotated, err)
	}
	if err := s.Enqueue(t.Context(), "r_test", path); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(t.Context(), rotated); err != nil {
		t.Fatal(err)
	}
	current, err := s.QueueLookup(t.Context(), "r_test", path)
	if err != nil || current.Sequence != rotated.Sequence || current.Revision != rotated.Revision+1 || current.RootID != "r_test" || !bytes.Equal(current.Path, path) {
		t.Fatalf("current invalidation lost: %+v, %v", current, err)
	}
	for _, root := range []string{"r_other", "r_test"} {
		candidate := path
		if root == "r_test" {
			candidate = []byte("absent")
		}
		if _, err := s.QueueLookup(t.Context(), root, candidate); !errors.Is(err, ErrNotFound) {
			t.Fatalf("absent/cross-root lookup: %v", err)
		}
	}
	if _, err := s.QueueLookup(t.Context(), "r_test", bytes.Repeat([]byte("x"), s.Options().MaxRecordBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized lookup admitted: %v", err)
	}
	var id, parent, unused int
	var plan string
	err = s.db.QueryRowContext(t.Context(), "EXPLAIN QUERY PLAN SELECT sequence,revision FROM frontier WHERE root=? AND path=?", "r_test", path).Scan(&id, &parent, &unused, &plan)
	if err != nil || !strings.Contains(plan, "SEARCH frontier USING INDEX") || !strings.Contains(plan, "root=? AND path=?") {
		t.Fatalf("frontier lookup does not seek unique key: %q, %v", plan, err)
	}
}

func TestFrontierRotationRetainsWorkAndNewInvalidations(t *testing.T) {
	s, path := openTestStore(t, Options{})
	for _, path := range []string{"directory", "file.go"} {
		if err := s.Enqueue(t.Context(), "r_test", []byte(path)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Take(t.Context(), "r_test", 2)
	if err != nil || len(items) != 2 {
		t.Fatalf("take = %+v, %v", items, err)
	}
	if err := s.Rotate(t.Context(), items[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	items, err = reopened.Take(t.Context(), "r_test", 2)
	if err != nil || len(items) != 2 || string(items[0].Path) != "file.go" || string(items[1].Path) != "directory" {
		t.Fatalf("rotation did not survive reopen: %+v, %v", items, err)
	}
	stale := items[0]
	if err := reopened.Enqueue(t.Context(), "r_test", stale.Path); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Rotate(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	items, err = reopened.Take(t.Context(), "r_test", 2)
	if err != nil || len(items) != 2 || items[0].Revision != stale.Revision+1 || string(items[0].Path) != "file.go" {
		t.Fatalf("stale rotation lost or moved a newer invalidation: %+v, %v", items, err)
	}
}
