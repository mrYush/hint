package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

func echoTool() fakeTool {
	return fakeTool{
		name:        "echo",
		description: "Return the text it was given.",
		schema:      objectSchema(),
		handler: func(_ context.Context, args json.RawMessage) (*sdk.CallToolResult, error) {
			return textResult(string(args)), nil
		},
	}
}

func TestManagerListsAndCallsTools(t *testing.T) {
	ctx := context.Background()
	fail := fakeTool{
		name:        "fail",
		description: "Always fails.",
		schema:      objectSchema(),
		handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{
				Content: []sdk.Content{&sdk.TextContent{Text: "boom"}},
				IsError: true,
			}, nil
		},
	}
	m, _ := testManager(t, []fakeTool{echoTool(), fail}, ServerConfig{Permission: agentapi.ClassRead}, Options{})
	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	if tools[0].Name() != "fake.echo" || tools[1].Name() != "fake.fail" {
		t.Errorf("names = %q, %q", tools[0].Name(), tools[1].Name())
	}
	if tools[0].Class() != agentapi.ClassRead {
		t.Errorf("class = %q, want read", tools[0].Class())
	}
	if err := agentapi.SchemaOf(tools[0]).Validate(); err != nil {
		t.Errorf("schema: %v", err)
	}
	res, err := tools[0].Run(ctx, "call-1", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Run echo: %v", err)
	}
	if res.IsError || res.Text() != `{"text":"hi"}` {
		t.Errorf("echo result = %+v", res)
	}
	res, err = tools[1].Run(ctx, "call-2", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run fail: %v", err)
	}
	if !res.IsError || res.Text() != "boom" {
		t.Errorf("fail result = %+v", res)
	}
}

func TestManagerAllowAndDeny(t *testing.T) {
	ctx := context.Background()
	tools := []fakeTool{echoTool(), {name: "other", description: "Other.", schema: objectSchema(), handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) { return textResult("other"), nil }}}

	t.Run("allow", func(t *testing.T) {
		m, _ := testManager(t, tools, ServerConfig{Tools: ToolFilter{Allow: []string{"other"}}}, Options{})
		got, err := m.Tools(ctx)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if len(got) != 1 || got[0].Name() != "fake.other" {
			t.Errorf("tools = %v", toolNames(got))
		}
	})
	t.Run("deny", func(t *testing.T) {
		m, _ := testManager(t, tools, ServerConfig{Tools: ToolFilter{Deny: []string{"other"}}}, Options{})
		got, err := m.Tools(ctx)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if len(got) != 1 || got[0].Name() != "fake.echo" {
			t.Errorf("tools = %v", toolNames(got))
		}
	})
}

func TestManagerBudgetHidesWholeServers(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("d", 300)
	tool := fakeTool{
		name: "one", description: long, schema: objectSchema(),
		handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) { return textResult("one"), nil },
	}
	server := newFakeServer(t, tool)
	cfgs := []ServerConfig{
		{Name: "a", Transport: TransportStdio, Command: "unused"},
		{Name: "b", Transport: TransportStdio, Command: "unused"},
	}
	m, err := NewManager(cfgs, Options{
		Dial:   newMemoryDial(server).dial,
		Budget: Budget{Total: 1 << 20, PerServer: 1 << 20},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	all, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("tools = %v; want both servers", toolNames(all))
	}
	oneSize := schemaSize(all[0])

	// A total that fits exactly one server hides the second.
	total, err := NewManager(cfgs, Options{
		Dial:   newMemoryDial(server).dial,
		Budget: Budget{Total: oneSize, PerServer: 1 << 20},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = total.Close() })
	got, err := total.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("tools = %v; want only the first server", toolNames(got))
	}
	if !hasWarning(total, "over the "+strconv.Itoa(oneSize)+"-byte budget") {
		t.Errorf("warnings = %v; want the total-budget warning", total.Warnings())
	}

	// A per-server cap smaller than one server hides both.
	capped, err := NewManager(cfgs, Options{
		Dial:   newMemoryDial(server).dial,
		Budget: Budget{Total: 1 << 20, PerServer: oneSize - 1},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = capped.Close() })
	got, err = capped.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tools = %v; want none", toolNames(got))
	}
	if !hasWarning(capped, "per-server budget") {
		t.Errorf("warnings = %v; want the per-server warning", capped.Warnings())
	}
}

func TestManagerSchemaFidelity(t *testing.T) {
	schemaWith := func(number any) map[string]any {
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number", "default": number},
			},
		}
	}
	tests := []struct {
		name   string
		number any
		ok     bool
	}{
		{"safe integer", int64(9007199254740991), true},
		{"fraction", 0.5, true},
		{"exact 2^53", int64(9007199254740992), false},
		{"above 2^53", int64(9007199254740993), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := testManager(t, []fakeTool{{
				name: "number", description: "A number.", schema: schemaWith(tt.number),
				handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) { return textResult("ok"), nil },
			}}, ServerConfig{}, Options{})
			got, err := m.Tools(context.Background())
			if err != nil {
				t.Fatalf("Tools: %v", err)
			}
			if tt.ok {
				if len(got) != 1 {
					t.Fatalf("tools = %v; want one", toolNames(got))
				}
			} else {
				if len(got) != 0 {
					t.Fatalf("tools = %v; want the unsafe tool hidden", toolNames(got))
				}
				if !hasWarning(m, "unsupported numeric precision") {
					t.Errorf("warnings = %v; want the precision rejection", m.Warnings())
				}
			}
		})
	}
}

func TestManagerDescriptionCap(t *testing.T) {
	m, _ := testManager(t, []fakeTool{{
		name: "long", description: strings.Repeat("x", 100), schema: objectSchema(),
		handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) { return textResult("ok"), nil },
	}}, ServerConfig{DescriptionMax: 20}, Options{})
	got, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("tools = %v", toolNames(got))
	}
	desc := got[0].Description()
	if len(desc) > 20 {
		t.Errorf("description length = %d, want <= 20", len(desc))
	}
	if !strings.HasSuffix(desc, "…") {
		t.Errorf("description = %q; want a truncated marker", desc)
	}
}

func TestManagerRestartRetriesReadsOnly(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name    string
		class   agentapi.ActionClass
		wantErr bool
	}{
		{"read retries", agentapi.ClassRead, false},
		{"execute does not retry", agentapi.ClassExecute, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, d := testManager(t, []fakeTool{echoTool()}, ServerConfig{Permission: tt.class}, Options{})
			tools, err := m.Tools(ctx)
			if err != nil {
				t.Fatalf("Tools: %v", err)
			}
			d.kill()
			res, err := tools[0].Run(ctx, "call-1", json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if tt.wantErr {
				if !res.IsError || !strings.Contains(res.Text(), "may have reached the server") {
					t.Errorf("result = %+v; want the ambiguity error", res)
				}
				return
			}
			if res.IsError {
				t.Errorf("result = %+v; want a retried success", res)
			}
			if d.count() != 2 {
				t.Errorf("dials = %d, want a reconnect", d.count())
			}
		})
	}
}

func TestManagerConnectBackoff(t *testing.T) {
	ctx := context.Background()
	dials := 0
	now := time.Unix(0, 0)
	m, err := NewManager([]ServerConfig{{Name: "s", Transport: TransportStdio, Command: "unused"}}, Options{
		Dial: func(context.Context, ServerConfig) (*sdk.ClientSession, error) {
			dials++
			return nil, errors.New("no server")
		},
		Now:     func() time.Time { return now },
		Backoff: func(int) time.Duration { return time.Minute },
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if _, err := m.Tools(ctx); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if _, err := m.Tools(ctx); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if dials != 1 {
		t.Errorf("dials = %d; want the second attempt held by backoff", dials)
	}
	if !hasWarning(m, "restart backoff") {
		t.Errorf("warnings = %v; want a backoff warning", m.Warnings())
	}
}

func TestManagerNoServers(t *testing.T) {
	m, err := NewManager(nil, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	got, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tools = %v", toolNames(got))
	}
}

func toolNames(tools []agentapi.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name()
	}
	return out
}

func hasWarning(m *Manager, substr string) bool {
	for _, w := range m.Warnings() {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestManagerWarnsOnSchemaReferences(t *testing.T) {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/value"}},
		"$defs":      map[string]any{"value": map[string]any{"type": "string"}},
	}
	m, _ := testManager(t, []fakeTool{{
		name: "ref", description: "Uses a reference.", schema: schema,
		handler: func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) { return textResult("ok"), nil },
	}}, ServerConfig{}, Options{})
	got, err := m.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("tools = %v; want the schema passed through", toolNames(got))
	}
	if !hasWarning(m, "$ref or $defs") {
		t.Errorf("warnings = %v; want the reference warning", m.Warnings())
	}
}

func TestManagerConnectTimeout(t *testing.T) {
	ctx := context.Background()
	m, err := NewManager([]ServerConfig{{Name: "slow", Transport: TransportStdio, Command: "unused"}}, Options{
		ConnectTimeout: 20 * time.Millisecond,
		Dial: func(ctx context.Context, _ ServerConfig) (*sdk.ClientSession, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	start := time.Now()
	got, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tools = %v; want none from a hung server", toolNames(got))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Tools took %s; want the connect timeout to bound it", elapsed)
	}
	if !hasWarning(m, "unavailable") {
		t.Errorf("warnings = %v; want the unavailable warning", m.Warnings())
	}
}

func TestManagerLegacyServer(t *testing.T) {
	// A server that speaks only the 2024-11-05 revision exercises the
	// SDK's initialize fallback; hint must not implement that handshake
	// itself, but it must still list and call the server's tools.
	ctx := context.Background()
	legacy := sdk.NewServer(
		&sdk.Implementation{Name: "legacy", Version: "v0"},
		&sdk.ServerOptions{SupportedProtocolVersions: []string{"2024-11-05"}},
	)
	tool := echoTool()
	legacy.AddTool(&sdk.Tool{Name: tool.name, Description: tool.description, InputSchema: tool.schema},
		func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return tool.handler(ctx, req.Params.Arguments)
		})
	d := newMemoryDial(legacy)
	m, err := NewManager([]ServerConfig{{Name: "old", Transport: TransportStdio, Command: "unused"}}, Options{Dial: d.dial})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "old.echo" {
		t.Fatalf("tools = %v", toolNames(tools))
	}
	res, err := tools[0].Run(ctx, "call-1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError || res.Text() != `{}` {
		t.Errorf("result = %+v", res)
	}
}
