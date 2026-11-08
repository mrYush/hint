# WP2.2 cross-component workspace review

Date: 2026-11-06.
Reviewer: the manager/resource implementation agent, independently inspecting the workspace agent's changes in the shared working tree.
This is an automated peer review record, not human approval or a claim that issue #93 is ready to close.
No commits or pushes were performed.

## Scope and method

The review read the current implementations and selected regression tests for these components:

- `internal/fswalk/walk.go`: positive-count enumeration, finite traversal limits, callback skip behavior and descriptor cleanup.
- `internal/project/ignore_lazy.go` and `ignore.go`: bounded Git policy queries, tracked descendants, ordinary-folder behavior, cancellation and failure handling.
- `internal/project/overview.go`: bounded initial tree output and examined-entry limits even when entries are excluded.
- `internal/project/rules.go` and `instructions.go`: bounded rule discovery, source input limits, precedence and full-document access.
- `internal/tool/root.go`: exact path bytes, deepest-existing-ancestor resolution, rooted operations, access checks and streaming walks.
- `cmd/hint/rpcinstructions.go`: target preparation, canonical and lexical ancestors, finite source/render budgets, delivered fingerprints and secondary mutation gating.
- `cmd/hint/rpcreporoute.go`: per-call root selection, entry handles, permission classes, previews and pure-Go routed traversal.
- `cmd/hint/rpcrepos_list.go` and the relevant administration branch in `rpcrepos.go`: bounded root pages, membership-bound continuation and process-wide mode changes.

Related call sites in `internal/project/project.go`, `internal/tool/builtin/instructions.go` and the manager's exclusion path were inspected to assess integration.
The review did not modify workspace source files; findings were sent to their owner for coordinated fixes.
The manager owner separately fixed the two manager-side findings below.

## Findings and dispositions

| ID | Finding | Disposition |
|---|---|---|
| W1 | Lazy file-tool ignores discover ancestor Git metadata, while the catalog initially treated every folder without its own `.git` marker independently; the same root could therefore show different exclusions in tools and the index. | Fixed on both sides with passing regressions: secondary ordinary folders use only their own Git metadata, while the primary catalog preserves established enclosing-Git startup policy. |
| W2 | The lazy tracked-descendant `git ls-files` probe initially inherited `core.fsmonitor`; a repository-configured hook could create a subprocess outside the direct Git timeout's lifetime. | Fixed by the workspace owner with `-c core.fsmonitor=false` for all lazy policy commands; a real hook sentinel regression passed. |
| W3 | `fswalk.WalkDir` advertised `fs.WalkDirFunc` skip semantics but initially returned `SkipDir`/`SkipAll` unchanged from error callbacks. | Fixed by the workspace owner; stat/open/read error callbacks now normalize both controls, with passing following-sibling regressions. |
| M1 | Catalog discovery pruned a Git-ignored directory even when it contained a force-added tracked source; direct tracked-file queries and lazy tools did not prune that source. | Fixed in manager: scoped 250 ms tracked-descendant probe stops at the first output byte, preserves the directory, and still excludes ignored untracked children. |
| M2 | Focused indexing shared the finite worker lease but could still start source allocation/extraction during a known memory or disk emergency. | Fixed in manager: guard before source open, cancellation of active source work, and emergency retention until a complete current healthy observation; existing catalog observations remain readable. |
| M3 | The parent's final inspection found that one huge root could occupy all eight retained directory streams and block a competing tiny root. | Fixed with one retained stream per root inside the global pool of eight, explicit capacity status, and current queue-item lookup when advancing an already open directory; fairness, file-priority and tail-completion regressions passed. |

## Mechanisms checked without a new finding

Streaming discovery uses `ReadDir(32)` and sorts only the current bounded batch.
Explicit traversal caps depth, examined entries and path bytes; excluded entries still consume the examined-entry budget.
The initial overview also caps displayed entries and does not enumerate a whole directory before producing a bounded view.
This is a finite interactive view, not a claim that one invocation traverses an arbitrarily large tree completely.

Secondary instruction preparation reads through the selected root, rejects nonregular sources, caps aggregate source bytes at 1 MiB, caps documents at 128 and ancestry at 64 directories, and caps entries inspected in one rules directory at 256.
The delivered fingerprint includes the root, target, membership revision and source paths/content.
A preparation in the current tool batch does not authorize a mutation before the next model request receives that fingerprint.
Canonical and lexical target ancestry are included; the rendered instruction envelope is separately bounded and full documents remain available through the instructions tool.
These checks are navigation and instruction-delivery guarantees, not a shell sandbox or proof that a model follows the instructions.

Routed file tools preserve action classes and permission handling, bind root selection to live session membership, and apply exact entry handles through a per-call path alias.
Routed traversal uses the bounded Go path; the existing standalone ripgrep path was outside this review's requested migration scope.
Root listings cap each page at 16 roots and 8 KiB serialized output, reserve continuation space before admission, and reject a continuation after membership revision changes.
Root-list cursors are navigation hints rather than authority tokens.

## Targeted verification

The manager reviewer directly ran the following commands with Go's cache in `/private/tmp/hint-repomap-gocache` and dependency downloads disabled:

```bash
GOCACHE=/private/tmp/hint-repomap-gocache GOPROXY=off go test ./internal/repomap \
  -run 'TestManagerGitIgnore|TestManagerFocusedEmergency|TestGovernor' -count=1
GOCACHE=/private/tmp/hint-repomap-gocache GOPROXY=off go test -race ./internal/repomap \
  -run 'TestManager|TestGovernor|TestResource|TestLinux' -count=1
GOCACHE=/private/tmp/hint-repomap-gocache GOPROXY=off go vet ./internal/repomap
GOCACHE=/private/tmp/hint-repomap-gocache \
  GOLANGCI_LINT_CACHE=/private/tmp/hint-repomap-lint-cache GOPROXY=off \
  /Users/mr_yush/go/bin/golangci-lint run ./internal/repomap/... --timeout=3m
```

The focused tests passed; the race command passed in 3.375 seconds; vet passed; pinned golangci-lint v2.13.2 reported zero issues.
`TestManagerGitIgnoreNULAndTrackedSemantics` now covers discovery of a force-added descendant and exclusion of its untracked sibling.
`TestManagerFocusedEmergencyPreservesCatalog` covers both memory and disk, catalog retention, unknown-sample retention and healthy recovery.
`TestManagerFocusedEmergencyCancelsActiveWorker` covers a new emergency during an active extraction.
These targeted commands do not substitute for the parent-owned final whole-repository gates.

The parent's subsequent inspection found that an old healthy sample could clear the first emergency-latch implementation.
Recovery now shares background admission's complete current-sample predicate, rejecting stale, future, NaN and out-of-range telemetry.
The following focused race command passed in 2.124 seconds after that correction:

```bash
GOCACHE=/private/tmp/hint-repomap-gocache GOPROXY=off go test -race ./internal/repomap \
  -run 'TestManagerFocusedEmergency|TestManagerGlobalAdmissionBeforeSourceOpen|TestGovernor' -count=1
```

The regression includes stale, future, NaN CPU, NaN I/O and unknown healthy-looking samples, followed by valid recovery.
Queued cancellation remains checked before source access; its existing admission regression was included in that run.

The workspace owner directly ran and reported this coordinated regression command:

```bash
GOCACHE=/private/tmp/hint-wp22-code-cache GOPROXY=off go test -race \
  ./internal/project ./internal/fswalk ./cmd/hint \
  -run 'TestRulesNeverRun|TestErrorCallbacks|TestAttachedOrdinary|TestRepo|TestSingleRoot' -count=1
```

It passed with package times of 2.168, 1.302 and 3.888 seconds respectively.
The manager reviewer inspected the resulting source changes; the command result above is attributed to the workspace owner rather than presented as a second independent execution.
`TestRulesNeverRunRepositoryFSMonitorHook` first proves its configured hook runs without an override, then verifies the bounded lazy probe does not run it.
`TestErrorCallbacksHonorSkipControls` covers stat/open/read errors with `SkipDir` and `SkipAll`, including following-sibling behavior.
`TestAttachedOrdinaryFolderDoesNotInheritAncestorGitIgnore` exercises the new secondary-root policy at the routed tool boundary.

The workspace owner's final targeted run added the exact entry-handle mutation and strict argument-shape regressions and passed with `cmd/hint` at 2.612 seconds, `internal/project` at 2.251 seconds and `internal/fswalk` at 2.058 seconds:

```bash
GOCACHE=/private/tmp/hint-wp22-code-cache GOPROXY=off go test -race \
  ./cmd/hint ./internal/project ./internal/fswalk \
  -run 'TestRepo|TestRulesNeverRun|TestErrorCallbacks|TestAttachedOrdinary|TestSingleRoot' -count=1 -v
```

The owner reported zero issues from pinned lint over those three packages and clean formatting/diff checks.
An entry-handle regression found and fixed an absolute-versus-relative instruction-target mismatch by routing the target's relative path into the mutation guard.
The leading-space filename case passed; the raw-invalid-byte filename subcase was skipped by the APFS fixture and is not reported as a platform pass.

The primary-only catalog adapter preserves established startup policy when Hint starts inside a larger Git working tree, while Git requests remain relative to the authorized primary root.
Its new regression checks an ancestor ignore rule, a force-added tracked file, a subsequent ignore edit, and rejection of a path outside the authorized root.
Those new tests were intentionally held while the separate million-entry metadata benchmark was timed, then executed after it finished.

The final stream policy retains at most one directory per root and eight directories in total, without discarding unfinished stream positions.
Already discovered files can still run while child directories wait durably in that root's frontier.
A ninth simultaneously streaming root receives `directory_stream_capacity` and waits for a slot; the change does not claim bounded fair latency for every configured root at once.
When a queued child cannot open because its root already owns a stream, that slice advances the retained directory instead and rotates only that blocked child alongside the active task.
This uses at most two bounded rotations per slice, preserves opportunities for files queued behind the child, and avoids rotating an entire growing child queue between directory pages.
The active task is looked up by its unique root/path key before use, so `Ack` receives the current sequence/revision after earlier rotations.

The final manager reviewer run of the targeted race command above passed in 6.018 seconds after the primary policy, growing-source and stream-scheduling changes.
Scoped repomap vet and pinned repomap lint also passed, with zero lint issues.
The final regressions include:

- `TestManagerPrimarySubdirectoryUsesEnclosingGitPolicy`: policy parity without expanding root authority.
- `TestManagerRejectsSourceGrowthDuringBoundedRead`: append after the first real-file read, rejection below and above the input limit, and no more than `MaxInputBytes+1` bytes read.
- `TestManagerTinyRootProgressesBesideWideRoot`: a tiny root indexes while a wide root retains its stream.
- `TestManagerWideDirectoriesAdvanceWithoutBlockedQueueChurn`: 100 child directories reach the parent's end in five directory slices, and the current parent task is acknowledged.
- `TestManagerBlockedDirectoryDoesNotDelayQueuedFileUntilParentCompletes`: a blocked child does not prevent a queued file from indexing before parent enumeration finishes.
- `TestManagerDirectoryStreamCapacityRetriesWithoutLosingTail`: a ninth streaming root reports capacity and resumes after a slot is released.

An intermediate race run caught new test fixtures calling the internal admitted-work method without its required shared slot; those fixtures now acquire the same slot as production.
The competing-roots fixture also now attaches both roots before retaining streams, since a membership change deliberately invalidates existing leases.
The passing final run includes those fixture corrections.

## Reviewed source snapshot

The source snapshot below was recorded after the final targeted checks, with source writers frozen and parent-owned whole-repository gates still pending.
Git HEAD was `752954bad3903c7b12ec8e48a05b342a054d1148`; these SHA-256 values identify the uncommitted source contents rather than implying the changes exist in that commit.

| Source | SHA-256 |
|---|---|
| `internal/fswalk/walk.go` | `5c4e8a90c817da7f9f1f4e1ef147852ea6433e26ba17d5e43d427130f2aa4b97` |
| `internal/project/ignore_lazy.go` | `d83e91c6d4b2ab5622e8b5195beeba61b826959287319538d76bdb8eef24abb8` |
| `internal/project/ignore.go` | `81286253f04cde8c49a01719929cc66bec444639913cf14d7004dffd4d6e0f8f` |
| `internal/project/overview.go` | `eabdb521a8a7eb18c267c03c43a5d20f2ca412d5a8c6ca35b63e5eda2df23282` |
| `internal/project/rules.go` | `354a2f3b2d7528a4a8628ed0b729ba01fca6727517cf018db0ae34a816edb3b5` |
| `internal/project/instructions.go` | `e5232b715779007b63de00f8e55922b9a739cdd0aa764f979ab36af6aa12a72b` |
| `internal/tool/root.go` | `83c39a83c27586cedbeb1c27f88ee49c49a330f7f21a76ffa4bed6fed751306c` |
| `cmd/hint/rpcinstructions.go` | `a29cff4a2067ce9df186acfe8cf929e24fa6e61dcd9ab01103bbfb42ec8f6235` |
| `cmd/hint/rpcreporoute.go` | `67c363384708e4884b9f171353cb6099b3f7efc376a074e88420017cfb79831b` |
| `cmd/hint/rpcrepos_list.go` | `deaba650a004681b8f390c6b79613e8f457a5188845e423e8e89d47afc718ffe` |
| `internal/repomap/manager.go` | `3b215400cf17f182572439b07f0938a18c8bec9e1ae775d8f9fa1d5baf440a51` |
| `internal/repomap/governor.go` | `2478743aa873ea72241a4d049c9030797dbf08be86f9fb88ad60f07941bf057e` |
| `internal/index/frontier.go` | `2d6302933efd8916648befb6040ad58ec29acbed259f557cf810ba30b81f3727` |

## Limits

The review examines one uncommitted working-tree state; later changes require the corresponding checks again.
Finite input and work limits are not universal hard wall-clock or RSS guarantees for ordinary filesystem syscalls, Git internals or the host scheduler.
Rooted filesystem operations protect the root boundary; this review is not a formal filesystem-race proof or a shell isolation audit.
Primary instruction loading retains its established fallback behavior; strict secondary preparation is a separate contract.
No Raspberry Pi, local-model 8k/32k, inference-overhead or Linux hardware measurement was performed as part of this review.
The separate corpus and metadata benchmarks measure different components and must not be described as workspace traversal acceptance.
Human owner review and the explicitly deferred measurements remain outstanding.
