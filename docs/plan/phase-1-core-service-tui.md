# Phase 1 — Core as a service + TUI

> Status: In progress · Target release: **v0.3** · Estimate: 4–6 weeks (part-time)
> Depends on: Phase 0 (`v0.2.0` shipped) · Does not wait on: WP0.14 (install indexes; that track runs in parallel)

## Goal

Extract the agent core behind a JSON-RPC interface so that every future
client (widget, mobile app) gets identical capabilities, and ship a proper
interactive TUI.

## Work packages

### WP1.1 — RPC server: `hint serve` and `hint --mode rpc`

Reference — [Pi RPC mode](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md).

- [x] `hint serve`: JSON-RPC over unix socket (macOS/Linux) / localhost TCP (Windows)
- [x] `hint --mode rpc`: same protocol over stdio for subprocess embedding
- [x] Auth for the socket mode (token file with 0600 perms; localhost binding only)
- [x] Concurrent sessions: one server, many client connections, session ownership rules
- [x] Graceful shutdown; in-flight turn cancellation

### WP1.2 — Event model as contract v1

- [x] Stabilize `pkg/agentapi` events: `turn_start` / `text_delta` / `tool_start` / `permission` / `turn_end` (the names already on the v0.2.0 wire; see the decision below)
- [x] Permission requests flow over RPC and block on the client's answer (with timeout + default-deny)
- [x] Versioned protocol handshake (`protocol_version` in hello)
- [x] Golden tests for the wire format; document the protocol in `docs/rpc.md`

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

Decisions (2026-10-11):

- **JSON-RPC 2.0, one object per line.**
  Pi's RPC mode is the reference for the two entry points (`serve` and `--mode rpc`), not for the bytes.
  A standard envelope gives every request an id, so a permission reply and a cancel name the turn they belong to.
  Events are notifications (`event`, then `turn.done`); `turn.start` returns the turn id before the turn finishes.
- **Event names stay as v0.2.0 shipped them.**
  `turn_start`, `text_delta`, `tool_start`, `tool_end`, `permission`, `turn_end`.
  Renaming to the sketch in the checklist (`turn_started`, `permission_request`, …) would break session files and every in-process consumer for no new information.
  The sketch names are not a second vocabulary.
- **One in-flight turn per session, owned by the connection that started it.**
  Any authenticated client may `session.attach` and, once the turn ends, start the next one.
  A second `turn.start` while a turn is running is an error.
  The connection that owns the turn is the only one that may `permission.reply` or `turn.cancel`.
  Grants from "always" stay inside that session's gate.
- **Socket auth is a token file, mode 0600. Stdio has no token.**
  Holding the pipes is the credential for `--mode rpc`.
  TCP listen is rejected unless the address is loopback.
  The wait for a permission answer is 2 minutes; expiry denies.
- **gRPC waits for a non-Go client of `hint serve`.**
  Phase 4's native shells (Swift, and WinUI if that spike wins) are the trigger: a second adapter on the same `Engine` and the same `pkg/agentapi` events, with generated stubs and a server stream.
  JSON-RPC stays for `--mode rpc` and for the Go TUI.
  The Phase 5 mobile core is in-process via gomobile, so it is not this trigger.
  `v0.3` does not grow a `.proto`.

## Acceptance criteria

1. A second terminal can attach to a running `hint serve` and continue the
   same session.
2. An external script drives a full agentic turn (incl. answering a
   permission request) through `hint --mode rpc` with JSON only.
3. All Phase 0 acceptance scenarios pass through the TUI.
