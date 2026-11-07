package repomap

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestCoverageReasonsFollowScopeAndRetainedGeneration(t *testing.T) {
	m, space, dir := queryFixture(t)
	indexQueryFile(t, m, space, dir, "good.go", "package p; func Good() {}")
	indexQueryFile(t, m, space, dir, "notes.txt", "unsupported text")
	indexQueryFile(t, m, space, dir, "bad.go", "package p; func invalid(")
	indexQueryFile(t, m, space, dir, "scope/small.go", "package p; func Small() {}")
	mapTool := &mapTool{manager: m, space: space}
	first := runMap(t, mapTool, `{"limit":1}`, MaxOutputBytes)
	checkCoverage(t, first.Coverage, 4, 2, map[string]int64{"unsupported": 1, "parse_failed": 1})
	if first.Page.NextCursor == "" {
		t.Fatal("fixture did not retain a generation")
	}
	indexQueryFile(t, m, space, dir, "bad.go", "package p; func Recovered() {}")
	next := runMap(t, mapTool, fmt.Sprintf(`{"cursor":%q}`, first.Page.NextCursor), MaxOutputBytes)
	checkCoverage(t, next.Coverage, 4, 2, first.Coverage.Omissions)
	current := runMap(t, mapTool, `{}`, MaxOutputBytes)
	checkCoverage(t, current.Coverage, 4, 1, map[string]int64{"unsupported": 1})
	narrow := runMap(t, mapTool, `{"path":"scope"}`, MaxOutputBytes)
	if narrow.Coverage.KnownFiles != nil || narrow.Coverage.OmittedFiles != nil || len(narrow.Coverage.Omissions) != 0 {
		t.Fatalf("subtree inherited root aggregates: %+v", narrow.Coverage)
	}
	small := runMap(t, tool.WithLimits(mapTool, tool.Limits{MaxOutput: 1024}), `{}`, 1024)
	if small.Coverage.KnownFiles != nil || small.Coverage.OmittedFiles != nil || len(small.Coverage.Omissions) != 0 {
		t.Fatalf("small-budget aggregates were partially stripped: %+v", small.Coverage)
	}
}

func checkCoverage(t *testing.T, coverage Coverage, known, omitted int64, reasons map[string]int64) {
	t.Helper()
	if coverage.KnownFiles == nil || coverage.IndexedFiles == nil || coverage.PendingFiles == nil || coverage.OmittedFiles == nil {
		t.Fatalf("missing maintained counts: %+v", coverage)
	}
	if *coverage.KnownFiles != known || *coverage.OmittedFiles != omitted || *coverage.IndexedFiles+*coverage.PendingFiles+*coverage.OmittedFiles != known || !reflect.DeepEqual(coverage.Omissions, reasons) {
		t.Fatalf("inconsistent coverage: %+v reasons=%v; want known=%d omitted=%d reasons=%v", coverage, coverage.Omissions, known, omitted, reasons)
	}
}
