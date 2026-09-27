package builtin_test

import (
	"reflect"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestReadOnly_ContainsNoWriteOrExecuteTool(t *testing.T) {
	root := newRoot(t, nil)
	reg, err := tool.NewRegistry(builtin.ReadOnly(root)...)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"glob", "grep", "list_dir", "read_file", "todo"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadOnly = %v, want %v", got, want)
	}
	for _, tl := range reg.Tools() {
		if tl.Class() != agentapi.ClassRead {
			t.Errorf("%s has class %s in the read-only set", tl.Name(), tl.Class())
		}
	}
}

func TestAll_RegistersWithValidSchemas(t *testing.T) {
	root := newRoot(t, nil)
	reg, err := tool.NewRegistry(builtin.All(root)...)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bash", "edit_file", "glob", "grep", "list_dir", "read_file", "todo", "write_file"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("All = %v, want %v", got, want)
	}
	classes := map[string]agentapi.ActionClass{
		"bash": agentapi.ClassExecute, "edit_file": agentapi.ClassWrite, "write_file": agentapi.ClassWrite,
	}
	for _, s := range reg.Schemas() {
		if err := s.Validate(); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
		if s.Description == "" {
			t.Errorf("%s has no description", s.Name)
		}
	}
	for name, class := range classes {
		tl, _ := reg.Lookup(name)
		if tl.Class() != class {
			t.Errorf("%s class = %s, want %s", name, tl.Class(), class)
		}
	}
}
