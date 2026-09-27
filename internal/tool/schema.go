package tool

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// MustSchema generates the JSON Schema describing the argument struct v, in
// the shape [agentapi.ToolSchema.InputSchema] expects: a self-contained
// object schema with no $ref, $defs, $schema or $id keys, because tool-call
// schemas are embedded verbatim into a provider request and no provider
// resolves references.
//
// Field descriptions come from a `jsonschema_description:"..."` struct tag;
// enumerations from `jsonschema:"enum=a,enum=b"`. A field is required unless
// its json tag carries omitempty — the same rule encoding/json uses to omit
// it, so the schema and the wire format cannot disagree.
//
// It panics on failure, like regexp.MustCompile: the argument is a
// compile-time known type, so a failure is a programmer error that should
// surface at construction, not be handled at every call site.
func MustSchema(v any) json.RawMessage {
	r := jsonschema.Reflector{
		// Inline nested structs instead of emitting $ref/$defs.
		DoNotReference: true,
		// Make the top level the object itself, not a $ref to it.
		ExpandedStruct: true,
		// Skip the $id keyword: the schema has no identity outside the
		// request it travels in.
		Anonymous: true,
	}
	s := r.Reflect(v)
	// Reflect stamps the draft URL onto the root; a tool schema is not a
	// standalone document, so drop it.
	s.Version = ""

	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("tool: schema for %T: %v", v, err))
	}
	return b
}
