# hint — Development Plan

> Status: living document · Last updated: 2026-10-24
> Source of truth for the roadmap. Detailed per-phase breakdowns live in [`docs/plan/`](docs/plan/).

## Vision

A cross-platform personal multimodal agent with a Go core. It starts as a CLI
developer assistant (the evolution of today's `hint`), and grows into a
universal agent with "eyes" (camera + VLM), "ears and voice" (microphone +
STT/TTS), memory (vector index), and access to device sensors. It works with
any OpenAI-compatible API and degrades gracefully to local models when offline
(Ollama on desktop, on-device runtimes on mobile). It is extensible through
tools: built-in, MCP servers, other agents over A2A, HTTP APIs described by
OpenAPI, and — eventually — a public catalog ("feature store") for
third-party developers. Instructions and knowledge come in the same open
way: rule files in the repository, MCP prompts and resources, local
databases and document indexes.

## Core architectural principle: "core as a service, shells as clients"

One Go core (agent runtime) exposes an API over stdio or a localhost socket.
Every interface is a thin client of that core.
`v0.3` speaks JSON-RPC 2.0 ([`docs/rpc.md`](docs/rpc.md)).
gRPC is a second adapter on the same engine, added when the first client of `hint serve` is not Go in this module — the native desktop shells in Phase 4.
It does not replace JSON-RPC: stdio embedding and the Go TUI keep the line protocol.
The in-process mobile core (Phase 5, gomobile) does not go through either wire.

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

The claims below are argued in full — with their price, and with the
conditions under which the project should be folded into someone else's
core — in [`docs/plan/why-hint.md`](docs/plan/why-hint.md).

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
| 0 | MVP CLI: agent loop, tools, permissions, sessions | v0.2.0 | 6–10 wk | [phase-0-mvp-cli.md](docs/plan/phase-0-mvp-cli.md) |
| — | Install indexes: apt + Scoop, cask cleanup. Parallel with Phase 1; does not gate v0.3 | current final tag | — | [WP0.14](docs/plan/phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows) |
| 1 | Core as a service + TUI | v0.3 | 4–6 wk | [phase-1-core-service-tui.md](docs/plan/phase-1-core-service-tui.md) |
| 2 | Memory, MCP client, A2A, open integration surfaces, code maturity | v0.4 | 8–10 wk | [phase-2-memory-mcp.md](docs/plan/phase-2-memory-mcp.md) |
| 3 | Multimodality: VLM, STT, TTS | v0.5 | 6–8 wk | [phase-3-multimodality.md](docs/plan/phase-3-multimodality.md) |
| 4 | Desktop widgets (macOS/Windows/Linux) | v0.6 | 6–10 wk | [phase-4-desktop-widgets.md](docs/plan/phase-4-desktop-widgets.md) |
| 5 | Mobile: iPhone / iPad / Android | v0.7 | 10–16 wk | [phase-5-mobile.md](docs/plan/phase-5-mobile.md) |
| 6 | Sensors and devices (parallel with 5) | v0.8 | 8–12 wk | [phase-6-sensors.md](docs/plan/phase-6-sensors.md) |
| 7 | Feature store (third-party catalog) | v0.9–v1.0 | 10–14 wk | [phase-7-feature-store.md](docs/plan/phase-7-feature-store.md) |

Version numbers start at `v0.2.0` (decided 2026-09-27): the `v0.1.0` tag
already in the repository belongs to the earlier, substantially different
application and is left as is. Pre-release tags carry a suffix
(`v0.2.0-alpha`); every tag is full semver, as Go modules require.

[WP0.14](docs/plan/phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows) publishes the current CLI to the indexes a user installs from: the Homebrew cask (macOS, already shipped), an apt repository (Ubuntu, Debian and the other apt systems, including Raspberry Pi OS) and a Scoop bucket (Windows).
It runs **in parallel with Phase 1**.
`v0.3` does not wait for it, and a tag that only updates those indexes is not the Phase 1 release.

Cross-cutting risks and mitigations: [`docs/plan/risks.md`](docs/plan/risks.md).

## Current status

- **Active phase:** Phase 2 — memory, MCP client, A2A (`v0.4`).
  Phase 0 shipped as `v0.2.0`.
  Phase 1 shipped as `v0.3.0`.
  WP1.1 and the RPC contract (WP1.2) are in the tree: `hint serve` and `hint --mode rpc`.
  WP1.3 is in the tree: bare `hint` and `hint -p` are RPC clients of a core served in the same process, and `hint --connect` joins a running `hint serve` from another terminal.
  Nothing in `cmd/hint` calls the agent loop except the engine the server serves.
  The shell is a real client, not a shortcut: it dials the core over a `net.Pipe` with `internal/rpc.Client`, so every message is marshalled and parsed rather than passed as a Go value, and it never calls `rpc.Engine` directly (decided 2026-10-12, confirmed 2026-10-15; [phase-1](docs/plan/phase-1-core-service-tui.md#work-packages), decisions).
  WP1.4–WP1.7 are in the tree: the TUI is the default on a terminal (`--plain` keeps the REPL), profile switch and fork go over RPC, completion is behind `completion.mode`, keys live in the OS secret store.
  The three Phase 1 acceptance criteria are met (2026-10-20).
- **In parallel:** [WP0.14](docs/plan/phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows) (apt repository, Scoop bucket, cask cleanup).
  It did not block `v0.3.0`.
  The apt side is in the tree: nFPM `.deb` packages, `scripts/apt-repo.sh` and a `release.yml` step that publishes a signed repository to `gh-pages`.
  The signing key (`APT_SIGNING_KEY`, and `APT_SIGNING_PASSPHRASE`) is set (2026-10-22).
  `v0.3.0` publishes that repository; Pages is enabled on `gh-pages` after the first push.
  `v0.2.0` was built before the packages existed and is not republished.
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
  shortcut; `cmd/hint` ran its one-shot turn through `Agent.RunTurn` until
  WP1.3 moved that call behind the RPC engine).
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
  `mrYush/homebrew-tap` for final versions; `hint --version` from ldflags
  with a `debug.ReadBuildInfo` fallback; the lint gate applied to the whole
  tree). The Raspberry Pi 5 smoke run that closes WP0.10's last checkbox is
  **deferred to the tag after `v0.2.0-alpha`** (decided 2026-09-14). Decision
  2026-10-11: that Pi 5 run is backlog and does not gate `v0.2.0` either.
  The linux/arm64 record is the Pi 4 4 GB `pi-bench` run `20261011-105151`
  on commit `b2aee40`
  ([summary](docs/plan/evidence/pi-4-20261011-105151/summary.md),
  [runs.jsonl](docs/plan/evidence/pi-4-20261011-105151/runs.jsonl)).
  The product guide for that board stays
  [`docs/raspberry-pi-4-4gb.md`](docs/raspberry-pi-4-4gb.md).
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
  WP0.12 rung 4 — split rules (`.hint/rules/*.md` with `paths:` front
  matter and `.cursor/rules/*.mdc` as a compatible source; a rule with
  paths loads once a tool reads or writes a matching file, one without
  loads always after its directory's `HINT.md`; `agent.Preamble`
  rebuilds the system prompt before every request from the tool calls in
  the conversation, so `-c` needs nothing new; `tool.Toucher` is how the
  file tools name what they touch; the glob matcher is now
  `internal/glob`).
- **Next:** Phase 2 (`v0.4`).
  `v0.2.0-alpha`, `v0.2.0` and `v0.3.0` are tagged; `v0.2.0` is the GitHub Release and the Homebrew cask in `mrYush/homebrew-tap`.
  WP0.1–WP0.12 have landed.
  [WP0.14](docs/plan/phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows) proceeds on its own and does not have to finish first.
  The audit fixes B1–B3 and M1–M2
  ([`docs/plan/review-2026-09-28-go-practices.md`](docs/plan/review-2026-09-28-go-practices.md))
  stay known issues and are backlog (decided 2026-10-11); they do not block
  either tag. The Raspberry Pi 5 smoke
  ([`docs/plan/raspberry-pi-smoke-test.md`](docs/plan/raspberry-pi-smoke-test.md))
  is backlog on the same date. The linux/arm64 record is the Pi 4 bench
  [`docs/plan/evidence/pi-4-20261011-105151/`](docs/plan/evidence/pi-4-20261011-105151/summary.md).
- Today's `hint` is the Phase 0 CLI surface served the Phase 1 way: bare
  `hint` opens an interactive session, `hint -p` answers once, and both
  are JSON-RPC clients of a core served in the same process over a
  `net.Pipe` — the same bytes `hint serve` speaks, so `hint --connect`
  points the same shell at a core running in another terminal. The core
  runs the agent loop with every built-in tool registered behind the
  permission gate (it explores the project itself and can edit it and run
  commands with confirmation), reads the global and the project's
  `HINT.md`, and records every conversation as a session that `-c`, `-r`
  or `--session` continues; independent reads in one tool batch run in
  parallel, and instruction files that outgrow the budget arrive as
  outlines the `instructions` tool expands, or split into rules that load
  when the conversation reaches their paths. CI and the release pipeline
  are in place. Nothing in the Phase 0 scope still blocks `v0.2.0`: the
  audit fixes and the Pi 5 smoke are backlog (2026-10-11), and criterion
  5 is the Pi 4 bench cited above.

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
   [WP0.12](docs/plan/phase-0-mvp-cli.md#wp012--instructions-beyond-the-budget--done).
9. ~~WP0.12 rung 4 (split rules loaded on first touch).~~ Done — see
   [WP0.12](docs/plan/phase-0-mvp-cli.md#wp012--instructions-beyond-the-budget--done).
10. ~~Tag `v0.2.0-alpha`.~~ Done — published pre-release.
    Known issues and the Pi 5 smoke did not block it (2026-10-11).
11. ~~Tag `v0.2.0`.~~ Done — GitHub Release and Homebrew cask `0.2.0` in `mrYush/homebrew-tap`.
    The hardware record is the Pi 4 bench, not a Pi 5 run.
    The tag needed `HOMEBREW_TAP_TOKEN`.
12. WP0.14, in parallel with Phase 1: apt repository (Debian family) and Scoop bucket (Windows) for the current final version.
    apt: implemented.
    `APT_SIGNING_KEY` and `APT_SIGNING_PASSPHRASE` are set (2026-10-22).
    `v0.3.0` publishes the signed repository to `gh-pages`; Pages is turned on for that branch after the push.
    Scoop is not started.
    The Homebrew cask field `homebrew_casks.binary` is replaced by `binaries` so `goreleaser check` passes.
    Still open after that: the Scoop bucket and the cask's `postflight_steps`.
    The cask already covers macOS.
    This did not gate `v0.3.0`.
    Official catalogs stay a separate parallel path and do not gate either track.
13. ~~Phase 1 (`v0.3`): `hint serve` (WP1.1, WP1.2) and the CLI as its client (WP1.3).~~ Done.
14. ~~WP1.4: the TUI on Bubble Tea v2, as a second client of the same protocol.~~ Done; its leftovers (the default-TUI flip, applying a saved setup without a restart) went to WP1.5 (decided 2026-10-18).
15. ~~WP1.5: session and profile operations over RPC — a profile switch mid-session and a session fork, each a protocol method and core work first; the TUI then shows them.~~ Done (2026-10-18): `session.profile`, `session.fork`, `profile.list`; `/profile` in both shells, `f` forks in the TUI, the session list is a tree, the TUI is the default on a terminal with `--plain` for the REPL.
16. ~~WP1.6: completion of what the user is typing.~~ Done (2026-10-19): `complete` over RPC with cancel, a `completion:` block with three triggers (`tab`, `idle`, `both`; off by default), the suggestion in the TUI's status line with Tab to accept, `/completion` with the cost so far, and the model argument of `/profile` and `/config` completed from what the profile serves.
    Backlog, not gating `v0.3`: the quality and cost evaluation on small models ([`docs/plan/completion-quality-eval.md`](docs/plan/completion-quality-eval.md)); until it has run, `off` is the only default.
17. ~~WP1.7: secrets out of the agent's reach.~~ Done (2026-10-19): keys in the OS secret store with `api_key: keyring:<profile>` and a `secrets.store` policy, `hint config show-key` as the only way to see one again, tool subprocesses without the key variables (by name and by value), every tool result scrubbed against the configured keys, hint's own files denied to the file tools, and `tool.Root` on `os.Root` (R8).
    Today the `bash` tool inherits the whole environment and can `cat` the config file; a sandbox for `bash`, which a same-user process needs to be truly kept out, is a later package.
18. Release leftovers from WP0.13, tracked in [#50](https://github.com/mrYush/hint/issues/50): `retract v0.1.0` in `go.mod`, `SECURITY.md`, a superseded note on the `v0.1.0` Release.
    Independent of every tag and of Phase 1.

## Open questions

| # | Question | Blocking | Resolution path |
|---|---|---|---|
| Q1 | api-bar.ru: exact `base_url`, model list, modality support (embeddings/vision/audio) | Phase 0 CI matrix entry only (config-driven, no code impact) | **Chat resolved by WP0.2**: `base_url` is `https://api-bar.ru/route/openai` (the gateway's public contour is `/route/<provider>/…`; models via `GET /route/openai/models`). Embeddings/vision/audio stay open for Phase 2–3 — other providers use different route shapes |
| Q2 | Product name: keep `hint` or rebrand? | Phase 7 (public catalog) at the latest; binary/config names are cheap to alias earlier | Decide before v0.6 (first non-CLI audience) |
| Q3 | Wails vs. native WinUI 3 for the Windows widget | Phase 4 | Spike at the start of Phase 4 |
| Q4 | llama.cpp bindings vs. MLC LLM as the primary mobile on-device runtime | Phase 5 | Spike on both; the `ChatProvider` interface isolates the choice |
| Q5 | License for the feature-store registry and SDK | Phase 7 | Decide with first external contributors |
| Q6 | Build our own MCP client (WP2.1) vs. validate against Goose's mature MCP ecosystem/community server catalog first | Phase 2 | See [prior-art research](docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic) — spike before WP2.1 starts |
| Q7 | Tool schedule: keep the series-parallel tree, or promote to a full `depends_on` DAG if a real turn cannot be expressed as nested groups and chains? | WP0.11 | Recorded as a decision in [WP0.11](docs/plan/phase-0-mvp-cli.md#wp011--tool-schedule-groups-and-chains--done) and implemented as the tree (2026-09-07); revisit only with a concrete counterexample |
| Q8 | Where split instruction files live: `.hint/rules/*.md` (hidden, tool-specific, like `.cursor/rules`) or a visible `hint/` directory; and whether to honour `.cursor/rules` / `.claude/` trees as compatible sources the way `AGENTS.md`/`CLAUDE.md` are | WP0.12 rung 4 | **Decided 2026-09-07**: `.hint/rules/*.md`, with `.cursor/rules` read as a compatible source (`globs:` mapped onto `paths:`); `.claude/` trees are not read. Recorded in [WP0.12](docs/plan/phase-0-mvp-cli.md#wp012--instructions-beyond-the-budget--done) |
| Q10 | A2A: which side first — `hint` as an A2A client that delegates to remote agents, or `hint serve` as an A2A server other agents call — and which Go implementation to build on (the A2A project's Go SDK, or a thin client over the JSON-RPC spec) | Phase 2 (WP2.6) | Spike at the start of WP2.6: read the current spec, run the reference samples against `hint serve`, decide; recorded in [phase-2](docs/plan/phase-2-memory-mcp.md#wp26--a2a-hint-among-other-agents) |
| Q9 | If some models are ever offered **by subscription** rather than paid per token by the user, how is that credential made unusable for ordinary API calls? The client runs on hardware the user administers, its source is public, and it can be rebuilt — so hiding the token is not an option. | Phase 7 (WP7.6); the provider layer must not foreclose it earlier | **Spike written 2026-09-08**: [`docs/plan/subscription-access.md`](docs/plan/subscription-access.md) — do not protect the credential, narrow what it unlocks (gateway holds the provider keys; a task-shaped advisor endpoint instead of an OpenAI-compatible passthrough; short-lived sender-constrained tokens; the user's own key always a complete path). Decide the gateway shape and whether running a gateway is in scope at all (Q9a) only when there are users. Risk R9 in [risks.md](docs/plan/risks.md) |

## Prior art

No existing tool implements the full product mechanic, but three actively
developed projects each already cover a large, different subset of it in
production or public beta (FutureOS's core-as-a-service design, Goose's MCP
maturity, OpenHuman's voice+desktop-widget experience). See
[`docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic`](docs/plan/architecture.md#prior-art-does-an-existing-tool-already-implement-the-whole-mechanic)
for the full comparison, sources, and what it means for the plan. Research
pass dated 2026-08-29; re-run before Phase 4/5 decisions (Q2–Q4).

## How this plan is maintained

- The rationale for the project's existence lives in
  [`docs/plan/why-hint.md`](docs/plan/why-hint.md); it carries a falsification
  table that is re-checked on the same cadence as the prior-art scan.
- Files under `docs/plan/` are the **source of truth**; GitHub Issues mirror
  the near-term work packages (currently Phase 1) for tracking and discussion.
  See [`docs/plan/README.md`](docs/plan/README.md) for conventions.
- When scope changes, update the phase file and this roadmap table in the same
  PR as the change (or the decision record).
