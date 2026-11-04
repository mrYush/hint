package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestMCPServersConversion(t *testing.T) {
	cfg := &config.Config{MCPServers: []config.MCPServer{{
		Name:           "files",
		Transport:      config.MCPTransportStdio,
		Command:        "npx",
		Args:           []string{"server"},
		Env:            map[string]string{"TOKEN": "secret"},
		Permission:     "read",
		Timeout:        0,
		Tools:          config.MCPToolFilter{Allow: []string{"read_file"}},
		Resources:      []string{"test://rules"},
		DescriptionMax: 100,
		Project:        true,
	}}}
	got := mcpServers(cfg)
	if len(got) != 1 {
		t.Fatalf("servers = %+v", got)
	}
	s := got[0]
	if s.Name != "files" || s.Transport != mcp.TransportStdio || s.Command != "npx" {
		t.Errorf("server = %+v", s)
	}
	if s.Permission != agentapi.ClassRead || s.DescriptionMax != 100 || !s.Project {
		t.Errorf("permission/description_max/project = %q/%d/%v", s.Permission, s.DescriptionMax, s.Project)
	}
	if !reflect.DeepEqual(s.Tools.Allow, []string{"read_file"}) || s.Env["TOKEN"] != "secret" {
		t.Errorf("tools/env = %+v/%v", s.Tools, s.Env)
	}

	// The conversion copies the mutable fields, so a later config reload
	// or edit cannot change a running manager's server.
	cfg.MCPServers[0].Env["TOKEN"] = "changed"
	cfg.MCPServers[0].Args[0] = "changed"
	cfg.MCPServers[0].Resources[0] = "test://changed"
	if s.Env["TOKEN"] != "secret" || s.Args[0] != "server" || s.Resources[0] != "test://rules" {
		t.Errorf("conversion aliased the config: env=%v args=%v resources=%v", s.Env, s.Args, s.Resources)
	}
}

type registryTestTool struct{}

func (registryTestTool) Name() string                 { return "mcp_test.echo" }
func (registryTestTool) Description() string          { return "A test tool." }
func (registryTestTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (registryTestTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (registryTestTool) Run(context.Context, string, json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.ToolResult{}, nil
}

func TestToolRegistryIncludesMCPTools(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := toolRegistry(root, nil, nil, nil, registryTestTool{})
	if err != nil {
		t.Fatalf("toolRegistry: %v", err)
	}
	if _, ok := reg.Lookup("mcp_test.echo"); !ok {
		t.Fatalf("MCP tool not registered; names = %v", reg.Names())
	}
	if reg.Len() != 10 {
		t.Errorf("registry holds %d tools, want 10", reg.Len())
	}
}

func TestEngineMCPStartsOnFirstTurn(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "ok" }}
	as, _, _, _ := testAssembly(t, chat, "")
	server := newMCPTestServer(t)
	dials := 0
	as.mcpDial = func(ctx context.Context, _ mcp.ServerConfig) (*sdk.ClientSession, error) {
		dials++
		return connectMCPTestClient(ctx, server)
	}
	as.cfg.MCPServers = []config.MCPServer{{
		Name:       "fake",
		Transport:  config.MCPTransportStdio,
		Command:    "unused",
		Permission: "read",
	}}
	eng, err := newEngine(context.Background(), as)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	t.Cleanup(func() { eng.closeMCP() })
	if dials != 0 {
		t.Fatalf("newEngine connected %d MCP servers; want lazy start", dials)
	}
	servers, err := eng.PromptServers(context.Background())
	if err != nil || len(servers) != 1 || servers[0] != "fake" {
		t.Fatalf("PromptServers = %v, %v", servers, err)
	}
	if dials != 0 {
		t.Fatalf("PromptServers connected %d MCP servers; want configuration only", dials)
	}
	live := eng.newSession(nil, "test")
	if dials != 0 {
		t.Fatalf("opening a session connected %d MCP servers; want lazy start", dials)
	}
	ch, err := live.RunTurn(context.Background(), "hello", nil)
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	for range ch {
	}
	if dials != 1 {
		t.Fatalf("first turn connected %d MCP servers, want 1", dials)
	}
	reqs := chat.seen()
	if len(reqs) == 0 {
		t.Fatal("the provider was not called")
	}
	found := false
	for _, schema := range reqs[0].Tools {
		if schema.Name == "fake.echo" {
			found = true
		}
	}
	if !found {
		t.Errorf("tools = %+v; the MCP tool was not registered before the request", reqs[0].Tools)
	}

	// The second turn reuses the loaded tools and does not reconnect.
	ch, err = live.RunTurn(context.Background(), "again", nil)
	if err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}
	for range ch {
	}
	if dials != 1 {
		t.Errorf("second turn connected %d MCP servers, want 1", dials)
	}
}

func newMCPTestServer(t *testing.T) *sdk.Server {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "fake", Version: "v0"}, nil)
	server.AddTool(&sdk.Tool{
		Name:        "echo",
		Description: "Echo.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "ok"}}}, nil
	})
	return server
}

func connectMCPTestClient(ctx context.Context, server *sdk.Server) (*sdk.ClientSession, error) {
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	go func() {
		_ = serverSession.Wait()
		_ = serverSession.Close()
	}()
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "v0"},
		&sdk.ClientOptions{Capabilities: &sdk.ClientCapabilities{}})
	return client.Connect(ctx, clientTransport, nil)
}

func TestMCPToolNameCollision(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Two MCP sources (or a duplicate tool list) that resolve to the same
	// <server>.<tool> name must fail registration; last-wins would silently
	// change which server the model calls.
	_, err = toolRegistry(root, nil, nil, nil, registryTestTool{}, registryTestTool{})
	if err == nil {
		t.Fatal("toolRegistry accepted two tools with the same name")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("error = %v; want the duplicate-name error", err)
	}
}

func TestPrintMCPList(t *testing.T) {
	var out bytes.Buffer
	err := printMCPList(&out, []mcp.ServerInfo{
		{Name: "files", Transport: mcp.TransportStdio, Permission: agentapi.ClassRead, Enabled: true},
		{Name: "proj", Transport: mcp.TransportStdio, Permission: agentapi.ClassExecute, Enabled: false, Reason: "consent changed (args)"},
	})
	if err != nil {
		t.Fatalf("printMCPList: %v", err)
	}
	for _, want := range []string{"files", "configured", "read", "proj", "disabled", "consent changed (args)", "all"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output:\\n%s\\nmissing %q", out.String(), want)
		}
	}
}

func TestPrintMCPStatus(t *testing.T) {
	var out bytes.Buffer
	err := printMCPStatus(&out, []mcp.ServerStatus{
		{Name: "files", Transport: mcp.TransportStdio, Permission: agentapi.ClassRead, State: mcp.StateRunning, Protocol: "2026-07-28", ServerName: "fs", Tools: 14, Bytes: 8647, Health: "ping"},
		{Name: "big", Transport: mcp.TransportHTTP, Permission: agentapi.ClassExecute, State: mcp.StateHidden, Tools: 2, Bytes: 30000, Reason: "over the 20 KiB budget"},
	})
	if err != nil {
		t.Fatalf("printMCPStatus: %v", err)
	}
	for _, want := range []string{"files", "running", "2026-07-28", "ping", "big", "hidden", "over the 20 KiB budget"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output:\\n%s\\nmissing %q", out.String(), want)
		}
	}
}

func TestEnableMCPServer(t *testing.T) {
	root := t.TempDir()
	store := mcp.NewConsentStore(filepath.Join(t.TempDir(), "mcp-consent.json"))
	srv := mcp.ServerConfig{Name: "proj", Transport: mcp.TransportStdio, Command: "npx", Permission: agentapi.ClassExecute, Project: true}
	if _, _, err := enableMCPServer([]mcp.ServerConfig{srv}, root, "missing", store); err == nil {
		t.Fatal("enableMCPServer accepted an unknown name")
	}
	if _, approved, err := enableMCPServer([]mcp.ServerConfig{srv}, root, "proj", store); err != nil || !approved {
		t.Fatalf("enableMCPServer = approved %v, err %v", approved, err)
	}
	if ok, reason := store.Check(root, srv); !ok {
		t.Fatalf("consent not recorded: %s", reason)
	}
	srv.Command = "other"
	if ok, _ := store.Check(root, srv); ok {
		t.Error("a changed server stayed approved")
	}
	if _, approved, err := enableMCPServer([]mcp.ServerConfig{srv}, root, "proj", store); err != nil || !approved {
		t.Fatalf("re-enable = approved %v, err %v", approved, err)
	}
	if ok, _ := store.Check(root, srv); !ok {
		t.Error("re-enable did not record the new definition")
	}
	global := srv
	global.Project = false
	if _, approved, err := enableMCPServer([]mcp.ServerConfig{global}, root, "proj", store); err != nil || approved {
		t.Fatalf("global enable = approved %v, err %v; want no consent needed", approved, err)
	}
}

func TestMCPListCommand(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	global := filepath.Join(xdg, "hint", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(global), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(global, []byte(`
mcp_servers:
  files:
    transport: stdio
    command: npx
    permission: read
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newMCPCommand(&config.Flags{})
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mcp list: %v\n%s", err, errOut.String())
	}
	for _, want := range []string{"files", "read", "configured"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output:\n%s\nmissing %q", out.String(), want)
		}
	}
}
