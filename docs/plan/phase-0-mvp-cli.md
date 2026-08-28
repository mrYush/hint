# Phase 0 — MVP CLI

> Status: Planned · Target release: **v0.1** · Estimate: 6–10 weeks (part-time)
> Platforms: macOS (arm64/amd64), Linux (arm64/amd64 incl. Raspberry Pi), Windows (amd64)

## Goal

A Go CLI agent, the evolution of `hint`: multi-turn dialogue with
tool-calling, working with any OpenAI-compatible API and with Ollama, with
safe action execution and persisted sessions. One static binary per platform.

## Non-goals (explicitly out of MVP)

TUI (Bubble Tea), MCP, RAG/vector index, repo map, VLM/STT/TTS, subagents,
git auto-commits, LSP, widgets, and mobile apps — all later phases.

## Non-functional requirements

- Go ≥ 1.22, **no cgo** (trivial cross-compilation).
- Single binary ≤ 30 MB; cold start ≤ 150 ms to first request.
- Test coverage of agent loop, tools, and permission logic ≥ 70%; golden
  tests for the session format.
- CI: GitHub Actions — build matrix {darwin,linux,windows} × {amd64,arm64},
  releases via goreleaser, Homebrew tap.
- `--debug` writes full requests/responses (keys masked) to
  `~/.local/state/hint/log`.

## Work packages

### WP0.1 — Public contract: `pkg/agentapi`

Types before code: `Message`, `ToolCall`, `ToolResult`, `Event`, `ToolSchema`,
plus the modality interfaces (`ChatProvider`, `Embedder`, `Transcriber`,
`Speaker`, `VisionProvider`) — only `ChatProvider` gets an implementation in
this phase.

- [ ] Define message/event/tool types in `pkg/agentapi`
- [ ] Declare all modality interfaces (empty implementations forbidden — unimplemented ones simply have no constructor yet)
- [ ] Doc comments on every exported symbol (this becomes the SDK)
- [ ] Unit tests: JSON round-trip for every wire type

### WP0.2 — Config: profiles and sources

Extends `internal/config`. Priority: CLI flags → env (`HINT_*`) → project
`./.hint/config.yaml` → global `~/.config/hint/config.yaml`.

- [ ] Provider profile type: `{name, base_url, api_key, model, kind: openai|ollama}`
- [ ] Multiple profiles; one `default_provider`, explicit `fallback_provider`
- [ ] Env references in values (`api_key: ${OPENAI_API_KEY}`)
- [ ] Secrets never logged; masking helper used by the debug logger
- [ ] Migration: map today's flat `api_url`/`api_key`/`model` config onto a single implicit profile (warn once)
- [ ] Table-driven tests for source priority and env expansion

Example config:

```yaml
providers:
  - name: api-bar
    kind: openai
    base_url: https://api-bar.ru/v1     # pin from the AnyAPI dashboard (see PLAN.md Q1)
    api_key: ${API_BAR_KEY}
    model: gpt-4o
  - name: local
    kind: ollama
    base_url: http://localhost:11434/v1
    model: qwen2.5:7b
default_provider: api-bar
fallback_provider: local
```

### WP0.3 — Provider layer

Replaces `internal/llm` with `internal/provider/openai` + `internal/provider/router`.

- [ ] `openai-compatible` implementation of `ChatProvider`: streaming chat completions + tool calls (covers OpenAI, Azure, OpenRouter, vLLM, LM Studio, aggregator gateways such as api-bar.ru, and Ollama via `/v1`)
- [ ] `ollama-native` client for health-check and model listing only
- [ ] Router: on network error/timeout of the default profile, automatically switch to the fallback profile with a notice on stderr
- [ ] Streaming output to the terminal
- [ ] Integration tests against OpenAI and Ollama (recorded fixtures for CI; live matrix job optional, incl. api-bar.ru — streaming and tool-calling especially)
- [ ] Delete `internal/llm`

### WP0.4 — Agent loop

- [ ] Claude Code pattern: `while` — model responds; tool calls → execute all, append results, repeat; pure text → finish the turn
- [ ] Limits: max iterations per turn (default 25), max context tokens
- [ ] Compaction at 80% of the window: summarize old turns into one system block (reference — auto-compact in opencode/Crush)
- [ ] Loop unit tests with a scripted fake provider (multi-tool turns, iteration limit, compaction trigger)

### WP0.5 — Built-in tools

Each tool: JSON Schema input, text output, timeout, large-output truncation
(head+tail with a marker).

- [ ] `read_file(path, offset?, limit?)`
- [ ] `write_file(path, content)`
- [ ] `edit_file(path, old_str, new_str)` — unique search/replace (Aider editblock format)
- [ ] `list_dir(path)`
- [ ] `glob(pattern)`
- [ ] `grep(pattern, path?)`
- [ ] `bash(command, timeout?)` — confirmation-gated (WP0.6)
- [ ] `todo(items[])` — current-task plan, shown to the user
- [ ] Tool registry + schema generation for the provider layer
- [ ] Per-tool unit tests incl. truncation and timeout behavior

### WP0.6 — Permission system

References — Crush, Codex CLI.

- [ ] Action classes: `read` (no confirmation), `write` (confirm with diff), `execute` (confirm with command shown)
- [ ] Run modes: `--ask` (default), `--auto-edit` (writes silent, execute asks), `--yolo` (nothing asks, loud warning)
- [ ] Session allow-list of remembered grants ("always allow `go test`")
- [ ] File tools confined to the working directory by default
- [ ] Tests: every class × every mode, allow-list persistence within a session, path-escape attempts

### WP0.7 — Sessions

Reference — Pi session format.

- [ ] Append-only JSONL, one file per session: `~/.local/share/hint/sessions/<project-hash>/<id>.jsonl`
- [ ] Record kinds: user/assistant/tool_call/tool_result/system/compaction
- [ ] Commands: `hint` (new session), `hint -c` (continue latest in this directory), `hint -r` (pick from a list), `hint --no-session`
- [ ] Golden tests for the session format (backward-compat gate)

### WP0.8 — Project context

- [ ] Read `HINT.md` (plus `AGENTS.md`/`CLAUDE.md` as compatible names) from the project root into the system prompt
- [ ] Auto-context of the current directory — as today, but with a size limit and `.gitignore` filtering

### WP0.9 — Run modes and CLI surface

Breaking change accepted (see PLAN.md): bare `hint` = interactive REPL.

- [ ] Interactive REPL (plain readline loop, no TUI framework)
- [ ] One-shot: `hint -p "question"` — exits after the answer; `--output json` for scripts
- [ ] Compatibility alias: `hint "question"` (positional args) behaves as one-shot, prints a one-line migration hint
- [ ] `--debug` logging with key masking

### WP0.10 — CI, release, distribution

- [ ] GitHub Actions: build matrix {darwin,linux,windows} × {amd64,arm64}, test + lint (golangci-lint) gates
- [ ] goreleaser config; binaries attached to GitHub Releases
- [ ] Homebrew tap
- [ ] Raspberry Pi smoke run of the acceptance scenarios on linux/arm64
- [ ] Update README for the new CLI surface

## Acceptance criteria

1. `hint -p "what files are in this project and what do they do"` — the agent
   calls `list_dir`/`read_file` itself and answers (no manual context assembly).
2. `hint "add --version flag handling to main.go"` — the agent shows a diff,
   asks for confirmation, applies the edit.
3. Internet off + Ollama running → the same scenario works through the
   fallback profile without changing the command.
4. `hint -c` continues yesterday's dialogue with preserved context.
5. A binary built with `GOOS=linux GOARCH=arm64` passes the same scenarios on
   a Raspberry Pi 5.

## Suggested order

WP0.1 → WP0.2 → WP0.3 → WP0.4 → WP0.5 (read-only tools first) → WP0.6 →
WP0.5 (write/execute tools) → WP0.7 → WP0.8 → WP0.9 → WP0.10.
Tag `v0.1-alpha` once WP0.1–WP0.7 land; `v0.1` after acceptance criteria pass.
