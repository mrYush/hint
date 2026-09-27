package builtin_test

import (
	"reflect"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestTodo_SetsAndRenders(t *testing.T) {
	list := builtin.NewTodoList()
	tl := builtin.NewTodo(builtin.WithTodoList(list))
	if tl.Class() != agentapi.ClassRead {
		t.Fatalf("class = %s", tl.Class())
	}

	got := ok(t, run(t, tl, `{"items":[
		{"content":"Read the config","status":"completed"},
		{"content":"Write the parser","status":"in_progress"},
		{"content":"Add tests"}
	]}`))
	want := "Todo list (1/3 completed):\n[x] Read the config\n[>] Write the parser\n[ ] Add tests"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	items := list.Items()
	wantItems := []builtin.TodoItem{
		{Content: "Read the config", Status: builtin.TodoCompleted},
		{Content: "Write the parser", Status: builtin.TodoInProgress},
		{Content: "Add tests", Status: builtin.TodoPending},
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Fatalf("stored %+v", items)
	}

	if got := ok(t, run(t, tl, `{"items":[]}`)); got != "Todo list is empty" {
		t.Fatalf("clear: %q", got)
	}
	if len(list.Items()) != 0 {
		t.Fatal("list not cleared")
	}
}

func TestTodo_Errors(t *testing.T) {
	tl := builtin.NewTodo()
	failed(t, run(t, tl, `{"items":[{"content":"","status":"pending"}]}`), "items[0]: content is empty")
	failed(t, run(t, tl, `{"items":[{"content":"x","status":"done"}]}`), `status "done" is not one of`)
	failed(t, run(t, tl, `{"items":"nope"}`), "invalid arguments")
}
