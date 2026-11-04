package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeTool is one tool of an in-memory MCP server.
type fakeTool struct {
	name        string
	description string
	schema      any
	handler     func(context.Context, json.RawMessage) (*sdk.CallToolResult, error)
}

// newFakeServer builds an MCP server over the SDK's in-memory transport.
func newFakeServer(t *testing.T, tools ...fakeTool) *sdk.Server {
	t.Helper()
	s := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, nil)
	for _, tool := range tools {
		tool := tool
		s.AddTool(
			&sdk.Tool{Name: tool.name, Description: tool.description, InputSchema: tool.schema},
			func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
				return tool.handler(ctx, req.Params.Arguments)
			},
		)
	}
	return s
}

func objectSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func textResult(text string) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}
}

// memoryDial is a DialFunc that connects to one in-memory server. Every
// call to dial creates a fresh session, so a restart is observable.
type memoryDial struct {
	mu       sync.Mutex
	server   *sdk.Server
	sessions []*sdk.ServerSession
	dials    int
}

func newMemoryDial(server *sdk.Server) *memoryDial {
	return &memoryDial{server: server}
}

func (d *memoryDial) dial(ctx context.Context, _ ServerConfig) (*sdk.ClientSession, error) {
	d.mu.Lock()
	d.dials++
	d.mu.Unlock()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	ss, err := d.server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.sessions = append(d.sessions, ss)
	d.mu.Unlock()
	go func() {
		_ = ss.Wait()
		_ = ss.Close()
	}()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"},
		&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
	return client.Connect(ctx, clientTransport, nil)
}

// kill closes every server session, as a crashed child would.
func (d *memoryDial) kill() {
	d.mu.Lock()
	sessions := append([]*sdk.ServerSession(nil), d.sessions...)
	d.mu.Unlock()
	for _, ss := range sessions {
		_ = ss.Close()
	}
}

func (d *memoryDial) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// testManager builds a manager around one in-memory server.
func testManager(t *testing.T, tools []fakeTool, cfg ServerConfig, opts Options) (*Manager, *memoryDial) {
	t.Helper()
	d := newMemoryDial(newFakeServer(t, tools...))
	cfg.Name = "fake"
	if cfg.Transport == "" {
		cfg.Transport = TransportStdio
		cfg.Command = "unused"
	}
	if opts.Dial == nil {
		opts.Dial = d.dial
	}
	m, err := NewManager([]ServerConfig{cfg}, opts)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, d
}
