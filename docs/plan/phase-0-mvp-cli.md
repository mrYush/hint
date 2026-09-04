# Phase 0 — MVP CLI

> Status: In progress · Target release: **v0.1** · Estimate: 6–10 weeks (part-time)
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

### WP0.1 — Public contract: `pkg/agentapi` — **done**

Types before code: `Message`, `ToolCall`, `ToolResult`, `Event`, `ToolSchema`,
plus the modality interfaces (`ChatProvider`, `Embedder`, `Transcriber`,
`Speaker`, `VisionProvider`) — only `ChatProvider` gets an implementation in
this phase.

- [x] Define message/event/tool types in `pkg/agentapi`
- [x] Declare all modality interfaces (empty implementations forbidden — unimplemented ones simply have no constructor yet)
- [x] Doc comments on every exported symbol (this becomes the SDK)
- [x] Unit tests: JSON round-trip for every wire type

Decisions taken while implementing, recorded here because later packages
depend on them:

- **`Message.Content` is `[]ContentPart`**, not a flat string. Phase 3
  multimodality then adds a part kind instead of reshaping a published type.
- **Sum types are structs with a `Kind` discriminator**, not sealed
  interfaces. The same values are persisted in session files (WP0.7) and
  shipped over RPC (Phase 1); a sealed interface would need hand-written
  `(Un)MarshalJSON` on that path. The cost is that `Kind` and payload can
  disagree, which every such type answers with a `Validate` method.
- **Modality interfaces live in `pkg/agentapi`**, not `internal/provider`, so
  a provider can be written outside the module. `architecture.md` updated to
  match.
- **Providers assemble streamed tool-call fragments**, so a `ChatToolCall`
  event always carries a complete, JSON-valid call.
- **`Message` carries no id or timestamp** — that metadata belongs to the
  session record wrapping it (WP0.7).

Scope taken beyond the original checklist, because WP0.3–WP0.6 need these
types to be public and would otherwise have to amend the contract later:

- [x] `ActionClass` (read/write/execute) — the `Tool` interface would
      otherwise pull `permission.Class` out of `internal/`, putting the class
      out of reach of third-party tool authors
- [x] Typed `Error`/`ErrorKind` with `Retryable`/`Fallbackable` — the WP0.3
      router needs to tell a network failure from a 401 to decide when to
      fall back
- [x] `Usage` — the WP0.4 compaction trigger needs token accounting
- [x] `Tool` interface (declared here, implemented in WP0.5),
      `PermissionRequest` and `Compaction` payloads (the client-facing
      `Event` is incomplete without them)
- [x] `go.mod` raised 1.20 → 1.22, the phase's stated floor
- [x] `.golangci.yml` — the contract depends on the `exhaustive` linter for a
      guarantee Go's compiler does not give (a `switch` over a string enum
      falling through to `default` when a constant is added). CI wires the
      gate itself in WP0.10; the config lands with the code that needs it.

Rules the wire types hold to, each pinned by a test after review found a way
around it:

- A **tool message may be empty** — a tool that succeeds silently produces no
  output, and the message is identified by its `ToolCallID`. Requiring content
  made `ToolResult.Message()` build a message `Message.Validate` rejected, so
  a silent `write_file` aborted the next turn with a non-retryable error.
- **Tool arguments and input schemas must be JSON objects**, not merely valid
  JSON. `null` — which is what a nil `json.RawMessage` decodes back into —
  and scalars would otherwise unmarshal into a zeroed argument struct, so a
  tool would run against empty inputs.
- **Raw JSON fields are `omitempty`**, so a nil value stays nil across a round
  trip instead of becoming the four bytes `null` and flipping `Validate` from
  rejecting to accepting.
- **Cancellation outranks a provider's own classification** in `KindOf`: an
  HTTP client reports a cancelled request as a transport failure, and treating
  that as `ErrNetwork` would make the router fail over on the user's Ctrl-C.
- **Unknown `Kind` values fail with the `ErrUnknownKind` sentinel**, so a
  decoder of a newer session file can tell "skip it" from "malformed" with
  `errors.Is`, as the forward-compatibility rule in the package doc promises.
- **`FinishReason` is validated strictly.** Providers must map their dialect
  onto it; forwarding a raw `max_tokens` would make the loop report a
  truncated answer as a normal stop.
- **A permission request carries its `CallID`.** Without it, two prompts from
  one assistant message are indistinguishable and an approval can authorise
  the wrong call.

### WP0.2 — Config: profiles and sources — **done**

Extends `internal/config`. Priority: CLI flags → env (`HINT_*`) → project
`./.hint/config.yaml` → global `~/.config/hint/config.yaml`.

- [x] Provider profile type: `{name, base_url, api_key, model, kind: openai|ollama}`
- [x] Multiple profiles; one `default_provider`, explicit `fallback_provider`
- [x] Env references in values (`api_key: ${OPENAI_API_KEY}`)
- [x] Secrets never logged; masking helper used by the debug logger
- [x] Migration: map today's flat `api_url`/`api_key`/`model` config onto a single implicit profile (warn once)
- [x] Table-driven tests for source priority and env expansion

Example config:

```yaml
providers:
  - name: api-bar
    kind: openai
    base_url: https://api-bar.ru/route/openai   # gateway route; the client appends /chat/completions
    api_key: ${API_BAR_KEY}
    model: gpt-4o
  - name: local
    kind: ollama
    base_url: http://localhost:11434/v1
    model: qwen2.5:7b
default_provider: api-bar
fallback_provider: local
```

Decisions taken while implementing, recorded here because later packages
depend on them:

- **Viper is gone; the loader is hand-written over `gopkg.in/yaml.v3`.**
  Viper is a process-global singleton (source-priority tests flake without
  `viper.Reset()` and cannot run in parallel), and it cannot merge the
  `providers` *list* by name — a later file replaces the whole list. The
  loader takes an `Options{HomeDir, WorkingDir, LookupEnv, Flags}` seam, so
  tests inject a map and `t.TempDir()` instead of process state. `yaml.v3`
  was already in `go.sum` as Viper's transitive dependency and is now direct;
  it is archived upstream but stable and ubiquitous (`goccy/go-yaml` is the
  standby if it ever needs replacing).
- **The product default endpoint is the api-bar gateway**
  (`https://api-bar.ru/route/openai`, model `gpt-4o`), not `api.openai.com`.
  This closes PLAN.md Q1 for chat: the gateway's public contour is
  `/route/<provider>/…`, so the earlier `https://api-bar.ru/v1` example here
  was wrong (`{base}/chat/completions` would hit a nonexistent path). OpenAI
  remains an explicit profile (or the `OPENAI_API_KEY`-only zero-config
  fallback). Revisit before a public release: a personal gateway as the
  compiled-in default is fine for the maintainer, not for strangers.
- **The default profile NAME is resolved before field overlays.**
  `--provider` → `HINT_PROVIDER` → files; only then do `HINT_API_KEY` /
  `HINT_API_URL` / `HINT_MODEL` and `--api-*` apply, and only to that
  selected profile. The fallback keeps its own values — otherwise
  `HINT_API_KEY` would hand a cloud key to the offline profile.
- **Usability is validated only for selected profiles.** Every profile must
  be well-formed (name, known kind), but "openai needs a key / ollama needs
  a model" is enforced only for `default_provider` and `fallback_provider`.
  A spare keyless profile (e.g. a local vLLM) must not block the run; it
  fails when selected.
- **Implicit key pickup happens only in zero-config synthesis.** With no
  profiles from any file, one is synthesized from `API_BAR_KEY` /
  `APIBAR_TOKEN` (api-bar wins) or `OPENAI_API_KEY` (legacy openai.com). An
  explicit profile must name its key via `${VAR}` — the loader never guesses
  a key for it, and never sends an OpenAI key to another host.
- **Flag defaults are empty strings.** The old `--model` default `gpt-4`
  silently beat every config file; built-in defaults now live in the loader,
  applied only after all overlays. Legacy paths (`~/.config/hint.yaml`,
  `./hint.yaml`) are still read with a deprecation warning when the
  canonical ones are absent.
- **`$VAR`/`${VAR}` expansion applies to file values only** (profile fields
  plus `default_provider`/`fallback_provider`), after merge. Flag and env
  overlay values are not expanded — the shell already did that. No
  `$(cmd)`, no `${VAR:-default}`.

### WP0.3 — Provider layer — **done**

Replaces `internal/llm` with `internal/provider/openai` + `internal/provider/router`.

- [x] `openai-compatible` implementation of `ChatProvider`: streaming chat completions + tool calls (covers OpenAI, Azure, OpenRouter, vLLM, LM Studio, aggregator gateways such as api-bar.ru, and Ollama via `/v1`)
- [x] `ollama-native` client for health-check and model listing only
- [x] Router: on network error/timeout of the default profile, automatically switch to the fallback profile with a notice on stderr
- [x] Streaming output to the terminal
- [x] Integration tests against OpenAI and Ollama (recorded fixtures for CI; live matrix job optional, incl. api-bar.ru — streaming and tool-calling especially)
- [x] Delete `internal/llm`

Decisions taken while implementing, recorded here because later packages
depend on them:

- **Hand-rolled HTTP + SSE over the stdlib, no `openai-go`.** Fixture tests
  replay recorded byte streams (`testdata/*.sse`), error mapping is explicit,
  and dialect quirks stay visible in the package instead of inside an SDK.
  The streaming-loop and accumulator shape follows Ollama's runner client and
  the message conversion follows opencode's provider (both MIT, rewritten
  against `agentapi` types, attributed in the commit).
- **The wire protocol lives in a sans-IO decoder** (`stream.go`): SSE lines
  go in, `ChatEvent`s come out, no HTTP anywhere. Protocol edge cases are
  table-tested from string literals without a server. The decoder implements
  the real SSE record grammar — multi-line `data:` joining, comment/field
  skipping, CRLF tolerance, bare-JSON lines from unframed servers, and an
  unterminated final record at EOF — because gateways in the wild produce all
  of these.
- **Streamed tool-call fragments are keyed by `index` (pointer, so absent ≠
  0), with an id-keyed fallback** for dialects that omit the index. Calls are
  validated (`ToolCall.Validate`) and emitted in index order only at stream
  end; truncated arguments JSON becomes a terminal `ChatError`, never a
  half-built `ChatToolCall`. A missing id is synthesized (`call_<index>`)
  because the contract requires one for result correlation.
- **Cancellation shape is pinned by test:** a stream that already delivered
  events ends with `ChatDone`/`FinishCanceled` (the user interrupted a
  healthy answer); a cancel that beat the first delivery ends with
  `ChatError`/`ErrCanceled`.
- **Two liveness guards beyond the checklist.** A per-chunk idle timeout
  (default 2 min, `WithIdleTimeout`) turns a provider that stalls mid-stream
  into `ErrTimeout` instead of a hung run, and a 200-OK stream that ends
  before sending anything is `ErrUnavailable`, not an empty success. The
  overall request keeps no deadline — a healthy stream may run for minutes;
  only `ResponseHeaderTimeout` (60 s TTFT) bounds the connect.
- **Router policy:** failover (and same-provider retry, two extra attempts
  with `Retry-After`/jittered-backoff capped at 30 s) applies only while the
  primary produced no user-visible events; after that a failure is forwarded
  as-is — splicing two providers' answers mid-stream is worse than an error.
  `Router.Stream` never returns a synchronous error: all failures arrive on
  the channel after the policy is exhausted. Usage events are withheld until
  the terminal so a retried attempt cannot leak a duplicate.
- **`Router.Name()` is the primary's static name**, deviating from the
  earlier "active profile" sketch: a router is shared between concurrent
  turns, so a mutable active-profile name would be a data race. Attribution
  of failures travels in `Error.Provider`; the switch itself is announced by
  the stderr notice.
- **`ReadyChecker` is a consumer-side interface in the router package.** The
  factory (`provider.Chat`) decorates an `ollama`-kind profile with the
  native `/api/version` probe; the router discovers it by type assertion, so
  failing over to a stopped daemon reports one clear `ErrUnavailable`.
- **Requests send `max_tokens`**, not the newer `max_completion_tokens`:
  every supported dialect (gateways, vLLM, LM Studio, Ollama) accepts the
  former, several reject the latter. Revisit if an o-series model profile
  ever needs it.
- **`PartThinking` is omitted from outbound requests; media parts fail
  loudly** with `ErrInvalidRequest` (Phase 3). Inbound
  `delta.reasoning_content`/`delta.reasoning` map to `ChatThinkingDelta`,
  which the CLI keeps off stdout.
- **Azure classic deployments are out of scope**; Azure's OpenAI-compatible
  `/openai/v1` surface works as any other aggregator profile. Classic
  (`api-version` query, `api-key` header) would be a WP0.2-shaped config
  change.
- **`hint` still does one-shot Q&A** — the WP0.4 loop is next; tool-call and
  usage events already flow through the CLI but have no consumer yet.

### WP0.4 — Agent loop — **done**

- [x] Claude Code pattern: `while` — model responds; tool calls → execute all, append results, repeat; pure text → finish the turn
- [x] Limits: max iterations per turn (default 25), max context tokens
- [x] Compaction at 80% of the window: summarize old turns into one system block (reference — auto-compact in opencode/Crush)
- [x] Loop unit tests with a scripted fake provider (multi-tool turns, iteration limit, compaction trigger)

Decisions taken while implementing, recorded here because later packages
depend on them:

- **`internal/agent.Agent.RunTurn` never returns a synchronous error** — the
  same shape as `router.Router.Stream` (WP0.3): a failure arrives as
  `EventError` followed by a terminal `EventTurnEnd` on the returned
  channel. `RunTurn` is stateless between turns; it takes the full history
  as an argument and never retains it — a stateful `Agent` would be the
  start of a session, which is WP0.7's job.
- **Tool calls run sequentially, in request order**, not through a parallel
  `errgroup.Group`. This follows opencode's loop (MIT, adapted, attributed
  in the commit) and keeps a stable per-call order for WP0.6's confirmation
  prompts, which ask about one call at a time. Parallel execution would
  trade that order — and WP0.6's upcoming per-call gating — for latency
  this MVP does not need.
- **Three distinct tool-failure shapes, per the `Tool` interface's own
  contract:** an unknown tool name or `Run` reporting `IsError` become an
  `agentapi.ToolResult` the model gets to react to and the turn continues;
  a panic inside `Run` is recovered into the same shape, so a tool's own
  bug cannot take the process down; only `Run` returning a non-nil error —
  the tool's machinery itself breaking, not the action failing — aborts the
  turn with no further `Stream` call.
- **New `agentapi.ErrorKind`: `ErrTurnLimit`.** Neither retryable nor
  fallbackable — repeating the same request hits the same cap, and it is
  not a provider failure, so another provider would not help either. Added
  to the `errorKindRouting` exhaustiveness table in
  `pkg/agentapi/error_test.go`; `WireVersion` was not bumped, since adding
  an `ErrorKind` value is additive.
- **Compaction is one attempt per iteration, in two internal steps, not two
  round-trips to the provider.** `Agent.compact` first drops every
  `PartThinking` part (cheap, and the contract already promises this
  happens first); only if the configured window says that was not enough
  does it fall through, within the same call, to summarizing the middle of
  the conversation via the injected `Compactor`. The loop only ever retries
  the actual model `Stream` call once after `compact` returns.
- **Occupancy tracking reuses the last call's real `Usage` when there is
  one:** `lastUsage.InputTokens` plus an estimate of the messages appended
  since, rather than re-estimating the whole history every iteration. The
  estimator itself (`chars/4`, no tokenizer, no cgo — see `estimate.go`)
  only has to be good enough to trigger compaction a bit before the real
  limit; the provider's own `ErrContextOverflow` remains the ground truth
  it anticipates, and `MaxContextTokens: 0` disables only the proactive
  check — the reactive path on `ErrContextOverflow` always applies.
- **The prefix/body/tail split never cuts a `ToolCall`/`RoleTool` pair
  apart.** The tail (from the last `RoleUser` message onward) is only
  shrunk, when it alone is heavier than half the window, to whole
  assistant+tool groups. A compaction summary is itself a `RoleSystem`
  message, so it is absorbed into the fixed prefix on any later compaction
  pass — this is what actually bounds a repeated compact-and-retry cycle:
  once a pass finds nothing left between prefix and tail, it fails fatally
  instead of retrying forever.
- **`cmd/hint`'s one-shot path now calls `agent.New(chat).RunTurn`** instead
  of `chat.Stream` directly, with no tools registered yet — WP0.5's
  built-in tools plug into the same `WithTools` option once they exist,
  with no further changes to `main.go`'s event loop. A non-terminal
  `EventError` (e.g. a failed proactive compaction) is remembered but only
  becomes the command's exit error if the turn's `EventTurnEnd` actually
  closes with `FinishError` — the turn is allowed to recover and finish
  normally otherwise.

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
