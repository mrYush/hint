package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromptArgument is one positional argument advertised by an MCP prompt.
type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

// Prompt is a server prompt exposed as a slash command.
type Prompt struct {
	Server      string
	Name        string
	Description string
	Arguments   []PromptArgument
}

// Command returns the TUI and REPL spelling of the prompt.
func (p Prompt) Command() string { return "/" + p.Server + ":" + p.Name }

// Prompts lists the prompts of every enabled server. A server without the
// prompts capability contributes none; an unavailable server is a warning.
func (m *Manager) Prompts(ctx context.Context) ([]Prompt, error) {
	var out []Prompt
	for _, srv := range m.servers {
		if !srv.enabled {
			continue
		}
		prompts, err := srv.listPrompts(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			m.warnf("MCP server %q could not list prompts: %v", srv.cfg.Name, err)
			continue
		}
		out = append(out, prompts...)
	}
	return out, nil
}

// ExpandPrompt validates positional arguments against the prompt's advertised
// list and expands the result as text for one user turn.
func (m *Manager) ExpandPrompt(ctx context.Context, serverName, name string, args []string) (string, error) {
	srv, err := m.promptServer(serverName)
	if err != nil {
		return "", err
	}
	prompts, err := srv.listPrompts(ctx)
	if err != nil {
		return "", fmt.Errorf("mcp: server %q: listing prompt %q: %w", serverName, name, err)
	}
	var descriptor *Prompt
	for i := range prompts {
		if prompts[i].Name == name {
			descriptor = &prompts[i]
			break
		}
	}
	if descriptor == nil {
		return "", fmt.Errorf("mcp: server %q has no prompt %q", serverName, name)
	}
	if len(args) > len(descriptor.Arguments) {
		return "", fmt.Errorf("mcp: prompt %q takes at most %d arguments; too many were given", name, len(descriptor.Arguments))
	}
	values := make(map[string]string, len(args))
	for i, arg := range descriptor.Arguments {
		if i < len(args) {
			values[arg.Name] = args[i]
			continue
		}
		if arg.Required {
			return "", fmt.Errorf("mcp: prompt %q argument %q is required", name, arg.Name)
		}
	}
	res, err := srv.getPrompt(ctx, name, values)
	if err != nil {
		return "", fmt.Errorf("mcp: server %q: expanding prompt %q: %w", serverName, name, err)
	}
	if res == nil {
		return "", fmt.Errorf("mcp: server %q: prompt %q returned no result", serverName, name)
	}
	parts := make([]string, 0, len(res.Messages))
	for _, message := range res.Messages {
		if message == nil {
			continue
		}
		switch content := message.Content.(type) {
		case *sdk.TextContent:
			parts = append(parts, content.Text)
		case *sdk.EmbeddedResource:
			parts = append(parts, embeddedText(content))
		case *sdk.ResourceLink:
			parts = append(parts, resourceLinkText(content))
		case *sdk.ImageContent:
			return "", fmt.Errorf("mcp: prompt %q returned an image (%s), which hint cannot show yet", name, content.MIMEType)
		case *sdk.AudioContent:
			return "", fmt.Errorf("mcp: prompt %q returned audio (%s), which hint cannot play yet", name, content.MIMEType)
		default:
			return "", fmt.Errorf("mcp: prompt %q returned unsupported content %T", name, message.Content)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// CompletePrompt asks a server for values of one prompt argument.
func (m *Manager) CompletePrompt(ctx context.Context, serverName, prompt, argument, value string) ([]string, error) {
	srv, err := m.promptServer(serverName)
	if err != nil {
		return nil, err
	}
	res, err := srv.completePrompt(ctx, prompt, argument, value)
	if err != nil {
		return nil, fmt.Errorf("mcp: server %q: completing prompt %q argument %q: %w", serverName, prompt, argument, err)
	}
	if res == nil {
		return nil, nil
	}
	return append([]string(nil), res.Completion.Values...), nil
}

func (m *Manager) promptServer(name string) (*server, error) {
	for _, srv := range m.servers {
		if srv.cfg.Name == name {
			if !srv.enabled {
				return nil, fmt.Errorf("mcp: server %q is disabled: %s", name, srv.reason)
			}
			return srv, nil
		}
	}
	return nil, fmt.Errorf("mcp: unknown server %q", name)
}

func (s *server) listPrompts(ctx context.Context) ([]Prompt, error) {
	s.mu.Lock()
	if err := s.connectLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	sess := s.sess
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	var out []Prompt
	for remote, err := range sess.Prompts(ctx, nil) {
		if err != nil {
			if isConnectionClosed(err) {
				s.dropIf(sess)
			}
			if isMethodNotFound(err) {
				return []Prompt{}, nil
			}
			return nil, fmt.Errorf("listing prompts: %w", err)
		}
		if remote == nil {
			continue
		}
		p := Prompt{Server: s.cfg.Name, Name: remote.Name, Description: remote.Description}
		for _, arg := range remote.Arguments {
			if arg != nil {
				p.Arguments = append(p.Arguments, PromptArgument{Name: arg.Name, Description: arg.Description, Required: arg.Required})
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *server) getPrompt(ctx context.Context, name string, args map[string]string) (*sdk.GetPromptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	res, sess, err := s.getPromptOnce(ctx, name, args)
	if err == nil || !isConnectionClosed(err) || ctx.Err() != nil {
		return res, err
	}
	s.dropIf(sess)
	res, _, err = s.getPromptOnce(ctx, name, args)
	return res, err
}

func (s *server) getPromptOnce(ctx context.Context, name string, args map[string]string) (*sdk.GetPromptResult, *sdk.ClientSession, error) {
	s.mu.Lock()
	if err := s.connectLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return nil, nil, errors.New("mcp: prompt server is not connected")
	}
	res, err := sess.GetPrompt(ctx, &sdk.GetPromptParams{Name: name, Arguments: args})
	return res, sess, err
}

func (s *server) completePrompt(ctx context.Context, prompt, argument, value string) (*sdk.CompleteResult, error) {
	s.mu.Lock()
	if err := s.connectLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	sess := s.sess
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	return sess.Complete(ctx, &sdk.CompleteParams{
		Ref:      &sdk.CompleteReference{Type: "ref/prompt", Name: prompt},
		Argument: sdk.CompleteParamsArgument{Name: argument, Value: value},
	})
}
