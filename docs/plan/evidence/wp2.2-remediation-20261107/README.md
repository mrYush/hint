# WP2.2 remediation review handoff

Date: 2026-11-07.
Branch: `dev/wp2.2-repo-map`; issue: [#93](https://github.com/mrYush/hint/issues/93).
Scope: the supplied WP2.2 correctness/recovery findings, not other Phase 2 work packages.
The owner authorized implementation → self-review → local commit; no push, PR or issue closure was performed.
This is the implementing agent's self-review and regression evidence, not independent human approval.
The [phase checklist](../../phase-2-memory-mcp.md#review-consolidation) remains the source of truth.

## Review boundary

- ✅ Implement and locally verify the supplied, concrete findings listed below.
- ✅ Preserve the owner's FIFO policy-read fix `5f60d93` and add subsequent fixes as separate commits.
- [ ] Obtain the full concluding `repomap` review and the concrete scenario behind the summary's unspecified P0; requested in this conversation but not supplied.
- [ ] Final owner review of the combined branch.
- [ ] Owner's Pi 4/local-model 8k/32k evaluation under the [saved methodology](../../repo-map-evaluation.md).

The missing review input prevents claiming that every blocker is resolved or that #93 is ready to close.
Unvalidated `auto` remains paused; these fixes do not expand its acceptance boundary.

## Findings and dispositions

| Mechanism from the supplied review | Disposition and evidence | Local commits |
|---|---|---|
| FIFO `.gitignore`, metadata, source extraction, freshness and directory replacement can block opens | Fixed: metadata-only `Lease.Stat`, typed pre/post-open admission and Unix nonblocking opens; subprocess timeout regressions cover policy, source and directory replacement, cleanup and recovery. | `5f60d93`, `625eb37`, `bd0469c`, `2b63071` |
| Exported ignore API accepts `..` | Fixed before policy/Git access; `TestIgnoreRejectsEscapingPathsBeforeAccess` and `TestPrepareIgnoreRejectsEscapingParentBeforeGit`. | `41cb70f` |
| Windows workspace/index identity mismatch and noncanonical stored paths | Shared host-native normalization and exact opaque identity; platform-specific path fixtures and Windows test cross-compilation. Windows runtime remains untested. | `07f0c0e` |
| Denied/unreadable/nested tasks never drain or retry truthfully | Terminal acknowledgement or durable delayed retry; failed/canceled scans do not prove deletion; queue/reconciliation/ready-state regressions. | `4eca3e0` |
| Unchanged policy epoch advances after 128 observations | Durable fingerprints and root revision replace the evicting RAM map; saturation, edit, restart and reattachment tests. | `ec700c4` |
| Reservations leak after `openQuery`, continuation errors and shutdown | New failed queries and definitive invalidations release owned pins; transient continuations remain retryable; bounded canceled-context cleanup and shutdown ownership tests. | `fab8685` |
| Oversized declaration passes extraction but permanently fails storage | Extraction limits follow admission limits; encoding overflow becomes a durable whole-source omission, with budget identity forcing retry after allowance changes. | `72572d7` |
| Missing/inconsistent `coverage.omissions` | Atomic finite reason buckets, pinned-generation coverage, unknown subtree counts omitted, legacy contributions explicitly `unclassified`; small-budget tests. | `344bb67` |
| `root_unavailable` absent; map disclosed below effective minimum output cap | Typed root availability and catalog construction using effective limits; missing/replaced root, missing scope, wrapper and disclosure boundary tests. | `2627aef` |
| No final `listRoots` budget assertion | Defensive hardening: no old overflow demonstrated; final check plus escaped-path pagination at 1023/1024/1025/2048 bytes. | `447d792` |
| `BeforeBatch` called on empty EOF; cleanup error after `SkipAll` | Empty preparation removed. Cleanup error deliberately retained and documented, including joined callback failure tests; this behavior is not silently discarded. | `e0ad808` |
| SQLite Open/PRAGMA waiting, BUSY classification, concurrent writers and conflicting limits | One initialization deadline, zero internal SQLite lock wait, bounded retries, write-serialized migration and persisted shared allowances; independent subprocess initialization/migration/write tests. | `277cbd5` |
| Discovery exhausts the shared quota; repeated failure/recovery | Queued work and admitted cursor survive pressure; 1 s contention/5 s capacity cooldowns; actual separate-connection locking, frontier exhaustion and recovered file processing. | `94b96c5` |
| Additional self-review: root replaced after successful cursor resume | Reproduced retryable `root_unavailable` despite released reservation; now terminal `stale_cursor` takes precedence and pin release is verified. | `748459b` |

Self-review checked cancellation/ownership, transaction rollback, stable pagination, omitted coverage, policy-cache eviction, native path semantics and propagation of errors from deferred frontier rotation.
The full suites exercise the workspace/map integration, permission and output wrappers, engine/RPC routing, provider catalog changes and the production extraction helper.
No live external model was used.

## Deliberate limits and follow-up

The [catalog cleanup follow-up](../../phase-2-memory-mcp.md#proposed-follow-up--catalog-quotas-and-cleanup) retains separate frontier/data quotas, reserved processing headroom, precise per-pin version GC, crashed-owner cleanup, detached-root cleanup and obsolete-schema cleanup.
The current frontier is disk-bounded by the shared quota, not by a separate row limit; it can legitimately exhaust the allowance and leave the catalog partial.
Pins conservatively retain obsolete versions for their root, and crashed owners can consume pin capacity until expiry; normal shutdown now releases only its own reservations.
An incompatible v1 derived cache remains on disk for explicit user handling.
These are efficiency/maintenance limitations, not permission to defer hangs, data loss or recovery when capacity becomes available.

All processes opening the same catalog must use its persisted disk/pin allowance; conflicting limits are rejected rather than silently widening another owner's budget.
Changing shared limits requires rebuilding the derived cache after all users close it.
Schema upgrades require stopping older binaries first; mixed-version live writers are unsupported.
Filesystem/device syscalls themselves are not preemptible by a Go context; the bounded-open evidence concerns FIFO admission and SQLite lock waiting.
The [user guide](../../../workspaces.md) documents cache handling, status, retry and coverage behavior.

## Final verification and measurement evidence

The [verification artifact](verification.json) records the exact source SHA-256 inventory, commands, results, platform and built helper smoke cases.
Its final success requires a stable source inventory through testing.
Tests needing local network listeners ran with sandbox escalation; Windows/Linux results are cross-build evidence, not runtime acceptance.
The [measurement report](measurements.md) records affected local corpus/catalog reruns and preserves a failed fixture-location attempt explicitly.
Historical million-record results remain [separate evidence](../wp2.2-mac-20261106/metadata.md), not a new million-file traversal or a new result for this source revision.
