// Package mcp connects hint to Model Context Protocol servers and adapts
// their tools to [agentapi.Tool].
//
// It is the only package that imports the official MCP Go SDK; the agent
// loop, the registry and [pkg/agentapi] see ordinary tools. The manager
// owns connections and the schema budget, and one adapter owns one tool's
// name, description, schema, permission class and Run.
//
// What a server returns is data, never instructions: tool descriptions and
// results are passed to the model as tool text, and nothing from a server
// is interpolated into the system prompt or run as a command by the
// adapter. The permission class comes from the configuration, not from the
// server's annotations, which the specification says are untrusted.
package mcp
