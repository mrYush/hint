package project_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mrYush/hint/internal/project"
)

// fixture is a small project as an in-memory fs.FS: no disk, no cleanup,
// and the same tree in every test.
func fixture() fstest.MapFS {
	return fstest.MapFS{
		"go.mod":                    {},
		"cmd/hint/main.go":          {},
		"cmd/hint/main_test.go":     {},
		"internal/agent/loop.go":    {},
		"internal/agent/deep/x.go":  {},
		"docs/plan/phase-0.md":      {},
		".git/HEAD":                 {},
		".hidden":                   {},
		"node_modules/pkg/index.js": {},
		"vendor/dep.go":             {},
	}
}

func TestTree_DepthAndIgnore(t *testing.T) {
	got, err := project.Tree{}.Render(context.Background(), fixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "cmd/\n" +
		"  hint/\n" +
		"docs/\n" +
		"  plan/\n" +
		"go.mod\n" +
		"internal/\n" +
		"  agent/\n" +
		"(showing 2 levels; use list_dir or glob to see deeper)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	got, err = project.Tree{Depth: 3}.Render(context.Background(), fixture(), project.Basic)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "    main.go\n") || strings.Contains(got, "x.go") || !strings.Contains(got, "showing 3 levels") {
		t.Fatalf("depth 3:\n%s", got)
	}
}

func TestTree_MaxEntries(t *testing.T) {
	got, err := project.Tree{MaxEntries: 3}.Render(context.Background(), fixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "cmd/\n  hint/\ndocs/\n... (cut at 3 entries; use list_dir or glob to see more)"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTree_CustomIgnorerAndEmpty(t *testing.T) {
	only := ignoreFunc(func(rel string, isDir bool) bool { return rel != "go.mod" })
	got, err := project.Tree{}.Render(context.Background(), fixture(), only)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "go.mod\n(showing") {
		t.Fatalf("custom ignorer:\n%s", got)
	}

	got, err = project.Tree{}.Render(context.Background(), fstest.MapFS{".only": {}}, nil)
	if err != nil || got != "(empty directory)" {
		t.Fatalf("empty: %q, %v", got, err)
	}
}

func TestTree_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (project.Tree{}).Render(ctx, fixture(), nil); err == nil {
		t.Fatal("a canceled walk must report the cancellation")
	}
}

func TestNone(t *testing.T) {
	got, err := project.None{}.Render(context.Background(), fixture(), nil)
	if err != nil || got != "" {
		t.Fatalf("None = %q, %v", got, err)
	}
}

type ignoreFunc func(rel string, isDir bool) bool

func (f ignoreFunc) Ignored(rel string, isDir bool) bool { return f(rel, isDir) }
