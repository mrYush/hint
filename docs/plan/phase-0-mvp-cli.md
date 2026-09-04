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
  this MVP does not need. WP0.11 generalizes this into an Agent-built
  series-parallel schedule (parallel **groups** and sequential **chains**);
  until that package lands, a batch is exactly one chain, in request order.
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

### WP0.5 — Built-in tools — **done**

Each tool: JSON Schema input, text output, timeout, large-output truncation
(head+tail with a marker).

- [x] `read_file(path, offset?, limit?)`
- [x] `write_file(path, content)`
- [x] `edit_file(path, old_str, new_str)` — unique search/replace (Aider editblock format)
- [x] `list_dir(path)`
- [x] `glob(pattern)`
- [x] `grep(pattern, path?)`
- [x] `bash(command, timeout?)` — confirmation-gated (WP0.6); built and
      tested here, registered in `cmd/hint` only once WP0.6 gates it
- [x] `todo(items[])` — current-task plan, shown to the user
- [x] Tool registry + schema generation for the provider layer
- [x] Per-tool unit tests incl. truncation and timeout behavior

Decisions taken while implementing, recorded here because later packages
depend on them:

- **Two packages, per `architecture.md`: `internal/tool` and
  `internal/tool/builtin`.** The parent holds everything that is not about
  a specific tool — `Registry`, `MustSchema`, the `WithLimits` decorator,
  `Root`, `DecodeArgs`/`Int` — and knows no tool by name, so the Phase 2
  MCP adapter gets the same registry, limits and confinement by being
  wrapped, not by copying code.
- **Timeout and truncation are one decorator, `tool.WithLimits`, not
  per-tool code.** It embeds the `agentapi.Tool` interface (the four
  descriptive methods pass through) and overrides `Run` to apply a
  `context.WithTimeout` and `Truncate` every text part. A tool that hits
  the cap is reported as an `IsError` result, not a machinery error: the
  model should see "timed out after 30s" and try something smaller, not
  have the turn aborted. `Unwrap()` exposes the inner tool, the same
  convention as `errors.Unwrap`. Defaults: 30 s, 50 kB (~12k tokens).
  `bash` opts out of the decorator's timeout (zero) because it enforces its
  own per-call one, which may legitimately exceed the default for a build.
- **Truncation keeps head and tail, cut on line boundaries, marker in the
  middle**, because the end of a command's output (the failing assertion,
  the summary line) is the most informative part. `tool.BoundedBuffer` is
  the streaming form for subprocess output — it retains head and a ring of
  the tail so a command printing gigabytes cannot exhaust memory — and a
  test pins that it renders byte-for-byte what `Truncate` would on the
  same bytes. (Idea from opencode and ollama, both MIT; rewritten.)
- **Schemas are generated from the argument structs with
  `invopop/jsonschema`** (chat decision; the alternatives were hand-written
  JSON literals, which drift from the struct, or a ~150-line reflection
  generator of our own). Required fields are those without `omitempty`,
  the same rule `encoding/json` applies, so schema and wire format cannot
  disagree; `$schema`, `$id`, `$ref`/`$defs` are stripped because provider
  requests embed the schema verbatim and resolve nothing. **Pinned to
  v0.13.0:** v0.14 requires Go 1.24, above the phase's 1.22 floor. It brings
  four indirect modules (`wk8/go-ordered-map`, `bahlo/generic-list-go`,
  `buger/jsonparser`, `mailru/easyjson`); raising the Go floor later
  unlocks the newer version. A test pins the complete generated document
  for a sample struct, since any drift changes every request.
- **`tool.Int` accepts a quoted number** (`"offset": "10"`) because small
  local models — the offline fallback profile — routinely quote integers,
  and rejecting an unambiguous call costs a round-trip. The advertised
  schema still says `integer` (the type implements `JSONSchema()`): the
  leniency is on the way in, not part of the contract.
- **Working-directory confinement is enforced now, by construction.**
  Every path a tool receives goes through `tool.Root.Resolve`, which checks
  the cleaned path lexically and then resolves the deepest existing
  ancestor's symlinks (dangling links by hand, since `EvalSymlinks` fails
  on them) and checks again, so `../..` and a link to `~/.ssh` both fail
  with `ErrOutsideRoot`. WP0.6's checkbox stays for the run-mode override;
  the mechanism is not optional per CONTRIBUTING. Go 1.22 has no `os.Root`,
  which is what ollama's tools use.
- **`glob` and `grep` shell out to ripgrep when it is installed and fall
  back to a pure-Go walk otherwise** (chat decision; the pure-Go path is
  the reference behaviour and the one every platform is guaranteed).
  Both prune the same set — hidden entries and `node_modules`, `vendor`,
  `target`, `dist`, `__pycache__` — which for rg means `--no-config`,
  `--glob '!.*'` and one `--glob '!name'` per directory, placed *after* the
  caller's own glob: rg gives later globs precedence and an explicit
  positive glob otherwise re-admits hidden files (found by test). rg
  additionally honours `.gitignore`; WP0.8 closes that gap in the walk.
  The pattern is always validated by the Go matcher first so an invalid
  glob or regex gets one message regardless of engine; the RE2 vs Rust
  regex dialect difference is accepted. Parity tests run when rg is in
  PATH. `grep` stops reading rg's output at the cap and cancels it rather
  than let a match-everything pattern scan the whole tree.
- **`edit_file` matches in four steps and never guesses.** Exact unique
  substring → the same with CRLF line endings when the file uses them →
  whole-line match ignoring a uniform indentation difference, re-indenting
  `new_str` to fit (an idea port of Aider's
  `replace_part_with_missing_leading_whitespace`, Apache 2.0) → failure
  with the closest lines rendered with visible whitespace (`→`, `·`).
  Any step that finds more than one candidate fails as ambiguous, listing
  line numbers; editing the wrong site is the one outcome worse than a
  failed call. An empty `old_str` creates a missing file or appends to an
  existing one, which is what Aider's edit-block format defines. There is
  no "read before you edit" bookkeeping (opencode's `lastRead` map): it is
  cross-tool state that belongs with a session (WP0.7), if anywhere.
- **`write_file` and `edit_file` write atomically** — temp file in the same
  directory, then rename, mode preserved — so a reader never sees a
  half-written file and a failure leaves the original intact. The result
  of `edit_file` echoes the changed region with line numbers so the model
  can verify without a second `read_file`.
- **`bash` runs a fresh `bash -c` (or `sh -c`) per call**, not opencode's
  persistent shell: shell state between calls is exactly the kind of
  hidden state a replayed session (WP0.7) cannot reproduce. The command
  gets its own process group, killed whole on timeout (ollama's approach,
  MIT), with `WaitDelay` so a detached child holding the pipes does not
  hang the call. Non-zero exit and timeout are `IsError` results carrying
  the output gathered so far; a missing shell is a machinery error.
  `timeout` is in seconds (default 60, max 600). There is no command
  blocklist — WP0.6's confirmation is the control, and a blocklist gives
  false confidence. Windows uses `cmd.exe /C` and plain `Process.Kill`.
- **`todo` keeps the plan in memory** (`builtin.TodoList`, mutex-guarded
  for WP0.11's parallel groups) and returns it rendered as a checklist;
  `cmd/hint` prints that result to stderr, which is how the plan is
  "shown to the user". No new `Event` kind: `EventToolEnd` already carries
  it, and a display concern does not justify touching the WP0.1 contract.
  Persistence is WP0.7's.
- **`list_dir` is recursive**, capped at 500 entries with a pointer at
  `glob`; `read_file` pages 2000 lines with a 1-based `offset` and
  `cat -n` numbering, refuses binaries by NUL probe and files over 50 MB.
- **`cmd/hint` registers `builtin.ReadOnly` only** (chat decision):
  `read_file`, `list_dir`, `glob`, `grep`, `todo`. `builtin.All` exists and
  is tested; wiring `write_file`/`edit_file`/`bash` before WP0.6 would make
  the binary run in what WP0.6 calls `--yolo` without the loud warning.
  Tool starts and failures are announced on stderr so stdout stays clean
  for pipes. Acceptance scenario 1 (`list_dir`/`read_file` without manual
  context assembly) is therefore live; scenario 2 waits for WP0.6.
- **Registry validates at registration**, not at request time: a
  malformed schema, unknown class or duplicate name fails `hint` at
  startup instead of as a provider 400 mid-turn. `Tools()`/`Schemas()` are
  sorted by name, matching `agent.schemas()`'s determinism rule.
- Crush was consulted for the todo and whitespace-hint patterns only. Its
  tree in `agents-research/` is now 0BSD-licensed, not FSL as
  CONTRIBUTING records; no code was copied either way — updating the rule
  is a separate docs decision.

### WP0.6 — Permission system

References — Crush, Codex CLI.

- [ ] Action classes: `read` (no confirmation), `write` (confirm with diff), `execute` (confirm with command shown)
- [ ] Run modes: `--ask` (default), `--auto-edit` (writes silent, execute asks), `--yolo` (nothing asks, loud warning)
- [ ] Session allow-list of remembered grants ("always allow `go test`")
- [ ] File tools confined to the working directory by default (the
      mechanism, `tool.Root`, landed in WP0.5; this covers the run-mode
      policy around it)
- [ ] Register `write_file`, `edit_file` and `bash` (`builtin.All`) in
      `cmd/hint` behind the permission layer — WP0.5 deliberately wired
      only `builtin.ReadOnly`
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

### WP0.11 — Tool schedule: groups and chains

The Agent — not the user, not a second planner round-trip — decides the
order of a turn's tool-call batch. Two composable nodes cover arbitrary
scenarios:

- **Group (`Par`)** — siblings start together; the loop waits for the
  whole group before continuing.
- **Chain (`Seq`)** — each child starts only after the previous child
  (itself a group, a chain, or a single call) has finished.

A schedule is a series-parallel tree of those nodes. Nesting is the point:
`Seq(Par(read_a, read_b), edit_file, Par(test, lint))` is one batch, not
four extra model turns. A missing or invalid schedule degrades to today's
WP0.4 chain (request order) so the loop never blocks on a planner.

Not on the `v0.1-alpha` critical path (WP0.1–WP0.7). Sequential execution
remains the Phase 0 acceptance default. Depends on WP0.5 (something to
schedule) and WP0.6 (gating must stay per-call and ordered).

- [ ] `Schedule` as a closed node tree (`Seq` / `Par` / `Call`) inside
      `internal/agent` — the loop walks it; `runTools` becomes the `Call`
      leaf
- [ ] Agent-built schedule from the model's `ToolCall` list: `ClassRead`
      calls with disjoint paths may share a `Par`; `ClassWrite` /
      `ClassExecute` and any call that shares a path with a sibling become
      `Seq` (never a gated call inside a `Par`)
- [ ] Deterministic result order: within a `Par`, results append in the
      model's request order, not finish order — session replay (WP0.7) and
      WP0.6 prompts stay stable
- [ ] Existing three tool-failure shapes still apply: `IsError` / unknown /
      panic of one `Par` leaf does not cancel siblings already started;
      a machinery `error` cancels the group's `ctx` and aborts the turn
- [ ] Optional additive `ToolCall.After []string` (call IDs) so the model
      can tighten a dependency the heuristic cannot see; unknown IDs are
      ignored and the conservative schedule wins. No `WireVersion` bump
- [ ] Loop unit tests: a `Par` of two reads; a `Seq` of write-then-read;
      a nested `Seq(Par(...), Call)`; fallback to a plain chain; a gated
      call never enters a `Par`

Decisions recorded now, because they constrain WP0.6's confirmation UI
and the `ToolCall` wire type:

- **Series-parallel tree, not a free DAG.** A group and a chain, nested,
  express the scenarios we want (fan-out reads, then a write, then a
  fan-out of checks) without a general topological scheduler, diamond
  joins, or a second model call that emits a graph. A full `depends_on`
  DAG is the more powerful alternative: it can say "C waits for A and B"
  without wrapping A+B in a `Par`. The cost is a real topo-sort, cycle
  detection, and a prompt/schema the model will get wrong; we would still
  need a conservative fallback. Start with the tree; promote to a DAG
  only if a real turn cannot be expressed as nested groups and chains.
- **The Agent builds the tree; the model does not have to.** Most
  providers have no schedule field, and asking the model to author a
  graph is a second source of invalid plans. The model's job stays
  "which tools, with which arguments". The loop's job is "what can
  overlap". `ToolCall.After` is an optional hint, not the source of
  truth: a missing hint must still produce a correct conservative
  schedule. The opposite (model-authored graph only) would make every
  provider dialect and every small local model a scheduler bug.
- **Gated calls stay a chain.** WP0.6 asks about one call at a time.
  Putting `ClassWrite` / `ClassExecute` in a `Par` would force either a
  parallel confirmation UI or starting work the user has not approved.
  Independent `ClassRead` calls are the only things that fan out. The
  alternative — collect every grant, then start the group — costs a
  more complex permission client for no MVP win: writes are rarely
  independent of each other.
- **Finish order is not append order.** A `Par` that appended results as
  they completed would make session JSONL and the next `Stream` history
  non-deterministic across runs of the same batch. Results are gathered,
  then appended in request order once the group joins. The cost is a
  few milliseconds of hold-back after the last sibling finishes; the
  win is a replayable history.

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

WP0.1 → WP0.2 → WP0.3 → WP0.4 → WP0.5 (all tools built; only read-only
ones wired) → WP0.6 (wires write/execute tools) → WP0.7 → WP0.8 → WP0.9 →
WP0.10.
WP0.11 sits after WP0.6 (it needs per-call gating to exist) and is not
required for `v0.1-alpha`.
Tag `v0.1-alpha` once WP0.1–WP0.7 land; `v0.1` after acceptance criteria pass.
