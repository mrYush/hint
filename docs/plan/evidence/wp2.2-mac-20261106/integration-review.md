# WP2.2 integration review

Reviewed on 2026-11-06 by the delegated query/index agent after its component work.
This review concerns the parent-owned integration and the explicitly identified seams with the other agents.
The reviewer is a delegated query/index implementer, independent only of the root/workspace code inspected here; IR4 and IR6 also record its own implementation and self-review.
It is a local review artifact, not a claim of independent human approval or completed whole-repository acceptance.
No commits, pushes or remote mutations were performed.

## Findings and dispositions

- ✅ **IR1 — Historical boundaries looked like live filesystem entries.**
  `internal/repomap/tree.go` accepted `workspace.ErrBoundary` for cached boundary entries without a current existence observation.
  The local-only, untracked reproduction in `.tmp/wp22-repro-boundary.go` attaches a child, stores its parent boundary marker, detaches it and deletes the child directory; the tree continued to show the marker.
  The observed reproduction output was `deleted_boundary_visible=true`.
  The parent deliberately retains this authority reservation but now labels it `ownership reservation; availability unverified` rather than implying that the directory exists.
  This avoids opening a nested root through its parent merely to infer availability.
  `TestTreeHistoricalBoundaryIsNotLiveExistenceClaim` in `internal/repomap/tree_test.go` passed in the targeted tree suite and provides the repository regression artifact.

- ✅ **IR2 — Secondary preparation lacked an opaque target handle.**
  `repo_prepare` originally accepted only a JSON path, while routed edits could target an `entry_id` for a non-UTF-8 filename.
  Such a filename could not be represented by the preparation request, so its exact target fingerprint could never be delivered before mutation.
  The workspace owner added mutually exclusive `entry_id` resolution through `Manager.ResolveEntry` in `cmd/hint/rpcinstructions.go`.
  The added regression then found that entry-ID routing passed the absolute target path to the secondary preparation guard, which requires a root-relative path.
  The owner corrected the guard argument to `target.RelativePath`.
  `TestRepoPrepareEntryIDPreservesExactTarget` in `cmd/hint/rpcinstructions_entry_test.go` covers exact-byte preparation, same-batch refusal and a later permitted write.
  The workspace owner reports a passing focused race run for the leading-space filename; APFS rejected the raw non-UTF-8 filename fixture, so that physical-file subcase was skipped on this host.

- ✅ **IR3 — Routing checked values rather than field presence.**
  The old routing code accepted `entry_id` combined with an explicitly empty `path`, despite the mutual-exclusion contract, and accepted null routing strings as zero values.
  The workspace owner added routing-field null checks and presence-based path/entry exclusivity in `cmd/hint/rpcreporoute.go`.
  `TestRepoRoutingRejectsNullAndAmbiguousFields` and `TestRepoPrepareRejectsNullAndAmbiguousFields` in `cmd/hint/rpcinstructions_entry_test.go` passed in the workspace owner's focused race run.

- ✅ **IR4 — Pure-Go glob retained every match before limiting output.**
  Routed tools intentionally bypass ripgrep, so the old `globWalk` accumulation defeated arbitrary-width memory bounds even after streaming directory iteration was added.
  The delegated fix retains a worst-first heap of at most 201 matches, preserving the existing newest-modified-first contract and adding deterministic lexical ties.
  It uses the workspace owner's bounded directory-batch/ignore API.
  `TestGlobWindowMatchesFullSortOracle` in `internal/tool/builtin/glob_window_test.go` checks 10,000 matches in ascending, reversed and shuffled arrival order.
  `TestGlobWindowPreservesShortResultsAndTruncationBoundary` covers 0/1/199/200/201/202 matches.
  Existing builtin tests, targeted glob race tests and scoped pinned lint passed.
  This does not claim the standalone ripgrep subprocess output buffer was redesigned; routed tools use the pure-Go path.

- ✅ **IR5 — No manager maintenance consumed obsolete catalog versions.**
  A search initially found no calls to `Store.GC` under `internal/repomap`, so even after snapshot pins expired, repeated edits could consume storage indefinitely.
  The manager owner added bounded maintenance under the shared admission lease and a regression test that retains active pins before collecting released versions.
  `TestManagerMaintenanceRetainsPinsThenCollects` in `internal/repomap/reconcile_test.go` passed in the targeted suite.
  Active pins still conservatively retain obsolete raw ordering anchors, so storage capacity can be reached legitimately during heavy edits or prolonged pins.
  Paused/disabled background work does not promise eventual cleanup; quota pressure remains explicit.

- ✅ **IR6 — A resumed map checked candidates but not its saved scope before emitting metadata.**
  The query owner corrected its own integration seam: every slice now checks the current scope under focused admission before constructing its envelope.
  A removed or newly excluded continuation scope yields a stale cursor instead of echoing saved scope metadata as a normal result.
  `TestMapCursorDoesNotRevealRemovedScope` in `internal/repomap/query_test.go` covers removal.
  This is the query implementer's self-review correction, not an independent review of that component.

## Inspected integration behavior without an additional finding

`cmd/hint/rpcmapprovider.go` starts inference observation before the provider call, handles startup error and nil streams, and releases once on terminal event, channel close or cancellation.
Provider construction and cache paths in `rpcengine.go`/`rpcprofiles.go` use the wrapper, including completion's `providerFor` path and the router containing fallback.
The wrapper has one buffered output event and replaces a pending buffered delta with a terminal `ChatError` carrying `ErrCanceled` on cancellation.
It closes promptly without retaining a forwarding goroutine when the former consumer disconnects.
This observes request lifetime; it cannot prove that an independently running inference server has stopped computation after an HTTP cancellation.

Engine initialization closes an opened root on failure, and later failures also close the manager, workspace registry and MCP manager.
The primary root and attached-root policy both deny the code-index cache directory.
Shutdown is idempotent and reports joined cleanup errors.

Turn execution takes the process index-mode read lease and workspace turn lease before MCP preparation and releases both on startup failure.
The session retains the existing terminal-event forwarding behavior so the plain REPL can observe interruption.
The RPC server, rather than a cancel-select in session forwarding, drains cancelled turns; leases release after the agent event channel is consumed.
Index-mode mutation uses a nonblocking exclusive process lease, so it cannot change the tool catalog in an active turn.
The next turn rebuilds the agent on a mode change, invalidating the context-cost baseline for the changed catalog.

Tree pagination reserves the actual continuation footer, maintains a consumed entry handle and installs a disk mapping for an undiscovered scope.
It treats tree pages as current observations rather than immutable snapshots.
Root listing and tree formatting bound escaped display bytes separately from opaque addressing.
The targeted small-budget, revoked-cursor and deleted-entry tests passed.

## Verification observed by this reviewer

Commands used `GOCACHE=/private/tmp/hint-wp22-code-cache GOPROXY=off`.

- After restoring terminal forwarding, `go test -race ./cmd/hint ./internal/rpc -run 'TestInferenceProvider|TestREPLInterrupt|TestClient_CancelEndsTurn|TestMapToolThroughEngineAndProvider|TestIndexModeChangesCatalogBetweenTurns|TestExtractionCommandRegisteredWithoutConfiguration' -count=1` passed.
- `go test ./internal/repomap -run 'TestTree|TestRootListing|TestRepoList|TestManagerMaintenance' -count=1` passed.
- `go test ./internal/tool/builtin ./internal/index` passed after the bounded glob change.
- `go test -race ./internal/tool/builtin -run TestGlob -count=1` passed.
- Pinned `golangci-lint run ./internal/tool/builtin/... ./internal/index/...` reported 0 issues after the glob change.
- `go test -race ./internal/index ./internal/repomap -run 'TestConcurrentGenerationAndPinAdmission|TestMapReportsExhaustedHashBudget|TestMapLargeExcludedRange' -count=1` passed for the expanded acceptance fixtures.
- `go test -race ./internal/index -count=1` passed after the bounded current-frontier lookup was added.
- `go vet ./internal/index ./internal/repomap` passed and pinned scoped `golangci-lint` reported 0 issues after those additions.
- The [focused verification transcript](integration-focused-tests.txt) records the final command outcomes observed directly by this reviewer.
- Response contracts and output-budget semantics are described in the [repo-map contract](../../repo-map-contract.md), with regression fixtures in `internal/repomap/query_test.go`, `internal/repomap/model_test.go` and `internal/tool/structured_redact_test.go`.
- `git diff --check` was clean at the review checkpoint; it does not include new untracked files in its output.

The parent still owns the final whole-repository gates, final six-target build, feature documentation synchronization and invitation to the user's review.
Pi 4/local-model 8k/32k acceptance measurements remain explicitly assigned to the user.

## Expanded local acceptance fixtures

The final hash-budget fixture follows all 50 declarations in 50 physical files through output-limit and redaction wrappers.
It asserts at most four content-checked files per page, truthful budget exhaustion for observed files, valid retained continuations, and no duplicate or lost files/declarations.
The 1,025-candidate prefix fixture traverses out-of-scope records using empty advancing pages before returning the following valid scoped declaration exactly once, without filesystem validation of excluded paths.
Concurrent admission races 24 pin requests against a four-pin global quota while two roots independently publish six replacement generations.
It checks the exact admission count, retained source/symbol consistency for every accepted snapshot, cross-root rejection and reuse of released capacity.
These are implementation tests by the query/index owner, not additional independent reviewers.
The separate [metadata scaling report](metadata.md) records actual 10k/100k/1m catalog timings, memory and query allocations with its workload limitations.
