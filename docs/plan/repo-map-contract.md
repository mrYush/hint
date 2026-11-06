# WP2.2 code-map contract

Design for review, 2026-11-06; subordinate to [WP2.2](phase-2-memory-mcp.md#wp22--repo-map) and tracked in [#93](https://github.com/mrYush/hint/issues/93).
This specifies the interfaces WP2.2 consumes and exposes, not an implemented API or a separate work package.
The workspace and shared-index foundations remain proposed prerequisites; their implementation and phase-level acceptance are separate from the map component.

## Request and result

`repo_map` is a read-class tool and uses the existing `agentapi.ToolResult` with one text part containing a compact JSON object.
It does not require a new provider error kind or a new top-level wire type.
Independent calls may run concurrently, but share the indexing scheduler and its global budgets.

| Request field | Contract |
|---|---|
| `repo` | Optional stable root ID or unique alias; omission means the immutable primary root, never the last root used. |
| `path` | Root-relative directory or file, default `.`; mutually exclusive with `entry_id`. |
| `entry_id` | Alternative scoped workspace entry handle for long or nonrepresentable paths; accepted by workspace navigation and file reads too. |
| `symbol` | Optional identifier filter; no filter means the declarations in the requested scope. |
| `match` | `exact` or `prefix`, default `exact`; only meaningful with `symbol`, using case-sensitive identifier spelling without document-language stemming. |
| `limit` | Default 20, range 1–50 declarations; the byte budget can produce fewer. |
| `cursor` | Opaque continuation token; mutually exclusive with all other request fields. |

Reject unknown fields, malformed combinations and paths outside the selected root instead of silently widening or changing scope.
Apply authorization, exclusions, root/subtree and symbol filters before pagination.
Resolve aliases once to a root ID; persist that ID, not the alias, in continuation state.

An illustrative response, shown expanded for readability:

```json
{
  "root_id": "r1",
  "membership_revision": 7,
  "generation": "g42",
  "scope": {"entry_id": "d9", "path_display": "src"},
  "order": "path,start_byte,kind,symbol_id",
  "status": {"state": "building", "reason": null},
  "coverage": {"discovery_complete": false, "known_files": 120, "indexed_files": 80, "omitted_files": 3},
  "files": [{"id": "f3", "path_display": "src/a.go", "source_version": "sha256:...", "freshness": "observed", "observed_at": "2026-11-06T07:00:00Z"}],
  "entries": [{"id": "s7", "file_id": "f3", "kind": "function", "name": "Open", "signature": "func Open() error", "enclosing": "store", "start_byte": 24, "end_byte": 81, "start_line": 3, "end_line": 5}],
  "verification": {"files_checked": 0, "bytes_read": 0, "budget_exhausted": false},
  "page": {"truncated": true, "query_complete": false, "stop_reason": "limit", "next_cursor": "opaque-token"}
}
```

File records deduplicate paths, hashes and freshness metadata shared by several declarations.
Declaration IDs are stable within a source/extractor version, not a promise of identity across arbitrary edits.
Declaration ranges are zero-based UTF-8 byte intervals `[start_byte,end_byte)` in the observed source version, with one-based line coordinates.
The range may cover the declaration body for locating a later read, but the returned signature never contains that body.
`enclosing` names the containing type/module where the extractor can establish it syntactically; it does not claim semantic name resolution.

`status.state` is `ready`, `building`, `paused` or `unavailable`, describing index capability/work, not request validity or source freshness.
Coverage describes the selected scope at the observed generation, including omitted files by reason and whether discovery completed.
Use maintained aggregates or omit unavailable counts; calculating exact subtree counts must not trigger a complete scan on each query.
Omission reasons include `unsupported`, `oversized`, `parse_failed`, `paused`, `deadline_exceeded` and `work_budget_exceeded`.
Unknown total counts are absent; never derive a percentage from an undiscovered denominator.
`ready` does not imply zero omissions, and no next cursor means the known generation is exhausted, not that the live filesystem has no other matches.
Bound omission details using `coverage.omissions` as reason/count buckets; unavailable aggregate counts are absent, and more detail is available through scoped `repo_list` status queries.
`page.query_complete` means that this query exhausted its retained generation, independently of discovery coverage.
`page.stop_reason` is `limit`, `byte_budget`, `work_budget`, `time_budget` or null; a normal work slice may return no entries and a progressing cursor.

### Byte limit and wrapper integration

The cap is at most 8192 UTF-8 bytes of the actual serialized JSON text, including JSON escaping, file records, coverage, status, errors and cursor.
It excludes the outer `ToolResult`/provider framing, whose cost must still be measured separately.
The effective cap is the minimum of the map cap and the configured outer text-output limit; zero in the outer limiter does not remove the map cap.
The builder reserves envelope/cursor space and admits whole entries, counting each required file record before accepting its declarations.
Long display names or signatures may be abbreviated with explicit truncation flags, while scoped opaque handles and coordinates remain usable.
Reduce an oversized entry to an addressable stub with declaration/file handles, coordinates and an explicit omission reason before consuming it; do not silently lose its location or trap every subsequent page behind it.
Bound identifiers/cursors and optional display metadata so the minimal envelope plus one stub fits 1024 bytes; verify that invariant with worst-case serialization fixtures.
If the effective configured cap is below 1024 bytes, do not register/disclose `repo_map`; expose the configuration reason through status instead of trying to fit JSON into an arbitrarily small limit.
Apply capability/catalog changes between turns and invalidate the context-cost baseline accordingly; cached calls to an unavailable tool use the existing dispatcher failure path, outside this map-payload contract.

The existing generic `tool.WithLimits` performs head/tail text truncation and can turn JSON into invalid data or lose its cursor.
The map builder must receive the effective budget before serialization, so the generic wrapper never truncates a valid map envelope.
Set the cooperative query deadline before the outer tool deadline, reserving time for serialization and saving continuation state.
Normal work-slice exhaustion returns a bounded partial page and cursor; use structured `query_timeout` only when no safe continuation can be saved.
This does not guarantee JSON before an earlier caller cancellation or a blocked noncooperative operation; those retain the existing cancellation/timeout semantics.
Test through the actual limits and permission wrappers, including Unicode/escaping, outer limits below 8 KiB and whole-turn cancellation.
Do not disable the common permission or cancellation mechanisms to preserve formatting.

### Cursor and errors

A bounded opaque token references saved query parameters, scope, root ID, session membership revision, extractor/exclusion policy version, retained immutable generation and last consumed ordering key, including the exact/prefix phase when applicable.
Cursor lifetime and retained state have finite configured TTL/count/disk limits; no read transaction stays open between calls.
Admit a cursor only with a retention reservation; new generations or new cursor requests do not silently evict that reservation before expiry, except for explicit emergency eviction or authority/policy invalidation.
Retention uses bounded versioned storage, not a full copy of the scope per cursor; exhaustion refuses new reservations or reports an explicit eviction.
If a required reservation cannot be admitted, return `index_unavailable` with `retryable: true` and bounded reason `retention_capacity`, never a successful page that falsely implies exhaustion without a cursor.
A complete traversal of an arbitrarily large scope within a finite TTL is not guaranteed; return expiry metadata and support narrower scopes instead of promising indefinite retention.
Advance the consumed key for returned records, definitive filter exclusions, known invalidations and explicit permanent omissions; this permits progress even on pages without returned declarations.
Do not consume the next eligible candidate merely because this page cannot finish its mandatory checks or fit its complete record; resume before that candidate on the next page.
Changing membership, changing policy or evicting the retained generation invalidates the cursor; return `stale_cursor` and require a fresh request without it.
Never silently restart pagination or fall back to another root.
Every continuation rechecks current access before exposing saved scope or text; a cursor or entry handle is not an access grant.
For an unchanged retained generation, eligible records are reachable without page-induced duplication or omission; source invalidations are reported separately.

Every query slice has finite examined-record, index-read-byte, mandatory-check and time budgets in addition to its output limit.
Use ordered indexed range iteration and resumable filtering; `LIMIT` over an unbounded scan or full-result sort does not satisfy this contract.
If the budget runs out after filtering many records, return the consumed position and `query_complete: false`, rather than repeatedly restarting from the beginning.
Do not promise constant page latency or `O(log N + page_size)` for arbitrary filter combinations without a measured access path.

Request failures return `IsError: true` and a JSON `error` object with `code`, bounded `message` and `retryable` fields.
Codes are `invalid_args`, `unknown_root`, `access_denied`, `root_unavailable`, `scope_not_found`, `stale_cursor`, `index_unavailable` and `query_timeout`.
`stale_cursor` is not retryable with the same arguments; a new scoped request may succeed.
Partial coverage, paused extraction and unsupported source files are ordinary response states, not failed tool calls.
Keep the code in the text payload: the existing `ToolResult.Message()` does not carry `IsError` into the provider message.
Machinery failures and cancellation inherited from the turn retain the existing Go error/turn-cancellation semantics.

## Freshness without unbounded foreground reads

Source hashes identify the full bytes used for extraction and are stored with extractor/query-policy versions.
Current filesystem access remains authoritative, and an index generation is an observed view rather than an atomic filesystem snapshot.
Return one of two per-file freshness states:

| State | Evidence |
|---|---|
| `content_checked` | This request reread the entire source through authorized rooted access, matched its content digest and applicable extractor policy, detected no identity/stat change around the read, and rechecked access before publication; include `checked_at`. |
| `observed` | The map refers to an earlier observation that has not been completely revalidated in this request; include `observed_at` and omit `checked_at`. |

Stat changes trigger invalidation, but matching stats never establish `content_checked`.
Known changed, deleted or inaccessible sources are suppressed from normal map entries; report bounded counts/reasons and prioritize refresh.
Do not retain a known stale signature under the weaker `observed` label.
Every returned candidate, including `observed`, must pass a current rooted existence/access and metadata check before publication.
These mandatory checks use the query budget; if there is insufficient budget, end the page before consuming the candidate instead of exposing unchecked text or losing the candidate from continuation.
For optional full-content revalidation, propose initial per-response limits of four distinct files, 1 MiB of additional source reads and 100 ms of cooperative hashing time, whichever is exhausted first.
These are reviewable work budgets, not achieved latency guarantees; effective limits may be lower under resource policy and are visible in diagnostics.
Deduplicate validation by file within a response; oversized candidates remain `observed` after mandatory checks rather than blocking every page.
Do not start a complete hash when the observed size exceeds the remaining byte budget, cap actual reads if the file grows, and charge every byte read to that budget.
A read stopped by the cap has no complete digest and cannot produce `content_checked`; the same source need not be fully hashed again on every page.
A partial tool read is reusable range evidence but cannot validate the full AST, enclosing context or whole-file digest.
Hash only complete bounded reads; never update the digest on old extracted data merely because new bytes were observed.

Reject an extraction or validation result when a source mutation or identity change is detected before publication.
Even pre/post metadata checks cannot exclude every concurrent same-stat mutation; `content_checked` means the bytes read matched at the recorded observation, not that a file is immutable or safe to edit without another current read.
External same-size/restored-mtime edits are discovered by candidate rehash or background content reconciliation, not reliably by stat-gated checks or watchers alone.
A page without entries and `query_complete: false` says nothing about the unexamined remainder of the indexed generation.
Only a completed traversal without matches is a negative result for that generation; it still never proves absence on the current disk.
Hash-budget exhaustion returns truthful `observed` entries that passed mandatory checks, plus status, instead of upgrading confidence or silently extending the request indefinitely.
Notes and edits that require fresh evidence cannot treat `observed` map entries as verified source reads.

## Deterministic first version; graph ranking deferred

The first version indexes declarations, signatures, enclosing context and source ranges only.
It does not extract references, resolve names, construct a dependency graph or run PageRank.
Without a symbol filter, order by `(relative_path_bytes, declaration_start_byte, kind, stable_symbol_id)`.
For a prefix filter, use `(exact_bucket, name_bytes, relative_path_bytes, declaration_start_byte, kind, stable_symbol_id)` so name-index iteration does not require sorting every match into path order; exact matches form the first bucket and are excluded from the prefix remainder.
Exact mode excludes nonmatching names and keeps path order; `order` in the result identifies the applied tuple.
Subtree/exclusion filtering still consumes the bounded examined-record budget and may require several empty progress pages for sparse matches.
Use canonical relative path bytes for ordering and escaped display text for presentation.
Agent notes and read frequency do not alter this ordering.
The absence of a graph is not incomplete declaration coverage.

A future graph experiment requires its own reviewed input contract: file versus declaration nodes, syntactic reference roles, repeated-name weighting, unresolved/shadowed names, ranking formula and a small exact oracle.
`go/ast` and Tree-sitter declaration queries alone do not establish semantic reference resolution.
Graph work must demonstrate task benefit over this baseline, including construction cost, and cannot block v0.4 or scoped lookup.
Do not reuse the old spike's source-declaration rank as destination-rank or Aider-parity evidence.

## Consumed workspace and scheduling contracts

The following are foundation obligations, verified by WP2.2 with fake services and by the phase integration gates with real services:

- The startup primary root is immutable and the default for legacy tools; starting inside a repository grants no automatic access to a parent.
- Only user-owned configuration or user actions add roots or enable extractors; project files, tool output and notes cannot do so.
- Stable root IDs distinguish worktrees; aliases cannot silently rebind during a session.
- Nested roots own their subtrees, including symlink aliases; parent operations cannot bypass that boundary, and submodules are not initialized automatically.
- Membership changes occur between turns, invalidate affected cursors/grants and queued publications, and prevent in-flight results reaching a detached session; work needed by another authorized session may continue.
- Root instructions must reach the agent before its first edit/execute there; reads, cached metadata and notes do not confer write/execute grants.
- Scoped entry handles work for navigation and reads, including long/non-UTF-8 path names, and are reauthorized on every use.
- The existing shell does not become a filesystem sandbox through this design.

The shared scheduler grants bounded work leases for query, discovery, extraction and maintenance; WP2.2 does not create a separate pool.
Each lease specifies input/output bytes, item or capture/work-unit limits where supported, deadline, source/scope revision and resource reservation.
The deadline is the earlier of the caller deadline and the finite configured job limit.
Parser adapters require a tested native interrupt/fuel mechanism or a supervised process boundary; `context.WithTimeout` around a synchronous parser, including `go/ast`, is not preemption.
An adapter without adequate bounded behavior is excluded from automatic background execution until supervised appropriately.
Never release a worker slot while its canceled job still runs or accumulate abandoned goroutines to simulate cancellation.
Distinguish caller cancellation, the job's own deadline, exhausted work budget and scheduler pause.
Ordinary filesystem calls may remain blocked; bound outstanding operations and report the limitation instead of promising a universal hard deadline.

### Resource admission and resumption

Configured capability mode (`off`, `auto`, `on`) is distinct from effective state (`ready`, `building`, `paused`, `unavailable`).
`on` requests the capability but does not bypass authorization, finite job budgets or emergency resource protection.
Admission atomically checks current authority, capability, available leases, staging/disk reservation and a sufficiently recent resource snapshot.
Required signals are memory headroom/pressure, CPU contention, I/O pressure, disk headroom, active inference requests, active workers/helpers and sample age; a platform-specific source such as Linux PSI is an implementation choice.
The low-resource automatic profile does not start background extraction or maintenance during inference and asks current jobs to checkpoint/pause.
Count inference activity around all provider requests, including main turns, completion, compaction and retries/fallback; release the counter on every terminal/error/cancel path.
Unknown inference placement is treated conservatively as competing work; provider names and context-window sizes do not identify hardware capacity.

High-water crossings stop admission immediately; memory/disk emergencies take the immediate pause/termination path.
Resume only after all required signals satisfy their lower-water conditions continuously for `resume_dwell`, `cooldown` has expired and inference is inactive.
Configuration requires `pause_available_memory < resume_available_memory`, `pause_free_disk < resume_free_disk` and lower resume thresholds for pressure/CPU/I/O than their pause thresholds.
Missing/stale samples cannot be interpreted as zero pressure; use a separately validated conservative profile or keep optional work paused.
Pausing retains checkpoints and a reason; revocation cancels authorization and forbids publication, rather than simply pausing work for later reuse.
Foreground live reads and bounded cached queries bypass the background backlog while retaining their own finite budgets.
Fairness applies among eligible jobs when capacity is available; permanent resource pressure or continuous inference may prevent background completion.

Before auto mode ships, publish a measured low-resource profile with numerical job limits, worker/cache quotas, high/low thresholds, sample interval/max age, dwell and cooldown values.
Validate the state machine with fake clocks/telemetry, including oscillation, emergency pressure, unavailable telemetry, overlapping requests and no premature lease reuse.
The 128 MiB additional RSS and 5% median inference-degradation targets remain proposed evaluation targets; include p95 latency, first-token latency, throughput, thermal state, storage, model/configuration and cold/warm cache conditions.
Do not claim that a Go memory limit or database-cache setting is a process-family RSS ceiling.

`/repos` for the user and `repo_list` for the agent expose root/membership identity, capability mode/state/reason, coverage counts, active/queued jobs, telemetry availability/sample age and effective budgets on request.
`repo_map` returns only the relevant bounded status subset, with no unsolicited background messages.

## Schema and context-budget integration

Measure the exact serialized tool schema and request framing separately from map-result bytes.
Compare the same baseline tool catalog with and without `repo_map` on pinned models/templates at 8k and 32k context sizes, with cold/warm prompt caches and an explicit output reserve.
Use provider usage deltas or a matching tokenizer for token measurements; a bytes/4 estimate is labeled as an approximation.
Test positive tasks and preclassified negative tasks where a map is unnecessary, with paired repetitions and a fixed rubric for ambiguous cases.
Report negative tasks with an unnecessary map call divided by all negative tasks, unnecessary calls divided by all calls, invalid arguments, useless repeats after partial/paused results, missed useful calls and final task correctness.
Measure schema cost for every provider request carrying tools, including repeated tool roundtrips and retries, and aggregate it by turn even when the map is never called.
Also measure map-result costs, prompt-processing latency, overflow and additional compaction.

The current cold-request occupancy estimate counts messages but not tool schemas; later provider usage does not repair that first-request gap.
WP2.2's integration must account for the actual tool catalog in cold requests and after catalog changes without double-counting schemas already represented in provider usage.
Map results remain ordinary tool messages handled by existing history/compaction; they do not consume the separate instruction-preamble allocation.
Define acceptable overhead and false-call thresholds from the paired evaluation before enabling the tool by default; do not substitute an automatic model-window cutoff for that evaluation.
