package builtin_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestListDir_Tree(t *testing.T) {
	root := newRoot(t, map[string]string{
		"go.mod":                "",
		"cmd/hint/main.go":      "",
		"internal/agent/a.go":   "",
		".git/HEAD":             "",
		".hidden":               "",
		"node_modules/x/y.js":   "",
		"vendor/z.go":           "",
		"docs/plan/phase-0.md":  "",
		"docs/plan/.private.md": "",
	})
	tl := builtin.NewListDir(root)
	if tl.Class() != agentapi.ClassRead {
		t.Fatalf("class = %s", tl.Class())
	}

	got := ok(t, run(t, tl, `{}`))
	want := "./\n" +
		"  cmd/\n" +
		"    hint/\n" +
		"      main.go\n" +
		"  docs/\n" +
		"    plan/\n" +
		"      phase-0.md\n" +
		"  go.mod\n" +
		"  internal/\n" +
		"    agent/\n" +
		"      a.go\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	got = ok(t, run(t, tl, `{"path":"docs"}`))
	if got != "docs/\n  plan/\n    phase-0.md\n" {
		t.Fatalf("subdir listing:\n%s", got)
	}
}

func TestListDir_EmptyAndErrors(t *testing.T) {
	root := newRoot(t, map[string]string{"f.txt": "", "empty/.keep": ""})
	tl := builtin.NewListDir(root)

	if got := ok(t, run(t, tl, `{"path":"empty"}`)); got != "empty/\n(empty directory)" {
		t.Fatalf("empty: %q", got)
	}
	failed(t, run(t, tl, `{"path":"f.txt"}`), "is a file")
	failed(t, run(t, tl, `{"path":"nope"}`), "no such file")
	failed(t, run(t, tl, `{"path":".."}`), "outside the working directory")
}

func TestListDir_CapsEntries(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 600; i++ {
		files[fmt.Sprintf("f%03d", i)] = ""
	}
	root := newRoot(t, files)
	got := ok(t, run(t, builtin.NewListDir(root), `{}`))
	if !strings.Contains(got, "listing cut at 500 entries") {
		t.Fatalf("no cap note: %s", got[len(got)-100:])
	}
	if n := strings.Count(got, "\n"); n != 500+2 {
		t.Fatalf("%d lines, want 502", n)
	}
}
