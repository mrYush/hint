// Package tool holds the machinery shared by every agentapi.Tool
// implementation: the registry the agent loop is fed from, JSON Schema
// generation for argument structs, the limits decorator (timeout and
// head+tail output truncation), the working-directory root that file tools
// are confined to, and argument decoding helpers.
//
// The tools themselves live in the builtin subpackage (WP0.5) and, later,
// in an MCP adapter (Phase 2). This package deliberately knows nothing
// about any specific tool, so that an MCP-provided tool gets the same
// limits and registry treatment as a built-in one.
package tool
