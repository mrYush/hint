package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrYush/hint/internal/rpc"
)

// promptsCommand lists MCP prompts without starting servers at REPL startup.
func (a *app) promptsCommand(ctx context.Context) {
	prompts, err := a.client.Prompts(ctx)
	if err != nil {
		fmt.Fprintln(a.io.err, "hint:", err)
		return
	}
	if len(prompts) == 0 {
		fmt.Fprintln(a.io.err, "hint: no MCP prompts are available")
		return
	}
	for _, prompt := range prompts {
		fmt.Fprint(a.io.err, prompt.Command())
		if prompt.Description != "" {
			fmt.Fprint(a.io.err, " — "+prompt.Description)
		}
		if len(prompt.Arguments) > 0 {
			args := make([]string, len(prompt.Arguments))
			for i, arg := range prompt.Arguments {
				args[i] = arg.Name
				if arg.Required {
					args[i] += " (required)"
				}
			}
			fmt.Fprint(a.io.err, "\n  arguments: "+strings.Join(args, ", "))
		}
		fmt.Fprintln(a.io.err)
	}
}

// expandPrompt turns a dynamic slash command into the text of a user turn.
func (a *app) expandPrompt(ctx context.Context, command string, args []string) (string, error) {
	server, prompt, ok := rpc.ParsePromptCommand(command)
	if !ok {
		return "", fmt.Errorf("not a prompt command")
	}
	return a.client.ExpandPrompt(ctx, rpc.PromptRequest{Server: server, Name: prompt, Arguments: args})
}
