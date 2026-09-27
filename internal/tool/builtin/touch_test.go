package builtin_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestFileTools_Touches(t *testing.T) {
	root := newRoot(t, map[string]string{"main.go": "package main\n"})
	want := filepath.Join(root.Dir(), "main.go")
	tools := map[string]agentapi.Tool{
		"read_file":  builtin.NewReadFile(root),
		"write_file": builtin.NewWriteFile(root),
		"edit_file":  builtin.NewEditFile(root),
	}
	for name, tl := range tools {
		call := agentapi.ToolCall{ID: "c", Name: name, Arguments: json.RawMessage(`{"path":"main.go"}`)}
		if got := tool.Touches(call, tl); len(got) != 1 || got[0] != want {
			t.Errorf("%s touches %v, want [%s]", name, got, want)
		}
		// A path outside the root, or none at all, touches nothing — the
		// call itself will be refused anyway.
		for _, args := range []string{`{"path":"../secret"}`, `{}`, `{"path":`} {
			call.Arguments = json.RawMessage(args)
			if got := tool.Touches(call, tl); got != nil {
				t.Errorf("%s %s touches %v, want nil", name, args, got)
			}
		}
	}
	// The tools that do not name a file say nothing: list_dir, glob,
	// grep look, they do not read a file the rules could be about.
	call := agentapi.ToolCall{ID: "c", Name: "list_dir", Arguments: json.RawMessage(`{"path":"."}`)}
	if got := tool.Touches(call, builtin.NewListDir(root)); got != nil {
		t.Errorf("list_dir touches %v, want nil", got)
	}
}
