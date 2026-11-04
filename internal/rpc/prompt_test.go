package rpc

import (
	"context"
	"reflect"
	"testing"
)

func TestClientPromptOperations(t *testing.T) {
	client, engine := dialFake(t, &Server{})
	engine.prompts = []PromptInfo{{
		Server: "docs", Name: "review", Description: "Review a file.",
		Arguments: []PromptArgument{{Name: "path", Required: true}},
	}}
	ctx := context.Background()
	prompts, err := client.Prompts(ctx)
	if err != nil || !reflect.DeepEqual(prompts, engine.prompts) {
		t.Errorf("Prompts = %+v, %v", prompts, err)
	}
	text, err := client.ExpandPrompt(ctx, PromptRequest{Server: "docs", Name: "review", Arguments: []string{"main.go"}})
	if err != nil || text != "Review main.go" {
		t.Errorf("ExpandPrompt = %q, %v", text, err)
	}
	values, err := client.CompletePrompt(ctx, PromptCompleteRequest{Server: "docs", Prompt: "review", Argument: "path", Value: "ma"})
	if err != nil || !reflect.DeepEqual(values, []string{"main.go", "model.go"}) {
		t.Errorf("CompletePrompt = %v, %v", values, err)
	}
	if !reflect.DeepEqual(engine.promptRequest.Arguments, []string{"main.go"}) {
		t.Errorf("request seen by engine = %+v", engine.promptRequest)
	}
}
