package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/session"
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

func TestSessionModeOf(t *testing.T) {
	cases := []struct {
		cont, resume, off bool
		want              sessionMode
	}{
		{false, false, false, sessionNew},
		{true, false, false, sessionContinue},
		{false, true, false, sessionResume},
		{false, false, true, sessionOff},
	}
	for _, c := range cases {
		if got := sessionModeOf(c.cont, c.resume, c.off); got != c.want {
			t.Errorf("sessionModeOf(%v,%v,%v) = %d, want %d", c.cont, c.resume, c.off, got, c.want)
		}
	}
}

// TestOpenSession walks the four modes against one store: a new session
// per run by default, -c continuing the latest (or starting one when
// there is none), -r reading the choice through the shared line reader,
// and --no-session recording nothing.
func TestOpenSession(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	cwd := t.TempDir()
	ctx := context.Background()
	noInput := console.NewLineReader(strings.NewReader(""))

	if sess, err := openSession(ctx, store, cwd, sessionOff, noInput, io.Discard); sess != nil || err != nil {
		t.Fatalf("sessionOff = %v, %v; want nil, nil", sess, err)
	}

	var stderr bytes.Buffer
	first, err := openSession(ctx, store, cwd, sessionContinue, noInput, &stderr)
	if err != nil {
		t.Fatalf("continue with nothing to continue: %v", err)
	}
	if !strings.Contains(stderr.String(), "starting a new one") {
		t.Errorf("no notice about the fallback:\n%s", stderr.String())
	}
	if err := first.AppendMessage(agentapi.UserMessage("first")); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := openSession(ctx, store, cwd, sessionNew, noInput, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID() == first.ID() {
		t.Fatal("sessionNew reused the existing session")
	}
	_ = second.Close()

	stderr.Reset()
	// The first session is older by creation but touched last: -c follows
	// modification time.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(first.Path(), later, later); err != nil {
		t.Fatal(err)
	}
	cont, err := openSession(ctx, store, cwd, sessionContinue, noInput, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if cont.ID() != first.ID() || cont.Len() != 1 {
		t.Fatalf("continue = %s with %d messages, want %s with 1", cont.ID(), cont.Len(), first.ID())
	}
	if !strings.Contains(stderr.String(), "continuing session "+first.ID()) {
		t.Errorf("no notice:\n%s", stderr.String())
	}
	_ = cont.Close()

	// -r: the second listed entry is the older, untouched session.
	stderr.Reset()
	picked, err := openSession(ctx, store, cwd, sessionResume, console.NewLineReader(strings.NewReader("2\n")), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if picked.ID() != second.ID() {
		t.Fatalf("resume picked %s, want %s", picked.ID(), second.ID())
	}
	_ = picked.Close()

	// -r with nobody answering is a refusal, not a silent new session.
	if _, err := openSession(ctx, store, cwd, sessionResume, noInput, io.Discard); !errors.Is(err, session.ErrNoChoice) {
		t.Fatalf("resume without an answer = %v, want ErrNoChoice", err)
	}

	// -r in a directory with no sessions starts a new one, like -c.
	fresh, err := openSession(ctx, store, t.TempDir(), sessionResume, noInput, io.Discard)
	if err != nil || fresh == nil {
		t.Fatalf("resume with nothing to resume = %v, %v", fresh, err)
	}
	_ = fresh.Close()
}
