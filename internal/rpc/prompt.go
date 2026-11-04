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
	Server    string            `json:"server"`
	Prompt    string            `json:"prompt"`
	Argument  string            `json:"argument"`
	Value     string            `json:"value"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// PromptServers is prompt.servers. It reads configuration without connecting.
func (c *Client) PromptServers(ctx context.Context) ([]string, error) {
	var res promptServersResult
	if err := c.call(ctx, "prompt.servers", nil, &res); err != nil {
		return nil, err
	}
	return res.Servers, nil
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

// CompletePrompt is prompt.complete. hasMore reports that the server had
// further values it did not return.
func (c *Client) CompletePrompt(ctx context.Context, req PromptCompleteRequest) (values []string, hasMore bool, err error) {
	var res promptCompletionResult
	if err := c.call(ctx, "prompt.complete", req, &res); err != nil {
		return nil, false, err
	}
	return res.Values, res.HasMore, nil
}

func (c *conn) promptServers(req request) {
	servers, err := c.srv.Engine.PromptServers(c.ctx)
	if err != nil {
		_ = c.writeError(req.ID, engineError(err))
		return
	}
	if servers == nil {
		servers = []string{}
	}
	_ = c.writeResult(req.ID, promptServersResult{Servers: servers})
}

// besideLoop runs an MCP prompt call off the connection's read loop.
// Prompts, expansion and completion wait on a server, and permission.reply
// and turn.cancel must not wait with them. The call ends when the
// connection does. Stopping the client does not cancel it: there is no
// prompt.cancel, so the server keeps working until the connection closes
// or the MCP timeout fires. prompt.servers stays on the loop: it only
// reads config.
func (c *conn) besideLoop(fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(c.ctx)
	go func() {
		defer cancel()
		fn(ctx)
	}()
}

func (c *conn) promptList(req request) {
	c.besideLoop(func(ctx context.Context) {
		prompts, err := c.srv.Engine.Prompts(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			_ = c.writeError(req.ID, engineError(err))
			return
		}
		if prompts == nil {
			prompts = []PromptInfo{}
		}
		_ = c.writeResult(req.ID, promptsResult{Prompts: prompts})
	})
}

func (c *conn) promptGet(req request) {
	var p PromptRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.Server == "" || p.Name == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "server and name are required"))
		return
	}
	c.besideLoop(func(ctx context.Context) {
		text, err := c.srv.Engine.ExpandPrompt(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			_ = c.writeError(req.ID, engineError(err))
			return
		}
		_ = c.writeResult(req.ID, promptTextResult{Text: text})
	})
}

func (c *conn) promptComplete(req request) {
	var p PromptCompleteRequest
	if err := unmarshalParams(req.Params, &p); err != nil || p.Server == "" || p.Prompt == "" || p.Argument == "" {
		_ = c.writeError(req.ID, errCode(CodeInvalidParams, "server, prompt and argument are required"))
		return
	}
	c.besideLoop(func(ctx context.Context) {
		values, hasMore, err := c.srv.Engine.CompletePrompt(ctx, p)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			_ = c.writeError(req.ID, engineError(err))
			return
		}
		if values == nil {
			values = []string{}
		}
		_ = c.writeResult(req.ID, promptCompletionResult{Values: values, HasMore: hasMore})
	})
}
