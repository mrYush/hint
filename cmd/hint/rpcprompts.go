package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/internal/rpc"
)

// PromptServers lists enabled MCP server names without connecting to them.
func (e *rpcEngine) PromptServers(context.Context) ([]string, error) {
	servers := e.mcp.Servers()
	out := make([]string, 0, len(servers))
	for _, server := range servers {
		if server.Enabled {
			out = append(out, server.Name)
		}
	}
	return out, nil
}

// Prompts lists MCP prompts on demand, preserving lazy server startup.
func (e *rpcEngine) Prompts(ctx context.Context) ([]rpc.PromptInfo, error) {
	prompts, err := e.mcp.Prompts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]rpc.PromptInfo, 0, len(prompts))
	for _, prompt := range prompts {
		out = append(out, rpcPrompt(prompt))
	}
	return out, nil
}

func rpcPrompt(prompt mcp.Prompt) rpc.PromptInfo {
	out := rpc.PromptInfo{Server: prompt.Server, Name: prompt.Name, Description: prompt.Description}
	for _, arg := range prompt.Arguments {
		out.Arguments = append(out.Arguments, rpc.PromptArgument{Name: arg.Name, Description: arg.Description, Required: arg.Required})
	}
	return out
}

// ExpandPrompt expands an MCP prompt as text for a user turn.
func (e *rpcEngine) ExpandPrompt(ctx context.Context, req rpc.PromptRequest) (string, error) {
	text, err := e.mcp.ExpandPrompt(ctx, req.Server, req.Name, req.Arguments)
	return text, promptError(err)
}

// CompletePrompt asks an MCP server to complete one prompt argument.
func (e *rpcEngine) CompletePrompt(ctx context.Context, req rpc.PromptCompleteRequest) ([]string, bool, error) {
	values, hasMore, err := e.mcp.CompletePrompt(ctx, req.Server, req.Prompt, req.Argument, req.Value, req.Arguments)
	return values, hasMore, promptError(err)
}

func promptError(err error) error {
	if errors.Is(err, mcp.ErrNotFound) {
		return fmt.Errorf("%w: %w", rpc.ErrNotFound, err)
	}
	return err
}
