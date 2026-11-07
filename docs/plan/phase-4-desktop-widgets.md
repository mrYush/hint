# Phase 4 — Desktop widgets and editor integration (macOS / Windows / Linux)

> Status: Planned · Target release: **v0.6** · Estimate: 6–10 weeks for WP4.1–WP4.4 (part-time); WP4.5 is re-estimated with the phase
> Depends on: Phase 1 (RPC core), Phase 3 (camera/mic/screenshot scenarios)

## Goal

The "widget on the desktop" from the original product vision: thin native
shells over `hint serve`, with the core installed as a background service — a
resident assistant daemon.

The second desktop surface is the editor.
Zed runs `hint` as an ACP agent in its agent panel (WP4.5), with the thread, the diff review and the permission prompts rendered by the editor and no resident service in between.

Scope added 2026-11-06: WP4.5 embeds `hint` in the Zed editor over the Agent Client Protocol.
It is a client of the core like the widgets, and it needs neither WP4.1's resident service nor Phase 3's capture, so it may be delivered before or beside WP4.1–WP4.4.
The 6–10-week estimate above covers WP4.1–WP4.4 only; re-estimate the phase with WP4.5 once the adapter's scope is settled.

## Work packages

### WP4.1 — Core as a system service

- [ ] Install/uninstall commands: `hint service install` → launchd (macOS), systemd user unit (Linux), autostart (Windows)
- [ ] Single-instance socket discovery for clients
- [ ] Idle resource budget: near-zero CPU, bounded RSS when no session is active
- [ ] Auto-update channel decision (goreleaser + built-in updater vs. package managers) — spike
- [ ] If the shell is not Go (Swift, or WinUI if that spike wins): add a gRPC adapter beside JSON-RPC, same `Engine` and same `pkg/agentapi` events. JSON-RPC stays for stdio and for a Wails/Go shell. Decided 2026-10-11; not a `v0.3` deliverable.

### WP4.2 — macOS menu-bar app

- [ ] Swift/SwiftUI menu-bar shell talking to the core over unix socket
- [ ] Global hotkey → quick-ask window
- [ ] Screenshot-to-question flow
- [ ] Camera/mic capture via native APIs, streamed to the core (STT/VLM)
- [ ] Permission prompts rendered natively (reuse the RPC `permission_request` event)
- [ ] Signed + notarized build

### WP4.3 — Windows tray app

- [ ] Spike: WinUI 3 vs. Wails (decision Q3 in PLAN.md) — pick one, record the decision here
- [ ] Tray shell with the same features as WP4.2 (hotkey, quick ask, screenshot, mic)
- [ ] Installer (MSIX or Inno Setup)

### WP4.4 — Linux tray app

- [ ] Tray via Wails/GTK, same feature set
- [ ] Wayland + X11 screenshot paths

### WP4.5 — Zed integration: `hint` as an ACP agent

**Branch:** `dev/wp4.5-zed-acp`

Zed embeds third-party agents through the [Agent Client Protocol](https://agentclientprotocol.com) (ACP): the editor spawns the agent as a child process and speaks JSON-RPC 2.0 over stdio, so the thread, the diff review and the permission prompts stay in the editor.
Zed installs agents from the [ACP Registry](https://zed.dev/docs/ai/external-agents) rather than from extensions (extension-provided agents are deprecated), and a custom `agent_servers` entry in `settings.json` is the development path.
The adapter is one more client of the core, beside the JSON-RPC server and the shells; it does not get its own agent loop, provider wiring or session store.

- [ ] `hint acp`: an ACP agent server on stdio, startable from a custom `agent_servers` entry and listed in the ACP Registry for users; it is the same binary the CLI already is, so the integration stays an adapter rather than a second product.
- [ ] `initialize` negotiates the stable protocol version (v1 today; v2 is a draft), advertises only what the adapter implements, and never claims `loadSession` or another session capability before a test covers it.
- [ ] `session/new` maps a Zed thread onto a session in the project's `cwd`; `additionalDirectories` becomes additional roots when that capability is advertised, and the profile's models are what the session's model selector offers.
- [ ] `session/load`, `session/resume`, `session/list` and `session/close` map onto recorded sessions: history replays as `session/update` chunks in order, and a thread reopened in Zed continues the session `-c` would continue.
- [ ] `session/prompt` runs one `Engine` turn, streams `agent_message_chunk`, `agent_thought_chunk`, `tool_call`/`tool_call_update` with edit locations and the ACP diff content for writes so the change opens in Zed's multi-buffer review, and reports `usage_update` and the matching `stopReason`.
- [ ] `session/cancel` reaches the core as the existing turn cancellation, and a pending permission request is answered `cancelled` rather than left to time out; a cancelled turn is not reported as an error.
- [ ] Permission questions arrive as `session/request_permission` with the choices the shell shows (allow once, allow for the session, deny) and the diff preview the permission layer already builds; the run mode still applies, so `yolo` adds no prompt the CLI would skip.
- [ ] File access prefers the client's `fs/read_text_file` and `fs/write_text_file` inside the session's root set and falls back to the tool root when the client does not offer them; nothing outside the root set is read without the permission the file tools already require.
- [ ] MCP servers Zed forwards in `session/new` join the existing MCP client under the WP2.1 rules (lazy start, budget, declared permission class) or are declined by one recorded decision; there is no second MCP stack, and no credential reaches an update, a log or the model.
- [ ] Terminal commands: one recorded decision whether `bash` runs in the core under today's permission gate or through the ACP terminal methods so Zed renders the output; it names who confirms the command and where its output lives.
- [ ] Tests: a fake ACP client over pipes covers initialize, a new session, a prompt with a tool call and a permission round-trip, cancel, load and a protocol error; the run is race-clean and malformed input becomes an ACP error, not a panic.
- [ ] A manual checklist against Zed itself (`dev: open acp logs`): a thread answers, an edit appears as a diff in a multi-buffer after a permission prompt, a running turn cancels, and the thread survives a restart through `session/load`.
- [ ] Install and docs: the custom `agent_servers` snippet, the registry manifest and listing, and a `docs/zed.md` page plus one README line stating what stays in `hint` (keys, profiles, sessions) and what the editor owns (UI, diffs, thread list).

Decisions to make when the work starts, recorded here: whether to take `github.com/coder/acp-go-sdk` or hand-roll the adapter over the JSON-RPC types `internal/rpc` already has (the repository prefers the standard library, so a new module needs its reason), and whether the surface is an `acp` subcommand or a flag of `hint serve`.

Acceptance: in Zed with `hint` installed from the ACP Registry, a thread answers a question, edits a file with the change shown as a diff after a permission prompt, cancels a running turn, and reopens the same thread after Zed restarts.

## Acceptance criteria

1. After `hint service install`, the core survives reboot and the widget
   connects instantly.
2. Global hotkey → question → answer without opening a terminal, on all three
   OSes.
3. A screenshot taken from the widget produces a correct VLM answer.
4. Killing the widget does not kill in-flight agent work (core owns the turn).
5. In Zed, a `hint` thread answers, shows its edits as a diff after a
   permission prompt, cancels a running turn, and reopens the same thread
   after a restart.
