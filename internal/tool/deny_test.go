package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestRoot_DenyRefusesHintsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{".config/hint", ".local/share/hint/sessions/abc", ".hint", "src"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".hint", "config.yaml"), []byte("providers: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hint", "rules.md"), []byte("# rule\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	real := base.Dir()
	root := base.Deny(filepath.Join(real, ".config", "hint"), filepath.Join(real, ".local", "share", "hint", "sessions"),
		filepath.Join(real, ".hint", "config.yaml"), "", "/nowhere/outside")

	for _, p := range []string{".config/hint", ".config/hint/config.yaml", ".local/share/hint/sessions/abc/s.jsonl", ".hint/config.yaml", filepath.Join(real, ".hint", "config.yaml")} {
		if _, err := root.Resolve(p); !errors.Is(err, tool.ErrDenied) {
			t.Errorf("Resolve(%q) = %v, want ErrDenied", p, err)
		}
	}
	for _, p := range []string{"src/a.go", ".hint/rules.md", ".hint", ".config", ".local/share/hint/other"} {
		if _, err := root.Resolve(p); err != nil {
			t.Errorf("Resolve(%q) = %v, want it allowed", p, err)
		}
	}
	// The base root is untouched: Deny returns a copy.
	if _, err := base.Resolve(".hint/config.yaml"); err != nil {
		t.Errorf("Deny changed the original root: %v", err)
	}
	if !root.Denied(filepath.Join(real, ".config", "hint", "x")) || root.Denied(filepath.Join(real, ".config", "hinted")) {
		t.Error("Denied does not match whole path components")
	}
	got := root.DeniedUnder(real)
	want := map[string]bool{".config/hint": true, ".local/share/hint/sessions": true, ".hint/config.yaml": true}
	if len(got) != len(want) {
		t.Fatalf("DeniedUnder = %v, want %v", got, want)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("DeniedUnder lists %q", g)
		}
	}
	if under := root.DeniedUnder(filepath.Join(real, ".local")); len(under) != 1 || under[0] != "share/hint/sessions" {
		t.Errorf("DeniedUnder(.local) = %v", under)
	}

	if runtime.GOOS == "windows" {
		return
	}
	// A symlink to a denied directory is denied by what it resolves to.
	if err := os.Symlink(filepath.Join(real, ".config", "hint"), filepath.Join(real, "src", "cfg")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Resolve("src/cfg/config.yaml"); !errors.Is(err, tool.ErrDenied) {
		t.Errorf("Resolve through a link into a denied directory = %v, want ErrDenied", err)
	}
}

// echoTool returns its arguments' text as its result.
type echoTool struct{}

func (echoTool) Name() string                 { return "echo" }
func (echoTool) Description() string          { return "echo" }
func (echoTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (echoTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (echoTool) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in struct{ Text string }
	_ = json.Unmarshal(args, &in)
	return agentapi.TextResult(callID, "echo", in.Text), nil
}

func TestRedactingScrubsEveryResult(t *testing.T) {
	secrets := []string{"sk-live-1234567890"}
	wrapped := tool.Redacting(echoTool{}, func() []string { return secrets })
	if wrapped.Name() != "echo" || wrapped.Class() != agentapi.ClassRead {
		t.Fatal("the decorator hides the tool's identity")
	}
	res, err := wrapped.Run(context.Background(), "c1", json.RawMessage(`{"Text":"token=sk-live-1234567890; again sk-live-1234567890"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Text(); got != "token=***; again ***" {
		t.Errorf("result = %q", got)
	}
	// The set is read on every call: a key added later is scrubbed too.
	secrets = append(secrets, "other-key-9876543210")
	res, _ = wrapped.Run(context.Background(), "c2", json.RawMessage(`{"Text":"other-key-9876543210"}`))
	if res.Text() != "***" {
		t.Errorf("a later secret was not scrubbed: %q", res.Text())
	}
	plain := tool.Redacting(echoTool{}, nil)
	res, _ = plain.Run(context.Background(), "c3", json.RawMessage(`{"Text":"sk-live-1234567890"}`))
	if res.Text() != "sk-live-1234567890" {
		t.Errorf("a nil secrets function changed the result: %q", res.Text())
	}
}

// TestRoot_OpenHoldsAfterASymlinkSwap is risk R8 as a test: Resolve
// approved a regular file, the file is then replaced by a symlink to a
// place outside the root, and the open that follows is refused by
// os.Root — the kernel resolves the path under the directory, so the
// window between the check and the open closes nothing.
func TestRoot_OpenHoldsAfterASymlinkSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(victim, []byte("harmless"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := root.Resolve("notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	// The swap, between the check and the open.
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), victim); err != nil {
		t.Fatal(err)
	}
	if f, err := root.Open(abs); err == nil {
		_ = f.Close()
		t.Fatal("the swapped symlink was opened")
	}
	if _, err := root.ReadFile(abs); err == nil {
		t.Fatal("the swapped symlink was read")
	}
	if _, err := root.Stat(abs); err == nil {
		t.Fatal("the swapped symlink was stat'ed")
	}
	// Writes through the root cannot land outside either.
	if err := root.Rename(abs, filepath.Join(outside, "moved")); err == nil {
		t.Fatal("a rename out of the root succeeded")
	}
	if _, err := root.OpenFile(filepath.Join(dir, "..", "escape"), os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		t.Fatal("a file was created outside the root")
	}
	// A walk goes through os.Root too and gives absolute paths.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var seen []string
	if err := root.WalkDir(root.Dir(), func(p string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		seen = append(seen, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 || seen[0] != root.Dir() || seen[1] != filepath.Join(root.Dir(), "a.txt") {
		t.Errorf("WalkDir saw %v", seen)
	}
}
