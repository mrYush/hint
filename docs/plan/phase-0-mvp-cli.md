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
  have the turn aborted. The decorator's own timer is told apart from a
  deadline or cancel inherited from the caller by `context.Cause`: the
  timer is created with `WithTimeoutCause` and a sentinel, so a caller's
  5 s budget expiring first passes through as the caller's error instead
  of being relabelled "timed out after 30s" (review finding, pinned by
  test). `Unwrap()` exposes the inner tool, the same convention as
  `errors.Unwrap`. Defaults: 30 s, 50 kB (~12k tokens).
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
  the mechanism is not optional per CONTRIBUTING. **Known limit: the check
  is not atomic with the file operation.** Go 1.22 has no `os.Root` (Go
  1.24; ollama's tools use it), which opens every path component with
  `openat` and so leaves no window. `Root.Resolve` runs before
  `os.Open`/`os.Rename`, so a symlink swapped in between (TOCTOU) escapes
  the root. Acceptable for a local single-user CLI, where the only other
  writer to the tree is the user; not acceptable once the core serves
  several users on one host. Recorded as risk R8; migrate to `os.Root`
  when the Go floor reaches 1.24, the `Root` type is the seam.
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
  than let a match-everything pattern scan the whole tree. `grep`'s
  `include` is anchored to the working directory in both engines — a
  slash-free pattern selects by file name anywhere, `cmd/**/*.go` by path
  from the root — which for rg means running it with the root as cwd and
  a relative target, since `--glob` patterns with a slash are anchored to
  rg's cwd (review finding: the walk matched on the base name only).
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
  directory, fsync, then rename, mode preserved — so a reader never sees a
  half-written file and a crash cannot leave an empty one. A symlink at
  the target is written *through*, not replaced: rename alone would swap
  a link the user placed on purpose for a plain file (review finding);
  `Root.Resolve` has already confirmed the link's target is inside the
  root, so following it stays confined. The result of `edit_file` echoes
  the changed region with line numbers so the model can verify without a
  second `read_file`.
- **`bash` runs a fresh `bash -c` (or `sh -c`) per call**, not opencode's
  persistent shell: shell state between calls is exactly the kind of
  hidden state a replayed session (WP0.7) cannot reproduce. The command
  gets its own process group, killed whole on timeout (ollama's approach,
  MIT), with `WaitDelay` so a detached child holding the pipes does not
  hang the call. Non-zero exit and timeout are `IsError` results carrying
  the output gathered so far; a missing shell is a machinery error.
  `timeout` is in seconds (default 60, max 600). **`bash` is not confined
  to the working directory and cannot be**: `cmd.Dir` only sets where the
  shell starts, and `cd / && cat ~/.ssh/id_rsa` runs as written. The
  confinement `tool.Root` gives the file tools does not extend to a
  subprocess. There is no command blocklist either — WP0.6's confirmation,
  with the command shown, is the control, and a blocklist gives false
  confidence. Until WP0.6 the tool is simply not registered. Windows uses
  `cmd.exe /C` and plain `Process.Kill`.
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
  The wiring itself is pinned by `cmd/hint/main_test.go` (every registered
  tool is `ClassRead`), so the switch to `builtin.All` has to land with
  the gating, not before it.
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

### WP0.6 — Permission system — **done**

References — Crush, Codex CLI.

- [x] Action classes: `read` (no confirmation), `write` (confirm with diff), `execute` (confirm with command shown)
- [x] Run modes: `--ask` (default), `--auto-edit` (writes silent, execute asks), `--yolo` (nothing asks, loud warning)
- [x] Session allow-list of remembered grants ("always allow `go test`")
- [x] File tools confined to the working directory by default (the
      mechanism, `tool.Root`, landed in WP0.5; this covers the run-mode
      policy around it)
- [x] Register `write_file`, `edit_file` and `bash` (`builtin.All`) in
      `cmd/hint` behind the permission layer — WP0.5 deliberately wired
      only `builtin.ReadOnly`. `bash` is the one tool `tool.Root` cannot
      confine (a subprocess goes where it likes), so its prompt must show
      the full command and `--auto-edit` must keep asking for it
- [x] Classify a tool's `error` in `internal/agent.runTools` before
      wrapping it: a `context.Canceled` / `DeadlineExceeded` that a tool
      passes through (the WP0.5 limits decorator forwards a caller's
      cancel or deadline untouched) currently becomes `ErrUnknown` "tool
      machinery failed" and ends the turn with `FinishError` instead of
      `FinishCanceled`. Route it through `agentapi.KindOf` so Ctrl-C
      during a running tool ends the turn the same way it does between
      tools — a denied permission (WP0.6's own new outcome) needs the
      same seam, so fix both together
- [x] Tests: every class × every mode, allow-list persistence within a session, path-escape attempts

Decisions taken while implementing, recorded here because later packages
depend on them:

- **Three packages share the work, by dependency direction.**
  `internal/permission` is pure policy over an `agentapi.PermissionRequest`
  — `Mode`, the allow-list, the `Prompter` — and knows no tool by name.
  The preview a prompt shows (`tool.Describer`, `tool.Describe`) is tool
  machinery and lives in `internal/tool` next to `WithLimits`, so
  `builtin` implements a preview without importing the policy package,
  and an MCP-provided tool (Phase 2) that has no preview is described by
  its name and raw arguments. The diff itself is `internal/diff`, a
  hand-written Myers line diff with unified rendering (the stdlib has
  none and CONTRIBUTING prefers no new module). `internal/agent` imports
  neither: it declares the consumer-side `Authorizer` interface
  (`Review` + `Authorize`) that `permission.Gate` satisfies, and a test
  stands in a fake.
- **The prompt is emitted before it blocks, and only when it is really
  asked.** `Review` decides — mode first, then the allow-list — and only
  builds the preview (a diff means reading the file) when somebody will
  look at it; the loop emits `EventPermission` and then blocks in
  `Authorize`. Read-class calls, calls a mode silences, and calls an
  earlier "always" covers are never announced. `cmd/hint` ignores the
  event because the gate's `ReaderPrompter` already drew the question on
  stderr; the event still travels the stream for Phase 1's RPC client,
  which will answer it instead. No `PermissionResponse` wire type was
  added — that is Phase 1's, with the transport that carries it.
- **"Always" exists for commands only, and only as `program subcommand`.**
  A grant is the program plus the word after it, for programs whose first
  argument names the action (`go`, `git`, `npm`, `cargo`, `docker`,
  `kubectl`, `pip`, …): `go test ./...` grants `go test`, which then
  covers `go test ./internal/...` but not `go testify` or `go build`. A
  later command matches only if the text after the scope carries no shell
  metacharacter (`; & | < > $ \` ( )` or a newline), so `go test; rm -rf ~`
  is not covered. Nothing else is granted: not a bare program name
  (`sudo`, `bash -c`, `python -c`, `xargs`, `env` run whatever follows,
  and `curl` or `make` take their whole behaviour from arguments a prefix
  does not look at), not a program whose next word is a flag, not a
  command that starts with `VAR=value`. Those are confirmed one at a time
  — a list of programs safe to grant by name is never complete, so there
  is none. Review finding: the first cut scoped unknown programs to their
  first word, which made `sudo x` cover `sudo rm -rf /`. The remaining,
  accepted limit of a prefix grant: `go test` covers `go test -exec
  'rm -rf /'`; the user who answers "always" trusts the program, not each
  future argument. Writes get no "always" at all: a per-file grant would
  hide the next diff to that file, and a run-wide one is `--auto-edit`
  with a keystroke, which exists as the flag where it is visible. The
  list lives in memory for the process; persisting grants is a WP0.7
  decision, not an oversight.
- **The stdin prompter owns stdin.** A blocked read cannot be interrupted,
  so `ReaderPrompter` reads lines on one goroutine for the life of the
  process and a prompt cancelled by Ctrl-C leaves it parked on the next
  line; a line typed while no prompt was waiting is discarded before the
  next question, so it cannot be taken as its answer. Consequence for
  WP0.9: the REPL must take its input through the same reader, not open
  stdin beside it. The prompt shows at most 16 kB of a diff
  (`tool.Truncate`, head and tail); the `PermissionRequest` on the event
  stream stays whole — how to show a long `Detail` is the client's call,
  per the contract.
- **Fail closed.** No prompter, a prompter error with the context still
  alive, stdin at EOF (`hint … < /dev/null`, a closed pipe), or an empty
  answer all deny; only `y`/`yes`/`a`/`always` allow. `cmd/hint` says up
  front when stdin is not a terminal and what will be denied, and
  `--yolo` prints a loud warning before the first request. The model
  reads a denial as an error result telling it not to retry the same
  action unchanged; the turn continues.
- **Preview cost.** `Gate.Review` checks the mode before building a
  preview and the allow-list after, because a command grant needs the
  command text. Only execute-class calls can be granted and their preview
  is the command itself, so no diff is ever computed for a call a grant
  then silences.
- **No mode lifts `tool.Root`.** `--yolo` stops asking; it does not let
  `write_file ../secret` through. Pinned by `TestYoloDoesNotLiftRoot`.
- **Cancel is classified by the context, not by the error's type.**
  `runTools` now treats any error — from a tool, or from the prompt —
  with `ctx.Err() != nil` as a cancel: the interrupted call and every
  call after it get a `canceled` result and the turn ends
  `FinishCanceled`, exactly as a cancel between calls did. An error with
  the context alive is a machinery failure and aborts the turn, wrapped
  with `KindOf(err)` so a classified `*agentapi.Error` keeps its kind
  instead of flattening to `ErrUnknown`.
- **`edit_file`'s preview and edit share one plan.** Run and Describe
  both start from `editFile.plan`, which resolves the path and computes
  the new content without writing, so the diff the user confirms and the
  bytes that land cannot disagree; the file changing in between is the
  TOCTOU limit WP0.5 already accepts. `write_file` previews a binary or
  oversized target with a size-only summary rather than a diff.
- **Modes are flags, not config.** `--ask`/`--auto-edit`/`--yolo` are
  mutually exclusive cobra flags and not part of `config.Flags`: a run
  policy is not a provider profile. A config or environment default for
  the mode is a possible later addition, not part of this package.
- Crush and Codex were consulted for the approval-mode vocabulary and
  the grant/deny shape (pattern references only); the prompter-plus-scope
  idea follows ollama's `ApprovalPrompter` (MIT), rewritten around
  `agentapi.PermissionRequest`. No code was copied.

### WP0.7 — Sessions — **done**

Reference — Pi session format.

- [x] Append-only JSONL, one file per session:
      `~/.local/share/hint/sessions/<project-hash>/<created>_<id>.jsonl`
      (`$XDG_DATA_HOME` respected; the creation timestamp in the name makes
      a plain listing sort by age, the hash is SHA-256 of the resolved
      working directory, the header records the path in clear)
- [x] Record kinds: `session` (header), `message` (one `agentapi.Message`
      verbatim — user/assistant/tool/system are its roles, and a tool call
      travels inside its assistant message), `compaction` (checkpoint)
- [x] Commands: `hint` (new session), `hint -c` (continue latest in this directory), `hint -r` (pick from a list), `hint --no-session`
- [x] Golden tests for the session format (backward-compat gate)

Decisions taken while implementing, recorded here because later packages
depend on them:

- **A record wraps an `agentapi.Message` verbatim** (chat decision). The
  checklist's kind list — user/assistant/tool_call/tool_result/system —
  is the message's role, not a record kind of its own: splitting a tool
  call out of the assistant message that made it would force every reader
  to reassemble what `Message.Validate` already guarantees, and the
  contract promises sessions are persisted in its terms. The file is
  therefore a `Record{Kind, At, …}` tagged union in the pkg/agentapi
  style, round-tripping through `encoding/json` with no custom
  marshalling; `session.FormatVersion` (1) and `agentapi.WireVersion`
  both sit in the header.
- **A compaction is a checkpoint, and `agentapi.Compaction` gained
  `History`** (chat decision; Pi's `retainedTail`). The event carried
  only the summary and a count, from which the post-compaction history
  cannot be rebuilt — the replaced region is not contiguous once the
  tail has been shrunk, and the split is the loop's private policy. The
  loop now fills `History` with the exact messages the next request
  carries (pinned against the fake provider's next request), the
  `compaction` record stores it, and loading a file is "the last
  checkpoint, then every message after it". The field is optional on the
  wire, so `WireVersion` stays 1; the cost is the retained tail written
  twice, which compaction's rarity and the window bound make cheap. The
  alternatives — re-compacting on every resume (an LLM call per `-c` of
  a long session, the earlier summary thrown away) or exporting the
  split algorithm for the loader to replay (a session then depends on
  the estimator's arithmetic) — were rejected.
- **The system prompt is not stored** (chat decision, as Pi does).
  WP0.8 derives it from HINT.md and the project, both of which change
  between runs; a frozen copy would go stale. `cmd/hint` prepends a fresh
  preamble every run and tells `session.Recorder` its length, and the
  Recorder strips exactly that many leading messages from a checkpoint's
  History — safe because compaction keeps the leading system run
  verbatim and inserts its summary after it. Earlier summaries are
  `RoleSystem` too and stay in the file; the preamble count, not the
  role, is what tells them apart.
- **"Always" grants stay in the process** (chat decision). `hint -c`
  starts with an empty allow-list: a grant given yesterday must not run a
  command silently today. A `grant` record kind can be added later
  without a format bump.
- **`internal/console.LineReader` is the one reader of stdin.** WP0.6's
  prompter owned stdin on a goroutine; the session picker needs the same
  stream before the first prompt, and two `bufio.Reader`s on one
  descriptor would race for bytes. The reader moved to its own package —
  `DiscardPending` and a sticky terminal error included — and
  `permission.NewLinePrompter` and `session.Choose` share one instance
  built in `cmd/hint`. WP0.9's REPL takes its input through it as well.
- **A torn tail is truncated on open, like a journal.** A crash mid-write
  leaves a partial last line; readers ignore it with a warning, and
  `Store.Open` truncates the file back to the last complete record
  before appending, since the bytes can never parse and the next record
  would otherwise garble with them. That is the only time bytes are
  removed from a session file. `List` is read-only and skips a file it
  cannot read, so one damaged session does not hide the others.
- **Forward compatibility follows the wire types' rule.** An unknown
  record kind, or a message carrying an unknown part kind
  (`agentapi.ErrUnknownKind`), is skipped with a warning the CLI prints
  when it continues the session; a header with a newer `FormatVersion`
  is refused with a message naming both versions; a malformed complete
  line is an error, not a skip — silently dropping real data would be
  worse than failing.
- **Dangling tool calls are repaired on read.** A run interrupted
  mid-batch leaves an assistant message whose tool calls have no
  results (a machinery abort writes none for the failing call), and
  every supported dialect rejects that history. `Session.Messages`
  synthesizes an error result saying no result was recorded; it is a
  view — `Len` still counts the file's records — so the file is never
  rewritten to hide the interruption.
- **Recording is best effort once the answer streams.** Writing the
  user message fails the run before the first request (a read-only
  disk is better learned about now); a later write failure sticks in
  the Recorder and is reported once at the end, with the exit status
  still the turn's.
- **"Latest" is modification time**, not creation: `-c` continues the
  conversation that last grew. The picker lists at most 20, newest
  first, with the first prompt's first line, and accepts a number or an
  id prefix (older sessions stay reachable by id). It refuses — rather
  than silently starting a new session — when nobody answers, while
  `-c` or `-r` in a directory with no sessions starts one with a
  notice.
- Known limits, deliberately not addressed: two concurrent `hint -c` in
  one directory both append to the same file (a single-user CLI; a lock
  is a Phase 1 concern with the core as a service); the `todo` list is
  not restored on resume (the model resends the full list); `--session
  <id>` and `hint sessions` listing are WP0.9's CLI surface.
- Incidental: `cmd/hint` printed a run-time error twice (cobra and
  main); `SilenceErrors` is now set alongside `SilenceUsage` inside
  `RunE`, so flag errors keep cobra's usage text and everything else is
  printed once.

### WP0.8 — Project context — **done**

References — codex (`agents_md.rs`: root-bounded walk, shared byte
budget), openhuman (bounded read), gemini-cli (`getFolderStructure`: item
cap), Crush (prompt placement; pattern reference only, FSL).

- [x] Read `HINT.md` (plus `AGENTS.md`/`CLAUDE.md` as compatible names) from the project root into the system prompt
- [x] Auto-context of the current directory — as today, but with a size limit and `.gitignore` filtering

Decisions taken while implementing, recorded here because later packages
depend on them:

- **`internal/project` replaces `internal/context`** (the layout in
  `architecture.md` already named it). The old package was fifty lines
  of `os.ReadDir`; nothing of it survives, and the `dirctx` import alias
  that dodged `context.Context` goes with it.
- **`.gitignore` is git's job: `git ls-files --cached --others
  --exclude-standard`** (chat decision, the same shape as WP0.5's
  ripgrep: an external tool as the fast, exact path and pure Go as the
  fallback). Alternatives priced and rejected: `go-git`'s gitignore
  package plus a hand-built hierarchical matcher (Crush's route — ~200
  lines and a large module for one subpackage), `sabhiram/go-gitignore`
  (unmaintained, known negation bugs, used by nobody in the research
  tree), a home-grown parser (gitignore semantics — anchoring, `**`,
  negation, dir-only — are deceptively deep). Cost accepted: outside a
  repository, or without git installed, only the hidden-and-dependency
  rule applies; a process is spawned per listing, which the cold-start
  budget absorbs. `project.Rules` is the one seam: `cmd/hint` builds
  the overview through it and `internal/tool/builtin` gives it to the
  pure-Go walks of `list_dir`, `glob` and `grep` (`WithGit`), which
  closes the difference WP0.5 recorded between the walks and ripgrep.
  A `git ls-files` listing is turned into an `Ignorer` — kept if git
  listed the path or something beneath it, everything under a listed
  path kept wholesale so a submodule's contents are not hidden — and
  `project.Basic` (hidden entries, `node_modules`/`vendor`/`target`/
  `dist`/`__pycache__`) still applies on top, so a committed `vendor/`
  is pruned as before.
- **Instruction files are searched from the git root down to the
  working directory, outermost first** (chat decision; codex, gemini-cli
  and goose do the same, opencode-TS and pi-mono too). In each
  directory the first of `HINT.md` > `AGENTS.md` > `CLAUDE.md` wins and
  the others are not read, so a project keeping both is not told the
  same thing twice (opencode-TS's argument). The root is the nearest
  ancestor with a `.git` entry (directory or worktree file); without
  one only the working directory is searched. A global
  `~/.config/hint/HINT.md` was deferred to WP0.9 with the other flags.
- **One 32 KiB budget for all instruction files, shared, truncating**
  (chat decision; codex's number and policy). The budget is shared
  rather than per file because it is the total that competes with the
  conversation for the context window, and the preamble is the one
  part of a request that compaction never touches. A file that
  overflows is cut at the remaining budget and marked; later files are
  skipped; each cut or skip is a stderr warning. The read itself is
  bounded (`io.LimitReader`, openhuman's detail), so a symlinked log
  is never loaded to be discarded. claurst's per-file skip was
  rejected because one large file would silently leave a project with
  no instructions at all. Whitespace-only files cost nothing.
- **The overview is a `project.Overview` strategy; `Tree{Depth: 2,
  MaxEntries: 100}` is the default** and `None` exists. gemini-cli's
  200-item cap and qwen-code's 20 bracket the number; two levels is
  what a developer glances at. Truncation says so in the listing so
  the model reaches for `list_dir`. The interface, rather than a
  constant, is deliberate: the user's intent is to let the *agent*
  choose the overview shape later from the conversation history
  (which files it has been reading, what the user keeps asking
  about). That is a Phase 1–2 item once there is a per-turn hook to
  hang it on; it is not scheduled here.
- **Placement stays one `SystemMessage`.** codex sends instructions as
  a user-role message so they can be swapped mid-session without
  invalidating a cached system prefix; `hint` rebuilds the preamble
  every run and never stores it (WP0.7), so the swap has nothing to
  buy. `session.NewRecorder(sess, len(preamble))` is unchanged.
  Instruction files render as `<project_instructions><file path=…>`
  blocks after the overview, with a line saying the nearest file wins.
  They are the one file content placed in the system prompt rather
  than quoted as untrusted tool output, because they are the user's
  own words to the agent.
- **Limits are functional options on `project.Load`** (`WithGit`,
  `WithInstructionBudget`, `WithInstructionNames`, `WithOverview`)
  with package constants as defaults, so WP0.9's flags and config keys
  attach without changing a signature. The overview walks an `fs.FS`
  (`os.DirFS` in production, `fstest.MapFS` in tests); instruction
  files are read through `os` directly, since walking up past a
  working directory does not fit a rooted `fs.FS` cleanly.
- **Failures degrade to warnings, printed once by `cmd/hint`.** Only a
  working directory that cannot be read is an error. A git failure
  that is not "not a repository" (a corrupt index, a killed process)
  is reported by `Load`; the tools that meet the same failure fall back
  to `Basic` silently rather than report it on every call.
- Known limits, deliberately not addressed: `.hintignore` (Crush has
  `.crushignore`); `@import` expansion inside instruction files
  (gemini-cli, goose, claurst); case-insensitive filesystems where
  `HINT.md` and `hint.md` are one file are not special-cased. What
  happens when the instruction files do not fit the budget — today a
  blind cut, with the nearest files the first to be dropped — and how
  the model reaches the text that was cut, is worked out in
  [WP0.12](#wp012--instructions-beyond-the-budget); just-in-time loading
  of a subdirectory's instructions lives there too.

### WP0.9 — Run modes and CLI surface — **done**

Breaking change accepted (see PLAN.md): bare `hint` = interactive REPL.

- [x] Interactive REPL (plain readline loop, no TUI framework)
- [x] One-shot: `hint -p "question"` — exits after the answer; `--output json` for scripts
- [x] Compatibility alias: `hint "question"` (positional args) behaves as one-shot, prints a one-line migration hint
- [x] `--debug` logging with key masking
- [x] The session surface WP0.7 left here: `--session <id>` (a full id or
      a unique prefix) and `hint sessions`
- [x] `hint models` (left here by WP0.3): Ollama's native listing for an
      ollama profile, `GET /models` of the dialect for every other kind
- [x] WP0.8's limits as flags and config keys: `--instruction-budget` /
      `instructions.budget`, `--overview-depth` / `overview.depth` (0 for
      none), `--overview-entries` / `overview.max_entries`, each also as a
      `HINT_*` variable
- [x] Global `~/.config/hint/HINT.md`, read before the repository's files
      under the same budget
- [x] Profile `context_window` (also `HINT_CONTEXT_WINDOW`,
      `--context-window`) sizes the agent's proactive compaction — the
      input WP0.12's rung 0 needs

Decisions taken while implementing, recorded here because later packages
depend on them:

- **The REPL is a line loop over `console.LineReader`, and it lives in
  `cmd/hint`.** No readline library: the terminal's cooked mode gives
  backspace and Ctrl-U, and a real line editor with history is what
  Phase 1's TUI brings (`--plain` keeps this loop). Each line is one
  question; `/help`, `/exit` and `/quit` are the only commands, and an
  unknown `/word` is refused rather than sent, so a mistyped command does
  not become a prompt. Answers go to stdout and everything else — the
  `> ` prompt, notices, permission questions — to stderr, so
  `hint > transcript` captures the answers alone, as it does for a
  one-shot. The loop cannot live in `internal/console` as the layout
  once suggested: it needs the agent, the session and the permission
  gate, and `console` is the leaf those import. WP1.3 replaces it with
  an RPC client anyway.
- **Bare `hint` without a terminal is an error, not a pipe reader.**
  `echo q | hint` could have read stdin as the question, but the same
  stream is the permission prompter's, and a pipe cannot answer; the
  error names `-p`. A one-shot with a non-terminal stdin still runs and
  says up front what will be denied (WP0.6's notice).
- **Ctrl-C.** A one-shot cancels the run as before. The REPL takes
  SIGINT on its own channel: at the prompt it prints how to leave
  (Ctrl-D or `/exit`), during a turn it cancels that turn's context
  only. The session, the run's `always` grants and the line reader all
  survive; the reader is left parked on the next line as its contract
  promises, and the terminal has already discarded the half-typed line.
  A SIGINT that arrives between turns is dropped before the next prompt
  so it cannot cancel the question that follows. SIGTERM ends both modes.
- **`session.Recorder` is the run's conversation.** It seeds itself
  from the continued session, keeps the conversation in memory
  (`Messages`, `Append`) and journals to disk. The REPL asks it for the
  history before every turn — one code path with and without a session
  file — and a disk failure mid-session degrades to a warning printed
  once rather than a lost conversation. The alternative, re-reading
  `Session.Messages()` each turn, diverges from the run the moment a
  write fails. `Append` still fails the turn before the first request
  when the user message cannot be written at all (WP0.7's rule).
- **`--output json` is one object, printed after the turn**: `answer`
  (every text fragment of the turn, joined — what the text output
  streamed), `finish_reason`, `session_id`, `usage`, `error`. A failed
  turn still prints the object, with `error` and `finish_reason:
  "error"`, and exits non-zero, so a script can parse stdout regardless
  of the outcome. A JSON Lines event stream was rejected: that is the
  Phase 1 RPC surface, and the events on it are already `agentapi.Event`;
  `--output` is refused without `-p`, since a session has no one answer.
- **The debug trace is a file per run** under `$XDG_STATE_HOME/hint/log/`
  (default `~/.local/state/hint/log/`), named by time and pid, its path
  printed on stderr at start; `--debug` or `HINT_DEBUG` turns it on.
  Contents: the arguments, the working directory, the profiles (masked by
  `Profile.String`), the mode and window, the system prompt, every
  request body as the client sends it (its own line, key masked by
  `config.Mask`), the response status, every complete message as JSON,
  tool starts with their arguments, tool ends with size and first line,
  permission requests, compactions, errors and usage. Every line passes
  through `config.Redact` with `Config.Secrets()` inside an `io.Writer`
  decorator (`internal/debuglog`), so a secret quoted in a body is
  scrubbed even where a client forgot to mask. Plain timestamped text,
  not `slog`: the file is read by a person chasing a bad answer, and a
  request body reads better unescaped; structured logs come with the
  core-as-a-service, where a line has more than one consumer. Failing to
  open the file is a warning, not a refusal. The file and its directory
  are owner-only: the trace holds the user's prompts and code.
- **Numeric flags are strings.** Flag defaults must stay empty (WP0.2's
  rule) and 0 is a legal `--overview-depth`; a cobra int flag cannot tell
  unset from 0. The loader parses them: a bad flag is an error naming the
  flag, a bad variable a warning that leaves the value alone (the
  `HINT_DEBUG` rule), and a negative value is refused wherever it came
  from, a file included. The YAML side uses `*int` for the same reason,
  so `overview: {depth: 0}` in a project file survives the merge with a
  global file that says 3.
- **Config owns the limit defaults; `project` keeps its constants.**
  `config` is loaded before anything else and must not import `project`,
  so the three numbers exist twice and `internal/project`'s test asserts
  they are equal. The resolved `Config` always carries final values (the
  loader applies defaults, per WP0.2), so `cmd/hint` never knows a
  default and `project.Load` is always given explicit options.
- **The global `HINT.md` is the outermost layer**, read first under the
  shared budget and rendered with the same `<file path=…>` block, so the
  model sees where a rule came from. It lives beside the config
  (`config.GlobalDir`, honouring `XDG_CONFIG_HOME`); a missing file
  costs nothing, and a run inside `~/.config/hint` itself lists it once.
- **`context_window` sizes the default profile's compaction only.**
  `agent.DefaultLimits()` is exported so the CLI overrides one field. The
  fallback profile keeps the same limits; a smaller window there is
  caught by its own `ErrContextOverflow` and WP0.4's reactive compaction.
  WP0.12's rung 0 takes the same number for the instruction budget.
- **`hint models` for the dialect is `GET /models`** with the profile's
  key, failures classified as for a chat request (`ErrAuth`,
  `ErrNetwork`), the URL derived from the base the way the completions
  URL is; an ollama profile uses the native listing, which knows sizes
  and pull dates. `provider.Models` is the composition root's adapter
  over the two shapes. Output is one model per line, name first, so
  `grep` and `cut -f1` work; the count and the endpoint go to stderr.
- Known limits, deliberately not addressed: no line editing or history
  beyond cooked mode, no multi-line questions, no `/new`, `/mode` or
  `/compact` commands (Phase 1's TUI); piped stdin is not read as a
  question; `hint sessions` and `--session` see the working directory's
  sessions only; the trace directory is never pruned.

### WP0.10 — CI, release, distribution

- [x] GitHub Actions: build matrix {darwin,linux,windows} × {amd64,arm64}, test + lint (golangci-lint) gates
- [x] goreleaser config; binaries attached to GitHub Releases
- [x] Homebrew tap
- [ ] Raspberry Pi smoke run of the acceptance scenarios on linux/arm64
- [x] Update README for the new CLI surface

Decisions (2026-09-07):

- **Tests run on Linux only; the other targets are compiled, not tested.**
  One `ubuntu` job runs `gofmt`, `golangci-lint`, `go vet` and
  `go test -race` with ripgrep installed; a six-way matrix cross-compiles
  every release target with `CGO_ENABLED=0`. Native macOS and Windows
  runners would test the `shell_windows.go` and tty paths for real, at
  three to ten times the runner minutes and a ripgrep install per OS;
  the Pi acceptance run covers the one non-Linux-amd64 platform the
  phase promises. Revisit when a platform-specific bug slips through.
- **Homebrew cask, not formula.** goreleaser is retiring its `brews`
  block in favour of `homebrew_casks`, and a prebuilt binary is what a
  cask is for. The cask depends on the `ripgrep` formula and carries the
  quarantine-lifting post-install hook goreleaser documents for unsigned
  binaries. Signing and notarization stay out of scope for `v0.1`.
- **The tap push is a separate secret.** `HOMEBREW_TAP_TOKEN` must be a
  token with write access to `mrYush/homebrew-hint`; the workflow's own
  `GITHUB_TOKEN` is scoped to this repository. Pre-releases skip the tap
  (`skip_upload: auto`), so `v0.1-alpha` needs no token; the first final
  tag does.
- **Version from two sources.** goreleaser sets `main.version`, `commit`
  and `date` through `-ldflags -X`; a `go install ...@vX.Y.Z` or a plain
  `go build` in a checkout gets the same shape from
  `debug.ReadBuildInfo` (module version, `vcs.revision`, `vcs.time`,
  `-dirty`). The flags win when set.

### WP0.11 — Tool schedule: groups and chains — **done**

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

- [x] `Schedule` as a closed node tree (`Seq` / `Par` / `Call`) inside
      `internal/agent` — the loop walks it; `runTools` becomes the `Call`
      leaf (`schedule.go`: `node` sealed by an unexported marker method,
      `seq` / `par` / `call`; `tools.go`: a `batch` walks the tree)
- [x] Agent-built schedule from the model's `ToolCall` list: consecutive
      `ClassRead` calls share a `Par`; `ClassWrite` / `ClassExecute` and
      unknown tools are steps of their own (never a gated call inside a
      `Par`). The path rule was dropped — see the decisions below
- [x] Deterministic result order: within a `Par`, results append in the
      model's request order, not finish order — session replay (WP0.7) and
      WP0.6 prompts stay stable
- [x] Existing three tool-failure shapes still apply: `IsError` / unknown /
      panic of one `Par` leaf does not cancel siblings already started;
      a machinery `error` cancels the group's `ctx` and aborts the turn
- [x] Optional additive `ToolCall.After []string` (call IDs) so the model
      can tighten a dependency the heuristic cannot see; unknown IDs are
      ignored and the conservative schedule wins. No `WireVersion` bump
- [x] Loop unit tests: a `Par` of two reads; a `Seq` of write-then-read;
      a nested `Seq(Par(...), Call)`; fallback to a plain chain; a gated
      call never enters a `Par` — plus a panic beside a sibling, a
      machinery error canceling the group, Ctrl-C with a group in flight,
      an `After` hint; `buildSchedule` has its own table test

Decisions (2026-09-07):

- **Grouping by action class, not by path.** The checklist asked for
  "reads with disjoint paths"; the implementation looks at `Class()`
  only. Two reads cannot conflict whatever they touch, and a read/write
  conflict is already ordered because a write is never in a `Par`: the
  batch `read a, read b, edit a, read a` becomes
  `Seq(Par(read a, read b), edit a, read a)` with no path knowledge at
  all. A path rule would have needed either argument sniffing by JSON key
  or a new interface no read tool implements, to forbid overlaps that
  are harmless. Revisit if a tool ever appears whose reads are unsafe to
  overlap with each other.
- **Read-class tools must be safe for concurrent use.** That is now part
  of the `agentapi.Tool` contract (doc comment; no wire change). Every
  built-in read tool is stateless or, for `todo`, already behind a
  mutex; an MCP-backed tool (Phase 2) will have to serialize inside its
  adapter if its transport cannot.
- **Fan-out is bounded and configurable in code, not yet in config.**
  `Limits.MaxParallelTools` (default 8) caps how many leaves of one
  `Par` run at once; `<= 1` is the WP0.4 chain, which the tests about
  "the call after the interrupted one" opt into. A flag or config key is
  a WP0.9-style addition for when someone needs it.
- **Events interleave; messages do not.** `tool_start` / `tool_end` for
  the leaves of a group arrive as they happen, so a client can show what
  is running; the tool result messages are appended — and the session
  recorded — in request order once the group joins. The Recorder never
  looked at tool events, so the session format is untouched.
- **Prompts inside a group are serialized anyway.** The Authorizer
  contract says it never asks about read-class calls and the schedule
  never groups gated classes, but a mutex around the prompt costs one
  line and turns a violated contract into a slow batch instead of two
  questions racing for one terminal.
- **Nobody sets `After` yet.** No provider dialect carries such a field;
  it exists on the wire for a Phase 1 client or a tool wrapper that
  knows a dependency, and the schedule honours it only in the direction
  that cannot reorder the model's request.

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

### WP0.12 — Instructions beyond the budget — **rungs 0–3 done, rung 4 open**

WP0.8 reads `HINT.md` / `AGENTS.md` / `CLAUDE.md` under one 32 KiB budget
and cuts blindly when it runs out. That is the right floor — the preamble
is the one part of a request compaction never touches, so it must be
bounded — but the cut has three defects worth a package of their own:

1. **It is blind to structure.** A file is cut mid-sentence, and whatever
   sat below the cut (often the "never do this" section at the end) is
   gone without the model knowing what it missed.
2. **It drops the wrong files first.** The budget fills root→leaf, so
   when it is spent the *nearest* files — the ones "nearest wins" says
   matter most — are the ones skipped.
3. **The model cannot fetch the rest.** A parent directory's `HINT.md`
   sits outside `tool.Root`, so `read_file` refuses it; even the marker
   naming the file leads nowhere.

The package is an escalation ladder. Each rung is cheaper and more
faithful than the next, and a rung is taken only when the previous one
cannot make the instructions fit. Nothing here is required for `v0.1`;
the first three rungs are Phase 0 material once WP0.9 gives `Load` its
config knobs, the fourth needs a per-turn hook in the agent loop.

**Rung 0 — a budget the model can afford.** `32 KiB` is ~8k tokens: a
few percent of a 128k window, but the whole of an 8k local model's. The
budget becomes `min(DefaultInstructionBudget, fraction × context window)`
with the window taken from the provider profile (WP0.9 wires it), so a
small model is not handed a preamble it cannot read.

**Rung 1 — reserve before filling.** Every discovered file gets a
guaranteed minimum share (enough for its outline, rung 2); the remainder
is filled root→leaf as now. A nearer file is therefore never dropped
outright; at worst it appears as an outline.

**Rung 2 — outline instead of cut, and a tool to expand it
(progressive disclosure).** A Markdown file over its share is rendered as
its heading tree, each heading followed by its first sentence and an
anchor, closed by a line saying how to read the rest. Cutting still
happens, but at section boundaries and with the table of contents kept,
so the model knows what exists. Alongside it, a new read-only built-in,
`instructions` (name to settle), does two things: `{path, section}`
returns the full text of one section or file, and `{quote}` finds which
instruction file and line a rule came from (path:line). It reads only
the files WP0.8 discovered — never an arbitrary path — so it is not a
hole in the working-directory confinement, and it is `ClassRead`, so no
permission prompt. This is also the honest answer to "where does this
instruction come from?": the prompt already labels every block with its
absolute path, and the tool turns that label into text the model can
read even when the file is above the working directory.

**Rung 3 — summarize, opt-in, cached.** When even the outlines do not
fit, the user may allow `instructions.summarize: true`: each oversized
file is summarized once by the configured model, the summary cached
under `$XDG_CACHE_HOME/hint/instructions/<sha256 of content>.md` and
reused until the file changes, and rendered as
`<file path=… summary="true">` so the model knows it is reading a
paraphrase and can fetch the original through the rung-2 tool. Last on
the ladder because it rewrites the user's own words — a rule the
summarizer drops is invisible — and because it costs a model call per
changed file. Never on by default.

**Rung 4 — split at the source (the author's fix).** A project that
outgrows one file splits it: `HINT.md` stays a short index, and
`.hint/rules/*.md` (location is [Q8](../../PLAN.md#open-questions))
hold the detail. A rule file may carry front matter with `paths:` globs
(the Cursor / Cline `globs` convention); a file with globs is loaded only
once the agent touches a matching path in the turn — goose's
`SubdirectoryHintTracker` and opencode's `Instruction.resolve` are the
prior art — and until then is listed in the index by title. A rule file
without globs is loaded always, as part of the index's budget. Nested
`AGENTS.md` files are the directory-scoped special case of the same idea
and keep working unchanged. This rung is what keeps a large project's
instructions under budget for good; the first three make the failure
graceful while the project has not split yet.

Interplay with the rest of the plan: rung 3 is the only "compaction"
the preamble ever gets — WP2.5's compaction v2 keeps ignoring it; the
rung-4 hook (react to the paths a turn touched) is the same hook the
agent-chosen overview from WP0.8 needs, so they land together; the
repo map (WP2.2) competes for the same preamble budget and will need
the rung-0 arithmetic to include it.

- [x] Rung 0: budget scaled to the profile's context window
      (`WithInstructionBudget` stays the override) —
      `project.InstructionBudgetFor`, `WithContextWindow`;
      `config.InstructionSettings.BudgetExplicit` says when not to scale
- [x] Rung 1: per-file minimum share, then root→leaf fill; test that a
      nearest file survives a spent budget as an outline
      (`TestFit_NearestFileSurvivesAsOutline`)
- [x] Rung 2: Markdown outline renderer (heading tree + first sentence,
      cuts at section boundaries; the heading is the anchor);
      `instructions` built-in (`ClassRead`, reads only discovered files,
      `{}`, `{path}`, `{path, section}` and `{quote}` forms);
      `RenderInstructions` marks outlined files
- [x] Rung 3: `instructions.summarize` config key (default off), content-
      hash cache under XDG cache, `summary="true"` attribute, a warning
      naming each summarized file
- [ ] Rung 4: `.hint/rules/*.md` discovery, `paths:` front matter, load
      on first touch via a turn observer in `internal/agent`, index lists
      the unloaded ones by title; golden test of the rendered preamble
      for a split project. Split into its own branch (decided
      2026-09-07): it needs a per-turn hook in the agent loop and a
      decision on how a rule's text enters a running turn, which
      deserves its own fork — see Q8 below for where the files live

Decisions (2026-09-07, rungs 0–3):

- **Rungs 0–3 in one package, rung 4 in the next.** The first four
  rungs are pure layout: they change what `project.Load` puts in the
  preamble and add one read-only tool. Rung 4 changes the agent loop
  (rules loaded when a turn touches a matching path) and has to decide
  how mid-turn text reaches the model; that fork is asked when the
  branch starts, not answered in passing here.
- **Q8 answered: `.hint/rules/*.md`, with `.cursor/rules` as a
  compatible source.** Our own files are plain Markdown with optional
  `paths:` front matter; a project that already keeps `.cursor/rules/`
  (`globs:` front matter, `.mdc` files) is read the same way, `globs:`
  mapped onto `paths:`, so a team does not maintain two trees. `.claude/`
  trees are not read: they carry no globs convention worth mapping.
- **The budget scales as a sixteenth of the window, four bytes a
  token.** 128k → the 32 KiB default; 8k → 2 KiB; never below 1 KiB so
  a heading list always fits. An explicit budget — file, variable or
  flag — is taken as given: the user who wrote `65536` meant it.
- **Reserve is the outline; the outline is heading plus first
  sentence.** A file's guaranteed share is the size of its outline (its
  first kilobyte when it has no headings). Under a shortfall the layout
  is greedy in document order: a section is shown in full while the
  upgrade still leaves every later section its entry, so the table of
  contents is never lost. The heading itself is the anchor the tool
  takes; a separate id would be one more thing for the model to copy
  wrong.
- **A file that cannot show one whole line is skipped.** The cut path
  (outlines overflow, no summarizer) used to leave four-byte fragments;
  it now warns and moves on.
- **Summaries: equal split, small files keep their words.** When even
  outlines overflow and summaries are allowed, a file that fits an equal
  share of the budget is left alone and only the rest are summarized
  into what remains — a short `HINT.md` next to a huge one is never
  paraphrased to make room. Cached by content hash under
  `$XDG_CACHE_HOME/hint/instructions/` (`~/.cache` otherwise), as a
  decorator over the `Summarizer` interface so the cache is tested
  without a model.
- **The tool's scope is the run's discovered list, not a directory.**
  `instructions` takes `project.Context.InstructionPaths`; a bare file
  name resolves when unique, a parent-relative one otherwise, and
  anything else is refused with the list of files it does know.

Decisions recorded when the package was planned:

- **Cut, outline, summarize — in that order, and summarize never by
  default.** Each rung trades a little more fidelity for a little more
  room; the user's own words are the most valuable thing in the prompt,
  and a paraphrase of them must be a choice, not a surprise.
- **The `instructions` tool is scoped to discovered files, not to a
  directory.** Reading a parent's `HINT.md` from a subdirectory is
  legitimate; reading `../../.env` through the same tool is not. The
  tool takes paths from `project.Context.Instructions`, so the
  confinement is by construction, as `tool.Root` is for the file tools.
- **Splitting is a convention, not a format.** Rule files are plain
  Markdown with optional front matter; no schema, no `@import` language.
  gemini-cli's and goose's import syntaxes buy little over a directory
  of files and add a parser that can loop.

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
ones wired) → WP0.6 (wires write/execute tools) → WP0.7 → WP0.8 →
WP0.9 (done) → WP0.10 (done; the Raspberry Pi run is pending) →
WP0.11 (done) → WP0.12 (rungs 0–3 done; rung 4 is its own branch).
WP0.11 sat after WP0.6 (it needed per-call gating to exist) and WP0.12 sits
after WP0.9 (it needs the config knobs); neither was required for
`v0.1-alpha`, but the decision of 2026-09-07 is to land both first: the
Raspberry Pi smoke run and the `v0.1-alpha` tag follow WP0.12.
`v0.1` after acceptance criteria pass.
