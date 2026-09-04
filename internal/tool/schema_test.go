package tool_test

import (
	"encoding/json"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

type sampleArgs struct {
	Path   string   `json:"path" jsonschema_description:"File to read"`
	Offset tool.Int `json:"offset,omitempty" jsonschema_description:"1-based line"`
	Status string   `json:"status,omitempty" jsonschema:"enum=pending,enum=done"`
	Items  []item   `json:"items"`
}

type item struct {
	Content string `json:"content"`
	Done    bool   `json:"done,omitempty"`
}

// TestMustSchema pins the exact shape sent to providers: any drift here
// changes every request, so the whole document is compared, not just a few
// keys.
func TestMustSchema(t *testing.T) {
	got := tool.MustSchema(sampleArgs{})

	want := `{
	  "properties": {
	    "path": {"type": "string", "description": "File to read"},
	    "offset": {"type": "integer", "description": "1-based line"},
	    "status": {"type": "string", "enum": ["pending", "done"]},
	    "items": {
	      "items": {
	        "properties": {
	          "content": {"type": "string"},
	          "done": {"type": "boolean"}
	        },
	        "additionalProperties": false,
	        "type": "object",
	        "required": ["content"]
	      },
	      "type": "array"
	    }
	  },
	  "additionalProperties": false,
	  "type": "object",
	  "required": ["path", "items"]
	}`
	assertJSONEqual(t, got, want)

	if err := (agentapi.ToolSchema{Name: "x", InputSchema: got}).Validate(); err != nil {
		t.Fatalf("generated schema rejected by the contract: %v", err)
	}
	for _, key := range []string{"$schema", "$id", "$ref", "$defs"} {
		var m map[string]any
		_ = json.Unmarshal(got, &m)
		if _, ok := m[key]; ok {
			t.Errorf("schema carries %s, which providers do not resolve", key)
		}
	}
}

func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	gb, _ := json.MarshalIndent(g, "", "  ")
	wb, _ := json.MarshalIndent(w, "", "  ")
	if string(gb) != string(wb) {
		t.Fatalf("schema mismatch\n got: %s\nwant: %s", gb, wb)
	}
}
