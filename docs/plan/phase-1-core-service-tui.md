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

### WP1.3 — CLI/REPL on top of the RPC API — **done**

- [x] REPL and one-shot modes become RPC clients of an in-process (or spawned) core (`internal/rpc.Client` over a `net.Pipe` to the same `rpc.Server` that `hint serve` runs; `hint --connect` points the same shell at a running server)
- [x] No behavior change for users; acceptance scenarios from Phase 0 re-run green (`cmd/hint` tests unchanged in what they assert; `TestPermissionOverRPC` drives a permission question and its answer across the wire)
- [x] Delete any direct agent-loop calls from `cmd/hint` — the API is the only path (`TestShellNeverImportsTheAgent` pins it: only the engine wiring imports `internal/agent`)

### WP1.4 — TUI on Bubble Tea

**Branch:** `dev/wp1.4-tui`

References — Crush (reading only: its license is FSL-1.1-MIT, so no code is borrowed, per [risks.md](risks.md) R2), archived opencode (MIT).

- [x] `hint --tui`: streaming chat view with markdown rendering (glamour for finished messages; a message still streaming is plain text)
- [x] Diff view for write-permission prompts; `y` / `n` / `a` answer through `permission.reply`
- [x] The history of a continued session (`-c`, `--session`) rendered on attach
- [x] Session list view: list and resume (`-r` opens it)
- [ ] `hint` launches the TUI by default when stdout is a TTY; `--plain` keeps the readline REPL — the last step, once the Phase 0 scenarios pass through the TUI (acceptance 3)

Moved to WP1.5 (2026-10-17): the model/profile switcher and the session tree with branching.
Both need the protocol and the core before any UI, and WP1.4 does not touch the protocol.

### WP1.5 — Session and profile operations over RPC

**Branch:** `dev/wp1.5-session-ops`

- [ ] `session.profile`: switch the provider profile of a session mid-conversation; the engine builds a provider per profile from the config instead of one per process
- [ ] `session.fork`: a new session that starts from an existing one's history up to a given message
- [ ] The TUI exposes both: a profile picker, and a fork action in the session list, which becomes a tree

Additive methods, no `WireVersion` bump expected; the UI half is small once the methods exist.

Decisions (2026-10-17):

- **Bubble Tea v2, through the `charm.land/...` module paths.**
  v2 is the module's canonical path (its `go.mod` says `module charm.land/bubbletea/v2`; the `github.com/charmbracelet/bubbletea/v2` path refuses to build).
  `bubbletea/v2`, `bubbles/v2`, `lipgloss/v2` and `glamour/v2` are pinned.
  v1 is maintenance only and Crush, the reference, is on v2; the price is fewer examples and a different key and view API.
- **glamour from the first commit, for finished messages only.**
  A message still streaming is shown as plain text until `turn_end`, then rendered once.
  Re-rendering the whole message on every delta is the cost Crush had to engineer around (a cached render of a stable prefix); this plan does not pay it until a profile shows the need.
- **`internal/tui` is a client of the protocol, like the REPL.**
  It imports `internal/rpc`, `pkg/agentapi` and `internal/permission`, never `internal/agent`; `TestShellNeverImportsTheAgent` covers it.
- **Events reach the model through `tea.Program.Send`**, from one goroutine per turn, the way Crush and opencode deliver agent events.
  A `tea.Cmd` that waits on the channel returns one message and has to be re-armed after each; `Send` does not lose messages and keeps the turn's reader where the REPL's is.
- **One owner for the event semantics: `internal/shell`.**
  `shell.RunTurn` drives a turn and reports it through a small consumer-side `Sink` interface; the REPL's sink writes to the terminal, the TUI's sends messages to its program.
  Rules such as "`error` ends the turn only when `turn_end` says so" stay in one place, and so does the fix that `turn.start` runs to its answer even when the user cancels under it — a turn the client gave up on before it knew the id would run on with no owner.
- **Go 1.26.**
  `charm.land/bubbletea/v2@v2.1.0` declares `go 1.26.0`, so the module's `go` directive moves from 1.22 to 1.26.0; CI and the release build read the version from `go.mod` and follow.
  The alternative, an older v2.0.x, buys nothing: the series is weeks old and 1.26 is the current stable Go.
- **Opt-in before default.**
  `--tui` until acceptance 3 passes, then one commit flips the default and `--plain` keeps the REPL; that commit is the easy one to revert.
- **Tests drive the model.**
  `Update` and `View` are called directly against an `rpc.Server` on a `net.Pipe` with the fake engine already in `internal/rpc`; no `teatest`.

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
- **The CLI is a real client, not a shortcut (2026-10-12).**
  Bare `hint` serves the engine on one end of a `net.Pipe` and dials the other with the same `rpc.Client` an external Go program would use; the messages are marshalled and parsed, not passed as Go values.
  The price is one JSON round trip per event inside one process, which is nothing at terminal speed; the gain is that every bug in the wire format is hit by the daily CLI, not first by Phase 4's shells.
  Calling `rpc.Engine` directly would have kept the agent loop reachable from the shell and left the protocol exercised only by its own tests.
- **Permission labels travel with the request (2026-10-12).**
  `agentapi.PermissionRequest.Always` carries what an "always" answer would remember.
  The gate computes it; the client shows it.
  The alternative, the client recomputing `permission.CommandScope`, would have put policy in every client and let them drift from the core.
  Additive, `omitempty`, no `WireVersion` bump.
- **`--no-session` is an ephemeral session on the server (2026-10-12).**
  `session.open {"ephemeral":true}`: an id with no file.
  Turns need a session id, and the alternative — dropping `--no-session` — was a behavior change WP1.3 rules out.
- **Engine notices stay on the server's stderr (2026-10-12).**
  Project-context warnings, a session file that stops being writable and the `--debug` trace belong to the core; in-process they are the same stderr the shell uses, with `--connect` they appear where the server runs.
  A `notice` notification can be added when a client needs them; nothing today does.
- **gRPC waits for a non-Go client of `hint serve`.**
  Phase 4's native shells (Swift, and WinUI if that spike wins) are the trigger: a second adapter on the same `Engine` and the same `pkg/agentapi` events, with generated stubs and a server stream.
  JSON-RPC stays for `--mode rpc` and for the Go TUI.
  The Phase 5 mobile core is in-process via gomobile, so it is not this trigger.
  `v0.3` does not grow a `.proto`.

## Acceptance criteria

1. A second terminal can attach to a running `hint serve` and continue the
   same session. **Met 2026-10-12** by `hint --connect -c` (checked by hand
   against `hint serve --listen tcp:127.0.0.1:0`; since 2026-10-16
   `TestConnect` in `cmd/hint` publishes a server the way `hint serve`
   does and continues its session from a second `--connect` shell).
2. An external script drives a full agentic turn (incl. answering a
   permission request) through `hint --mode rpc` with JSON only.
3. All Phase 0 acceptance scenarios pass through the TUI.
