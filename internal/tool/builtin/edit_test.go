package builtin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func editArgs(path, old, new string) string {
	b, _ := json.Marshal(map[string]string{"path": path, "old_str": old, "new_str": new})
	return string(b)
}

const sample = `package main

func main() {
	x := 1
	fmt.Println(x)
}

func other() {
	x := 2
	fmt.Println(x)
}
`

func TestEditFile_ExactUniqueReplace(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": sample})
	tl := builtin.NewEditFile(root)
	if tl.Class() != agentapi.ClassWrite {
		t.Fatalf("class = %s", tl.Class())
	}

	got := ok(t, run(t, tl, editArgs("main.go", "\tx := 1\n\tfmt.Println(x)\n", "\tx := 10\n\tfmt.Println(x * 2)\n")))
	if !strings.HasPrefix(got, "Edited main.go. The changed region now reads:\n") {
		t.Fatalf("result: %q", got)
	}
	// The snippet shows the new lines, numbered, with context around them.
	for _, want := range []string{"     3\tfunc main() {", "     4\t\tx := 10", "     5\t\tfmt.Println(x * 2)", "     6\t}"} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(readBack(t, root, "main.go"), "x := 10\n\tfmt.Println(x * 2)\n}\n\nfunc other") {
		t.Fatal("file not edited as expected")
	}
}

func TestEditFile_AmbiguousListsLines(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": sample})
	tl := builtin.NewEditFile(root)
	got := failed(t, run(t, tl, editArgs("main.go", "\tfmt.Println(x)\n", "\tfmt.Println(y)\n")), "occurs 2 times")
	if !strings.Contains(got, "lines 5, 10") {
		t.Fatalf("line numbers missing: %q", got)
	}
	if readBack(t, root, "main.go") != sample {
		t.Fatal("file changed despite the ambiguity")
	}
}

func TestEditFile_IndentationFallback(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": sample})
	tl := builtin.NewEditFile(root)

	// The model dropped the leading tab on both old and new: the block is
	// still unique once indentation is ignored, so the edit applies and the
	// replacement gets the file's indentation back.
	got := ok(t, run(t, tl, editArgs("main.go", "x := 2\nfmt.Println(x)\n", "x := 3\nfmt.Println(x + 1)\n")))
	if !strings.Contains(got, "matched ignoring a uniform indentation difference") {
		t.Fatalf("note missing: %q", got)
	}
	if !strings.Contains(readBack(t, root, "main.go"), "func other() {\n\tx := 3\n\tfmt.Println(x + 1)\n}\n") {
		t.Fatalf("re-indentation wrong:\n%s", readBack(t, root, "main.go"))
	}

	// Ambiguous even ignoring indentation: refused, file untouched.
	root2 := newRoot(t, map[string]string{"main.go": sample})
	tl2 := builtin.NewEditFile(root2)
	failed(t, run(t, tl2, editArgs("main.go", "  fmt.Println(x)\n", "  fmt.Print(x)\n")), "several places")
	// A partial-line difference is not an indentation problem.
	failed(t, run(t, tl2, editArgs("main.go", "  fmt.Println", "  fmt.Print")), "not found")
	if readBack(t, root2, "main.go") != sample {
		t.Fatal("file changed by a refused edit")
	}
}

func TestEditFile_NotFoundShowsClosestLines(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": sample})
	tl := builtin.NewEditFile(root)
	got := failed(t, run(t, tl, editArgs("main.go", "func main() {\n\tx := 1\n\tfmt.Printn(x)\n", "")), "old_str not found")
	if !strings.Contains(got, "Closest match at lines 3-5 (2 of 3 lines agree") {
		t.Fatalf("hint missing: %q", got)
	}
	if !strings.Contains(got, "→x := 1") {
		t.Fatalf("whitespace not made visible: %q", got)
	}
}

func TestEditFile_CRLF(t *testing.T) {
	root := newRoot(t, map[string]string{"win.txt": "alpha\r\nbeta\r\ngamma\r\n"})
	tl := builtin.NewEditFile(root)
	got := ok(t, run(t, tl, editArgs("win.txt", "beta\n", "BETA\nbeta2\n")))
	if !strings.Contains(got, "CRLF") {
		t.Fatalf("note missing: %q", got)
	}
	if readBack(t, root, "win.txt") != "alpha\r\nBETA\r\nbeta2\r\ngamma\r\n" {
		t.Fatalf("line endings not preserved: %q", readBack(t, root, "win.txt"))
	}
}

func TestEditFile_EmptyOldCreatesOrAppends(t *testing.T) {
	root := newRoot(t, map[string]string{"notes.md": "# Notes"})
	tl := builtin.NewEditFile(root)

	got := ok(t, run(t, tl, editArgs("new/file.txt", "", "fresh\n")))
	if got != "Created new/file.txt (6 bytes)" || readBack(t, root, "new/file.txt") != "fresh\n" {
		t.Fatalf("create: %q", got)
	}

	got = ok(t, run(t, tl, editArgs("notes.md", "", "- item\n")))
	if !strings.HasPrefix(got, "Appended 7 bytes to notes.md") {
		t.Fatalf("append: %q", got)
	}
	if readBack(t, root, "notes.md") != "# Notes\n- item\n" {
		t.Fatalf("append result: %q", readBack(t, root, "notes.md"))
	}
}

func TestEditFile_Errors(t *testing.T) {
	root := newRoot(t, map[string]string{"a.txt": "hello\n", "d/x": ""})
	tl := builtin.NewEditFile(root)
	tests := []struct{ name, args, want string }{
		{"missing path", `{"old_str":"a","new_str":"b"}`, "path is required"},
		{"identical", editArgs("a.txt", "hello", "hello"), "identical"},
		{"not found file", editArgs("nope", "a", "b"), "no such file"},
		{"directory", editArgs("d", "a", "b"), "is a directory"},
		{"outside", editArgs("../a", "a", "b"), "outside the working directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { failed(t, run(t, tl, tt.args), tt.want) })
	}
}
