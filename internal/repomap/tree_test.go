package repomap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/tool"
)

func treeNext(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if cursor, ok := strings.CutPrefix(line, "next_cursor: "); ok {
			return cursor
		}
	}
	return ""
}

func TestTreeSmallBudgetProgressAndUndiscoveredScope(t *testing.T) {
	m, space, root := managerFixture(t, nil)
	scope := strings.Repeat("scope-", 25)
	if err := os.Mkdir(filepath.Join(root, scope), 0700); err != nil {
		t.Fatal(err)
	}
	roots, _ := space.Snapshot()
	rootID := roots[0].ID
	want := make(map[string]bool)
	for i := range 45 {
		rel := filepath.ToSlash(filepath.Join(scope, fmt.Sprintf("%03d-quoted-\"-%s.go", i, strings.Repeat("x", 100))))
		managerWrite(t, filepath.Join(root, filepath.FromSlash(rel)), "package p")
		id := index.EntryID(rootID, rel)
		want[id] = true
		if _, err := m.store.PutSource(t.Context(), index.File{RootID: rootID, ID: id,
			Path: []byte(rel), Kind: index.KindFile, State: index.StatePending, ObservedAt: time.Now()}, nil); err != nil {
			t.Fatal(err)
		}
	}
	wrapped := tool.WithLimits(&listTool{manager: m, space: space}, tool.Limits{MaxOutput: 1024, Timeout: 2 * time.Second})
	args, err := json.Marshal(listArgs{Path: scope})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	previous := ""
	for page := 0; ; page++ {
		if page > len(want)+1 {
			t.Fatal("pagination did not terminate")
		}
		result, err := wrapped.Run(t.Context(), "list", args)
		if err != nil || result.IsError || len(result.Text()) > 1024 || strings.Contains(result.Text(), "output truncated") {
			t.Fatalf("page %d: err=%v result=%s", page, err, result.Text())
		}
		for id := range want {
			if strings.Contains(result.Text(), "["+id+"]") {
				if seen[id] {
					t.Fatalf("duplicate %s", id)
				}
				seen[id] = true
			}
		}
		next := treeNext(result.Text())
		if next == "" {
			break
		}
		if next == previous {
			t.Fatal("no progress at minimum output budget")
		}
		previous = next
		args, err = json.Marshal(listArgs{Cursor: next})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("visited %d of %d entries", len(seen), len(want))
	}
	if _, err := m.store.EntryLookup(t.Context(), rootID, index.EntryID(rootID, scope)); err != nil {
		t.Fatalf("focused scope not addressable: %v", err)
	}
}

func TestRootListingSmallBudgetAndRevokedCursor(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	for i := range 9 {
		if _, err := space.AttachLocal(t.TempDir(), fmt.Sprintf("library%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	roots, _ := space.Snapshot()
	seen := make(map[string]bool)
	cursor := ""
	first := ""
	for pages := 0; ; pages++ {
		if pages > len(roots) {
			t.Fatal("root pagination did not progress")
		}
		text, err := m.listRoots(space, cursor, 1024)
		if err != nil || len(text) > 1024 {
			t.Fatalf("listing: %v, %d bytes", err, len(text))
		}
		for _, root := range roots {
			if strings.Contains(text, "["+root.ID+"]") {
				if seen[root.ID] {
					t.Fatal("duplicate root")
				}
				seen[root.ID] = true
			}
		}
		next := treeNext(text)
		if next == "" {
			break
		}
		if cursor == next {
			t.Fatal("repeated cursor")
		}
		if first == "" {
			first = next
		}
		cursor = next
	}
	if len(seen) != len(roots) || first == "" {
		t.Fatalf("saw %d of %d roots, first cursor=%q", len(seen), len(roots), first)
	}
	if err := space.Detach("library0"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.listRoots(space, first, 1024); !errors.Is(err, index.ErrStale) {
		t.Fatalf("revoked cursor: %v", err)
	}
}

func TestRepoListRejectsAmbiguousArgs(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	list := &listTool{manager: m, space: space}
	for _, args := range []string{`null`, `{"unknown":1}`, `{} {}`, `{"cursor":""}`, `{"cursor":"x","repo":""}`, `{"entry_id":"x","path":""}`} {
		res, err := list.Run(t.Context(), "list", json.RawMessage(args))
		if err != nil || !res.IsError {
			t.Errorf("%s: expected argument error, got %v %+v", args, err, res)
		}
	}
}

func TestTreeSuppressesDeletedEntries(t *testing.T) {
	m, space, _ := managerFixture(t, nil)
	roots, _ := space.Snapshot()
	id := index.EntryID(roots[0].ID, "missing.go")
	if _, err := m.store.PutSource(t.Context(), index.File{RootID: roots[0].ID, ID: id, Path: []byte("missing.go"), Kind: index.KindFile, State: index.StatePending}, nil); err != nil {
		t.Fatal(err)
	}
	text, err := m.Tree(t.Context(), space, "", ".", "")
	if err != nil || strings.Contains(text, id) {
		t.Fatalf("deleted source was exposed: %v %s", err, text)
	}
}

func TestTreeHistoricalBoundaryIsNotLiveExistenceClaim(t *testing.T) {
	m, space, root := managerFixture(t, nil)
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := space.Resolve("", ".")
	if err != nil {
		t.Fatal(err)
	}
	attached, err := space.AttachLocal(child, "child")
	if err != nil {
		t.Fatal(err)
	}
	id := index.EntryID(parent.RootID, "child")
	if _, err := m.store.PutSource(t.Context(), index.File{RootID: parent.RootID, ID: id,
		Path: []byte("child"), Kind: index.KindBoundary, State: index.StateOmitted, Reason: "nested_root"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := space.Detach(attached.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	text, err := m.Tree(t.Context(), space, "", ".", "")
	if err != nil || !strings.Contains(text, "["+id+"] boundary ownership reservation; availability unverified") {
		t.Fatalf("historical boundary lacks explicit availability qualification: %v %s", err, text)
	}
}
