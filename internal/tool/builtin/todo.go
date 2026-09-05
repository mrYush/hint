package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const todoName = "todo"

const todoDescription = `Replace the plan for the current task with the given list of items, each with a status. The list is shown to the user.

Use it for multi-step work: write the plan before starting, mark the item you are working on in_progress (ideally one at a time), and mark items completed as you finish them, sending the full updated list each time. Skip it for a single-step task.`

// TodoStatus is the state of one plan item.
type TodoStatus string

// The states a plan item moves through.
const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoCompleted  TodoStatus = "completed"
)

// Valid reports whether s is a defined status.
func (s TodoStatus) Valid() bool {
	switch s {
	case TodoPending, TodoInProgress, TodoCompleted:
		return true
	default:
		return false
	}
}

// TodoItem is one entry of the plan.
type TodoItem struct {
	Content string     `json:"content" jsonschema_description:"What needs to be done, in imperative form"`
	Status  TodoStatus `json:"status" jsonschema:"enum=pending,enum=in_progress,enum=completed" jsonschema_description:"pending, in_progress or completed"`
}

type todoArgs struct {
	Items []TodoItem `json:"items" jsonschema_description:"The complete, updated plan; an empty list clears it"`
}

var todoSchema = tool.MustSchema(todoArgs{})

// TodoList is the plan the todo tool maintains. It lives in memory for the
// run: the session store (WP0.7) is where it would persist, and until then
// the rendered list travels to the user in the tool's result text.
//
// Safe for concurrent use: WP0.11's parallel tool groups may run alongside
// a todo update.
type TodoList struct {
	mu    sync.Mutex
	items []TodoItem
}

// NewTodoList returns an empty list.
func NewTodoList() *TodoList { return &TodoList{} }

// Items returns a copy of the current plan.
func (l *TodoList) Items() []TodoItem {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]TodoItem(nil), l.items...)
}

// Set replaces the plan.
func (l *TodoList) Set(items []TodoItem) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append([]TodoItem(nil), items...)
}

// Render formats the plan as a checklist.
func (l *TodoList) Render() string {
	items := l.Items()
	if len(items) == 0 {
		return "Todo list is empty"
	}
	done := 0
	for _, it := range items {
		if it.Status == TodoCompleted {
			done++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Todo list (%d/%d completed):\n", done, len(items))
	for _, it := range items {
		mark := "[ ]"
		switch it.Status {
		case TodoInProgress:
			mark = "[>]"
		case TodoCompleted:
			mark = "[x]"
		case TodoPending:
		}
		fmt.Fprintf(&b, "%s %s\n", mark, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

type todo struct {
	list *TodoList
}

func newTodo(o options) agentapi.Tool {
	return tool.WithLimits(&todo{list: o.todos}, o.limits)
}

func (*todo) Name() string                 { return todoName }
func (*todo) Description() string          { return todoDescription }
func (*todo) InputSchema() json.RawMessage { return todoSchema }
func (*todo) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *todo) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in todoArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, todoName, err), nil
	}
	for i, it := range in.Items {
		if strings.TrimSpace(it.Content) == "" {
			return tool.InvalidArgs(callID, todoName, fmt.Errorf("items[%d]: content is empty", i)), nil
		}
		if it.Status == "" {
			in.Items[i].Status = TodoPending
		} else if !it.Status.Valid() {
			return tool.InvalidArgs(callID, todoName,
				fmt.Errorf("items[%d]: status %q is not one of pending, in_progress, completed", i, it.Status)), nil
		}
	}
	t.list.Set(in.Items)
	return agentapi.TextResult(callID, todoName, t.list.Render()), nil
}
