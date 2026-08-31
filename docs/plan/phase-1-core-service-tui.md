# Phase 1 — Core as a service + TUI

> Status: Planned · Target release: **v0.2** · Estimate: 4–6 weeks (part-time)
> Depends on: Phase 0

## Goal

Extract the agent core behind a JSON-RPC interface so that every future
client (widget, mobile app) gets identical capabilities, and ship a proper
interactive TUI.

## Work packages

### WP1.1 — RPC server: `hint serve` and `hint --mode rpc`

Reference — [Pi RPC mode](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md).

- [ ] `hint serve`: JSON-RPC over unix socket (macOS/Linux) / localhost TCP (Windows)
- [ ] `hint --mode rpc`: same protocol over stdio for subprocess embedding
- [ ] Auth for the socket mode (token file with 0600 perms; localhost binding only)
- [ ] Concurrent sessions: one server, many client connections, session ownership rules
- [ ] Graceful shutdown; in-flight turn cancellation

### WP1.2 — Event model as contract v1

- [ ] Stabilize `pkg/agentapi` events: `turn_started` / `delta` / `tool_call` / `permission_request` / `turn_done`
- [ ] Permission requests flow over RPC and block on the client's answer (with timeout + default-deny)
- [ ] Versioned protocol handshake (`protocol_version` in hello)
- [ ] Golden tests for the wire format; document the protocol in `docs/rpc.md`

### WP1.3 — CLI/REPL on top of the RPC API

- [ ] REPL and one-shot modes become RPC clients of an in-process (or spawned) core
- [ ] No behavior change for users; acceptance scenarios from Phase 0 re-run green
- [ ] Delete any direct agent-loop calls from `cmd/hint` — the API is the only path

### WP1.4 — TUI on Bubble Tea

References — Crush, archived opencode.

- [ ] Streaming chat view with markdown rendering
- [ ] Diff view for write-permission prompts
- [ ] Model/profile switcher mid-session
- [ ] Session tree/list view (resume, branch)
- [ ] `hint` launches the TUI by default when stdout is a TTY; `--plain` keeps the readline REPL

## Acceptance criteria

1. A second terminal can attach to a running `hint serve` and continue the
   same session.
2. An external script drives a full agentic turn (incl. answering a
   permission request) through `hint --mode rpc` with JSON only.
3. All Phase 0 acceptance scenarios pass through the TUI.
