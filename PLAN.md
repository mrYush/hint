# hint — Development Plan

> Status: living document · Last updated: 2026-09-07
> Source of truth for the roadmap. Detailed per-phase breakdowns live in [`docs/plan/`](docs/plan/).

## Vision

A cross-platform personal multimodal agent with a Go core. It starts as a CLI
developer assistant (the evolution of today's `hint`), and grows into a
universal agent with "eyes" (camera + VLM), "ears and voice" (microphone +
STT/TTS), memory (vector index), and access to device sensors. It works with
any OpenAI-compatible API and degrades gracefully to local models when offline
(Ollama on desktop, on-device runtimes on mobile). It is extensible through
tools: built-in, MCP servers, and — eventually — a public catalog ("feature
store") for third-party developers.

## Core architectural principle: "core as a service, shells as clients"

One Go core (agent runtime) exposes an API (JSON-RPC/gRPC over stdio or a
localhost socket). Every interface is a thin client of that core:

```
                    ┌─────────────────────────────┐
   CLI / TUI ──────►│                             │
   Desktop widget ─►│      AGENT CORE (Go)        │──► Providers (LLM/VLM/STT/TTS/Embed)
   iOS/iPadOS ─────►│  loop · tools · sessions ·  │──► Local models (Ollama/llama.cpp)
   Android ────────►│  permissions · memory       │──► MCP servers (third-party tools)
   Raspberry Pi ───►│                             │──► Sensors/camera/mic (via the client)
                    └─────────────────────────────┘
```

See [`docs/plan/architecture.md`](docs/plan/architecture.md) for the package
layout, key interfaces, references, and the platform/provider matrices.

## What makes this different from the references

1. **Not only code.** Coding agents (Claude Code, Aider, Crush) are the
   starting scenario because it is the most mature and testable one, but the
   target is a personal assistant with perception: camera→VLM, mic→STT, TTS
   replies, device sensors as context sources.
2. **Offline as a first-class mode.** A provider router with degradation:
   cloud → Ollama → on-device. Automatic fallback, not a "plug it in yourself"
   option.
3. **One runtime on every platform.** The Go core cross-compiles natively for
   macOS/Linux/Windows/Raspberry Pi and embeds into iOS/Android via gomobile —
   thin native shells instead of rewriting the logic.
4. **Feature store.** A catalog of tools/skills for third-party developers
   built on MCP plus the Pi packages model (install from npm/git/URL with a
   trust mechanism).

## Roadmap

Each phase is an independently useful release. Estimates assume one developer
part-time; roughly halve them for full-time.

| Phase | Name | Release | Duration | Detailed plan |
|---|---|---|---|---|
| 0 | MVP CLI: agent loop, tools, permissions, sessions | v0.1 | 6–10 wk | [phase-0-mvp-cli.md](docs/plan/phase-0-mvp-cli.md) |
| 1 | Core as a service + TUI | v0.2 | 4–6 wk | [phase-1-core-service-tui.md](docs/plan/phase-1-core-service-tui.md) |
| 2 | Memory, MCP client, code maturity | v0.3 | 6–8 wk | [phase-2-memory-mcp.md](docs/plan/phase-2-memory-mcp.md) |
| 3 | Multimodality: VLM, STT, TTS | v0.4 | 6–8 wk | [phase-3-multimodality.md](docs/plan/phase-3-multimodality.md) |
| 4 | Desktop widgets (macOS/Windows/Linux) | v0.5 | 6–10 wk | [phase-4-desktop-widgets.md](docs/plan/phase-4-desktop-widgets.md) |
| 5 | Mobile: iPhone / iPad / Android | v0.6 | 10–16 wk | [phase-5-mobile.md](docs/plan/phase-5-mobile.md) |
| 6 | Sensors and devices (parallel with 5) | v0.7 | 8–12 wk | [phase-6-sensors.md](docs/plan/phase-6-sensors.md) |
| 7 | Feature store (third-party catalog) | v0.8–v1.0 | 10–14 wk | [phase-7-feature-store.md](docs/plan/phase-7-feature-store.md) |

Cross-cutting risks and mitigations: [`docs/plan/risks.md`](docs/plan/risks.md).

## Current status

- **Active phase:** Phase 0 — MVP CLI (in progress).
- **Done:** WP0.1 — the public contract `pkg/agentapi` (wire types, event
  streams, modality interfaces, `Tool`, `ActionClass`, classified errors).
  WP0.2 — config profiles and sources (provider profiles,
  default/fallback pair, `${VAR}` expansion, masking helpers, flat-config
  migration; Viper replaced by a hand-written loader over `yaml.v3`).
  WP0.3 — provider layer (streaming `internal/provider/openai` over
  hand-rolled SSE, native Ollama health/list client, retry + failover
  router; `internal/llm` deleted).
  WP0.4 — agent loop (`internal/agent`: tool-calling `while`, iteration and
  context-window limits, LLM-summary compaction with a drop-thinking
  shortcut; `cmd/hint` now runs its one-shot turn through `Agent.RunTurn`).
  WP0.5 — built-in tools (`internal/tool`: registry, schema generation,
  timeout/truncation decorator, working-directory root;
  `internal/tool/builtin`: read_file, write_file, edit_file, list_dir,
  glob, grep, bash, todo).
  WP0.6 — permission system (`internal/permission`: run modes
  ask/auto-edit/yolo, in-memory allow-list with command scopes, stdin
  prompter; `tool.Describe` previews with a hand-written `internal/diff`;
  `cmd/hint` now registers every built-in behind the gate, so edits show
  a diff and commands are confirmed before they run).
  WP0.7 — sessions (`internal/session`: append-only JSONL per
  conversation under `~/.local/share/hint/sessions/<project-hash>/`,
  compaction checkpoints via the new `agentapi.Compaction.History`,
  golden format test; `internal/console`: the one stdin reader shared by
  the permission prompter and the session picker; `hint -c`, `hint -r`,
  `hint --no-session`).
  WP0.8 — project context (`internal/project` replaces
  `internal/context`: `HINT.md`/`AGENTS.md`/`CLAUDE.md` read from the git
  root down to the working directory under one 32 KiB budget, a depth-2
  overview behind a pluggable `Overview`, `.gitignore` honoured through
  `git ls-files` with the hidden-and-dependency rule as the fallback —
  shared with the pure-Go walks of `list_dir`, `glob` and `grep`).
  WP0.9 — run modes and CLI surface (bare `hint` is a line-loop REPL
  over the shared stdin reader; `hint -p` is the one-shot, with
  `--output json` printing one object; positional questions still work
  with a migration note; `--debug` writes a redacted trace to
  `~/.local/state/hint/log/<run>.log` through `internal/debuglog`;
  `--session <id>`, `hint sessions`, `hint models`; WP0.8's limits as
  `--instruction-budget` / `--overview-depth` / `--overview-entries` and
  `instructions:` / `overview:` config keys; a global
  `~/.config/hint/HINT.md`; a profile's `context_window` sizing
  compaction).
  WP0.10 — CI, release, distribution (`ci.yml`: gofmt + golangci-lint,
  `go test -race` on Linux with ripgrep, a cross-compilation matrix of
  `{linux,darwin,windows}` × `{amd64,arm64}`; `release.yml` + goreleaser on
  a `v*` tag: archives, checksums, GitHub Release, Homebrew cask in
  `mrYush/homebrew-hint` for final versions; `hint --version` from ldflags
  with a `debug.ReadBuildInfo` fallback; the lint gate applied to the whole
  tree). The Raspberry Pi smoke run is the one box still open.
  WP0.11 — tool schedule (`internal/agent/schedule.go`: a sealed
  `seq` / `par` / `call` tree the agent builds from the model's batch —
  consecutive read-class calls fan out, at most `Limits.MaxParallelTools`
  (8) at once, every write, command or unknown tool is a step of its own;
  results and the session stay in request order; a machinery error
  cancels the group, an error result or panic does not; optional
  `ToolCall.After` hint on the wire, no `WireVersion` bump; read-class
  tools are now contractually safe for concurrent `Run`).
  WP0.12 rungs 0–3 — instructions beyond the budget (the default budget
  scales to the profile's context window; every instruction file is
  guaranteed its outline before any gets more, so a nearer file is never
  dropped; an over-budget Markdown file shows whole sections while they
  fit and the rest as heading + first sentence + `[...]`; the read-only
  `instructions` tool returns a file, a section or the `path:line` a
  quoted rule comes from, over the run's discovered files only; opt-in
  `instructions.summarize` has a model summarize what not even outlines
  can fit, cached by content hash under `~/.cache/hint/instructions`).
- **Next:** WP0.12 rung 4 — split rules (`.hint/rules/*.md` with
  `paths:` front matter, `.cursor/rules` read as a compatible source,
  loaded when a turn first touches a matching path) on its own branch,
  with the mid-turn injection mechanism as its opening fork. Then the
  Raspberry Pi smoke run on a `linux_arm64` release archive and the
  `v0.1-alpha` tag (deferred behind WP0.11 and WP0.12 on 2026-09-07).
- Today's `hint` is the Phase 0 CLI surface: bare `hint` opens an
  interactive session, `hint -p` answers once, both run through the
  agent loop with every built-in tool registered behind the permission
  gate (it explores the project itself and can edit it and run commands
  with confirmation), read the global and the project's `HINT.md`, and
  record every conversation as a session that `-c`, `-r` or `--session`
  continues; independent reads in one tool batch run in parallel, and
  instruction files that outgrow the budget arrive as outlines the
  `instructions` tool expands. CI and the release pipeline are in place;
  what is left for `v0.1` is WP0.12's last rung and the acceptance run
  on a Raspberry Pi.

## Compatibility decision

Breaking changes to the current CLI contract are accepted (decided
2026-08-28). In the MVP, bare `hint` starts an interactive REPL and one-shot
moves to `hint -p "question"`. `hint "question"` (positional args) is kept as
a one-shot alias for a soft migration and may be removed later. Implemented
by WP0.9 (2026-09-07); the alias prints a one-line note pointing at `-p`.

## Next 5 steps (can start today)

1. ~~Create `pkg/agentapi` with Message/ToolCall/Event types (contract before
   code).~~ Done — see [WP0.1](docs/plan/phase-0-mvp-cli.md#wp01--public-contract-pkgagentapi--done).
2. ~~Rewrite `internal/llm` → `internal/provider/openai` with streaming and
   tools; test against OpenAI and Ollama.~~ Done — see
   [WP0.3](docs/plan/phase-0-mvp-cli.md#wp03--provider-layer--done).
3. ~~Implement the agent loop (`internal/agent`, tool-calling `while` +
   limits + compaction).~~ Done — see
   [WP0.4](docs/plan/phase-0-mvp-cli.md#wp04--agent-loop--done).
4. ~~Add `read_file`/`list_dir` tools — the first real agentic scenario~~
   Done — see [WP0.5](docs/plan/phase-0-mvp-cli.md#wp05--built-in-tools--done)
   and [WP0.6](docs/plan/phase-0-mvp-cli.md#wp06--permission-system--done)
   for the permission layer and `bash` with confirmation.
5. ~~JSONL sessions + `-c`.~~ Done — see
   [WP0.7](docs/plan/phase-0-mvp-cli.md#wp07--sessions--done).
6. ~~CI, goreleaser, Homebrew.~~ Done — see
   [WP0.10](docs/plan/phase-0-mvp-cli.md#wp010--ci-release-distribution).
7. ~~Tool schedule: parallel reads, ordered writes.~~ Done — see
   [WP0.11](docs/plan/phase-0-mvp-cli.md#wp011--tool-schedule-groups-and-chains--done).
8. ~~WP0.12 rungs 0–3 (window-scaled budget, outlines, the
   `instructions` tool, opt-in summaries).~~ Done — see
   [WP0.12](docs/plan/phase-0-mvp-cli.md#wp012--instructions-beyond-the-budget--rungs-03-done-rung-4-open).
9. WP0.12 rung 4 (split rules loaded on first touch). Then the Raspberry
   Pi smoke run and tag `v0.1-alpha` (deferred behind WP0.11 and WP0.12
   on 2026-09-07).

## Open questions

| # | Question | Blocking | Resolution path |
|---|---|---|---|
| Q1 | api-bar.ru: exact `base_url`, model list, modality support (embeddings/vision/audio) | Phase 0 CI matrix entry only (config-driven, no code impact) | **Chat resolved by WP0.2**: `base_url` is `https://api-bar.ru/route/openai` (the gateway's public contour is `/route/<provider>/…`; models via `GET /route/openai/models`). Embeddings/vision/audio stay open for Phase 2–3 — other providers use different route shapes |
| Q2 | Product name: keep `hint` or rebrand? | Phase 7 (public catalog) at the latest; binary/config names are cheap to alias earlier | Decide before v0.5 (first non-CLI audience) |
| Q3 | Wails vs. native WinUI 3 for the Windows widget | Phase 4 | Spike at the start of Phase 4 |
| Q4 | llama.cpp bindings vs. MLC LLM as the primary mobile on-device runtime | Phase 5 | Spike on both; the `ChatProvider` interface isolates the choice |
| Q5 | License for the feature-store registry and SDK | Phase 7 | Decide with first external contributors |
| Q6 | Build our own MCP client (WP2.1) vs. validate against Goose's mature MCP ecosystem/community server catalog first | Phase 2 | See [prior-art research](docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic) — spike before WP2.1 starts |
| Q7 | Tool schedule: keep the series-parallel tree, or promote to a full `depends_on` DAG if a real turn cannot be expressed as nested groups and chains? | WP0.11 | Recorded as a decision in [WP0.11](docs/plan/phase-0-mvp-cli.md#wp011--tool-schedule-groups-and-chains--done) and implemented as the tree (2026-09-07); revisit only with a concrete counterexample |
| Q8 | Where split instruction files live: `.hint/rules/*.md` (hidden, tool-specific, like `.cursor/rules`) or a visible `hint/` directory; and whether to honour `.cursor/rules` / `.claude/` trees as compatible sources the way `AGENTS.md`/`CLAUDE.md` are | WP0.12 rung 4 | **Decided 2026-09-07**: `.hint/rules/*.md`, with `.cursor/rules` read as a compatible source (`globs:` mapped onto `paths:`); `.claude/` trees are not read. Recorded in [WP0.12](docs/plan/phase-0-mvp-cli.md#wp012--instructions-beyond-the-budget--rungs-03-done-rung-4-open) |

## Prior art

No existing tool implements the full product mechanic, but three actively
developed projects each already cover a large, different subset of it in
production or public beta (FutureOS's core-as-a-service design, Goose's MCP
maturity, OpenHuman's voice+desktop-widget experience). See
[`docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic`](docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic)
for the full comparison, sources, and what it means for the plan. Research
pass dated 2026-08-29; re-run before Phase 4/5 decisions (Q2–Q4).

## How this plan is maintained

- Files under `docs/plan/` are the **source of truth**; GitHub Issues mirror
  the near-term work packages (currently Phase 0) for tracking and discussion.
  See [`docs/plan/README.md`](docs/plan/README.md) for conventions.
- When scope changes, update the phase file and this roadmap table in the same
  PR as the change (or the decision record).
