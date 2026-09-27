package builtin_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

var globFixture = map[string]string{
	"main.go":                "",
	"cmd/hint/main.go":       "",
	"cmd/hint/main_test.go":  "",
	"internal/tool/tool.go":  "",
	"docs/README.md":         "",
	".hidden.go":             "",
	"node_modules/lib.go":    "",
	"vendor/dep/dep.go":      "",
	"internal/.cache/gen.go": "",
}

func TestGlob_PureGo(t *testing.T) {
	root := newRoot(t, globFixture)
	tl := builtin.NewGlob(root, builtin.WithRipgrep(""))
	if tl.Class() != agentapi.ClassRead {
		t.Fatalf("class = %s", tl.Class())
	}

	tests := []struct {
		args string
		want []string
	}{
		{`{"pattern":"*.go"}`, []string{"cmd/hint/main.go", "cmd/hint/main_test.go", "internal/tool/tool.go", "main.go"}},
		{`{"pattern":"cmd/**/*_test.go"}`, []string{"cmd/hint/main_test.go"}},
		{`{"pattern":"*.{go,md}","path":"docs"}`, []string{"docs/README.md"}},
		{`{"pattern":"**/tool.go"}`, []string{"internal/tool/tool.go"}},
		{`{"pattern":"*.rs"}`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.args, func(t *testing.T) {
			got := ok(t, run(t, tl, tt.args))
			if tt.want == nil {
				if got != "No files found" {
					t.Fatalf("got %q", got)
				}
				return
			}
			set := lineSet(got)
			wantSet := map[string]bool{}
			for _, w := range tt.want {
				wantSet[filepath.FromSlash(w)] = true
			}
			if !reflect.DeepEqual(set, wantSet) {
				t.Fatalf("got %v, want %v", set, wantSet)
			}
		})
	}
}

func TestGlob_NewestFirst(t *testing.T) {
	root := newRoot(t, map[string]string{"old.txt": "", "new.txt": ""})
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(root.Dir(), "old.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	got := ok(t, run(t, builtin.NewGlob(root, builtin.WithRipgrep("")), `{"pattern":"*.txt"}`))
	if got != "new.txt\nold.txt" {
		t.Fatalf("order: %q", got)
	}
}

func TestGlob_Errors(t *testing.T) {
	root := newRoot(t, map[string]string{"a.go": ""})
	tl := builtin.NewGlob(root, builtin.WithRipgrep(""))
	failed(t, run(t, tl, `{}`), "pattern is required")
	failed(t, run(t, tl, `{"pattern":"[bad"}`), "invalid pattern")
	failed(t, run(t, tl, `{"pattern":"*","path":"a.go"}`), "not a directory")
	failed(t, run(t, tl, `{"pattern":"*","path":"../"}`), "outside the working directory")
}

func TestGlob_RipgrepAgreesWithPureGo(t *testing.T) {
	rg := ripgrep(t)
	root := newRoot(t, globFixture)
	withRg := builtin.NewGlob(root, builtin.WithRipgrep(rg))
	pure := builtin.NewGlob(root, builtin.WithRipgrep(""))

	for _, args := range []string{`{"pattern":"*.go"}`, `{"pattern":"cmd/**/*.go"}`, `{"pattern":"*.md","path":"docs"}`, `{"pattern":"*.rs"}`} {
		a := lineSet(ok(t, run(t, withRg, args)))
		b := lineSet(ok(t, run(t, pure, args)))
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: rg %v != go %v", args, a, b)
		}
	}
	// An invalid pattern is reported by the Go validator before rg runs.
	got := failed(t, run(t, withRg, `{"pattern":"[bad"}`), "invalid pattern")
	if strings.Contains(got, "rg:") {
		t.Fatalf("rg's own message leaked: %q", got)
	}
}
