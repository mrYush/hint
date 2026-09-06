# hint — Development Plan

> Status: living document · Last updated: 2026-09-06
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
- **Next:** WP0.8 — project context (`HINT.md`, `.gitignore`-aware
  auto-context).
- **Planned after permissions:** WP0.11 — Agent-built tool schedule
  (parallel groups + sequential chains; see
  [phase-0-mvp-cli.md](docs/plan/phase-0-mvp-cli.md#wp011--tool-schedule-groups-and-chains)).
  Sequential request order stays the WP0.4 default until then.
- Today's `hint` is still a one-shot CLI (cobra), now running through the
  agent loop with every built-in tool registered behind the permission
  gate (it explores the project itself and can edit it and run commands
  with confirmation), and every run is recorded as a session that `-c`
  continues. The REPL is WP0.9's.

## Compatibility decision

Breaking changes to the current CLI contract are accepted (decided
2026-08-28). In the MVP, bare `hint` starts an interactive REPL and one-shot
moves to `hint -p "question"`. `hint "question"` (positional args) is kept as
a one-shot alias for a soft migration and may be removed later.

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
   [WP0.7](docs/plan/phase-0-mvp-cli.md#wp07--sessions--done). Then tag
   `v0.1-alpha` and build for Raspberry Pi.

## Open questions

| # | Question | Blocking | Resolution path |
|---|---|---|---|
| Q1 | api-bar.ru: exact `base_url`, model list, modality support (embeddings/vision/audio) | Phase 0 CI matrix entry only (config-driven, no code impact) | **Chat resolved by WP0.2**: `base_url` is `https://api-bar.ru/route/openai` (the gateway's public contour is `/route/<provider>/…`; models via `GET /route/openai/models`). Embeddings/vision/audio stay open for Phase 2–3 — other providers use different route shapes |
| Q2 | Product name: keep `hint` or rebrand? | Phase 7 (public catalog) at the latest; binary/config names are cheap to alias earlier | Decide before v0.5 (first non-CLI audience) |
| Q3 | Wails vs. native WinUI 3 for the Windows widget | Phase 4 | Spike at the start of Phase 4 |
| Q4 | llama.cpp bindings vs. MLC LLM as the primary mobile on-device runtime | Phase 5 | Spike on both; the `ChatProvider` interface isolates the choice |
| Q5 | License for the feature-store registry and SDK | Phase 7 | Decide with first external contributors |
| Q6 | Build our own MCP client (WP2.1) vs. validate against Goose's mature MCP ecosystem/community server catalog first | Phase 2 | See [prior-art research](docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic) — spike before WP2.1 starts |
| Q7 | Tool schedule: keep the series-parallel tree, or promote to a full `depends_on` DAG if a real turn cannot be expressed as nested groups and chains? | WP0.11 | Recorded as a decision in [WP0.11](docs/plan/phase-0-mvp-cli.md#wp011--tool-schedule-groups-and-chains); revisit only with a concrete counterexample |

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
