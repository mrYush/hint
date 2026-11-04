package main

import (
	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/mcp"
	"github.com/mrYush/hint/pkg/agentapi"
)

// mcpServers converts the loaded configuration into the manager's shape.
// The permission string becomes the action class the agent's gate reads;
// an empty one is execute, applied by the config loader and again by the
// manager as a safety net.
func mcpServers(cfg *config.Config) []mcp.ServerConfig {
	out := make([]mcp.ServerConfig, 0, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		out = append(out, mcp.ServerConfig{
			Name:      s.Name,
			Transport: mcp.Transport(s.Transport),
			Command:   s.Command,
			Args:      append([]string(nil), s.Args...),
			Env:       copyStrings(s.Env),
			URL:       s.URL,
			Headers:   copyStrings(s.Headers),

			Permission:     agentapi.ActionClass(s.Permission),
			Timeout:        s.Timeout,
			Tools:          mcp.ToolFilter{Allow: append([]string(nil), s.Tools.Allow...), Deny: append([]string(nil), s.Tools.Deny...)},
			DescriptionMax: s.DescriptionMax,
			Project:        s.Project,
		})
	}
	return out
}

func copyStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
