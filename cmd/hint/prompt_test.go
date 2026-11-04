package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestREPLListsAndExpandsMCPPrompt(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "reviewed" }}
	as, sio, _, stderr := testAssembly(t, chat, "/prompts\n/fake:review \"the auth flow\"\n/exit\n")
	server := newMCPTestServer(t)
	server.AddPrompt(&sdk.Prompt{
		Name: "review", Description: "Review a file.",
		Arguments: []*sdk.PromptArgument{{Name: "path", Required: true}},
	}, func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{{
			Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "Review " + req.Params.Arguments["path"]},
		}}}, nil
	})
	as.mcpDial = func(ctx context.Context, _ mcp.ServerConfig) (*sdk.ClientSession, error) {
		return connectMCPTestClient(ctx, server)
	}
	as.cfg.MCPServers = []config.MCPServer{{Name: "fake", Transport: config.MCPTransportStdio, Command: "unused"}}
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.repl(context.Background(), make(chan os.Signal)); err != nil {
		t.Fatalf("repl: %v", err)
	}
	if !strings.Contains(stderr.String(), "/fake:review") || !strings.Contains(stderr.String(), "path (required)") {
		t.Errorf("prompt list = %q", stderr.String())
	}
	reqs := chat.seen()
	if len(reqs) == 0 {
		t.Fatal("the expansion did not start a turn")
	}
	messages := reqs[len(reqs)-1].Messages
	found := false
	for _, msg := range messages {
		if msg.Role == agentapi.RoleUser && msg.Text() == "Review the auth flow" {
			found = true
		}
		if msg.Role == agentapi.RoleSystem && strings.Contains(msg.Text(), "Review the auth flow") {
			t.Error("prompt expansion entered the system prompt")
		}
	}
	if !found {
		t.Errorf("expanded user message not found: %+v", messages)
	}
}

func TestREPLCtrlCDuringPromptExpansionDoesNotCancelNextTurn(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "ok" }}
	as, sio, _, stderr := testAssembly(t, chat, "/fake:review x\nhello\n/exit\n")
	server := newMCPTestServer(t)
	started := make(chan struct{})
	var once sync.Once
	server.AddPrompt(&sdk.Prompt{
		Name: "review", Arguments: []*sdk.PromptArgument{{Name: "path", Required: true}},
	}, func(ctx context.Context, _ *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	})
	as.mcpDial = func(ctx context.Context, _ mcp.ServerConfig) (*sdk.ClientSession, error) {
		return connectMCPTestClient(ctx, server)
	}
	as.cfg.MCPServers = []config.MCPServer{{Name: "fake", Transport: config.MCPTransportStdio, Command: "unused"}}
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	sigs := make(chan os.Signal, 1)
	go func() {
		select {
		case <-started:
			sigs <- os.Interrupt
		case <-time.After(5 * time.Second):
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.repl(ctx, sigs); err != nil {
		t.Fatalf("repl: %v", err)
	}
	if !strings.Contains(stderr.String(), "prompt expansion stopped") {
		t.Errorf("stderr = %q", stderr.String())
	}
	var sawHello bool
	for _, req := range chat.seen() {
		for _, msg := range req.Messages {
			if msg.Role == agentapi.RoleUser && msg.Text() == "hello" {
				sawHello = true
			}
		}
	}
	if !sawHello {
		t.Fatalf("the question after Ctrl-C did not run: %+v", chat.seen())
	}
}
