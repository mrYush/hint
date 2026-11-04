package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestManagerStatusRunning(t *testing.T) {
	m, _ := testManager(t, []fakeTool{echoTool()}, ServerConfig{Permission: agentapi.ClassRead}, Options{})
	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %+v", statuses)
	}
	st := statuses[0]
	if st.State != StateRunning || st.Protocol == "" || st.ServerName == "" {
		t.Errorf("status = %+v; want a running server with protocol and name", st)
	}
	if st.Tools != 1 || st.Bytes <= 0 {
		t.Errorf("tools/bytes = %d/%d", st.Tools, st.Bytes)
	}
	if st.Health == "" || strings.HasPrefix(st.Health, "error:") {
		t.Errorf("health = %q; want a successful check", st.Health)
	}
}

func TestManagerStatusDisabled(t *testing.T) {
	m, err := NewManager([]ServerConfig{{
		Name: "proj", Transport: TransportStdio, Command: "unused", Project: true,
	}}, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != StateDisabled || statuses[0].Reason == "" {
		t.Errorf("statuses = %+v; want disabled with a reason", statuses)
	}
}

func TestManagerStatusFailed(t *testing.T) {
	m, err := NewManager([]ServerConfig{{
		Name: "down", Transport: TransportStdio, Command: "unused",
	}}, Options{
		Dial: func(context.Context, ServerConfig) (*sdk.ClientSession, error) {
			return nil, errors.New("no server")
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != StateFailed || !strings.Contains(statuses[0].Reason, "no server") {
		t.Errorf("statuses = %+v; want failed with the dial error", statuses)
	}
}

func TestManagerStatusHidden(t *testing.T) {
	m, _ := testManager(t, []fakeTool{echoTool()}, ServerConfig{}, Options{
		Budget: Budget{Total: 1 << 20, PerServer: 1},
	})
	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != StateHidden || !strings.Contains(statuses[0].Reason, "per-server budget") {
		t.Errorf("statuses = %+v; want hidden with the budget reason", statuses)
	}
}

func TestManagerStatusPing(t *testing.T) {
	conn := newHangingConn()
	conn.toolsReply = true
	m, err := NewManager([]ServerConfig{{Name: "old", Transport: TransportStdio, Command: "unused"}}, Options{
		Dial: func(ctx context.Context, _ ServerConfig) (*sdk.ClientSession, error) {
			client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"},
				&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
			return client.Connect(ctx, hangingTransport{conn: conn}, nil)
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Health != "ping" {
		t.Fatalf("statuses = %+v; want health ping", statuses)
	}
	if got := conn.pings.Load(); got != 1 {
		t.Errorf("ping calls = %d, want 1", got)
	}
}
