package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// newRoot creates a temporary working directory populated with files —
// map keys are slash-separated relative paths — and returns its Root.
func newRoot(t *testing.T, files map[string]string) tool.Root {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// run calls the tool with args (a JSON object literal) and fails the test
// on a machinery error — every test that expects one calls Run directly.
func run(t *testing.T, tl agentapi.Tool, args string) agentapi.ToolResult {
	t.Helper()
	res, err := tl.Run(context.Background(), "call-1", json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s returned a machinery error: %v", tl.Name(), err)
	}
	if res.CallID != "call-1" {
		t.Fatalf("%s result not attributed to the call: %+v", tl.Name(), res)
	}
	if err := res.Validate(); err != nil {
		t.Fatalf("%s produced an invalid result: %v", tl.Name(), err)
	}
	return res
}

// ok asserts a successful result and returns its text.
func ok(t *testing.T, res agentapi.ToolResult) string {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Text())
	}
	return res.Text()
}

// failed asserts an error result containing want and returns its text.
func failed(t *testing.T, res agentapi.ToolResult, want string) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("expected an error result, got success: %s", res.Text())
	}
	if !strings.Contains(res.Text(), want) {
		t.Fatalf("error %q does not mention %q", res.Text(), want)
	}
	return res.Text()
}

func readBack(t *testing.T, root tool.Root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root.Dir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ripgrep returns the rg path or skips the test.
func ripgrep(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep not installed")
	}
	return p
}

// sortedLines splits text into its non-empty lines, sorted, for
// order-insensitive comparison.
func lineSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "(") {
			set[l] = true
		}
	}
	return set
}
