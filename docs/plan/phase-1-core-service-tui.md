# Phase 1 — Core as a service + TUI

> Status: In progress · Target release: **v0.3** · Estimate: 4–6 weeks (part-time)
> Depends on: Phase 0 (`v0.2.0` shipped) · Does not wait on: WP0.14 (install indexes; that track runs in parallel)

## Goal

Extract the agent core behind a JSON-RPC interface so that every future
client (widget, mobile app) gets identical capabilities, and ship a proper
interactive TUI.

## Work packages

### WP1.1 — RPC server: `hint serve` and `hint --mode rpc` — **done** (2026-10-16)

**Branch:** `dev/wp1.1-rpc`

Reference — [Pi RPC mode](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/rpc.md).

- [x] `hint serve`: JSON-RPC over unix socket (macOS/Linux) / localhost TCP (Windows)
- [x] `hint --mode rpc`: same protocol over stdio for subprocess embedding
- [x] Auth for the socket mode (token file with 0600 perms; localhost binding only)
- [x] Concurrent sessions: one server, many client connections, session ownership rules
- [x] Graceful shutdown; in-flight turn cancellation

### WP1.2 — Event model as contract v1 — **done** (2026-10-16)

**Branch:** `dev/wp1.1-rpc` (shipped with WP1.1 in one commit, before the one-branch rule)

- [x] Stabilize `pkg/agentapi` events: `turn_start` / `text_delta` / `tool_start` / `permission` / `turn_end` (the names already on the v0.2.0 wire; see the decision below)
- [x] Permission requests flow over RPC and block on the client's answer (with timeout + default-deny)
- [x] Versioned protocol handshake (`protocol_version` in hello)
- [x] Golden tests for the wire format; document the protocol in `docs/rpc.md`

### WP1.3 — CLI/REPL on top of the RPC API — **done** (2026-10-17)

**Branch:** `dev/wp1.3-cli-client`

- [x] REPL and one-shot modes become RPC clients of an in-process (or spawned) core (`internal/rpc.Client` over a `net.Pipe` to the same `rpc.Server` that `hint serve` runs; `hint --connect` points the same shell at a running server)
- [x] No behavior change for users; acceptance scenarios from Phase 0 re-run green (`cmd/hint` tests unchanged in what they assert; `TestPermissionOverRPC` drives a permission question and its answer across the wire)
- [x] Delete any direct agent-loop calls from `cmd/hint` — the API is the only path (`TestShellNeverImportsTheAgent` pins it: only the engine wiring imports `internal/agent`)

Decisions for WP1.1–WP1.3 (2026-10-11):

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

### WP1.4 — TUI on Bubble Tea

**Branch:** `dev/wp1.4-tui`

References — Crush (reading only: its license is FSL-1.1-MIT, so no code is borrowed, per [risks.md](risks.md) R2), archived opencode (MIT).

- [x] `hint --tui`: streaming chat view with markdown rendering (glamour for finished messages; a message still streaming is plain text)
- [x] Diff view for write-permission prompts; `y` / `n` / `a` answer through `permission.reply`
- [x] The history of a continued session (`-c`, `--session`) rendered on attach
- [x] Session list view: list and resume (`-r` opens it)
- [x] Copying text out: the terminal's own selection works (mouse reporting is off by default), `ctrl+y` / `/copy [answer|question|code|all]` copy to the clipboard, and `/mouse` turns on a mode where the wheel scrolls and a click copies the block under it
- [x] Tab completes slash commands and their arguments from one table that also drives `/help`; a command that cannot be used right now (`/copy` with an empty transcript, `/sessions` during a turn) is neither offered nor run
- [x] A rejected key names the configuration in use and offers to fill it in: `hint setup` (line-based), a `/setup` form in the TUI (key masked), and an offer after the failed turn in the REPL; `internal/config.WriteProfile` edits the global file in place, keeping comments, mode 0600
- [x] First run without any configuration: on a terminal `hint` offers the setup questions (defaults for everything but the key, which is typed with echo off) and carries on with the file it wrote; `config.example.yaml` documents every key and a test keeps it current
- [x] Every config key editable without an editor: `internal/config/schema.go` is the one table of keys (type, allowed values, default, overlays, help) that the setup form, `hint config set|unset|show|keys`, `/config` with Tab completion and the example test all read; `config.Edit` checks a value against its key and the result against the loader before one atomic write
- [x] Scrolling without the mouse: ↑ on the input's first line and ↓ on its last move the transcript (so does a wheel, which a terminal without mouse reporting sends as arrows), shift+↑↓ by line, pgup/pgdn by page, ctrl+home/end to the ends, and the header says how many lines are below
- [x] A message browser: esc in an empty input (or ctrl+↑) selects a block, marked by a bar and not a fill; ↑↓/j/k move, `y` copies, `f` is the hook for the fork, esc returns; every transcript entry knows its turn
- [x] One-letter answers and commands work in any layout: the TUI takes the US key the kitty protocol reports (`BaseCode`), and a table of the Cyrillic layouts (`internal/console.QWERTY`) covers terminals without it and the line prompts; text typed into the input is never rewritten
- [ ] `hint` launches the TUI by default when stdout is a TTY; `--plain` keeps the readline REPL — the last step, once the Phase 0 scenarios pass through the TUI (acceptance 3)

Merged under the scope exception of [README.md](README.md) ("Branch per work package"), 2026-10-18: interactive configuration (`hint setup`, `hint config`, `/config`, `internal/config` schema and in-place writes) grew inside this branch because a rejected key in the TUI needed it, and the branch also wrote down WP1.6 and WP1.7.
Splitting it after review had started would have cost a rebase of reviewed commits for no gain in what `develop` receives.

Moved to WP1.5 (2026-10-17): the model/profile switcher and the session tree with branching.
Both need the protocol and the core before any UI, and WP1.4 does not touch the protocol.
Decided 2026-10-18: the whole of WP1.4's remaining debt moved to WP1.5 as well — the default-TUI flip, applying a saved setup to the running core (below, "Setup writes the global config"), and the `f` hook of the message browser.

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
- **Selecting text is the terminal's job, copying a block is ours.**
  Mouse reporting makes the terminal hand the mouse to the program, which disables its text selection; that is why nothing could be copied.
  It is off by default; `/mouse` turns it on for wheel scrolling and click-to-copy, and `ctrl+y` / `/copy` work either way.
  A copy goes out two ways because neither reaches everywhere: the terminal's clipboard sequence (OSC 52, works through ssh and tmux, ignored by some terminals such as VTE-based ones) and the system clipboard (`atotto/clipboard`, which needs xclip, xsel or wl-copy on Linux).
- **One table for commands.**
  `internal/tui/commands.go` holds name, help, closed argument set and an `enabled` predicate; `/help`, dispatch and Tab all read it.
  Completion of what is not a command (a continuation of a sentence) is WP1.6.
- **Setup writes the global config, never the project's.**
  A key does not belong in a file that sits in a repository, so `WriteProfile` targets `~/.config/hint/config.yaml` (or `$XDG_CONFIG_HOME/hint`), edits it as a YAML document so comments and unknown keys survive, and replaces it atomically with mode 0600.
  The key is a literal or a `${VAR}` reference; the line-based setup asks only for the variable's name, because stdin is shared with the line reader and cannot be switched to no-echo, while the TUI form masks what is typed.
  A project file that is read after the global one is named in the result, since it may override what was just written.
  Not done: applying the new config to the running core.
  The provider is built at start-up, so setup says "restart hint"; a reload belongs with WP1.5's provider-per-session.
  With `--connect` the core's configuration is on another machine, and the shell says so instead of offering a form.
- **Opt-in before default.**
  `--tui` until acceptance 3 passes, then one commit flips the default and `--plain` keeps the REPL; that commit is the easy one to revert.
- **Tests drive the model.**
  `Update` and `View` are called directly against an `rpc.Server` on a `net.Pipe` with the fake engine already in `internal/rpc`; no `teatest`.

### WP1.5 — Session and profile operations over RPC — **done** (2026-10-18)

**Branch:** `dev/wp1.5-session-ops`

- [x] `session.profile`: switch the provider profile of a session mid-conversation; the engine builds a provider per profile from the config instead of one per process.
      An optional `model` overrides the profile's own for that session
- [x] `session.fork`: a new session that starts from an existing one's history up to the end of a given user turn (`{"id": …, "turn": N}`, turns counted from 1; 0 or omitted is the whole conversation).
      A turn, not a message index: a client counts the questions it showed, but not the tool messages the core added between them; the TUI's entries already carry their turn, and the message browser's `f` calls it
- [x] `profile.list`: the configured profiles with their roles and whether a session can switch to them, read from the files as they stand
- [x] The TUI exposes both: `/profile [name [model]]` with Tab completion and a picker, the profile in the header, `f` in the message browser (fork after that turn) and in the session list (fork the whole row), and the list drawn as a tree with forks under their source
- [x] The line REPL has `/profile` too, so `--plain` is not left without the switch
- [x] WP1.4 debt: a saved `hint setup` / `/setup` is applied to the running session instead of asking for a restart; the TUI is the default on a terminal, `--plain` keeps the REPL and `--tui` stays as a hidden no-op

Additive methods, no `WireVersion` or `ProtocolVersion` bump; an older core answers them with method-not-found.

Decisions (2026-10-18):

- **A provider per session, rebuilt on switch.**
  The engine keeps a cache of providers keyed by profile and model override, seeded with the one it started with; a switch builds the profile's router (the configured fallback behind it unless it is the same profile) and installs a new `agent.Agent` over it.
  The agent is stateless between turns, so rebuilding it is cheaper and safer than making its provider swappable; the permission gate, the recorder and the preamble stay, so an "always" grant and the conversation survive the switch.
  The context window follows the profile (`limitsFor`).
- **The configuration is re-read on every switch and every `profile.list`.**
  That is how a key written by setup reaches a running core: the shells call `session.profile` with the saved profile after a successful save, and the "restart hint" message is gone.
  The reload uses the same flags the run started with, so `--provider`/`--model` overlays stay consistent; a reload failure is an internal error, not a silent fallback.
- **The switch is persisted as a `profile` record in the session file.**
  Only the name and the model override, never the profile's contents; a reader that does not know the record skips it (`FormatVersion` stays 1).
  What was asked is recorded, not what it resolved to: a model left to the profile follows the profile when its config changes.
  On attach the engine restores it; a profile the configuration lost, or that cannot be used, falls back to the default with a warning the client shows.
- **A fork is a standalone file.**
  The header names the parent (`parent: {id, turn}`), the copied conversation is written as plain `message` records, and the source's profile record comes first, so deleting the parent later loses nothing.
  A source with a turn in flight is refused as busy: a cut through a half-finished turn would leave a dangling tool call.
  An ephemeral fork is allowed and stays in memory.
- **Error codes reuse the existing set.**
  An unknown session or profile is `-32005`, a request that names things which exist but cannot be used that way (a profile without a key, a turn the conversation does not have) is `-32602`, a session or source mid-turn is `-32004`; the engine signals the first two with `rpc.ErrNotFound` and `rpc.ErrInvalid`.
- **The session list is a tree in the TUI only.**
  `session.list` stays flat and carries `parent`; the shell builds the forest (roots by recency, forks under their source, an orphan at the root still naming its parent).
  `hint sessions` prints the flat list with a `forked from` note.
- **Default TUI without a Phase 0 re-run under a model.**
  Acceptance 3 is covered by the automated TUI tests (tools and their results in the transcript, the permission dialog with the diff, `-c` history on attach, sessions) and a terminal smoke test of the default; a manual pass of the four Phase 0 scenarios through the TUI against a live provider is left to the release branch.

### WP1.6 — Completion of what the user is typing — **done** (2026-10-19)

**Branch:** `dev/wp1.6-input-completion`

- [x] `complete` over RPC: given the text typed so far, the core returns a short continuation from a model; `complete.cancel` stops it; a newer `complete` on the connection cancels the older; the request runs beside the connection's loop so `permission.reply` and `turn.cancel` are never held up (a test pins it)
- [x] The suggestion in the TUI: shown in the status line (`tab accepts: …`), Tab accepts it, any other key discards it
- [x] A `completion:` block in the config with the usual layering — `mode` (`off` | `tab` | `idle` | `both`), `profile`, `model`, `idle_after`, `warn_after`; `HINT_COMPLETION_MODE` and `--completion` for the mode; an explicit `off` in the project file beats a global `on`; a named profile that does not exist or cannot be used switches the block off with a warning instead of stopping the run
- [x] A slow or failing completion never gets in the way: the request runs beside the input; an edit cancels it; a suggestion belongs to the exact text it was asked for and a late answer is dropped by its id; past `warn_after` the status line says the model is slow; three failures in a row switch it off for the session with a notice; `/completion on` tries again
- [x] `/completion [off|tab|idle|both]` sets the trigger for the session and alone reports the mode, its source, the profile and the requests and tokens spent so far
- [x] Command arguments that depend on the configuration: the model argument of `/profile` and the value of `/config set providers.<p>.model` complete from `profile.models`, fetched by the first Tab on it and cached for the session

Decisions (2026-10-19):

- **Three triggers instead of one.**
  Review of the first draft (2026-10-18) found that a request on every pause is the one thing in the plan that spends tokens without a key press, against the lean-harness goal.
  `tab` is the mode to start with: one request per Tab, nothing otherwise.
  `idle` asks after `idle_after` and is held back by thresholds the shell owns (12 characters, two seconds between requests, three new characters); `both` is the union.
  Off by default, as recorded before any code.
- **The session's own profile and model answer by default.**
  A separate small model is optional (`completion.profile`, `completion.model`), documented with the warning that on a small machine a second resident model evicts the main one; nothing extra runs unless asked for.
- **The status line, not ghost text in the input.**
  `bubbles/textarea` has no suggestions; drawing one under the cursor means rewriting its rendered output around wrapping and a prompt.
  The status line costs nothing, works at every width, and the contract — see it, Tab takes it — is the same.
  Inline ghost text is backlog, to be done only if the status line proves not enough in use.
- **`complete` is the first method the server runs beside its loop.**
  `conn.dispatch` is synchronous; a completion waits on a model for seconds.
  The connection keeps a map of completions in flight by the client's id for `complete.cancel` and for the newer-cancels-older rule.
- **The cost is visible before it is a default.**
  `complete` returns the provider's usage; `/completion` sums requests and tokens for the session.
  Whether any mode may ever be on by default is decided from that number and a run against a 1–3B model (backlog, with the Pi evidence format), not now.
- **What leaves the machine.**
  The text typed so far (its last 1000 characters) and the last question of the session (500); never files, tool results or the system prompt.
  README says so in the first paragraph of the feature.
- **The REPL has no completion.**
  `--plain` has no status line to show one in; the method stays in the protocol for other clients.

Backlog from the review: inline ghost text; a request budget enforced by the core when there is a second client; prefix caching of suggestions; and the quality, latency and cost measurement of small models for completions, written up as [`completion-quality-eval.md`](completion-quality-eval.md) with its pass criteria — nothing but `off` is a default until it has run.

### WP1.7 — Secrets out of the agent's reach

**Branch:** `dev/wp1.7-secrets`

Once entered, an API key must not be readable by the agent: not by its tools, not in what goes to the model, not in a session file.
Only the user sees it again, through a command that exists for that and is not reachable over RPC or from a tool.

Where it leaks today (2026-10-18):
the `bash` tool inherits the whole environment (`internal/tool/builtin/bash.go`, `cmd.Env = os.Environ()`), so `env` prints `APIBAR_KEY`;
`~/.config/hint/config.yaml` is mode 0600 but owned by the same user, so `cat` reads it;
the file tools are confined to the project root, which is the home directory when `hint` runs there;
tool results reach the model and the session file unmasked — only the `--debug` trace is redacted.

- [ ] Keys in the OS secret store (Keychain, Windows Credential Manager, Secret Service over D-Bus) instead of the config file: `api_key: keyring:<profile>`; `hint setup` and `/setup` write there.
      A headless Linux box without a Secret Service needs a decided fallback (the 0600 file as today, with a warning), not a silent one
- [ ] `hint config show-key <profile>`: the one way to see a key again; terminal only, asks for confirmation, prints a masked form unless `--reveal`; no RPC method and no tool reaches it
- [ ] Tool subprocesses run without the keys: `bash` gets the environment minus every variable a profile's `${VAR}` names and the well-known ones (`APIBAR_KEY`, `APIBAR_TOKEN`, `API_BAR_KEY`, `OPENAI_API_KEY`, `HINT_API_KEY`)
- [ ] Every tool result is redacted against `Config.Secrets()` before it reaches the model, an event or the session file — the catch-all for a key that leaked some other way
- [ ] The file tools refuse the config directory and the session directory even when the project root contains them
- [ ] `tool.Root` opens files through `os.Root` (Go 1.24; the floor is 1.26 since WP1.4), which closes the symlink-swap window between the check and the open ([risks.md](risks.md) R8)
- [ ] Tests that pin each path: `env`, `cat` of the config, `read_file` from a root at `$HOME`, a tool output that contains the key

Limits to state, not to hide: a process running as the same user can ask the secret store itself (`secret-tool lookup`, `security find-generic-password`), and a determined model can transform a value before printing it, which defeats redaction.
Closing that needs a sandbox for `bash` (bubblewrap or Landlock on Linux, `sandbox-exec` on macOS), which is its own work package; this one removes every accidental path and the easy deliberate ones.

## Acceptance criteria

1. A second terminal can attach to a running `hint serve` and continue the
   same session. **Met 2026-10-12** by `hint --connect -c` (checked by hand
   against `hint serve --listen tcp:127.0.0.1:0`; since 2026-10-16
   `TestConnect` in `cmd/hint` publishes a server the way `hint serve`
   does and continues its session from a second `--connect` shell).
2. An external script drives a full agentic turn (incl. answering a
   permission request) through `hint --mode rpc` with JSON only.
3. All Phase 0 acceptance scenarios pass through the TUI.
