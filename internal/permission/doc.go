// Package permission decides whether a tool call may run: the WP0.6 gate
// between the agent loop and a tool of class write or execute.
//
// It is pure policy over an [agentapi.PermissionRequest]: a run [Mode]
// says which classes ask at all, an in-process allow-list remembers what
// the user answered "always" to, and a [Prompter] is how the question
// reaches whoever is there to answer — stdin for the CLI, an RPC client
// in Phase 1. The package knows no tool by name; the preview a prompt
// shows comes from [tool.Describe], and read-class tools never reach it.
//
// The gate fails closed: when a prompt is needed and nobody can answer
// (no Prompter, stdin at EOF), the call is denied, never allowed.
package permission
