package main

import (
	"context"

	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/internal/rpc"
)

// Prompts lists MCP prompts on demand, preserving lazy server startup.
func (e *rpcEngine) Prompts(ctx context.Context) ([]rpc.PromptInfo, error) {
	if e.mcp == nil {
		return []rpc.PromptInfo{}, nil
	}
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
	if e.mcp == nil {
		return "", nil
	}
	return e.mcp.ExpandPrompt(ctx, req.Server, req.Name, req.Arguments)
}

// CompletePrompt asks an MCP server to complete one prompt argument.
func (e *rpcEngine) CompletePrompt(ctx context.Context, req rpc.PromptCompleteRequest) ([]string, error) {
	if e.mcp == nil {
		return []string{}, nil
	}
	return e.mcp.CompletePrompt(ctx, req.Server, req.Prompt, req.Argument, req.Value)
}
