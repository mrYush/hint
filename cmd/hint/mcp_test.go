package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

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
	if s.Env["TOKEN"] != "secret" || s.Args[0] != "server" {
		t.Errorf("conversion aliased the config: env=%v args=%v", s.Env, s.Args)
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
