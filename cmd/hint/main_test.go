package main

import (
	"reflect"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestToolRegistry_ReadOnlyUntilPermissions pins the product constraint of
// WP0.5: until WP0.6 can ask the user, the binary must not be able to
// write a file or run a command. The builtin package has its own test for
// ReadOnly's contents; this one guards the wiring, so that changing
// ReadOnly to All in main.go fails CI instead of silently shipping an
// unconfirmed write path.
func TestToolRegistry_ReadOnlyUntilPermissions(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := toolRegistry(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"glob", "grep", "list_dir", "read_file", "todo"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}
	for _, tl := range reg.Tools() {
		if tl.Class() != agentapi.ClassRead {
			t.Errorf("%s has class %s; write/execute tools need WP0.6's permission layer first", tl.Name(), tl.Class())
		}
	}
}
