// Package builtin implements the Phase 0 tool set: read_file, write_file,
// edit_file, list_dir, glob, grep, bash and todo.
//
// Every tool is confined to a [tool.Root] (the working directory), returns
// plain text, and is wrapped by [tool.WithLimits] at construction, so a
// caller never has to remember the timeout and truncation rules. Tools
// report a failure of the requested action (file not found, pattern
// ambiguous, non-zero exit) as an error [agentapi.ToolResult] the model can
// react to; a non-nil error from Run is reserved for the tool machinery
// itself breaking, per the [agentapi.Tool] contract.
package builtin
