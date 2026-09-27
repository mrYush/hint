package builtin_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func needsPOSIXShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tests use POSIX shell syntax")
	}
}

func TestBash_RunsInRoot(t *testing.T) {
	needsPOSIXShell(t)
	root := newRoot(t, map[string]string{"marker.txt": ""})
	tl := builtin.NewBash(root)
	if tl.Class() != agentapi.ClassExecute {
		t.Fatalf("class = %s", tl.Class())
	}

	got := ok(t, run(t, tl, `{"command":"ls && pwd"}`))
	if !strings.HasPrefix(got, "marker.txt\n") {
		t.Fatalf("not run in root: %q", got)
	}
	if !strings.Contains(got, root.Dir()) {
		t.Fatalf("pwd = %q, want %q", got, root.Dir())
	}
	if got := ok(t, run(t, tl, `{"command":"true"}`)); got != "(no output)" {
		t.Fatalf("silent command: %q", got)
	}
}

func TestBash_ExitCodeAndStderr(t *testing.T) {
	needsPOSIXShell(t)
	tl := builtin.NewBash(newRoot(t, nil))

	got := failed(t, run(t, tl, `{"command":"echo out; echo err >&2; exit 3"}`), "[exit code 3]")
	if got != "out\n--- stderr ---\nerr\n[exit code 3]" {
		t.Fatalf("layout: %q", got)
	}
	// stderr alone, with success, is still reported.
	got = ok(t, run(t, tl, `{"command":"echo warn >&2"}`))
	if got != "--- stderr ---\nwarn\n" {
		t.Fatalf("stderr only: %q", got)
	}
}

func TestBash_TimeoutKillsProcessGroup(t *testing.T) {
	needsPOSIXShell(t)
	tl := builtin.NewBash(newRoot(t, nil))

	start := time.Now()
	got := failed(t, run(t, tl, `{"command":"echo started; sleep 30; echo never","timeout":"1"}`), "[timed out after 1s; process killed]")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s; the sleep was not killed", time.Since(start))
	}
	if !strings.HasPrefix(got, "started\n") || strings.Contains(got, "never") {
		t.Fatalf("partial output: %q", got)
	}
}

func TestBash_CanceledByCaller(t *testing.T) {
	needsPOSIXShell(t)
	tl := builtin.NewBash(newRoot(t, nil))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	res, err := tl.Run(ctx, "c1", json.RawMessage(`{"command":"sleep 30"}`))
	if err != nil {
		t.Fatalf("machinery error: %v", err)
	}
	failed(t, res, "[canceled]")
}

func TestBash_LargeOutputIsBounded(t *testing.T) {
	needsPOSIXShell(t)
	tl := builtin.NewBash(newRoot(t, nil), builtin.WithLimits(tool.Limits{MaxOutput: 3000}))
	got := ok(t, run(t, tl, `{"command":"seq 1 100000"}`))
	if len(got) > 3000 {
		t.Fatalf("output is %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "1\n2\n") || !strings.HasSuffix(got, "99999\n100000\n") {
		t.Fatalf("head/tail lost: %q ... %q", got[:10], got[len(got)-20:])
	}
	if !strings.Contains(got, "[output truncated:") {
		t.Fatal("marker missing")
	}
}

func TestBash_Errors(t *testing.T) {
	tl := builtin.NewBash(newRoot(t, nil))
	failed(t, run(t, tl, `{}`), "command is required")
	failed(t, run(t, tl, `{"command":"   "}`), "command is required")
	failed(t, run(t, tl, `{"command":"true","timeout":-1}`), "must not be negative")
}
