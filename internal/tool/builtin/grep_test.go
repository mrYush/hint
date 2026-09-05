package builtin_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

var grepFixture = map[string]string{
	"a.go":          "package a\n\nfunc Hello() {}\nfunc hello2() {}\n",
	"sub/b.go":      "package b\n// Hello again\n",
	"sub/notes.txt": "Hello from notes\n",
	"bin.dat":       "Hello\x00binary",
	".secret":       "Hello hidden\n",
	"vendor/v.go":   "Hello vendored\n",
	"dots.txt":      "a.b\naxb\n",
}

func TestGrep_PureGo(t *testing.T) {
	root := newRoot(t, grepFixture)
	tl := builtin.NewGrep(root, builtin.WithRipgrep(""))
	if tl.Class() != agentapi.ClassRead {
		t.Fatalf("class = %s", tl.Class())
	}

	tests := []struct {
		name string
		args string
		want string
	}{
		{"regex across files", `{"pattern":"Hello"}`,
			"a.go:3: func Hello() {}\nsub/b.go:2: // Hello again\nsub/notes.txt:1: Hello from notes"},
		{"case-insensitive flag", `{"pattern":"(?i)hello\\d"}`, "a.go:4: func hello2() {}"},
		{"include glob", `{"pattern":"Hello","include":"*.txt"}`, "sub/notes.txt:1: Hello from notes"},
		{"include path glob", `{"pattern":"Hello","include":"sub/**/*.go"}`, "sub/b.go:2: // Hello again"},
		{"include path glob with path", `{"pattern":"Hello","path":"sub","include":"sub/*.txt"}`, "sub/notes.txt:1: Hello from notes"},
		{"directory path", `{"pattern":"Hello","path":"sub"}`, "sub/b.go:2: // Hello again\nsub/notes.txt:1: Hello from notes"},
		{"file path", `{"pattern":"Hello","path":"a.go"}`, "a.go:3: func Hello() {}"},
		{"literal dot", `{"pattern":"a.b","literal":true}`, "dots.txt:1: a.b"},
		{"regex dot", `{"pattern":"a.b"}`, "dots.txt:1: a.b\ndots.txt:2: axb"},
		{"no matches", `{"pattern":"zzz"}`, "No matches found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ok(t, run(t, tl, tt.args))
			if strings.ReplaceAll(got, "\\", "/") != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestGrep_Errors(t *testing.T) {
	root := newRoot(t, grepFixture)
	tl := builtin.NewGrep(root, builtin.WithRipgrep(""))
	failed(t, run(t, tl, `{}`), "pattern is required")
	failed(t, run(t, tl, `{"pattern":"("}`), "invalid regular expression")
	failed(t, run(t, tl, `{"pattern":"x","include":"[bad"}`), "include")
	failed(t, run(t, tl, `{"pattern":"x","path":"nope"}`), "no such file")
	failed(t, run(t, tl, `{"pattern":"x","path":"../"}`), "outside the working directory")
}

func TestGrep_CapsMatches(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "needle %d\n", i)
	}
	root := newRoot(t, map[string]string{"hay.txt": sb.String()})
	got := ok(t, run(t, builtin.NewGrep(root, builtin.WithRipgrep("")), `{"pattern":"needle"}`))
	if !strings.Contains(got, "showing the first 200 matches") {
		t.Fatalf("cap note missing: %s", got[len(got)-80:])
	}
	if n := strings.Count(got, "hay.txt:"); n != 200 {
		t.Fatalf("%d matches shown, want 200", n)
	}
}

func TestGrep_RipgrepAgreesWithPureGo(t *testing.T) {
	rg := ripgrep(t)
	root := newRoot(t, grepFixture)
	withRg := builtin.NewGrep(root, builtin.WithRipgrep(rg))
	pure := builtin.NewGrep(root, builtin.WithRipgrep(""))

	for _, args := range []string{
		`{"pattern":"Hello"}`, `{"pattern":"(?i)hello\\d"}`, `{"pattern":"Hello","include":"*.txt"}`,
		`{"pattern":"Hello","include":"sub/**/*.go"}`, `{"pattern":"Hello","path":"sub","include":"sub/*.txt"}`,
		`{"pattern":"Hello","path":"sub"}`, `{"pattern":"Hello","path":"a.go"}`,
		`{"pattern":"a.b","literal":true}`, `{"pattern":"zzz"}`,
	} {
		a := ok(t, run(t, withRg, args))
		b := ok(t, run(t, pure, args))
		if !reflect.DeepEqual(lineSet(a), lineSet(b)) {
			t.Errorf("%s:\nrg:\n%s\ngo:\n%s", args, a, b)
		}
	}

	var sb strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&sb, "needle %d\n", i)
	}
	big := newRoot(t, map[string]string{"hay.txt": sb.String()})
	got := ok(t, run(t, builtin.NewGrep(big, builtin.WithRipgrep(rg)), `{"pattern":"needle"}`))
	if n := strings.Count(got, "hay.txt:"); n != 200 || !strings.Contains(got, "showing the first 200") {
		t.Fatalf("rg path not capped: %d matches", n)
	}
}
