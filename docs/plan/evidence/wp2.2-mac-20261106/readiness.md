# WP2.2 implementation review record

Date: 2026-11-06.
Issue: [#93](https://github.com/mrYush/hint/issues/93).
Branch: `dev/wp2.2-repo-map`; baseline HEAD: `752954bad3903c7b12ec8e48a05b342a054d1148`.
The implementation is ready for the owner's code review as an uncommitted working-tree change.
No assistant commit, push, pull request or issue closure is part of this delivery.

## Final local verification

- ✅ `go test ./...` and `go test -race ./...` passed after all source fixes.
- ✅ `go vet ./...`, pinned golangci-lint v2.13.2, source formatting and `git diff --check` passed.
- ✅ All six `CGO_ENABLED=0 go build ./...` targets passed: Linux, macOS and Windows on amd64 and arm64.
- ✅ The built production helper extracted declarations from Go, Python, JavaScript, TypeScript, TSX and Rust fixtures.
- ✅ The pinned real corpus contains 163 files and 248,811 physical lines; cold/warm results and explicit omissions are recorded in the [measurement report](README.md).
- ✅ Complete 10k/100k/1m metadata measurements; the [catalog report](metadata.md) records construction time, database size, process RSS, query costs and the limits of the synthetic workload.
- ✅ Preserve cross-component [integration](integration-review.md) and [workspace](workspace-review.md) reviews with concrete findings and attributed checks.
- [ ] Owner review and Pi 4/local-model 8k/32k evaluation under the [manual methodology](../../repo-map-evaluation.md).

The [verification artifact](verification.json) records exact commands, exit statuses, output, durations, the source-file SHA-256 manifest and the built executable's SHA-256 and smoke cases.
The runner checked that source hashes and inventory did not change during verification.
Local HTTP/RPC listener tests required execution outside the filesystem/network sandbox; this was a test-environment constraint, not a product test failure.
Formatting was checked on repository source files; a literal recursive `gofmt -l .` also reaches ignored experiment caches and old recovery copies, which were intentionally not rewritten.
The checks exercise the final source state; subsequent changes to this report are documentation only.

The local resume checklist is `.tmp/wp22-resume.md` and preserves work in progress across interruptions.
This tracked report holds the final local review evidence; neither checklist overrides the [phase plan](../../phase-2-memory-mcp.md#wp22--repo-map).

## Delivered behavior

Users attach explicit local Git repositories, worktrees or ordinary folders, or clone a validated remote into a new destination.
The primary root remains immutable and default; session history retains membership changes and nested ownership reservations.
`/repos` is available through RPC in both shells, with bounded root/tree pages and status.
Built-in tools resolve the selected root and opaque entry IDs under current authority.
Secondary edits and commands require a bounded instruction snapshot delivered to a preceding model request.

The local SQLite catalog persists source observations, declaration generations, a discovery frontier and pagination reservations outside source roots.
Discovery uses bounded directory batches, keyset reads and resumable work instead of a resident inventory of every file.
Queries return complete JSON envelopes within the configured output allowance, recheck current authority/source evidence and preserve unconsumed records for continuation.
Go uses its standard AST parser; other supported languages use pinned embedded Tree-sitter WASM modules in a supervised helper.
One admission slot covers extraction and catalog maintenance; cancellation retains the slot until the worker stops.
Retained directory streams use the current durable task identity after queue rotation.
When a child waits behind its root's active stream, the same slice advances that stream and rotates only the blocked child, avoiding a growing scan of the whole waiting frontier while retaining discovered-file priority.

Automatic indexing stays paused while its resource profile is unvalidated.
Explicit `/repos index on` enables experimental operation under finite limits, current telemetry and memory/disk emergency protection.
Provider inference pauses background work, including completion, compaction and fallback requests.
The [user guide](../../../workspaces.md) documents commands, limits and the agent workflow.

## Evidence boundaries and remaining acceptance

- Pi 4 with 4 GB, real local-model 8k/32k task usefulness and inference degradation remain unmeasured and assigned to the owner.
- Linux/macOS resource adapters have unit/fixture coverage; only the Mac adapter and Mac executables were exercised on real hardware in this session.
- Windows cross-compilation does not establish runtime validation; Windows background telemetry remains unavailable and optional background work stays paused.
- APFS rejected creation of a non-UTF-8 filename in the physical-file preparation fixture, so that subcase was skipped explicitly.
  Opaque/raw-byte serialization tests and the leading-space entry-ID preparation/write regression passed; Linux raw-filename runtime coverage remains separate.
- The real corpus has three explicit TypeScript omissions: one oversized source and two parse failures with the pinned grammar.
  It does not establish full TypeScript 5.9 grammar coverage.
- The metadata scale experiment isolates catalog insertion/query behavior; it does not measure million-file crawling, parsing or model interference.
- Bounded memory does not imply infinite storage or constant completion time.
  Quotas, active pagination reservations, continuous pressure and changing files can prevent full completion; status remains partial.
  Retained discovery streams are limited to one per root and eight globally; additional roots wait with explicit capacity status rather than receiving a per-root latency guarantee.
- Shell routing selects a working directory under the existing execute permission policy; it is not an operating-system filesystem sandbox.
- Full-text documents, PDF/DOCX/OCR modules, durable agent notes, embeddings and reference ranking remain later v0.4 work.
- Issue #93's copied contract can be replaced with the canonical repository link after the owner publishes these files.
  That publication follow-up remains open in `PLAN.md`.
