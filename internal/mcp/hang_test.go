package mcp

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// hangingConn is a minimal MCP connection that answers the handshake and
// then never answers tools/list. It lets the test prove that the list
// timeout, not the caller's context, bounds a hung server.
type hangingConn struct {
	replies chan jsonrpc.Message
	done    chan struct{}
	once    sync.Once
	lists   atomic.Int32
	pings   atomic.Int32
	// toolsReply answers tools/list with an empty list instead of hanging;
	// a status test uses it to reach the ping path.
	toolsReply bool
}

func newHangingConn() *hangingConn {
	return &hangingConn{
		replies: make(chan jsonrpc.Message, 8),
		done:    make(chan struct{}),
	}
}

func (c *hangingConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case msg := <-c.replies:
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *hangingConn) Write(_ context.Context, msg jsonrpc.Message) error {
	req, ok := msg.(*jsonrpc.Request)
	if !ok || !req.ID.IsValid() {
		return nil
	}
	switch req.Method {
	case "server/discover":
		// The client falls back to initialize on a non-modern error.
		c.enqueue(&jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{
			Code:    jsonrpc.CodeMethodNotFound,
			Message: "server/discover is not supported",
		}})
	case "initialize":
		c.enqueue(&jsonrpc.Response{ID: req.ID, Result: json.RawMessage(
			`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"hang","version":"v0"}}`,
		)})
	case "ping":
		c.pings.Add(1)
		c.enqueue(&jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{}`)})
	case "tools/list":
		c.lists.Add(1)
		if c.toolsReply {
			c.enqueue(&jsonrpc.Response{ID: req.ID, Result: json.RawMessage(`{"tools":[]}`)})
		}
		// Otherwise deliberately no response: the test is about the timeout.
	default:
		c.enqueue(&jsonrpc.Response{ID: req.ID, Error: &jsonrpc.Error{
			Code:    jsonrpc.CodeMethodNotFound,
			Message: "method not supported",
		}})
	}
	return nil
}

func (c *hangingConn) enqueue(msg jsonrpc.Message) {
	select {
	case c.replies <- msg:
	case <-c.done:
	}
}

func (c *hangingConn) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

func (c *hangingConn) SessionID() string { return "" }

type hangingTransport struct{ conn *hangingConn }

func (t hangingTransport) Connect(context.Context) (sdk.Connection, error) {
	return t.conn, nil
}

func TestManagerListTimeout(t *testing.T) {
	conn := newHangingConn()
	m, err := NewManager([]ServerConfig{{Name: "hang", Transport: TransportStdio, Command: "unused"}}, Options{
		ListTimeout: 20 * time.Millisecond,
		Dial: func(ctx context.Context, _ ServerConfig) (*sdk.ClientSession, error) {
			client := sdk.NewClient(
				&sdk.Implementation{Name: "test", Version: "v0"},
				&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}},
			)
			return client.Connect(ctx, hangingTransport{conn: conn}, nil)
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	start := time.Now()
	tools, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("tools = %v; want none from a hung list", toolNames(tools))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Tools took %s; want the list timeout to bound it", elapsed)
	}
	if got := conn.lists.Load(); got != 1 {
		t.Errorf("tools/list calls = %d, want 1", got)
	}
	if !hasWarning(m, "unavailable") {
		t.Errorf("warnings = %v; want the unavailable warning", m.Warnings())
	}
}
