package tool

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/mrYush/hint/pkg/agentapi"
)

// DecodeArgs unmarshals a tool call's raw argument JSON into v.
//
// Unknown fields are ignored on purpose: models routinely add keys the
// schema never mentioned, and rejecting the call for that would waste an
// iteration on a harmless mistake. A wrong type for a known field is still
// an error — that is a call the tool cannot act on.
func DecodeArgs(raw json.RawMessage, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// InvalidArgs returns the error [agentapi.ToolResult] for a call whose
// arguments could not be decoded or validated. The message goes back to the
// model, which gets to fix the call on its next turn.
func InvalidArgs(callID, name string, err error) agentapi.ToolResult {
	return agentapi.ErrorResult(callID, name, err.Error())
}

// Int is an integer argument that also accepts its value spelled as a JSON
// string ("10" as well as 10).
//
// Small local models — the offline fallback profile — frequently quote
// numbers in tool arguments. Rejecting "offset": "10" would cost an extra
// round-trip for a call whose intent is unambiguous, so the leniency lives
// here, in one type, rather than in every tool that takes a number.
type Int int

// UnmarshalJSON implements json.Unmarshaler.
func (i *Int) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		return nil
	}
	s = strings.TrimSpace(strings.Trim(s, `"`))
	if s == "" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("%q is not an integer", s)
	}
	*i = Int(n)
	return nil
}

// JSONSchema tells the schema generator that Int is a plain integer, so the
// model is never told it may send a string — the leniency is a courtesy on
// the way in, not part of the advertised contract.
func (Int) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "integer"}
}
