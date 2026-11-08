package rpc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestClientPromptOperations(t *testing.T) {
	client, engine := dialFake(t, &Server{})
	engine.prompts = []PromptInfo{{
		Server: "docs", Name: "review", Description: "Review a file.",
		Arguments: []PromptArgument{{Name: "path", Required: true}},
	}}
	ctx := context.Background()
	servers, err := client.PromptServers(ctx)
	if err != nil || !reflect.DeepEqual(servers, []string{"docs"}) {
		t.Errorf("PromptServers = %v, %v", servers, err)
	}
	prompts, err := client.Prompts(ctx)
	if err != nil || !reflect.DeepEqual(prompts, engine.prompts) {
		t.Errorf("Prompts = %+v, %v", prompts, err)
	}
	text, err := client.ExpandPrompt(ctx, PromptRequest{Server: "docs", Name: "review", Arguments: []string{"main.go"}})
	if err != nil || text != "Review main.go" {
		t.Errorf("ExpandPrompt = %q, %v", text, err)
	}
	engine.promptHasMore = true
	values, hasMore, err := client.CompletePrompt(ctx, PromptCompleteRequest{
		Server: "docs", Prompt: "review", Argument: "path", Value: "ma", Arguments: map[string]string{"repo": "hint"},
	})
	if err != nil || !hasMore || !reflect.DeepEqual(values, []string{"main.go", "model.go"}) {
		t.Errorf("CompletePrompt = %v hasMore=%v, %v", values, hasMore, err)
	}
	if !reflect.DeepEqual(engine.promptRequest.Arguments, []string{"main.go"}) {
		t.Errorf("request seen by engine = %+v", engine.promptRequest)
	}
	if !reflect.DeepEqual(engine.promptComplete.Arguments, map[string]string{"repo": "hint"}) {
		t.Errorf("completion context = %+v", engine.promptComplete)
	}
}

// TestPromptRequestsDoNotBlockTheTurn pins why prompt.list, prompt.get
// and prompt.complete run beside the connection loop: a permission
// answer still gets through while the MCP call is waiting.
func TestPromptRequestsDoNotBlockTheTurn(t *testing.T) {
	for _, method := range []string{"list", "get", "complete"} {
		t.Run(method, func(t *testing.T) {
			c, engine := dialFake(t, &Server{PermissionWait: 5 * time.Second})
			engine.promptGate = make(chan struct{})
			ctx := context.Background()
			view, err := c.OpenSession(ctx, OpenOptions{})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := c.StartTurn(ctx, view.ID, "go")
			if err != nil {
				t.Fatal(err)
			}
			ev := <-turn.Events()
			for ev.Kind != agentapi.EventPermission {
				ev = <-turn.Events()
			}
			done := make(chan error, 1)
			go func() {
				var err error
				switch method {
				case "list":
					_, err = c.Prompts(ctx)
				case "get":
					_, err = c.ExpandPrompt(ctx, PromptRequest{Server: "docs", Name: "review"})
				default:
					_, _, err = c.CompletePrompt(ctx, PromptCompleteRequest{Server: "docs", Prompt: "review", Argument: "path"})
				}
				done <- err
			}()
			time.Sleep(50 * time.Millisecond)
			replied := make(chan error, 1)
			go func() { replied <- turn.Reply(ctx, ev.Permission.CallID, permission.Allow) }()
			select {
			case err := <-replied:
				if err != nil {
					t.Fatalf("Reply during %s: %v", method, err)
				}
			case <-time.After(time.Second):
				t.Fatalf("permission.reply waited behind prompt.%s", method)
			}
			close(engine.promptGate)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("the prompt request did not finish")
			}
			for range turn.Events() {
			}
		})
	}
}

func TestClientPromptErrors(t *testing.T) {
	client, _ := dialFake(t, &Server{})
	ctx := context.Background()
	tests := []struct {
		name string
		call func() error
		want int
	}{
		{name: "get params", call: func() error { _, err := client.ExpandPrompt(ctx, PromptRequest{}); return err }, want: CodeInvalidParams},
		{name: "complete params", call: func() error { _, _, err := client.CompletePrompt(ctx, PromptCompleteRequest{}); return err }, want: CodeInvalidParams},
		{name: "get missing", call: func() error {
			_, err := client.ExpandPrompt(ctx, PromptRequest{Server: "docs", Name: "missing"})
			return err
		}, want: CodeNotFound},
		{name: "complete missing", call: func() error {
			_, _, err := client.CompletePrompt(ctx, PromptCompleteRequest{Server: "docs", Prompt: "missing", Argument: "path"})
			return err
		}, want: CodeNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			var rpcErr *Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != tt.want {
				t.Errorf("error = %v, want code %d", err, tt.want)
			}
		})
	}
}
