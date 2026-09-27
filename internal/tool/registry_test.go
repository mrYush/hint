package tool_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

type namedTool struct {
	stubTool
	schema string
	class  agentapi.ActionClass
}

func (n namedTool) InputSchema() json.RawMessage { return json.RawMessage(n.schema) }
func (n namedTool) Class() agentapi.ActionClass  { return n.class }

func named(name string) namedTool {
	return namedTool{stubTool: stubTool{name: name}, schema: `{"type":"object"}`, class: agentapi.ClassRead}
}

func TestRegistry_SortedAccessors(t *testing.T) {
	r, err := tool.NewRegistry(named("grep"), named("bash"), named("read_file"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bash", "grep", "read_file"}
	if got := r.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	var fromTools, fromSchemas []string
	for _, tl := range r.Tools() {
		fromTools = append(fromTools, tl.Name())
	}
	for _, s := range r.Schemas() {
		fromSchemas = append(fromSchemas, s.Name)
	}
	if !reflect.DeepEqual(fromTools, want) || !reflect.DeepEqual(fromSchemas, want) {
		t.Fatalf("Tools = %v, Schemas = %v, want %v", fromTools, fromSchemas, want)
	}
	if got, ok := r.Lookup("grep"); !ok || got.Name() != "grep" {
		t.Fatalf("Lookup(grep) = %v, %v", got, ok)
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Fatalf("Lookup found an unregistered tool")
	}
	if r.Len() != 3 {
		t.Fatalf("Len = %d", r.Len())
	}
}

func TestRegistry_RejectsBadRegistrations(t *testing.T) {
	tests := []struct {
		name string
		tool agentapi.Tool
		want string
	}{
		{"nil", nil, "nil tool"},
		{"empty name", named(""), "no name"},
		{"missing schema", namedTool{stubTool: stubTool{name: "x"}, class: agentapi.ClassRead}, "no input schema"},
		{"non-object schema", namedTool{stubTool: stubTool{name: "x"}, schema: `"str"`, class: agentapi.ClassRead}, "malformed input schema"},
		{"unknown class", namedTool{stubTool: stubTool{name: "x"}, schema: `{}`, class: "delete"}, "unknown action class"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r tool.Registry
			err := r.Register(tt.tool)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		_, err := tool.NewRegistry(named("a"), named("a"))
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("err = %v", err)
		}
	})
}
