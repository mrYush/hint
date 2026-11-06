package repomap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

func queryFixture(t *testing.T) (*Manager, *workspace.Session, string) {
	t.Helper()
	return managerFixture(t, &managerTestExtractor{run: extract.Extract})
}

func indexQueryFile(t *testing.T, m *Manager, space *workspace.Session, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	managerWrite(t, full, content)
	lease, err := space.Resolve("", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.refresh(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
}

func runMap(t *testing.T, target agentapi.Tool, args string, cap int) Response {
	t.Helper()
	call := agentapi.ToolCall{ID: "map", Name: "repo_map", Arguments: json.RawMessage(args)}
	gate := permission.New(permission.ModeAsk, nil)
	if _, ask := gate.Review(t.Context(), call, target); ask {
		t.Fatal("read-class map requested write permission")
	}
	result, err := target.Run(t.Context(), call.ID, call.Arguments)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("map failed: %s", result.Text())
	}
	if len(result.Text()) > cap || !json.Valid([]byte(result.Text())) {
		t.Fatalf("broken envelope: %d bytes %s", len(result.Text()), result.Text())
	}
	var out Response
	if err := json.Unmarshal([]byte(result.Text()), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMapWrappersBoundPagesWithoutLosingDeclarations(t *testing.T) {
	for _, cap := range []int{1024, 1500, 2048, 8192} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			m, space, dir := queryFixture(t)
			for i := range 7 {
				indexQueryFile(t, m, space, dir, fmt.Sprintf("src/q%02d.go", i), fmt.Sprintf("package p\nfunc Function%02d() {}\n", i))
			}
			wrapped := tool.Redacting(tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: cap}), func() []string { return []string{"unrelated-secret"} })
			args, seen := `{"path":"src","limit":50}`, map[string]bool{}
			for page := range 30 {
				out := runMap(t, wrapped, args, cap)
				if out.Scope.EntryID == "" && out.Scope.PathDisplay == "" {
					t.Fatal("scope is not addressable")
				}
				if out.Scope.EntryID != "" {
					if _, err := m.ResolveEntry(t.Context(), space, out.RootID, out.Scope.EntryID); err != nil {
						t.Fatalf("scope has no disk mapping: %v", err)
					}
				}
				for _, entry := range out.Entries {
					if seen[entry.ID] {
						t.Fatalf("duplicate declaration on page %d: %s", page, entry.ID)
					}
					seen[entry.ID] = true
				}
				if out.Page.QueryComplete {
					if len(seen) != 7 {
						t.Fatalf("lost declarations: %d", len(seen))
					}
					return
				}
				if out.Page.NextCursor == "" || out.Page.ExpiresAt == "" {
					t.Fatal("partial page has no retained continuation")
				}
				data, err := json.Marshal(Request{Cursor: out.Page.NextCursor})
				if err != nil {
					t.Fatal(err)
				}
				args = string(data)
			}
			t.Fatal("pagination made no progress")
		})
	}
}

func TestMapReportsExhaustedHashBudgetWithoutUpgradingAllFiles(t *testing.T) {
	const fileCount, outputCap = 50, 8192
	m, space, dir := queryFixture(t)
	for i := range fileCount {
		indexQueryFile(t, m, space, dir, fmt.Sprintf("f%02d.go", i), fmt.Sprintf("package p; func Name%02d() {}", i))
	}
	wrapped := tool.Redacting(tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: outputCap}), func() []string { return []string{"unrelated-secret"} })
	args := `{"limit":50}`
	seenEntries, seenFiles := map[string]bool{}, map[string]bool{}
	totalObserved := 0
	for page := range fileCount + 1 {
		out := runMap(t, wrapped, args, outputCap)
		checked, observed := 0, 0
		for _, file := range out.Files {
			if seenFiles[file.ID] {
				t.Fatalf("duplicate file on page %d: %s", page, file.ID)
			}
			seenFiles[file.ID] = true
			switch file.Freshness {
			case "observed":
				observed++
				if file.ObservedAt == "" || file.CheckedAt != "" {
					t.Fatalf("bad observed metadata: %+v", file)
				}
			case "content_checked":
				checked++
				if file.CheckedAt == "" || file.ObservedAt != "" {
					t.Fatalf("bad checked metadata: %+v", file)
				}
			default:
				t.Fatalf("unknown freshness: %+v", file)
			}
		}
		for _, entry := range out.Entries {
			if seenEntries[entry.ID] {
				t.Fatalf("duplicate declaration on page %d: %s", page, entry.ID)
			}
			seenEntries[entry.ID] = true
		}
		if checked > 4 || out.Verification == nil || out.Verification.FilesChecked > 4 || out.Verification.FilesChecked < checked {
			t.Fatalf("page %d hash limit: checked=%d observed=%d verification=%+v", page, checked, observed, out.Verification)
		}
		if observed > 0 && !out.Verification.BudgetExhausted {
			t.Fatalf("page %d hid exhausted hash budget: %+v", page, out.Verification)
		}
		totalObserved += observed
		if out.Page.QueryComplete {
			if page == 0 || len(seenEntries) != fileCount || len(seenFiles) != fileCount || totalObserved == 0 {
				t.Fatalf("incomplete traversal: pages=%d declarations=%d files=%d observed=%d", page+1, len(seenEntries), len(seenFiles), totalObserved)
			}
			return
		}
		if out.Page.NextCursor == "" || out.Page.ExpiresAt == "" {
			t.Fatal("partial page has no retained continuation")
		}
		data, err := json.Marshal(Request{Cursor: out.Page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		args = string(data)
	}
	t.Fatal("50-file pagination made no progress")
}

func TestMapLargeExcludedRangeAdvancesWithoutLosingFollowingCandidate(t *testing.T) {
	const excludedCount, outputCap = 1025, 8192
	m, space, dir := queryFixture(t)
	root := spaceRootID(t, space)
	// Prefix ordering encounters these out-of-scope declarations before the
	// selected source. No filesystem access to those paths should be needed.
	for i := range excludedCount {
		name := fmt.Sprintf("Selected%04d", i)
		file := index.File{RootID: root, Path: []byte(fmt.Sprintf("outside/%04d.go", i)),
			Kind: index.KindFile, State: index.StateIndexed, Size: 2, Hash: "unvisited", Policy: "unvisited"}
		_, err := m.store.PutSource(t.Context(), file, []index.Symbol{{Name: name, Kind: "function", StartByte: 1, EndByte: 2, StartLine: 1, EndLine: 1}})
		if err != nil {
			t.Fatal(err)
		}
	}
	indexQueryFile(t, m, space, dir, "selected/z.go", "package p; func SelectedZZZZ() {}")
	wrapped := tool.Redacting(tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: outputCap}), func() []string { return []string{"unrelated-secret"} })
	args := `{"path":"selected","symbol":"Selected","match":"prefix","limit":50}`
	lastCursor, seenEntries, emptyProgress := "", 0, 0
	for page := range 20 {
		out := runMap(t, wrapped, args, outputCap)
		if len(out.Invalidations) != 0 {
			t.Fatalf("out-of-scope candidates reached filesystem validation: %+v", out.Invalidations)
		}
		for _, entry := range out.Entries {
			seenEntries++
			if entry.Name != "SelectedZZZZ" {
				t.Fatalf("out-of-scope declaration exposed: %+v", entry)
			}
		}
		if out.Page.QueryComplete {
			if seenEntries != 1 || emptyProgress < 2 {
				t.Fatalf("lost candidate or unbounded filtering: entries=%d empty_pages=%d pages=%d", seenEntries, emptyProgress, page+1)
			}
			return
		}
		if len(out.Entries) == 0 {
			emptyProgress++
		}
		if out.Page.NextCursor == "" || out.Page.NextCursor == lastCursor || out.Page.ExpiresAt == "" {
			t.Fatal("excluded range did not produce a progressing retained cursor")
		}
		lastCursor = out.Page.NextCursor
		data, err := json.Marshal(Request{Cursor: lastCursor})
		if err != nil {
			t.Fatal(err)
		}
		args = string(data)
	}
	t.Fatal("excluded range traversal did not terminate")
}

func TestMapPinnedCoverageDoesNotBecomeCompleteLater(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "x.go", "package p; func Open() {}")
	m.setStatus(spaceRootID(t, space), Status{State: "building"})
	q, err := m.openQuery(t.Context(), space, Request{Path: ".", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	token, err := m.cursor(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.setStatus(q.lease.RootID, Status{State: "ready"})
	payload, err := m.query(t.Context(), space, Request{Cursor: token}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	var out Response
	if err := json.Unmarshal(payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.Coverage.DiscoveryComplete {
		t.Fatal("old snapshot acquired newer completeness")
	}
}

func spaceRootID(t *testing.T, space *workspace.Session) string {
	t.Helper()
	lease, err := space.Resolve("", ".")
	if err != nil {
		t.Fatal(err)
	}
	return lease.RootID
}

func TestMapValidationSuppressesChangedSourcesAndPolicies(t *testing.T) {
	for _, change := range []string{"content", "policy", "deleted"} {
		t.Run(change, func(t *testing.T) {
			m, space, dir := queryFixture(t)
			indexQueryFile(t, m, space, dir, "x.go", "package p; func Old() {}")
			lease, err := space.Resolve("", "x.go")
			if err != nil {
				t.Fatal(err)
			}
			source, err := m.store.EntryLookup(t.Context(), lease.RootID, workspace.EntryID(lease.RootID, "x.go"))
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "content":
				managerWrite(t, filepath.Join(dir, "x.go"), "package p; func New() {}")
				if err := os.Chtimes(filepath.Join(dir, "x.go"), source.ModTime, source.ModTime); err != nil {
					t.Fatal(err)
				}
			case "policy":
				source.Policy = "obsolete"
			case "deleted":
				if err := os.Remove(filepath.Join(dir, "x.go")); err != nil {
					t.Fatal(err)
				}
			}
			work := Verification{}
			got, err := m.validate(t.Context(), space, source, &work, time.Now().Add(time.Second))
			if err != nil || got.usable {
				t.Fatalf("stale validation = %+v, %v", got, err)
			}
		})
	}
}

func TestMapReportsDefinitiveInvalidationsSeparatelyFromCoverage(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "a.go", "package p; func Old() {}; func OldTwo() {}")
	indexQueryFile(t, m, space, dir, "b.go", "package p; func Good() {}")
	managerWrite(t, filepath.Join(dir, "a.go"), "changed")
	wrapped := tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: 1024})
	args, invalidations, seen := `{}`, 0, 0
	for range 10 {
		out := runMap(t, wrapped, args, 1024)
		invalidations += out.Invalidations["source_changed"]
		seen += len(out.Entries)
		if out.Page.QueryComplete {
			if invalidations != 1 || seen != 1 {
				t.Fatalf("invalidations=%d entries=%d", invalidations, seen)
			}
			return
		}
		data, err := json.Marshal(Request{Cursor: out.Page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
		args = string(data)
	}
	t.Fatal("invalidation traversal failed to finish")
}

func TestMapValidationReportsHashBudgetExhaustion(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "x.go", "package p; func Open() {}")
	lease, err := space.Resolve("", "x.go")
	if err != nil {
		t.Fatal(err)
	}
	source, err := m.store.EntryLookup(t.Context(), lease.RootID, workspace.EntryID(lease.RootID, "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	work := Verification{}
	got, err := m.validate(t.Context(), space, source, &work, time.Now().Add(-time.Second))
	if err != nil || !got.usable || got.file.Freshness != "observed" || got.file.CheckedAt != "" || !work.BudgetExhausted || work.BytesRead != 0 {
		t.Fatalf("invalid observed result: %+v %+v %v", got, work, err)
	}
}

func TestMapCursorIsBoundToMembershipAndRejectsMalformedIDs(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "x.go", "package p; func Open() {}")
	q, err := m.openQuery(t.Context(), space, Request{Path: ".", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.cursor(q, &index.Key{SymbolID: "invalid"}); err == nil {
		t.Fatal("invalid catalog ID accepted")
	}
	token, err := m.cursor(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 96 {
		t.Fatalf("cursor bytes = %d", len(token))
	}
	if _, err := space.AttachLocal(t.TempDir(), "added"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.resume(t.Context(), space, token); !errors.Is(err, index.ErrStale) {
		t.Fatalf("membership change not rejected: %v", err)
	}
}

func TestMapCursorDoesNotRevealRemovedScope(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "scope/x.go", "package p; func Open() {}")
	q, err := m.openQuery(t.Context(), space, Request{Path: "scope", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	token, err := m.cursor(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "scope")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.query(t.Context(), space, Request{Cursor: token}, 1024); !errors.Is(err, index.ErrStale) {
		t.Fatalf("removedscope continuation=%v", err)
	}
}

func TestMapReportsRetentionCapacityAsRetryable(t *testing.T) {
	m, space, _ := queryFixture(t)
	root := spaceRootID(t, space)
	for range 128 {
		if _, err := m.store.Pin(t.Context(), root, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	result, err := (&mapTool{manager: m, space: space}).Run(t.Context(), "id", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Error queryError `json:"error"`
	}
	if err := json.Unmarshal([]byte(result.Text()), &response); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || response.Error.Code != "index_unavailable" || !response.Error.Retryable || response.Error.Reason != "retention_capacity" {
		t.Fatalf("wrong failure: %+v", response)
	}
}

func TestMapTurnCancellationIsPreserved(t *testing.T) {
	m, space, _ := queryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wrapped := tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: 1024})
	_, err := wrapped.Run(ctx, "id", json.RawMessage(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestMapStructuredRedactionThroughLimits(t *testing.T) {
	m, space, dir := queryFixture(t)
	secret := "SecretIdentifier123"
	indexQueryFile(t, m, space, dir, "x.go", "package p; func "+secret+"() {}")
	wrapped := tool.Redacting(tool.WithLimits(&mapTool{manager: m, space: space}, tool.Limits{MaxOutput: 8192}), func() []string { return []string{secret} })
	result, err := wrapped.Run(t.Context(), "id", json.RawMessage(`{}`))
	if err != nil || result.IsError || !json.Valid([]byte(result.Text())) || strings.Contains(result.Text(), secret) || !strings.Contains(result.Text(), "***") {
		t.Fatalf("redaction=%s err=%v", result.Text(), err)
	}
}
