# Hint v0.4.0 acceptance checklist

Issue [#95](https://github.com/mrYush/hint/issues/95), checked 2026-11-07 (Europe/Moscow).
GitHub returned zero comments.
The owner confirmed `release/v0.4.0` as the acceptance branch on 2026-11-07.
The current local candidate SHA is `4a8645df20a9e19006775d1589f064fcd6443ae1`.
The release executable SHA-256 is pending.
The local checkout was clean before evidence creation, on `develop`, at `4a8645df20a9e19006775d1589f064fcd6443ae1`.
Existing worktrees were the main checkout, detached `9c71a00` at `/private/tmp/hint-identity`, and `feature/tui-input-history` at `df361f6` in `/private/tmp/hint-tui-history`.
At the initial check no release ref existed; the local `release/v0.4.0` branch was subsequently created from `develop`.
The GitHub connector reported [CI run 114](https://github.com/mrYush/hint/actions/runs/42410041365) completed with failure for the current candidate SHA.
Run 114 is a PR-triggered workflow; release-branch CI remains unverified.

| Criterion | Release status | Command or scenario | Expected | Actual and next action | Evidence |
| --- | --- | --- | --- | --- | --- |
| Release SHA and CI | blocked | `git rev-parse release/v0.4.0^{commit}`; `gh run list --commit "$SHA"`; inspect check runs for the same SHA | Owner-confirmed SHA, required CI green | Local candidate SHA recorded; run 114 failed; check the updated candidate and required release CI after pipeline work | This record |
| A1 | blocked | [A1](smoke.md#a1) | Both roots, scoped locations and revision, membership invalidates old cursors | Await release build, then execute the saved scenario | null |
| A2 | blocked | [A2](smoke.md#a2) | Nested boundary bars primary access, explicit child and primary work | Await release build | null |
| A3 | blocked | [A3](smoke.md#a3) | Default paused, on exposes map, off hides map, restart resets, busy status readable and mutations refused | Await release build | null |
| A4 | blocked | [A4](smoke.md#a4) | Freshness and omissions correct, definitive invalidation stale, transient retry works, partial empty page inconclusive | Await release build; target transient I/O and verify with trace | null |
| A5 | blocked | [A5](smoke.md#a5) | Root loss and recovery, old cursor invalidated | Await release build | null |
| A6 | blocked | `CGO_ENABLED=0 go test ./internal/repomap -run '^TestToolDisclosureUsesEffectiveOutputLimit$' -count=1 -v` | Effective limits below 1024 suppress disclosure and preserve diagnostics/file tools | Named test selected as issue-authorized substitution; all seven cases passed on preliminary source; rerun on release SHA | [Preliminary test log](preliminary-tests.txt) |
| A7 | blocked | [A7](smoke.md#a7) | New primary-only, resume restores, fork preserves historical reservations, offline roots inactive | Await release build | null |
| A8 | blocked | `sh a8.sh "$BIN" "$FIXTURE/primary"` on macOS with profile local and fresh fixture | Validation before git or destination creation as applicable, failed clone remains unattached and sanitized | Preliminary CLI observed expected errors and filesystem effects using a closed loopback port and no agent; release rerun pending | [Preliminary transcript](preliminary-a8.txt) |
| #96 ancestry | pass | `git merge-base --is-ancestor 4a8645df20a9e19006775d1589f064fcd6443ae1 "$SHA"` | Identity implementation included | The local candidate tip is the #98 fix itself; recheck if the candidate changes | [#96](https://github.com/mrYush/hint/issues/96) |
| #96 fresh session | blocked | [Identity](smoke.md#identity) | Hint, mrYush and source URL | Preliminary unit test passed; live Ollama returned unexpected EOF; verify runtime health and retry with the release binary | [Preliminary test log](preliminary-tests.txt) |
| B1 8192 | blocked | [Paired protocol](measurements.md#b1) | At least ten valid pairs after warm-up with task quality, map-call and performance evidence | No release build or verified runtime capacity/accounting; freeze model and verify effective window | null |
| B1 32768 | blocked | [Paired protocol](measurements.md#b1) | Separate verified 32768 condition, same minimum | No release build or verified runtime capacity/accounting | null |
| B1 isolated schema and interference | not measured | See saved methodology, full-schema accounting | Frozen catalog/scheduler seam for controlled comparisons | Release CLI has no such seam; require a separately reviewed harness | null |
| B2 | blocked | [Pi protocol](measurements.md#b2) | Pi 4 4 GB samples for off, idle, initial build, backlog, query and pressure/recovery | Pi endpoint and release build unavailable; collect inventory and establish device access | null |
| Proposed numerical thresholds | not measured | Paired median throughput loss and additional simultaneous family RSS | At most 5% and 128 MiB, subject to review | Zero qualifying pairs; both values null | null |
| Review and auto | blocked | Owner review after B1/B2 | Keep auto paused until completed measurements and owner approval | Decision pending | null |
| Waivers | not measured | Explicit owner decision with date and reason | Record any decision in #95 | None received | null |

Preliminary source SHA is `4a8645df20a9e19006775d1589f064fcd6443ae1`.
Preliminary binary SHA-256 is `817f4ff18954e82971a5ad37f1f1a19c53870c8f726591b922dd2780b8cee381`.
Built from a Git archive with Go 1.26.10 darwin/arm64 and `CGO_ENABLED=0 go build -o hint ./cmd/hint`.
No preliminary outcome counts as release evidence.

## Preliminary runtime reproduction

From an empty fixture directory with an isolated local OpenAI-compatible profile, run `"$BIN" --provider local --api-url http://127.0.0.1:11434/v1 --model gemma4:26b --no-session -p 'кто ты?'`.
Ollama listed model `gemma4:26b` with ID `08ae7ec1744b` and reported effective slot context 4096 during the attempt.
The attempt ended with `stream broken: unexpected EOF` before any visible identity answer.
See the [raw response](preliminary-identity.txt).
This affects live verification of #96 and leaves effective 8192/32768 support unverified.
Verify local server stability and capacity before repeating; product changes require reproduction on the release build.

A8's supporting `TestCloneValidationNeverTouchesExistingDestination` and `TestCloneArgvEnvironmentAndCancellation` passed separately on the same preliminary source.
The SSH smoke used an unreachable loopback port, so it establishes failure handling, while the component test checks the explicit non-interactive git/SSH environment.
The full preliminary Go test invocation encountered sandbox-denied loopback listeners in the RPC, MCP and provider suites.
