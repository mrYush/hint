# Phase 2 — Memory, MCP, A2A, open integration surfaces, code maturity

> Status: In progress · Target release: **v0.4** · Estimate: re-estimation required after workspace/index decomposition; the previous 8–10 weeks excluded that scope
> Depends on: Phase 1
> Exit bar: core parity with a mature coding agent, workspace navigation and incremental indexing, plus `hint` reachable by and able to reach other agents.

## Goal

Open the agent to the outside in every direction it can be extended — tools (MCP servers, HTTP APIs described by OpenAPI), other agents (A2A, both as caller and as callee), instructions (MCP prompts and resources next to the rule files) and knowledge (local databases, document indexes) — make it effective across explicitly attached repositories and ordinary folders through bounded workspace navigation, incremental indexing and on-demand code maps, give it long-term memory (vector store), plus deeper git integration and smarter compaction.

Every surface goes through the same two gates the architecture already fixes: the permission layer (a class per tool, confirmation for writes and commands) and the untrusted-input rule (what comes back from a tool, a server, an agent or a document is data, never instructions).
Added 2026-10-18 together with WP2.6 and WP2.7.

Release scope confirmed on 2026-11-06: multiple roots, the shared lexical document/notes index and the on-demand code map all target v0.4.
Optional PDF extraction and heavy OCR retain explicit capability/admission rules; their absence does not block ordinary navigation.
The workspace/index foundations are required release capabilities, but their proposed WP2.11/WP2.12 decomposition still needs formal work packages and review.
Re-estimate the phase after that decomposition and the parser/backend measurements; do not carry forward the old estimate or invent a new duration without those inputs.

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

Implementation issue: [#93](https://github.com/mrYush/hint/issues/93).
Scope synchronized on 2026-11-06 from the workspace-navigation plan and research.
The owner authorized implementation to readiness in this existing branch on 2026-11-06, after the design review.
Owner review identified unresolved correctness and liveness defects; the branch is not ready to merge and the work package remains in progress.
The remediation checklist below is the current acceptance work for this branch.
The [earlier review record](evidence/wp2.2-mac-20261106/readiness.md) preserves verification, cross-component findings and reproducible Mac measurements; passing those checks does not cover the newly identified scenarios.
The owner will run Pi 4/local-model 8k/32k measurements separately using the [evaluation methodology](repo-map-evaluation.md); local Mac checks do not satisfy those gates.

Give the agent an on-demand view of code structure across explicitly attached roots, backed by the shared incremental index.
The read-class `repo_map` tool returns signatures, enclosing type/module context and source locations without function bodies.
Maps are tool results, never automatically inserted into the system prompt; no model-window threshold or `map_control` is required.
Ordinary file access and scoped symbol lookup must not wait for full discovery, parsing or deferred background graph work.

#### Package boundaries and workspace contract

The product includes user-added Git repositories, separate worktrees and ordinary folders, with multiple roots available in one task.
A URL source becomes available only after a successful clone.
Nested repositories and submodules are attached explicitly as separate roots; the parent shows their boundary and does not index or operate on their contents through the parent identity.
Users add or remove roots between turns, with membership revisions recorded in session history.
The user interface is `/repos` and a paginated text tree in both shells; the agent requests the code map separately.

Keep workspace administration and the shared index in separate internal packages, while integrating their necessary baseline in this branch:

| Area | Interface consumed by WP2.2 | Status of the boundary |
|---|---|---|
| Workspace foundation | Authorized scoped root/entry handles, membership revisions, prepared instructions and paginated navigation/status | Included in WP2.2 under the owner-authorized scope exception; `internal/workspace` owns authority and the RPC engine owns administration |
| Shared-index baseline | Versioned source observations, declaration storage/query generations, invalidation, bounded scheduling leases and coverage/status | Included in WP2.2 under the same exception; `internal/index` owns the disk catalog and frontier, with one shared manager for bounded work |
| WP2.2 | Code extraction, scoped symbol lookup and bounded deterministic map rendering | Consumes those contracts without introducing another catalog or worker pool |

Scope exception under the planning conventions: #93 requires these foundations to provide a usable end-to-end map, and the owner explicitly requested completion in the existing branch rather than splitting the implementation after review.
The delivery order remains workspace navigation, the shared indexing baseline, then code-map integration and acceptance.
The tentative WP2.11/WP2.12 branch names were proposals, not separate implemented packages; later document/notes work still requires its own finalized scope, issue and branch.
The shared index's first document-search profile is Russian/English, with basic lexical support for other languages; semantic retrieval is later work under WP2.3.
PDF text extraction may require a user-connected local module, and OCR remains a separate optional heavy capability.
Persistent agent notes live outside roots, may be saved automatically within a narrow permission grant, and depend on verified source observations.
They are untrusted, separately labeled suggestions and do not change source-hit ranking by default; rebuilding derived caches never deletes durable notes.
Document extraction, note storage, URL browsing, embeddings, reference-graph extraction/PageRank, an interactive file panel and a mandatory background daemon are not WP2.2 deliverables.

The detailed [code-map contract](repo-map-contract.md) defines request/result fields, byte limits, cursor/error semantics, freshness and the consumed workspace/catalog/scheduler interfaces.
It belongs to this work package; completed component checks do not by themselves establish full phase acceptance.

#### Extraction, retrieval and ranking

Use `go/ast` for Go.
The pinned comparison selected wasitter v0.1.0 through wazero v1.10.1 for Python, JavaScript, TypeScript/TSX and Rust; [adapter evidence](../../internal/repomap/extract/README.md) records grammar provenance, binary/cold/warm costs and the gotreesitter tradeoff.
The shared catalog uses modernc.org/sqlite v1.61.1 to retain CGO-free builds; this does not select a semantic-search or document-extraction backend.
Bound source bytes, extraction output, worker concurrency, per-job deadline and capture/work-unit counts where supported, with tested interruption and explicit incomplete-parse detection.
The deadline is the minimum of caller and job limits; synchronous `context.WithTimeout` alone does not stop an uncooperative parser, including `go/ast`.
Require native interrupt/fuel or process supervision before admitting such an adapter to automatic background work; do not release a lease while canceled work still runs.
Unsupported, oversized, paused, timed-out, work-budget-exhausted or failed sources remain visible files with an omission reason.
A parser failure or missing optional capability does not make the workspace unavailable.

Accept explicit root/subtree scope and exact/prefix symbol queries over all indexed tags in that scope before truncating output.
Apply current root permissions and exclusions before ordering and output selection.
The request selects `repo` (ID/alias, default immutable primary), `path` or scoped `entry_id`, optional `symbol`/`match`, and a bounded `limit`; continuation accepts only `cursor`.
The ceiling is 8192 UTF-8 bytes of the serialized JSON text payload, including escaping, metadata and cursor, further reduced by the configured output limit.
The builder emits whole records, not head/tail-truncated JSON; integrate with the existing tool limits without losing the cursor or bypassing permission/cancellation.
Every result identifies `root_id`, `membership_revision`, scope, observed generation, file versions/freshness, coverage, capability state, truncation and relevant failures.
Cursor state binds query, scope, membership/policy revisions, a retention reservation and last consumed key, with bounded lifetime and no open transaction between calls.
Bound examined records and index-read bytes as well as results; normal work-slice exhaustion returns partial progress, retaining any candidate whose mandatory checks or complete output did not fit.
Expiry, changed membership/policy or generation eviction returns `stale_cursor`, never a silent restart; errors remain explicit in the JSON text as well as `ToolResult.IsError`.
`paused`, incomplete coverage and unsupported files are response states, not request errors; `/repos` and `repo_list` expose detailed capability/status on demand.
An empty page with `query_complete: false` is not a negative lookup; only a completed traversal without matches establishes absence in that indexed generation, never absence on disk.

The first version labels deterministic `(relative_path_bytes, start_byte, kind, symbol_id)` ordering as `path` for unfiltered/exact lookup, and `(name_bytes, relative_path_bytes, start_byte, kind, symbol_id)` as `name` for prefix lookup.
Exact spellings naturally precede their longer extensions in name-byte order, avoiding a global sort or duplicate exact-match bucket.
No references or dependency graph are extracted, and equal names across files or roots do not imply semantic bindings.
PageRank is deferred to a separately reviewed experiment defining reference roles, node model, repeated-name policy, unresolved names and an exact oracle, followed by task-level benefit measurements.
Its absence is not incomplete declaration coverage and does not block v0.4.

#### Scale, freshness and resource limits

The consumed foundation contract requires a disk-backed catalog/frontier and bounded positive-count `File.ReadDir(n)` batches; an output cap over `WalkDir` does not bound wide-directory memory.
An all-files resident allowlist, whole-directory in-memory sort and materialized reference/definition Cartesian product are forbidden scalability shortcuts.
Retain a bounded pool of active directory streams between work slices so reopening a huge directory does not repeatedly skip its tail.
Restart unfinished enumeration idempotently after interruption and serve keyset pages from observed generations, with explicit partial/building and expired-cursor states.
An unreadable/offline directory or canceled enumeration is not an empty directory; infer deletion of unseen children only after a successful completed enumeration.
Do not promise an atomic live filesystem snapshot or a hard deadline for every filesystem operation.

Index current working-file observations, including dirty and eligible untracked files, rather than assuming Git commit state is current.
Reuse authorized reads and prioritize agent-touched files; a partial read covers only its observed range.
Version derived data by root, source and extractor policy, invalidate Hint edits promptly, and reconcile external changes incrementally.
File stats cannot prove freshness after equal-size, restored-mtime edits.
`content_checked` requires a full candidate reread/hash match during the request, matching extractor policy, no detected read race and rechecked access; otherwise use `observed` without implying current-source verification.
Known changed/deleted/inaccessible sources are suppressed, not relabeled `observed`; partial reads cannot validate a whole AST or enclosing context.
The proposed optional hashing budget is four distinct files, 1 MiB and 100 ms of cooperative work per response; mandatory access/metadata checks have a separate finite query budget, whose exhaustion preserves the next candidate for continuation.
Background reconciliation and candidate rehash discover external same-stat edits; `content_checked` describes the bytes checked at a time, not an immutable filesystem snapshot or permission to edit without a current read.
Recheck current access before publishing cached signatures/ranges, and invalidate affected jobs/cursors on root-membership changes.
Preserve rooted file access, existing exclusions, the permission layer and RPC shell separation; the index never grants access.
The primary root is immutable/default and grants no automatic parent access; only user-owned actions/configuration add roots or enable extractors.
Project files, tool output and notes cannot expand authority; nested ownership also applies through symlink aliases, and submodules are not initialized automatically.
Revocation prevents queued and in-flight publication to the affected session; root instructions precede the first edit/execute, and the shell remains outside file-tool confinement.

One resource scheduler governs discovery, extraction and maintenance across roots, with fair background progress when capacity permits.
Indexing is quiet, resumable and optional; explicit status requests show progress and pause reasons without chat spam.
On Pi 4 with 4 GB and a local model, auto mode pauses expensive work under resource pressure while retaining live access and bounded queries over existing data.
Admission and hysteresis follow the [scheduler contract](repo-map-contract.md#resource-admission-and-resumption): memory/CPU/I/O/disk signals, sample age, leases and inference activity, including completion/compaction/fallback requests.
Stop background admission on high-water conditions and pause background extraction/maintenance during inference in the low-resource auto profile; foreground queries retain their own finite budgets.
Handle emergencies immediately and resume background work only after all lower-water conditions hold for the configured dwell/cooldown; explicit `on` does not override authority, finite budgets or emergency protection.
Unknown telemetry is not zero pressure; numerical profiles must be measured and published before automatic execution is enabled.
The proposed evaluation targets are at most 128 MiB additional indexing RSS and no more than 5% median inference degradation; these are unmeasured review targets, not achieved limits.
Measure all helpers, cache pressure, temporary disk and I/O alongside model latency and throughput.
Arbitrarily large trees require bounded active work, not infinite storage or constant completion time; completion needs enough storage, available work time and a sufficiently stable tree.

#### Evidence and validation

[The parser benchmark ledger](https://github.com/odvcencio/gotreesitter/blob/main/BENCH.md), [the API-route timeout reproduction](https://github.com/odvcencio/gotreesitter/issues/1558) and [WASM adapter limits](https://github.com/zema1/wasitter/blob/main/USAGE.md) motivate a pinned comparison of correctness, cold/warm extraction, memory and cancellation rather than selecting a parser from headline timings.
The timeout report concerns C++ and does not prove a defect in every proposed language or that its tested native deadline fails.
[Aider's ranking implementation](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py), [a firsthand output report](https://github.com/Aider-AI/aider/issues/3932) and [an original retrieval comparison](https://agent-retrieval-bench.github.io/blog.html) motivate signature and task-level evaluation; the comparison's Aider-style baseline is not Aider itself.
The historical local spike allocated weighted rank to source declarations rather than destination declarations, so its ranking results are not Aider-parity evidence.
Document full-text backend comparison remains part of the later lexical foundation: compare pure-Go SQLite/FTS5, Bleve and a Tantivy helper under equal retrieval semantics without tying stable source identities to a postings format.
Public research has not established a universal backend winner or verified this design's Pi resource targets.

The completed checks below record the earlier implementation evidence.
They do not close the subsequent review findings; the remediation checklist requires new regression evidence and a final verification pass.

- ✅ Record the owner-authorized scope exception and prerequisite package boundaries in this branch (2026-11-06); final code review remains pending.
- ✅ Test the request/result/error contract through actual limits/permission wrappers, including JSON escaping, small output caps, long entry handles, empty-but-advancing pages and deterministic pagination; `query_test.go`, `model_test.go` and structured-redaction fixtures pass.
- ✅ Bound examined records and index-read bytes for rare prefixes and large excluded ranges; race regressions cover lossless continuation past 1,025 filtered candidates, 50 declarations in 50 files with a four-file hash budget, growth during bounded reading and concurrent generation/cursor admission.
- ✅ Validate declarations, signatures, enclosing context and source ranges on adapter fixtures, malformed/incomplete/generated/minified sources and a pinned 248,811-line real polyglot corpus; the [local report](evidence/wp2.2-mac-20261106/README.md) retains three explicit TypeScript omissions rather than claiming full grammar coverage.
- ✅ Record grammar versions, binary growth, cold/warm extraction cost, memory, finite job limits and interruption behavior, including a deliberately noncooperative helper fixture; see the [adapter evidence](../../internal/repomap/extract/README.md), worker tests and local report.
- ✅ With controlled workspace/index fixtures, test authority and generation changes between query/publication, nested ownership, cursor expiry, paused/unavailable states, partial-read limits, same-stat rewrites and verification-budget exhaustion; component race checks passed.
- ✅ With controlled scheduler/runner fixtures, test parser pause/cancellation, no premature slot reuse, no background-pool bypass and known-emergency cancellation; manager/governor/worker tests cover the delivered foundation, while document extraction and durable notes remain later work.
- [ ] Measure schema cost and unnecessary/invalid/repeated calls on paired positive/negative tasks at 8k/32k windows as specified in the contract; include output reserve, real token counts or labeled estimates, prompt processing, compaction and task correctness.
- ✅ Fix cold-request/catalog-change schema accounting without double-counting provider usage or changing the instruction-preamble budget; targeted agent tests, race checks and vet passed.
- ✅ Pass formatting, whole tests, race checks, vet, pinned golangci-lint v2.13.2 and all six `CGO_ENABLED=0` builds, including linux/arm64; final source hashes, command results and production-helper smoke are recorded in the [verification artifact](evidence/wp2.2-mac-20261106/verification.json).

#### Review remediation checklist

Recorded on 2026-11-06 after the owner's implementation-review feedback.
Work through these slices sequentially in `dev/wp2.2-repo-map`.
On 2026-11-07 the owner authorized each small remediation step to proceed through implementation, self-review, verification and a separate commit, with owner review of the combined result afterward.
Use `- [ ]` for work not started, `- ⏳` for work in progress and `- ✅` only when the stated checks pass.
Every fix starts with a reproducing regression, followed by the implementation and targeted verification.
This authorization is limited to WP2.2 / #93 remediation; it does not authorize pushing, PR creation or issue closure.

##### Review consolidation

- [ ] Collect the remaining `repomap` review and consolidate duplicate findings by mechanism, with source locations and reproduction conditions.
- [ ] Record every confirmed P0/P1 and relevant P2 in the slices below; the supplied summary does not describe its P0, so obtain its concrete scenario before claiming it addressed.
- [ ] Distinguish demonstrated defects, intentional tradeoffs and defensive hardening; retain a written disposition for each finding.

##### Slice 1 — Safe source access and path identity

The FIFO `.gitignore` policy-read and metadata-only `Lease.Stat()` fixes are complete (2026-11-07).
Extraction and freshness-validation opens admit only regular files, and discovery opens admit only directories.
Slice 1 implementation and local verification are complete; final owner review remains part of the combined handoff.

- ✅ Reproduce the FIFO policy-read hang in a subprocess with an external deadline, then implement regular-file admission that also handles replacement immediately before opening; both regressions failed by timeout before the fix and pass afterward (2026-11-07).
- ✅ Verify policy-read rejection, shared-slot release, cancellation/shutdown and unchanged regular `.gitignore` fingerprints; targeted and whole race/tests, vet, pinned lint, source formatting and all six CGO-disabled builds passed (2026-11-07).
  `TestPolicyFIFO` covers an existing FIFO and deterministic replacement immediately before the rooted open, with a parent-process timeout protecting the test itself.
  `TestPolicyFingerprintRegularAdmission` covers absent/directory/canceled policy reads; runtime verification was on macOS, while Linux/Windows builds establish compilation only.
  Command results are retained locally in `.tmp/wp22-policy-fifo-checks.json`; this narrow fix is ready for owner review and does not complete Slice 1.

- ✅ Replace `Lease.Stat()`'s content open with rooted metadata observations and authority/identity rechecks; the FIFO subprocess regression failed by timeout before the fix and passes afterward (2026-11-07).
- ✅ Verify FIFO metadata, file replacement, access revocation, nested boundaries and root replacement; targeted race checks, whole tests/race/vet, pinned lint, source formatting and all six CGO-disabled builds passed (2026-11-07).
  Self-review checked that metadata reads never open contents, errors return no metadata, and final authority checks reject revoked roots and changed ownership even when inode identity matches.
  `TestLeaseStatFIFO`, `TestLeaseStatMetadata` and `TestLeaseStatRechecks` retain the regressions; command results are local in `.tmp/wp22-stat-checks.json`.
  Runtime verification was on macOS; Linux/Windows cross-builds establish compilation only.

- ✅ Use regular-file admission for extraction, post-parse rehash and cached-candidate validation; existing FIFOs and replacement before opening/after parsing no longer wait for a writer (2026-11-07).
  `TestSourceFIFO` reproduced all four hangs before the fix and now checks durable omissions, suppressed stale declarations, ordinary-file recovery, cancellation, reusable admission and shutdown.
  Self-review checked both source opens, type-change invalidation, omission publication authority and error-path descriptor ownership.
  Whole tests/race/vet, pinned lint, source formatting and all six CGO-disabled builds passed; command results are local in `.tmp/wp22-source-fifo-checks.json`.

- ✅ Prevent blocking source opens on FIFO and other special files, including `.gitignore`, extraction and freshness validation (2026-11-07).
- ✅ Cover replacement of a regular source with a special file between inspection and opening; a preliminary `Stat` alone is insufficient (2026-11-07).
- ✅ Verify cancellation and manager shutdown after refused or interrupted source access without abandoning work that still owns the shared slot (2026-11-07).
  `TestSourceFIFO/directory` additionally reproduced a directory-to-FIFO race by timeout before the fix and now verifies rejection with no retained stream and successful shutdown.
  `Lease.OpenDirectory()` shares pre/post-open type, identity and authority checks with regular-file admission; `TestLeaseTypedOpen` covers both accepted and rejected types.
  Self-review checked shared admission behavior and descriptor cleanup; arbitrary filesystem/device-driver latency remains outside the guarantee.
  Targeted race checks, whole tests/race/vet, pinned lint, source formatting and all six CGO-disabled builds passed; command results are local in `.tmp/wp22-directory-fifo-checks.json`.
- ✅ Reject out-of-root inputs such as `..` at the ignore API boundary before filesystem or Git access, while preserving legitimate primary-root Git policy (2026-11-07).
  `TestIgnoreRejectsEscapingPathsBeforeAccess` reproduced uncached Git access and cached-path admission; it and `TestPrepareIgnoreRejectsEscapingParentBeforeGit` now pass.
  Self-review covered empty/root paths, cached decisions, batch preparation, NUL/absolute/traversal rejection and native Windows lexical forms without changing Git-root discovery or ancestor policy.
  This is a lexical API boundary; walkers remain responsible for symlinks and rooted authority.
  Targeted race checks, whole tests/race/vet, pinned lint, formatting and all six CGO-disabled builds passed; command results are local in `.tmp/wp22-ignore-boundary-checks.json`.
- ✅ Define one path/entry-ID canonicalization contract across workspace and catalog, including Windows native separators and drive forms without rejecting valid Unix filename bytes (2026-11-07).
  `internal/pathkey` owns separator normalization and canonical catalog-path validation; workspace/catalog wrappers share its entry-ID calculation.
  `TestEntryIDMatchesWorkspaceNativePaths` retains fixed hash vectors and `TestCatalogPlatformPaths` checks source/frontier admission and lookup without losing raw bytes.
  The Windows mismatch is established by the former differing hash inputs; its regression tests compile for Windows but were not executed on this macOS host.
  Self-review checked byte preservation, root scope, root-marker separation, Windows drive/backslash rejection and the absence of case/whitespace normalization or new access grants.
  Targeted race checks, whole tests/race/vet, pinned lint, formatting and all six CGO-disabled builds passed; command results are local in `.tmp/wp22-path-identity-checks.json`.
- ✅ Pass targeted access/path regressions and relevant portability checks, and retain the reviewed slice for final owner review (2026-11-07).

##### Slice 2 — Discovery completion and stable policy revisions

- ✅ Give frontier work explicit outcomes: completed observation/omission, delayed retry for temporary unavailability, and cancellation that preserves unfinished work (2026-11-07).
- ✅ Retire or defer already queued items that become denied, unreadable or nested ownership boundaries instead of continuously rotating them in the hot queue (2026-11-07).
- ✅ Preserve partial coverage for unreadable/offline directories; neither a failed scan nor a canceled scan proves emptiness or deletion (2026-11-07).
  `TestFrontierTerminalAndRetryOutcomes` first reproduced all three hot-queue failures; it now covers terminal drain, delayed recovery, offline-root reconciliation, retained-stream cleanup and cancellation without false deletion.
  `TestFrontierDelaySurvivesRestartAndNewInvalidation`, `TestFrontierV2MigrationPreservesWork` and `TestFrontierRetryUsesBoundedSeek` cover durable scheduling, revision races, migration/pin preservation and indexed ordering without a temporary sort.
  Self-review added clearing stale directory omissions after successful enumeration and retrying failed root reconciliation; publication, retained descriptor ownership, cancellation and newer invalidations were rechecked.
  Whole tests/race/vet, pinned lint, formatting and all six CGO-disabled builds passed; the additional retained-stream regression passed under race checks.
  Command results are local in `.tmp/wp22-frontier-outcomes-checks.json`.
- ✅ Make policy fingerprints durable independently of the bounded RAM cache, so revisiting an unchanged policy after 128 cached entries does not increment its epoch (2026-11-07).
  `TestPolicyRevisionBeyondCacheCapacityAndReattachment` reproduced an unchanged second pass advancing epoch 140 to 152; it now preserves the epoch, advances once for an edit beyond 128 entries and restores it on root reattachment.
  `TestPolicyObservationsAreDurableAndRootScoped` covers restart, absence/deletion/recreation, root separation and atomic rejection of canceled or oversized observations.
  Self-review removed the per-root path map, preserved monotonic publication across concurrent tracking/observation, retained discovery after a failed revision read, and avoided keeping absent-policy tombstones.
  Whole tests/race/vet, pinned lint, formatting and all six CGO-disabled builds passed; the expanded beyond-capacity edit case also passed under race checks.
  Command results are local in `.tmp/wp22-policy-durable-checks.json`.
- ✅ Test queue draining and truthful ready/partial states, retry recovery, ownership changes after enqueue, and policy-cache saturation at and beyond 128 entries (2026-11-07).
- ✅ Pass targeted discovery/policy regressions and retain this reviewed slice for final owner review (2026-11-07).

##### Slice 3 — Query reservations and truthful results

- [ ] Release reservations for failed new queries that never delivered a usable cursor and for definitively invalidated queries, using bounded cleanup independent of an already canceled caller context.
- [ ] Preserve reservations on retryable continuation errors; test that retrying the same cursor neither loses nor duplicates records.
- [ ] Release this manager's own reservations on normal shutdown without deleting reservations owned by another live process.
- [ ] Align extractor and catalog admission limits; an oversized declaration or source result must produce a durable explicit omission rather than a permanently failing pending task.
- [ ] Populate `coverage.omissions` consistently with aggregate counts, scope, generation and the permitted small-budget omissions.
- [ ] Align unavailable-root errors and effective output-budget disclosure with the contract, including the below-1024-byte case.
- [ ] Add an explicit final root-list budget check and boundary tests; treat the missing assertion as hardening unless an actual overflow is reproduced.
- [ ] Specify and test `fswalk` behavior for `Close` failure after `SkipAll` and for empty EOF batches; do not suppress cleanup errors without an explicit contract decision.
- [ ] Test post-Pin error/cancellation paths, repeated failed requests, oversized omissions, coverage buckets and low-budget behavior through the real wrappers, then hand this slice to the owner for review.

##### Slice 4 — SQLite initialization, contention and recovery

- [ ] Bound the complete catalog initialization path, including connection setup and PRAGMA work, and distinguish BUSY, capacity and permanent initialization failures.
- [ ] Define supported ownership of the shared cache across Hint processes: concurrent access with tested recovery, or an explicit owner restriction that leaves ordinary Hint operation available.
- [ ] Test simultaneous initialization and writes using separate Store instances/processes, including conflicting storage allowances; a concurrent test on one connection is insufficient.
- [ ] Test exhaustion of the shared disk allowance by discovery work, preserving queued tasks, admitted cursors and truthful partial/capacity status.
- [ ] Verify bounded retries and recovery after contention ends or capacity becomes available; quota-induced busy loops or inability to recover remain #93 defects.
- [ ] Pass targeted storage/recovery regressions and hand this slice to the owner for review.

##### Slice 5 — Integrated acceptance and review handoff

- [ ] Run the combined access, discovery, policy, reservation and storage failure scenarios through the integrated workspace/map path.
- [ ] Resolve all confirmed blockers from every review pass and retain reproduction, fix and verification evidence for each disposition.
- [ ] Synchronize the contract, phase plan, roadmap, user documentation and review record with the corrected behavior and remaining limitations.
- [ ] Run formatting, whole tests, race checks, vet, pinned lint and all six CGO-disabled builds against the final source state.
- [ ] Repeat only measurements affected by the fixes; synthetic million-record metadata results do not substitute for filesystem failure or traversal tests.
- [ ] Summarize the reviewed remediation commits and invite final owner review without pushing or closing #93.

#### Proposed follow-up — Catalog quotas and cleanup

This is a proposed subsequent task, not an implemented work package or an existing issue.
It improves storage efficiency and maintenance after #93's correctness and recovery requirements pass.
Hangs, data loss, broken continuation and failure to recover from available capacity cannot be deferred here as optimizations.

- [ ] Formalize the follow-up's scope, branch, issue and acceptance criteria under the planning conventions before implementation.
- [ ] Introduce separate frontier/data budgets and processing headroom, with backpressure that preserves bounded discovery and useful file-processing progress.
- [ ] Refine per-root GC to retain only necessary versions and consumed cursor anchors; active pins currently conservatively retain that root's obsolete versions.
- [ ] Reclaim reservations left by crashed owners without deleting another live process's pins; define ownership and expiry behavior explicitly.
- [ ] Define retention and bounded cleanup for detached-root obsolete versions and frontier tasks, accounting for other authorized sessions or processes.
- [ ] Clean obsolete derived-cache schema files safely, preserving user data and any future durable notes.
- [ ] Measure long-running wide/deep filesystem traversal, repeated edits, many roots, quota pressure and cleanup recovery separately from synthetic catalog insertion/query measurements.

#### Proposed separate acceptance task — Pi/model evaluation and auto mode

Separating these measurements from #93 is a proposed scope change, not approval to remove an existing acceptance requirement.
Until the owner agrees and the phase/issue criteria are updated, the existing Pi/model gates remain open.

- [ ] Obtain an explicit disposition on closing #93 as a correct manually enabled capability while moving Pi/model acceptance into a separately tracked task.
- [ ] If approved, record the revised closure boundary in this phase file and issue #93, with the new task's branch, issue and acceptance criteria.
- [ ] Have the owner run the saved Pi 4 4 GB and local-model 8k/32k methodology, retaining unsupported and unmeasured conditions explicitly.
- [ ] Measure task usefulness, schema overhead and indexing interference before approving numerical resource profiles or default tool disclosure.
- [ ] Keep unvalidated `auto` paused until the separate acceptance criteria and owner review pass.

Document full-text search, PDF/DOCX/OCR adapters, durable agent notes, semantic retrieval and reference ranking remain outside #93 as already specified above.
Their later implementation does not replace any remediation item in this branch.

### WP2.3 — Vector memory

WP2.3 owns embedding-backed storage and semantic retrieval for document chunks, notes and history.
It consumes authorized source identities, locators, extractor versions and invalidation from the lexical foundation; WP2.7 only exposes/configures that capability through integration adapters.
The dependency order for document search is the lexical foundation, then WP2.3 semantic indexing/retrieval, then WP2.7 integration; lexical search and code maps do not wait for embeddings.

- [ ] First `Embedder` implementation: openai-compatible `/v1/embeddings` + Ollama
- [ ] Storage: sqlite-vec
- [ ] `memory_search` tool + automatic fact capture (opt-in)
- [ ] Index and retrieve document chunks semantically with source locators, scoped authorization and deletion/version invalidation from the shared lexical foundation.
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
  Configure and expose WP2.3's semantic document retrieval through integration adapters after the local lexical baseline; WP2.3 owns semantic indexing/retrieval, not this package.
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

## Workspace/index integration gates for v0.4

These gates cover the phase's complete workspace/index scope.
The authorized WP2.2 scope exception implements the workspace and code-catalog baseline; document adapters and durable notes still need their own formalized work package.
The full workspace, lexical document/notes index and on-demand code-map scope is required for v0.4, with optional heavy modules allowed to be unavailable or paused.
Approval of WP2.2 does not imply that the later document/notes implementation or its backend has passed review.

| Owner | Verification boundary |
|---|---|
| WP2.2 component | Extraction correctness/cancellation, deterministic scoped retrieval, bounded JSON/cursors, schema cost and fake workspace/index/scheduler contract tests. |
| Workspace baseline in WP2.2 | Root/entry identity, clone lifecycle, routing, nested ownership, membership history, instruction readiness and authorization. |
| Code-index baseline in WP2.2 | Catalog/frontier, resource governor, invalidation, reconciliation and storage recovery. |
| Later lexical document/notes foundation | Document adapters, lexical retrieval, durable notes and their source-validation/retention contracts. |
| Phase integration | Real services working together across multiple roots, low-resource operation and measured task benefit. |

- [ ] Formalize the remaining document/notes scope, branch, issue and acceptance criteria, then re-estimate v0.4 work using dependency order and measured parser/backend costs.
- [ ] With real services, cover Git and ordinary folders, dirty/untracked files, separate worktrees, failed/interrupted clones, nested repositories/submodules, symlink aliases, duplicate names, detach/reattach and source changes; only explicitly authorized roots become available.
- [ ] Exercise `/repos`, the paginated text tree and agent navigation in both shells through RPC; root changes occur between turns, instructions precede edits, and revocation suppresses queued/in-flight cached publications to the affected session.
- [ ] In the index foundation, test 10k/100k/1m metadata entries, wide/deep trees, many roots and a tiny root competing with a huge one; assert bounded batches, queues, active streams/FDs, caches and staging disk, plus tail progress whenever capacity is available.
  The [Mac catalog measurements](evidence/wp2.2-mac-20261106/metadata.md) complete the synthetic 10k/100k/1m cases, and manager tests cover tiny/wide-root progress, eight-stream capacity, blocked-child priority and retained-tail completion without repeated frontier scans.
  This does not establish million-file filesystem traversal latency, peak staging disk under all workloads or hardware/model acceptance; the full phase gate remains open.
- [ ] Cover watcher loss/overflow, external same-stat changes, offline/unreadable directories, interrupted enumeration, generation/cursor eviction, crash/restart and disk exhaustion; unfinished scans never imply deletion, and partial status never implies full coverage.
- [ ] Index English/Russian text and DOCX with source locators, plus text-layer PDF through a connected local module; test absent modules, malformed/oversized inputs and optional OCR admission/cancellation without blocking live file access.
- [ ] Persist and inspect/delete source-linked agent notes across sessions; changed/deleted or unauthorized sources invalidate their evidence, stale jobs cannot resurrect deleted notes, and rebuilding derived caches preserves durable notes.
- [ ] Publish numerical resource profiles and verify governor hysteresis, telemetry loss, overlapping inference, emergency pause and resumable progress with fake clocks plus measurements on Pi 4 4 GB alongside a local model.
- [ ] Compare indexing off/on in cold/warm conditions with the same model/configuration, storage and cooling; record additional RSS for the Hint process and all indexing helpers relative to the indexing-off baseline, CPU/I/O, temporary disk, p95/median latency, time to first token, throughput and thermal/throttling state against the proposed 128 MiB and 5% median-degradation targets.
- [ ] Run paired tasks for live tools, file catalog, lexical text retrieval, symbol/map retrieval and added notes; report correctness, source-evidence accuracy, prompt/output tokens, tool calls, latency, index-creation cost and regressions before claiming a performance gain.

The million-entry discovery fixtures do not replace WP2.2's separate 100k-line polyglot extraction corpus.
The existing Pi hardware record predates indexing and includes thermal throttling; it is not evidence that these new resource targets have passed.
Continuous pressure, unavailable storage or a perpetually changing tree may prevent index completion; live navigation and truthful partial results must remain usable.

## Acceptance criteria

1. A third-party MCP server from config contributes tools that the agent uses under the permission system, a resource it serves is read as an instruction file, and a prompt it serves runs as a slash command.
2. Across attached Git and ordinary-folder roots, the agent answers "where is X implemented?" using an on-demand code map without reading every file, with explicit partial coverage and bounded resource use on large trees.
3. `memory_search` recalls a fact stored in a previous session.
4. With auto-commit on, every applied edit is a well-formed git commit.
5. Another agent fetches `hint serve`'s Agent Card and completes a task through A2A, answering a permission question on the way; `hint` delegates a question to a remote agent named in its config and uses the answer under the permission system.
6. An OpenAPI-described API and a read-only database appear as tools from config alone, each under its declared permission class.
7. With more MCP tools than the WP2.1 budget, the agent finds and calls a hidden tool without a restart.
8. The agent searches indexed text/documents and reuses source-linked notes across sessions; optional modules and resource pauses remain explicit, and deleting/rebuilding derived caches preserves durable notes.
