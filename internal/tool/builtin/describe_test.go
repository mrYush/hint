package builtin_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

// describe asks the tool (through the limits decorator, as the agent
// would) for its preview of args.
func describe(t *testing.T, tl agentapi.Tool, args string) (tool.Description, error) {
	t.Helper()
	var d tool.Describer
	for x := tl; x != nil; x = tool.Unwrap(x) {
		if dd, ok := x.(tool.Describer); ok {
			d = dd
			break
		}
	}
	if d == nil {
		t.Fatalf("%s has no Describer", tl.Name())
	}
	return d.Describe(context.Background(), json.RawMessage(args))
}

func mustDescribe(t *testing.T, tl agentapi.Tool, args string) tool.Description {
	t.Helper()
	d, err := describe(t, tl, args)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	return d
}

func TestWriteFile_DescribeNewFile(t *testing.T) {
	root := newRoot(t, nil)
	tl := builtin.NewWriteFile(root)
	d := mustDescribe(t, tl, `{"path":"a/new.txt","content":"one\ntwo\n"}`)
	if d.Summary != "create a/new.txt (8 bytes)" || d.Path != "a/new.txt" {
		t.Fatalf("description = %+v", d)
	}
	if !strings.HasPrefix(d.Detail, "--- /dev/null\n+++ b/a/new.txt\n") || !strings.Contains(d.Detail, "+one\n+two\n") {
		t.Fatalf("detail:\n%s", d.Detail)
	}
	// Preview must not create anything.
	if _, err := os.Stat(filepath.Join(root.Dir(), "a")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Describe created the parent directory")
	}
}

func TestWriteFile_DescribeOverwrite(t *testing.T) {
	root := newRoot(t, map[string]string{"f.txt": "a\nb\nc\n"})
	d := mustDescribe(t, builtin.NewWriteFile(root), `{"path":"f.txt","content":"a\nB\nc\nd\n"}`)
	if d.Summary != "overwrite f.txt (+2 -1 lines)" {
		t.Fatalf("summary = %q", d.Summary)
	}
	if !strings.Contains(d.Detail, "-b\n+B\n c\n+d\n") {
		t.Fatalf("detail:\n%s", d.Detail)
	}
	if readBack(t, root, "f.txt") != "a\nb\nc\n" {
		t.Fatal("Describe changed the file")
	}
}

func TestWriteFile_DescribeBinaryTarget(t *testing.T) {
	root := newRoot(t, map[string]string{"blob": "\x00\x01\x02"})
	d := mustDescribe(t, builtin.NewWriteFile(root), `{"path":"blob","content":"text"}`)
	if !strings.Contains(d.Summary, "binary") || d.Detail != "" {
		t.Fatalf("description = %+v", d)
	}
}

func TestWriteFile_DescribeRefusesEscape(t *testing.T) {
	root := newRoot(t, nil)
	_, err := describe(t, builtin.NewWriteFile(root), `{"path":"../outside.txt","content":"x"}`)
	if !errors.Is(err, tool.ErrOutsideRoot) {
		t.Fatalf("err = %v, want ErrOutsideRoot", err)
	}
	if _, err := describe(t, builtin.NewWriteFile(root), `{"path":"","content":"x"}`); err == nil {
		t.Fatal("empty path previewed")
	}
	if _, err := describe(t, builtin.NewWriteFile(root), `{"path":`); err == nil {
		t.Fatal("malformed arguments previewed")
	}
}

func TestEditFile_DescribeDiff(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"})
	tl := builtin.NewEditFile(root)
	d := mustDescribe(t, tl, `{"path":"main.go","old_str":"println(\"hi\")","new_str":"println(\"hello\")"}`)
	if d.Summary != "edit main.go (+1 -1 lines)" || d.Path != "main.go" {
		t.Fatalf("description = %+v", d)
	}
	want := "--- a/main.go\n+++ b/main.go\n@@ -1,5 +1,5 @@\n package main\n \n func main() {\n-\tprintln(\"hi\")\n+\tprintln(\"hello\")\n }\n"
	if d.Detail != want {
		t.Fatalf("detail:\n%s\nwant:\n%s", d.Detail, want)
	}
	if !strings.Contains(readBack(t, root, "main.go"), `"hi"`) {
		t.Fatal("Describe applied the edit")
	}

	// The preview and the edit agree: applying the same call yields the
	// file the diff promised.
	ok(t, run(t, tl, `{"path":"main.go","old_str":"println(\"hi\")","new_str":"println(\"hello\")"}`))
	if !strings.Contains(readBack(t, root, "main.go"), `"hello"`) {
		t.Fatal("edit not applied")
	}
}

func TestEditFile_DescribeCreateAndAppend(t *testing.T) {
	root := newRoot(t, map[string]string{"notes.txt": "one"})
	tl := builtin.NewEditFile(root)

	d := mustDescribe(t, tl, `{"path":"fresh.txt","old_str":"","new_str":"hello\n"}`)
	if d.Summary != "create fresh.txt (6 bytes)" || !strings.HasPrefix(d.Detail, "--- /dev/null\n") {
		t.Fatalf("description = %+v", d)
	}

	d = mustDescribe(t, tl, `{"path":"notes.txt","old_str":"","new_str":"two\n"}`)
	if d.Summary != "edit notes.txt (+2 -1 lines)" {
		t.Fatalf("summary = %q", d.Summary)
	}
	if !strings.Contains(d.Detail, "-one\n\\ No newline at end of file\n+one\n+two\n") {
		t.Fatalf("detail:\n%s", d.Detail)
	}
}

func TestEditFile_DescribeReportsRunsErrors(t *testing.T) {
	root := newRoot(t, map[string]string{"f.txt": "abc\n"})
	tl := builtin.NewEditFile(root)
	cases := map[string]string{
		`{"path":"f.txt","old_str":"zzz","new_str":"y"}`: "old_str not found",
		`{"path":"f.txt","old_str":"a","new_str":"a"}`:   "identical",
		`{"path":"missing","old_str":"a","new_str":"b"}`: "no such file",
		`{"path":"../x","old_str":"a","new_str":"b"}`:    "outside the working directory",
	}
	for args, want := range cases {
		_, err := describe(t, tl, args)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", args, err, want)
		}
	}
}

func TestBash_Describe(t *testing.T) {
	root := newRoot(t, nil)
	tl := builtin.NewBash(root)
	d := mustDescribe(t, tl, `{"command":"  go test ./...  ","timeout":120}`)
	if d.Summary != `run "go test ./..." (timeout 120s)` || d.Detail != "go test ./..." || d.Path != "" {
		t.Fatalf("description = %+v", d)
	}
	d = mustDescribe(t, tl, `{"command":"echo one\necho two"}`)
	if d.Summary != `run "echo one..."` || d.Detail != "echo one\necho two" {
		t.Fatalf("multi-line description = %+v", d)
	}
	if _, err := describe(t, tl, `{"command":"  "}`); err == nil {
		t.Fatal("empty command previewed")
	}
}

// TestYoloDoesNotLiftRoot is the checklist's path-escape case: no run
// mode changes what tool.Root allows, so even with nothing asking, a
// write outside the working directory fails and creates nothing.
func TestYoloDoesNotLiftRoot(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "project")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		tl   agentapi.Tool
		args string
	}{
		{builtin.NewWriteFile(root), `{"path":"../secret","content":"x"}`},
		{builtin.NewEditFile(root), `{"path":"../secret","old_str":"","new_str":"x"}`},
		{builtin.NewWriteFile(root), `{"path":"` + filepath.ToSlash(filepath.Join(parent, "secret")) + `","content":"x"}`},
	} {
		failed(t, run(t, c.tl, c.args), "outside the working directory")
	}
	if _, err := os.Stat(filepath.Join(parent, "secret")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was created outside the root")
	}
}
