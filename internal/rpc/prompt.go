package rpc

import (
	"context"
	"strings"
)

// PromptArgument describes one positional MCP prompt argument.
type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// PromptInfo is one MCP prompt offered by the core.
type PromptInfo struct {
	Server      string           `json:"server"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// Command returns the slash-command spelling of the prompt.
func (p PromptInfo) Command() string { return "/" + p.Server + ":" + p.Name }

// ParsePromptCommand splits /server:prompt into its two identifiers.
func ParsePromptCommand(command string) (server, prompt string, ok bool) {
	name, found := strings.CutPrefix(command, "/")
	if !found {
		return "", "", false
	}
	server, prompt, found = strings.Cut(name, ":")
	return server, prompt, found && server != "" && prompt != ""
}

// PromptRequest names a prompt and its positional argument values.
type PromptRequest struct {
	Server    string   `json:"server"`
	Name      string   `json:"name"`
	Arguments []string `json:"arguments,omitempty"`
}

// PromptCompleteRequest asks for completions of one prompt argument.
type PromptCompleteRequest struct {
	Server   string `json:"server"`
	Prompt   string `json:"prompt"`
	Argument string `json:"argument"`
	Value    string `json:"value"`
}

// Prompts is prompt.list.
func (c *Client) Prompts(ctx context.Context) ([]PromptInfo, error) {
	var res promptsResult
	if err := c.call(ctx, "prompt.list", nil, &res); err != nil {
		return nil, err
	}
	return res.Prompts, nil
}

// ExpandPrompt is prompt.get.
func (c *Client) ExpandPrompt(ctx context.Context, req PromptRequest) (string, error) {
	var res promptTextResult
	if err := c.call(ctx, "prompt.get", req, &res); err != nil {
		return "", err
	}
	return res.Text, nil
}

// CompletePrompt is prompt.complete.
func (c *Client) CompletePrompt(ctx context.Context, req PromptCompleteRequest) ([]string, error) {
	var res promptCompletionResult
	if err := c.call(ctx, "prompt.complete", req, &res); err != nil {
		return nil, err
	}
	return res.Values, nil
}

func (c *conn) promptList(req request) {
	prompts, err := c.srv.Engine.Prompts(c.ctx)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	if prompts == nil {
		prompts = []PromptInfo{}
	}
	_ = c.writeResult(req.ID, promptsResult{Prompts: prompts})
}

func (c *conn) promptGet(req request) {
	var p PromptRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.Server == "" || p.Name == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "server and name are required"))
		return
	}
	text, err := c.srv.Engine.ExpandPrompt(c.ctx, p)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	_ = c.writeResult(req.ID, promptTextResult{Text: text})
}

func (c *conn) promptComplete(req request) {
	var p PromptCompleteRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.Server == "" || p.Prompt == "" || p.Argument == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "server, prompt and argument are required"))
		return
	}
	values, err := c.srv.Engine.CompletePrompt(c.ctx, p)
	if err != nil {
		_ = c.writeError(req.ID, errCode(CodeInternal, "%v", err))
		return
	}
	if values == nil {
		values = []string{}
	}
	_ = c.writeResult(req.ID, promptCompletionResult{Values: values})
}
