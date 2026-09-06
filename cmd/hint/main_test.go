package main

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestToolRegistry_AllBehindPermissions replaces WP0.5's read-only pin: the
// binary now offers write_file, edit_file and bash, and it may only do so
// because run() builds the agent with a permission.Gate. The registry half
// is checked here; the gate half is a compile-time fact of run() plus
// TestRunMode below, since there is no way to build the agent in main.go
// without it.
func TestToolRegistry_AllBehindPermissions(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := toolRegistry(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"bash", "edit_file", "glob", "grep", "list_dir", "read_file", "todo", "write_file"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}
	classes := map[agentapi.ActionClass]int{}
	for _, tl := range reg.Tools() {
		classes[tl.Class()]++
	}
	if classes[agentapi.ClassWrite] != 2 || classes[agentapi.ClassExecute] != 1 {
		t.Fatalf("classes = %v, want 2 write + 1 execute", classes)
	}
}

func TestRunMode(t *testing.T) {
	cases := []struct {
		ask, autoEdit, yolo bool
		want                permission.Mode
	}{
		{false, false, false, permission.ModeAsk},
		{true, false, false, permission.ModeAsk},
		{false, true, false, permission.ModeAutoEdit},
		{false, false, true, permission.ModeYolo},
	}
	for _, c := range cases {
		if got := runMode(c.ask, c.autoEdit, c.yolo); got != c.want {
			t.Errorf("runMode(%v,%v,%v) = %s, want %s", c.ask, c.autoEdit, c.yolo, got, c.want)
		}
	}
}

func TestWarnMode(t *testing.T) {
	// A regular file stands in for a non-terminal stdin (/dev/null would
	// not do: it is a character device, exactly like a tty).
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var out bytes.Buffer
	warnMode(&out, permission.ModeYolo, f)
	if !bytes.Contains(out.Bytes(), []byte("WARNING")) || !bytes.Contains(out.Bytes(), []byte("--yolo")) {
		t.Fatalf("yolo warning missing: %q", out.String())
	}

	out.Reset()
	warnMode(&out, permission.ModeAsk, f)
	if !bytes.Contains(out.Bytes(), []byte("not a terminal")) || !bytes.Contains(out.Bytes(), []byte("file edits and shell commands")) {
		t.Fatalf("non-tty notice missing: %q", out.String())
	}

	out.Reset()
	warnMode(&out, permission.ModeAutoEdit, f)
	if !bytes.Contains(out.Bytes(), []byte("shell commands will be denied")) || bytes.Contains(out.Bytes(), []byte("file edits")) {
		t.Fatalf("auto-edit notice wrong: %q", out.String())
	}
}
