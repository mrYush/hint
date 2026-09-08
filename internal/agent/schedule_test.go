package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// classTool is a tool that is nothing but its action class: buildSchedule
// looks at no other method.
type classTool agentapi.ActionClass

func (classTool) Name() string                 { return "" }
func (classTool) Description() string          { return "" }
func (classTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t classTool) Class() agentapi.ActionClass {
	return agentapi.ActionClass(t)
}
func (classTool) Run(context.Context, string, json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.ToolResult{}, nil
}

var scheduleTools = map[string]agentapi.Tool{
	"read":  classTool(agentapi.ClassRead),
	"write": classTool(agentapi.ClassWrite),
	"exec":  classTool(agentapi.ClassExecute),
}

func toolCall(id, name string, after ...string) agentapi.ToolCall {
	return agentapi.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(`{}`), After: after}
}

func TestBuildSchedule(t *testing.T) {
	cases := []struct {
		name        string
		calls       []agentapi.ToolCall
		maxParallel int
		want        node
	}{
		{
			name:        "reads fan out, a write is a step of its own, reads fan out again",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "read"), toolCall("c3", "write"), toolCall("c4", "read"), toolCall("c5", "read")},
			maxParallel: 4,
			want:        seq{par{call(0), call(1)}, call(2), par{call(3), call(4)}},
		},
		{
			name:        "a lone read is a call, not a group of one",
			calls:       []agentapi.ToolCall{toolCall("c1", "read")},
			maxParallel: 4,
			want:        seq{call(0)},
		},
		{
			name:        "execute never joins a group",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "exec"), toolCall("c3", "read")},
			maxParallel: 4,
			want:        seq{call(0), call(1), call(2)},
		},
		{
			name:        "an unknown tool is a step of its own",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "ghost"), toolCall("c3", "read")},
			maxParallel: 4,
			want:        seq{call(0), call(1), call(2)},
		},
		{
			name:        "a limit of one is the plain chain",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "read")},
			maxParallel: 1,
			want:        seq{call(0), call(1)},
		},
		{
			name:        "a group larger than the limit is still one group; the executor bounds it",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "read"), toolCall("c3", "read")},
			maxParallel: 2,
			want:        seq{par{call(0), call(1), call(2)}},
		},
		{
			name:        "After naming a sibling in the open group splits the group",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "read", "c1"), toolCall("c3", "read")},
			maxParallel: 4,
			want:        seq{call(0), par{call(1), call(2)}},
		},
		{
			name:        "After naming an unknown ID is ignored",
			calls:       []agentapi.ToolCall{toolCall("c1", "read"), toolCall("c2", "read", "nope")},
			maxParallel: 4,
			want:        seq{par{call(0), call(1)}},
		},
		{
			name:        "After naming a later call is ignored: request order is never rewritten",
			calls:       []agentapi.ToolCall{toolCall("c1", "read", "c2"), toolCall("c2", "read")},
			maxParallel: 4,
			want:        seq{par{call(0), call(1)}},
		},
		{
			name:        "After naming a call already past is already satisfied",
			calls:       []agentapi.ToolCall{toolCall("c1", "write"), toolCall("c2", "read"), toolCall("c3", "read", "c1")},
			maxParallel: 4,
			want:        seq{call(0), par{call(1), call(2)}},
		},
		{
			name:        "no calls, no steps",
			calls:       nil,
			maxParallel: 4,
			want:        seq(nil),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildSchedule(c.calls, scheduleTools, c.maxParallel)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("schedule = %#v, want %#v", got, c.want)
			}
		})
	}
}
