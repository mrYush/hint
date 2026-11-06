# Phase 2 — Memory, MCP, A2A, open integration surfaces, code maturity

> Status: In progress · Target release: **v0.4** · Estimate: 8–10 weeks (part-time)
> Depends on: Phase 1
> Exit bar: core parity with a mature coding agent, plus `hint` reachable by and able to reach other agents.

## Goal

Open the agent to the outside in every direction it can be extended — tools (MCP servers, HTTP APIs described by OpenAPI), other agents (A2A, both as caller and as callee), instructions (MCP prompts and resources next to the rule files) and knowledge (local databases, document indexes) — make it effective on large codebases (repo map), give it long-term memory (vector store), plus deeper git integration and smarter compaction.

Every surface goes through the same two gates the architecture already fixes: the permission layer (a class per tool, confirmation for writes and commands) and the untrusted-input rule (what comes back from a tool, a server, an agent or a document is data, never instructions).
Added 2026-10-18 together with WP2.6 and WP2.7.

## Work packages

### WP2.1 — MCP client

**Branch:** `dev/wp2.1-mcp-client`

M0–M3 and the lifecycle/CLI slice are merged in PR [#87](https://github.com/mrYush/hint/pull/87); resources, prompts and final acceptance moved to WP2.9.

SDK — [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) **`v1.8.0`, pinned** (Q6).

- ✅ `mcp_servers:` section in config (stdio and streamable HTTP transports), layered like every other key: global file, project `.hint/config.yaml`, with the project able to add servers and never a key
- ✅ Discovered MCP tools registered in the tool registry alongside built-ins, under `<server>.<tool>` names
- ✅ MCP schemas preserve JSON and reject numbers with `abs(v) >= 2^53`; numeric normalization is documented and tested
- ✅ Permission wrapper around every MCP tool (class declared in config, default `execute`)
- ✅ MCP tool output treated as untrusted input (no instruction-following from tool results — architecture-level rule)
- ✅ MCP **resources** as instruction and knowledge sources: moved to WP2.9
- ✅ MCP **prompts** as reusable procedures: moved to WP2.9
- ✅ Lifecycle: health check, restart on crash, timeout budget
- ✅ Lazy start: connect a server only when its tools are first needed
- ✅ `hint mcp list|status` to see it
- ✅ `hint mcp enable <name>` records project-server consent
- ✅ Security tests: project-server consent resets on config change, cross-origin redirects and a direct off-origin request do not receive credentials, and a bare `${VAR}` token is redacted
- ✅ Integration test with a reference MCP server for tools, resources and prompts: moved to WP2.9

Decisions (2026-11-03, from the [Q6 spike](mcp-client-spike.md)):

- **SDK and spec.**
  Pin the official `go-sdk` at `v1.8.0` and target the `2026-07-28` revision.
  The SDK negotiates `2025-11-25`, `2025-06-18`, `2025-03-26` and `2024-11-05`; WP2.1 does not call `initialize`, `ping` or `notifications/initialized` itself.
  The spike measured both eras against live servers.
- **Context budget.**
  The measured ten-server catalog costs 9,981 tokens (44.6 KiB) and the seven reference servers cost 8,356.
  WP2.1 gets a 48 KiB global MCP schema budget and a 20 KiB per-server cap.
  Over budget, hide whole servers in config order, warn, and show them in `hint mcp status`; per-server `tools:` allow/deny lists are the escape hatch.
  Do not truncate descriptions by default.
  Full lazy disclosure is WP2.8.
- **Permission.**
  The config class is the only source; default `execute`.
  Never derive `read` from a server's `readOnlyHint` — MCP annotations are untrusted.
- **Auth.**
  WP2.1 supports static headers and `${VAR}` only, with credentials in the global file.
  OAuth 2.1 (Sentry, Linear, GitHub and similar) is a later package; a project file may not set `env` or `headers`.
  The header transport always rejects a cross-origin redirect with credentials, and the `RoundTripper` is origin-aware, so it does not rely on stripping a header in `CheckRedirect`; tests cover a 302 to another host and a direct different-origin request.
  `Config.Secrets()` includes each expanded header/env value and every referenced `${VAR}` value separately, so a bare token is redacted too.
- **Schema fidelity.**
  The SDK decodes `inputSchema` into `any`, so numbers become `float64`; this is not a byte-identical passthrough.
  WP2.1 rejects tools with `abs(v) >= 2^53` with "unsupported numeric precision" and documents the high-precision-decimal limitation.
  The bound is conservative: `9007199254740993` decodes to `9007199254740992` (2^53), so an exact 2^53 is rejected too; after decode they are indistinguishable.
  A raw `tools/list` path stays the fallback if a server needs exact numeric literals.
- **Project servers.**
  A server added by a project file may start a process, so it is **disabled by default**.
  `hint mcp enable <name>` records consent outside the repository, keyed by the canonical project root, server name, and a SHA-256 of `transport`, `command`, `args`, `url` and `permission`.
  A change to any of those fields after `git pull` invalidates the record and disables the server until it is enabled again.
  `hint mcp status` names the changed field; a test changes `args` after enabling.
  A global-file server is enabled by its presence.

Progress (2026-11-03/04, [#86](https://github.com/mrYush/hint/issues/86), [PR #87](https://github.com/mrYush/hint/pull/87)): the first slice landed on `dev/wp2.1-mcp-client` — the `internal/mcp` client, manager, tool adapter, schema budget and tool registration under `<server>.<tool>`, lazy start on the first turn, the `ping` health check, and `hint mcp list|status|enable`.
MCP resources and prompts, the live reference-server integration test, and the final acceptance pass moved to WP2.9.

### WP2.2 — Repo map

**Branch:** `dev/wp2.2-repo-map`

Planning issue: [#93](https://github.com/mrYush/hint/issues/93).
Scope synchronized on 2026-11-06 from the workspace-navigation plan and research.
This records the design for review; implementation and dependency selection are not approved by this documentation change.
All implementation checkboxes remain open.

Give the agent an on-demand view of code structure across explicitly attached roots, backed by the shared incremental index.
The read-class `repo_map` tool returns signatures, enclosing type/module context and source locations without function bodies.
Maps are tool results, never automatically inserted into the system prompt; no model-window threshold or `map_control` is required.
Ordinary file access and scoped symbol lookup must not wait for full discovery, parsing or global ranking.

#### Package boundaries and workspace contract

The product includes user-added Git repositories, separate worktrees and ordinary folders, with multiple roots available in one task.
A URL source becomes available only after a successful clone.
Nested repositories and submodules are attached explicitly as separate roots; the parent shows their boundary and does not index or operate on their contents through the parent identity.
Users add or remove roots between turns, with membership revisions recorded in session history.
The user interface is `/repos` and a paginated text tree in both shells; the agent requests the code map separately.

Keep workspace administration and the shared index outside this implementation package:

| Package | Responsibility | Status of the boundary |
|---|---|---|
| WP2.11, proposed | Root identity, clone lifecycle, nested ownership, live tree pagination, session routing and scoped instructions | Prerequisite proposal; separate package and issue still require review |
| WP2.12, proposed | Shared catalog/index, lexical search, resource scheduling, document adapters and persistent source-linked notes | Prerequisite proposal; separate package and issue still require review |
| WP2.2 | Code extraction, scoped symbol lookup, bounded map rendering and optional ranking | Consumes those contracts without introducing another catalog or worker pool |

The proposed delivery order is workspace navigation, the shared indexing baseline, then code-map integration.
The shared index's first document-search profile is Russian/English, with basic lexical support for other languages; semantic retrieval is later work under WP2.3.
PDF text extraction may require a user-connected local module, and OCR remains a separate optional heavy capability.
Persistent agent notes live outside roots, may be saved automatically within a narrow permission grant, and depend on verified source observations.
They are untrusted, separately labeled suggestions and do not change source-hit ranking by default; rebuilding derived caches never deletes durable notes.
Document extraction, note storage, URL browsing, embeddings, an interactive file panel and a mandatory background daemon are not WP2.2 deliverables.

#### Extraction, retrieval and ranking

Use `go/ast` for Go.
Compare pinned `gotreesitter` with upstream Tree-sitter through WASM/wazero for Python, JavaScript, TypeScript/TSX and Rust before choosing the non-Go adapter.
Bound source bytes, extraction output, worker concurrency and parser work, with native cancellation and explicit incomplete-parse detection.
Unsupported, oversized or failed sources remain visible files with an omission reason.
A parser failure or missing optional capability does not make the workspace unavailable.

Accept explicit root/subtree scope and exact/prefix symbol queries over all indexed tags in that scope before truncating output.
Apply current root permissions and exclusions before ranking and output selection.
The proposed initial response ceiling is 8 KiB including metadata, with scoped continuation or refinement so lower-ranked entries remain reachable.
Every result identifies scope, root, observed generation, coverage, truncation and relevant failures.
No match in a partial index means no match in the indexed portion, not no match on disk.

Start with deterministic scoped retrieval; global ranking must demonstrate task-level benefit before becoming a default cost.
If PageRank is retained, use reference-to-definition semantics with rank allocated to destination declarations, verified against a small reference graph.
Store counts and definition postings without materializing every reference/definition pair for repeated names.
Equal names in unrelated roots do not establish a dependency.
Ranking runs on a stable observed generation, never requires a resident all-files graph and never blocks scoped navigation.

#### Scale, freshness and resource limits

Use the shared disk-backed catalog/frontier and bounded positive-count `File.ReadDir(n)` batches; an output cap over `WalkDir` does not bound wide-directory memory.
Retain a bounded pool of active directory streams between work slices so reopening a huge directory does not repeatedly skip its tail.
Restart unfinished enumeration idempotently after interruption and serve keyset pages from observed generations, with explicit partial/building and expired-cursor states.
An unreadable, offline or canceled directory is not empty; infer deletion of unseen children only after a successful completed enumeration.
Do not promise an atomic live filesystem snapshot or a hard deadline for every filesystem operation.

Index current working-file observations, including dirty and eligible untracked files, rather than assuming Git commit state is current.
Reuse authorized reads and prioritize agent-touched files; a partial read covers only its observed range.
Version derived data by root, source and extractor policy, invalidate Hint edits promptly, and reconcile external changes incrementally.
File stats cannot prove freshness after equal-size, restored-mtime edits; validate returned evidence against current source within a bounded freshness policy.
Recheck current access before publishing cached code, and invalidate affected jobs/cursors on root-membership changes.
Preserve rooted file access, existing exclusions, the permission layer and RPC shell separation; the index never grants access.

One resource scheduler governs discovery, extraction and maintenance across roots, with fair background progress when capacity permits.
Indexing is quiet, resumable and optional; explicit status requests show progress and pause reasons without chat spam.
On Pi 4 with 4 GB and a local model, auto mode pauses expensive work under resource pressure while retaining live access and bounded queries over existing data.
The proposed evaluation targets are at most 128 MiB additional indexing RSS and no more than 5% median inference degradation; these are unmeasured review targets, not achieved limits.
Measure all helpers, cache pressure, temporary disk and I/O alongside model latency and throughput.
Arbitrarily large trees require bounded active work, not infinite storage or constant completion time; completion needs enough storage, available work time and a sufficiently stable tree.

#### Evidence and validation

[The parser benchmark ledger](https://github.com/odvcencio/gotreesitter/blob/main/BENCH.md) and [WASM adapter limits](https://github.com/zema1/wasitter/blob/main/USAGE.md) motivate a pinned comparison of correctness, cold/warm extraction, memory and cancellation rather than selecting a parser from headline timings.
[Aider's ranking implementation](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py), [a firsthand output report](https://github.com/Aider-AI/aider/issues/3932) and [an original retrieval comparison](https://agent-retrieval-bench.github.io/blog.html) motivate signature and task-level evaluation; the comparison's Aider-style baseline is not Aider itself.
The historical local spike allocated weighted rank to source declarations rather than destination declarations, so its ranking results are not Aider-parity evidence.
The shared index should compare pure-Go SQLite/FTS5, Bleve and a Tantivy helper under equal retrieval semantics and select one backend, without tying stable root/source identities to its postings format.
Public research has not established a universal backend winner or verified this design's Pi resource targets.

- [ ] Complete owner review and record the prerequisite package boundaries before implementing the new scope.
- [ ] Expose read-class `repo_map` with bounded signatures/locations, exact/prefix lookup, scoped continuation and explicit partial coverage; preserve access to all eligible indexed symbols.
- [ ] Validate Go and the selected non-Go adapter on pinned real, malformed, incomplete, generated and minified sources; record grammar versions, binary growth, extraction cost, memory and cancellation behavior.
- [ ] Test Git/non-Git roots, dirty files, worktrees, nested ownership, detach/reattach, identical names across roots, source changes and revoked access without stale evidence being labeled current.
- [ ] Verify bounded queue/memory/FD use at 10k, 100k and 1m metadata entries, including wide/deep trees, many roots and fairness for a tiny active root beside a huge one.
- [ ] Exercise tail progress, cursor expiry, interruption, crash recovery, disk exhaustion and oversized inputs while direct navigation remains available.
- [ ] If graph ranking is retained, verify destination-rank parity, self-edges, dangling nodes, deterministic ties and repeated-name fanout without quadratic edge materialization.
- [ ] Measure the complete indexing process family on Pi 4 alongside a specified local model, including first-token latency and throughput against indexing off, and verify automatic pause/resume under pressure.
- [ ] Compare live tools, file/text/symbol indexing, map ranking and notes separately on paired tasks, including correctness, useful evidence per token, tool calls, latency, creation cost and regressions.
- [ ] Pass formatting, tests, race checks, vet, the pinned linter and all six `CGO_ENABLED=0` release builds, including linux/arm64.

### WP2.3 — Vector memory

- [ ] First `Embedder` implementation: openai-compatible `/v1/embeddings` + Ollama
- [ ] Storage: sqlite-vec
- [ ] `memory_search` tool + automatic fact capture (opt-in)
- [ ] Keep semantic retrieval for documents, notes and history separate from WP2.2 code maps and the proposed shared lexical index; reuse source identities and freshness rules instead of duplicating the file catalog or durable navigation notes.
- [ ] Privacy: memory store is per-user, local, excluded from logs

### WP2.4 — Git integration

- [ ] `git` tool (status/diff/log/commit under the permission system)
- [ ] Optional auto-commit of agent edits (config flag, off by default)
- [ ] Dirty-tree guard: warn before the first write into a dirty repo

### WP2.5 — Compaction v2

- [ ] Layered: microcompact tool outputs → summarize turns → hard truncation
- [ ] Compaction records preserved in the session file (already a record kind)
- [ ] Regression tests: long multi-tool sessions stay under the window with no loss of task-critical facts (golden scenarios)

### WP2.6 — A2A: `hint` among other agents

Protocol — [A2A](https://a2a-protocol.org) (Agent2Agent, a Linux Foundation project): JSON-RPC over HTTP, an Agent Card for discovery, tasks with a lifecycle and streamed updates.
Which side goes first and which Go implementation to build on is open question Q10 in [PLAN.md](../../PLAN.md#open-questions); the spike settles it before any code.

- [ ] Spike: read the current spec, run the reference samples, decide client-first or server-first and the implementation to build on; record the decision here
- [ ] **`hint serve` as an A2A server.**
  An Agent Card at the documented well-known path describing `hint` (skills: the directory it serves, the tools it has); a task is a turn of a session; streamed task updates carry the same events the RPC protocol does; permission questions surface as `input-required` states that the calling agent — or the user through a shell attached to the same session — answers.
  The A2A adapter is one more client of the core, beside the JSON-RPC server and the shells; it does not get its own agent loop.
- [ ] **`hint` as an A2A client.**
  `agents:` section in config naming remote agents by their card URL; each becomes a tool (`delegate_<name>`) under the permission system, class `execute`; the remote agent's answer is untrusted input like any tool result.
- [ ] Authentication from the card's security schemes: bearer token from `${VAR}`, mutual TLS later; no credential ever in a project file.
- [ ] Transport limits: a budget per delegated task (time, tokens where reported) and cancellation that propagates to the remote task.
- [ ] Integration test: two `hint serve` processes in two directories, one delegating a read-only question to the other through A2A, with a recorded fixture for CI.
- [ ] Decision to record: what of a delegated task lands in the session file (the request, the final answer, the remote task id — not the remote agent's intermediate output).

Decisions (2026-11-01):

- **WP2.6 settles the seam for agents that speak A2A; it does not settle delegation to a *harness*.**
  An A2A agent owns its own loop, credentials and permissions and can be asked about its state; a harness is a process with its own loop and no protocol to ask it anything.
  That shape is Q11 in [PLAN.md](../../PLAN.md#open-questions) — a `Runner` beside `Tool`, or A2A only — not a checkbox here.
- **The order is the `bash` sandbox layer, then `hint`-in-`hint` over `-p --output json` in a worktree, then A2A (WP2.6), then a subprocess adapter for a foreign CLI.**
  Chosen over "A2A first" and over "wrap a foreign CLI in a bash-like tool": a child harness is less observable than a command the user confirmed and would run outside the file confinement of `tool.Root`.
  The price is that delegation does not ship before the sandbox layer exists.
- **The name is `runner`/`delegate`, not `harness`.**
  `harness` already means the evaluation bench ([architecture](architecture.md#implications-noted-for-the-plan)) and the coding-agent category ([why-hint](why-hint.md#why-start-with-a-solved-problem)).

### WP2.7 — Open integration surfaces beyond MCP and A2A

The ways an operator connects tools, instructions and knowledge without writing Go.
Each is a small adapter onto what the core already has (the tool registry, the instruction files, the permission gate); none adds a second model loop.

- [ ] **HTTP APIs described by OpenAPI.**
  `openapi_tools:` in config: a spec URL or file plus an allow-list of operations; each operation becomes a tool with its parameters as the schema, read-class for `GET`, `execute` otherwise, credentials from `${VAR}`; responses truncated to the tool-output budget.
- [ ] **Local databases as knowledge.**
  A read-only `sql` tool over SQLite and PostgreSQL connection strings from config (`databases:`), `SELECT` only, a row and byte budget, the schema offered to the model as an instruction outline; write access stays out until there is a sandbox story (see WP1.7's limits).
- [ ] **Document indexes.**
  Integrate semantic document retrieval with WP2.3 after the local lexical baseline, reusing its catalog and extractor contract.
  The lexical baseline for explicitly attached folders, PDF/DOCX adapters and source-linked navigation notes belongs to the proposed shared-index package described under WP2.2, not a second index implemented here.
  Document URL ingestion remains a separate integration surface; it is not implied by the workspace's clone-before-use Git URL support.
- [ ] **Instruction sources beyond the repository.**
  A `rules:` list of extra rule directories and URLs (a team's shared rule pack, a company-wide `HINT.md`), fetched with a cache and a signature or pinned hash, read with the same budget and outline rules as `.hint/rules`.
- [ ] **Procedure files (skills).**
  A `.hint/skills/<name>.md` file with front matter is a slash command: `/name` expands its body into the turn, its declared arguments complete in the TUI; shared the same way as rules.
- [ ] One `hint integrations` command that lists every configured surface with its status, and one place in the docs that explains them side by side.

Decisions to make when the work starts, recorded here: the config shape shared by all surfaces (a common `name`, `permission_class`, `budget` block), and whether a surface declared in a project file may add credentials (it may not; the global file holds them, the project file names the `${VAR}`).

### WP2.8 — Lazy MCP tool disclosure

WP2.1 hides whole servers when the 48 KiB schema budget is exceeded.
This package lets the model discover and load a hidden tool on demand instead of choosing a static subset at startup.

- [ ] A search/list meta-tool over hidden MCP tools: name, server and a one-line description; no full schemas in the preamble.
- [ ] Load the selected tool's schema into the next request, under the same 48 KiB ceiling.
- [ ] The model can call a loaded tool in the same turn or the next one; no session restart and no config edit.
- [ ] The budget stays the fallback when discovery itself would exceed it; `hint mcp status` reports hidden and loaded counts.
- [ ] Regression test with two fake servers whose schemas exceed the budget: a hidden tool is found and called while the full catalog never enters the prompt.

Acceptance: with more MCP tools than the budget, the agent finds and calls a hidden tool in one session, and the prompt carries only the schemas it selected.

### WP2.9 — MCP resources, prompts and acceptance

**Branch:** `dev/wp2.9-mcp-resources-prompts`

Follow-up to WP2.1 ([#86](https://github.com/mrYush/hint/issues/86)), using the pinned SDK and the decisions recorded above.

The configuration names instruction resources as `mcp_servers.<name>.resources: [<uri>, ...]`.
They load during the existing first-turn MCP initialization and share the instruction budget; on-demand reads use explicit `server` and `uri` arguments and remain tool data.
Prompt list and expansion are available in both the TUI and the plain REPL; `completion/complete` integrates with the TUI command table.
Tab learns server names from `prompt.servers` without connecting, and `prompt.list`, `prompt.get` and `prompt.complete` run beside the connection loop so a permission reply and `turn.cancel` are not stuck behind an MCP call.

- ✅ A resource named in the config loads like an instruction file, with the existing budget, outline and `instructions` tool.
- ✅ A model-requested resource is read on demand through `resource_read`; its contents remain data, never instructions.
- ✅ `/prompts` lists server prompts, and `/<server>:<prompt>` expands one into the turn with its arguments.
- ✅ Prompt arguments and `completion/complete` are integrated into the TUI command table.
- ✅ An opt-in integration test with a cached reference MCP server covers tools, resources and prompts; it is skipped by default and does not require network access in CI.
- ✅ Documentation and the full verification gate are updated, including `go test -race ./...` and the Linux ARM64 cross-build.

The reference-server integration run is a manual local check enabled by `HINT_TEST_MCP_REFERENCE=1` because CI does not pre-cache `@modelcontextprotocol/server-everything`; once enabled, any dial or client error fails the test.

### WP2.10 — `/new`: start a fresh session

**Branch:** `dev/wp2.10-new-session`

A fresh session starts only by leaving the shell and running `hint` again.
`/sessions` resumes or forks an existing one, and Esc there opens a new session only when the shell has none yet (`hint -r` on a directory with no sessions).
`session.open` already creates an empty session; neither shell offers it once one is open.

- ✅ `/new` in the TUI calls `session.open` and moves the shell onto that session: an empty transcript, the new id in the header, and a notice that names the session just left.
- ✅ The same command in the line REPL, so `--plain` is not left without it.
  It prints the id it left and the id it opened, and the next question is a turn of the new session.
- ✅ The command takes no argument and is neither offered nor run while a turn is in flight, the same rule as `/sessions`.
- ✅ The new session answers with the default profile.
  A `/profile` switch stays on the session that was left.
- ✅ A run started with `--no-session` keeps the new session ephemeral (`session.open` with `{"ephemeral": true}`).
- ✅ `/help` lists `/new` in both shells, and the README Sessions section says a new session starts with the command as well as with a fresh `hint`.
- ✅ Tests cover the TUI move onto an empty recorded session whose previous id stays listable, the REPL switch, a busy turn that does not run the command, and an ephemeral run that still records nothing.

Decisions (2026-11-04):

- **No new RPC method.**
  `/new` is `session.open`, which already returns an empty session on the default profile.
  The shell drops its view of the previous session; the file stays, and another client of `hint serve` can still `session.attach` it.
- **The default profile, not the one in use.**
  A new `hint` already answers with the default.
  Copying the switched profile would make `/new` a fork with an empty history.
- **`--no-session` stays off disk.**
  The flag asked the run to record nothing, so `/new` repeats `ephemeral: true` instead of opening a file.
- **Both shells.**
  WP1.5 put `/profile` in the REPL for the same reason: `--plain` is the same session, only a different client.
- **Fork stays `f`.**
  `/new` does not take an id or a turn, and the session list does not grow a "new" row.

## Acceptance criteria

1. A third-party MCP server from config contributes tools that the agent uses under the permission system, a resource it serves is read as an instruction file, and a prompt it serves runs as a slash command.
2. Across attached Git and ordinary-folder roots, the agent answers "where is X implemented?" using an on-demand code map without reading every file, with explicit partial coverage and bounded resource use on large trees.
3. `memory_search` recalls a fact stored in a previous session.
4. With auto-commit on, every applied edit is a well-formed git commit.
5. Another agent fetches `hint serve`'s Agent Card and completes a task through A2A, answering a permission question on the way; `hint` delegates a question to a remote agent named in its config and uses the answer under the permission system.
6. An OpenAPI-described API and a read-only database appear as tools from config alone, each under its declared permission class.
7. With more MCP tools than the WP2.1 budget, the agent finds and calls a hidden tool without a restart.
