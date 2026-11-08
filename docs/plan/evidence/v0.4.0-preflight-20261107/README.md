# Hint v0.4.0 acceptance checklist

Issue [#95](https://github.com/mrYush/hint/issues/95), checked 2026-11-07 (Europe/Moscow).
GitHub returned zero comments.
The owner confirmed `release/v0.4.0` as the acceptance branch on 2026-11-07.
The original candidate SHA was `4a8645df20a9e19006775d1589f064fcd6443ae1`.
The branch tip checked on 2026-11-08 is `fa4d2986eed486af19e0309a6df9591cc46d6e7c`, which adds acceptance evidence.
The Linux ARM64 B2 executable and its SHA-256 are recorded in the [Pi pilot](pi-pilot.txt); the macOS smoke executable and CI query are recorded in [release build and CI](release-verification.txt).
The local checkout was clean before evidence creation, on `develop`, at `4a8645df20a9e19006775d1589f064fcd6443ae1`.
Existing worktrees were the main checkout, detached `9c71a00` at `/private/tmp/hint-identity`, and `feature/tui-input-history` at `df361f6` in `/private/tmp/hint-tui-history`.
At the initial check no release ref existed; the local `release/v0.4.0` branch was subsequently created from `develop`.
The GitHub connector reported [CI run 114](https://github.com/mrYush/hint/actions/runs/42410041365) completed with failure for the original candidate SHA.
Run 114 is a PR-triggered workflow.
For the newer branch tip the Actions API returned no workflow runs across triggers and the commit API returned no check runs.
The workflow runs on PRs and pushes to main or develop.

| Criterion | Release status | Command or scenario | Expected | Actual and next action | Evidence |
| --- | --- | --- | --- | --- | --- |
| Release SHA and CI | blocked | `git rev-parse release/v0.4.0^{commit}`; `gh run list --commit "$SHA"`; inspect check runs for the same SHA | Owner-confirmed SHA, required CI green | No runs or checks for this SHA; run CI on a PR for the pinned branch tip or update the workflow trigger | [Build and CI](release-verification.txt) |
| A1 | pass | [A1](smoke.md#a1) | Both roots, scoped locations and revision, membership invalidates old cursors | Both roots listed; beta.go mapped under second at revision 2; remove and add changed the revision to 4 and the old cursor returned stale_cursor | [Release smoke](release-verification.txt) |
| A2 | pass | [A2](smoke.md#a2) | Nested boundary bars primary access, explicit child and primary work | Parent scope rejected nested map, read and edit; explicit child mapped HiddenFromParent and primary mapped LookupBeacon; the same-SHA Pi tree showed the nested boundary | [Release smoke](release-verification.txt), [Pi pilot](pi-pilot.txt) |
| A3 | fail | [A3](smoke.md#a3) | Default paused, on exposes map, off hides map, restart resets, busy status readable and mutations refused | Auto, on, off and restart behaved as expected; busy TUI refused read-only status. Scope of fix: allow status through the busy TUI gate while preserving the mutation guard | [Release smoke](release-verification.txt) |
| A4 | blocked | [A4](smoke.md#a4) | Freshness and omissions correct, definitive invalidation stale, transient retry works, partial empty page inconclusive | Same-size restored-mtime edit was reread with a changed hash; deleted file returned scope_not_found and authority change invalidated the cursor. Need a fault-injecting filesystem for live transient I/O and a retained generation with more than 512 filtered or invalidated records for an empty partial page | [Release smoke](release-verification.txt) |
| A5 | pass | [A5](smoke.md#a5) | Root loss and recovery, old cursor invalidated | Root loss returned root_unavailable; restored root returned beta.go; the retained cursor returned stale_cursor | [Release smoke](release-verification.txt) |
| A6 | pass | `CGO_ENABLED=0 go test ./internal/repomap -run '^TestToolDisclosureUsesEffectiveOutputLimit$' -count=1 -v` | Effective limits below 1024 suppress disclosure and preserve diagnostics/file tools | Issue-authorized substitution; all seven cases passed on the release source | [Release A6](release-verification.txt) |
| A7 | pass | [A7](smoke.md#a7) | New primary-only, resume restores, fork preserves historical reservations, offline roots inactive | New session had primary only; resume restored second; missing second was inactive and warned; TUI fork from turn 1 restored primary and the historical detached reservation | [Release smoke](release-verification.txt) |
| A8 | pass | `sh a8.sh "$BIN" "$FIXTURE/primary"` on macOS with profile local and fresh fixture | Validation before git or destination creation as applicable, failed clone remains unattached and sanitized | Release CLI reproduced the expected errors and filesystem effects with no agent and a closed loopback port; clone component tests also passed | [Release A8](release-verification.txt) |
| #96 ancestry | pass | `git merge-base --is-ancestor 4a8645df20a9e19006775d1589f064fcd6443ae1 "$SHA"` | Identity implementation included | The #98 fix is in the branch tip ancestry; recheck if the candidate changes | [#96](https://github.com/mrYush/hint/issues/96) |
| #96 fresh session | pass | Empty fixture, `hint --provider hub --no-session -p 'кто ты?'` | Hint, mrYush and source URL | Live response identified hint, mrYush and the source URL | [Release answer](release-verification.txt) |
| B1 8192 | waived | [Paired protocol](measurements.md#b1) | At least ten valid pairs after warm-up with task quality, map-call and performance evidence | No qualifying pairs or verified runtime capacity/accounting; freeze model and verify effective window | null |
| B1 32768 | waived | [Paired protocol](measurements.md#b1) | Separate verified 32768 condition, same minimum | No release build or verified runtime capacity/accounting | null |
| B1 isolated schema and interference | not measured | See saved methodology, full-schema accounting | Frozen catalog/scheduler seam for controlled comparisons | Release CLI has no such seam; require a separately reviewed harness | null |
| B2 | waived | [Pi protocol](measurements.md#b2) | Pi 4 4 GB samples for off, idle, initial build, backlog, query and pressure/recovery | Pi inventory, runtime version, model hash and a 4096-token active slot are recorded; the cross-build checksum and execution are verified on the Pi; off dry run returned the correct signature through read_file; on disclosed repo_map but background admission was blocked by telemetry_unavailable and the model supplied an unattached alias; host PSI and memory controller restored; Hint reports known telemetry, eligible background work and one tracked root with discovered entries; focused retry called repo_map with valid primary-scope arguments and located LookupBeacon; post-turn status returned to eligible; instrumented pilot captured server timings and sample counts; both task answers failed and active-on before the request was unverified; resource files validated; both sides reached 85.7 °C and throttled, with starts about 20 °C apart; establish a common thermal start band and verify active-on before measurement | [Pi pilot](pi-pilot.txt) |
| Proposed numerical thresholds | not measured | Paired median throughput loss and additional simultaneous family RSS | At most 5% and 128 MiB, subject to review | Zero qualifying pairs; both values null | null |
| Review and auto | blocked | Owner review after B1/B2 | Keep auto paused until completed measurements and owner approval | Decision pending | null |
| B1/B2 release timing waiver | waived | Owner confirmation on 2026-11-08 | Defer both measurement suites until after v0.4.0 | No qualifying pairs are available and the Pi requires stable thermal conditions; measurements remain pending and auto stays paused | Owner decision recorded here; #95 comment pending |

The owner confirmed that a Pi run with a local model is optional for release acceptance.
Completed Pi resource measurements and owner review remain prerequisites for enabling `auto`.

Preliminary source SHA is `4a8645df20a9e19006775d1589f064fcd6443ae1`.
Preliminary binary SHA-256 is `817f4ff18954e82971a5ad37f1f1a19c53870c8f726591b922dd2780b8cee381`.
Built from a Git archive with Go 1.26.10 darwin/arm64 and `CGO_ENABLED=0 go build -o hint ./cmd/hint`.
No preliminary outcome counts as release evidence.

## A3 and A4 follow-up

The existing release-SHA results above apply to `fa4d2986eed486af19e0309a6df9591cc46d6e7c`.
On 2026-11-08, the working-tree build passed the A3 busy-status and mutation checks in a live TUI.
The same build produced the missing live A4 empty partial page and successfully retried an unchanged cursor after a temporary path-resolution fault.
The [follow-up record](release-verification.txt) gives the executable hash, reproductions and actual tool responses.
Pin the fix to a commit and confirm these checks and CI for the resulting release SHA.
The current CI workflow has no release-branch push or manual trigger, and its PR checkout uses the synthetic merge commit.

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
