# Hint v0.4.0 acceptance checklist

Issue [#95](https://github.com/mrYush/hint/issues/95), reconciled on 2026-11-08 (Europe/Moscow).
The owner confirmed `release/v0.4.0` as the acceptance branch.
Published release: [v0.4.0](https://github.com/mrYush/hint/releases/tag/v0.4.0), source SHA `7d1f34783c42278a893f862b2422f7e00901e827`.
[CI](https://github.com/mrYush/hint/actions/runs/42471947871) passed on this exact SHA; [Release attempt 2](https://github.com/mrYush/hint/actions/runs/42472203232) passed and published the assets and apt repository.

This is one checklist with explicit build provenance, not a claim that every manual scenario was rerun on the final tag.
The initial manual suite used `fa4d2986eed486af19e0309a6df9591cc46d6e7c` (candidate F).
The A3/A4 follow-up used clean source `73c11e6885f32bdfb655e181be24c7c3c4b5504f` (candidate R).
The final tag is referred to as T below.
Executable hashes, commands, tool responses and the failed intermediate checks are preserved in [release-verification.txt](release-verification.txt).
The final CI verifies T; it does not replace the manual evidence on F and R.
The Pi pilot used F and does not contribute a qualifying measurement pair.

| Criterion | Release status | Command or scenario | Expected | Actual and next action | Evidence | Build SHA key |
| --- | --- | --- | --- | --- | --- | --- |
| Release SHA and CI | pass | `git rev-parse v0.4.0^{commit}`; inspect CI run 42471947871 and Release run 42472203232 head_sha and jobs | Owner-confirmed SHA, required CI green | T: all eight CI jobs passed; release attempt 2 passed; published tag resolves to T | [Build and CI](release-verification.txt) | T |
| A1 | pass | [A1](smoke.md#a1) | Both roots, scoped locations and revision, membership invalidates old cursors | Both roots listed; beta.go mapped under second at revision 2; remove and add changed the revision to 4 and the old cursor returned stale_cursor | [Release smoke](release-verification.txt) | F |
| A2 | pass | [A2](smoke.md#a2) | Nested boundary bars primary access, explicit child and primary work | Parent scope rejected nested map, read and edit; explicit child mapped HiddenFromParent and primary mapped LookupBeacon; the same-SHA Pi tree showed the nested boundary | [Release smoke](release-verification.txt), [Pi pilot](pi-pilot.txt) | F |
| A3 | pass | [A3](smoke.md#a3) | Default paused, on exposes map, off hides map, restart resets, busy status readable and mutations refused | F: mode switching and reset passed, busy status failed. R: live TUI status became readable during a turn; mode changes remained refused | [Release smoke](release-verification.txt) | F, R |
| A4 | pass | [A4](smoke.md#a4) | Freshness and omissions correct, definitive invalidation stale, transient retry works, partial empty page inconclusive | F: same-size restored-mtime edit was reread, deleted source suppressed and authority change returned stale_cursor. R: an empty incomplete page continued to Good and Last; a transient symlink-loop error left the exact cursor retryable, then returned Last. Component tests separately cover injected metadata faults | [Release smoke](release-verification.txt) | F, R |
| A5 | pass | [A5](smoke.md#a5) | Root loss and recovery, old cursor invalidated | Root loss returned root_unavailable; restored root returned beta.go; the retained cursor returned stale_cursor | [Release smoke](release-verification.txt) | F |
| A6 | pass | `CGO_ENABLED=0 go test ./internal/repomap -run '^TestToolDisclosureUsesEffectiveOutputLimit$' -count=1 -v` | Effective limits below 1024 suppress disclosure and preserve diagnostics/file tools | Issue-authorized substitution; all seven cases passed on the release source | [Release A6](release-verification.txt) | F |
| A7 | pass | [A7](smoke.md#a7) | New primary-only, resume restores, fork preserves historical reservations, offline roots inactive | New session had primary only; resume restored second; missing second was inactive and warned; TUI fork from turn 1 restored primary and the historical detached reservation | [Release smoke](release-verification.txt) | F |
| A8 | pass | `sh a8.sh "$BIN" "$FIXTURE/primary"` on macOS with profile local and fresh fixture | Validation before git or destination creation as applicable, failed clone remains unattached and sanitized | Release CLI reproduced the expected errors and filesystem effects with no agent and a closed loopback port; clone component tests also passed | [Release A8](release-verification.txt) | F |
| #96 inclusion | pass | Compare identity implementation and `TestSystemPrompt` from F to T | Identity implementation included | Identity prompt and its tests are unchanged; release squash merging prevents using original feature-commit ancestry as the final-tag proof | [#96](https://github.com/mrYush/hint/issues/96), [Release record](release-verification.txt) | T |
| #96 fresh session | pass | Empty fixture, `hint --provider hub --no-session -p 'кто ты?'` | Hint, mrYush and source URL | Live response identified hint, mrYush and the source URL | [Release answer](release-verification.txt) | F |
| B1 8192 | waived | [Paired protocol](measurements.md#b1) | At least ten valid pairs after warm-up with task quality, map-call and performance evidence | No qualifying pairs or verified runtime capacity/accounting; freeze model and verify effective window | null | not measured / owner decision |
| B1 32768 | waived | [Paired protocol](measurements.md#b1) | Separate verified 32768 condition, same minimum | No qualifying 32768 pairs or verified runtime capacity/accounting | null | not measured / owner decision |
| B1 isolated schema and interference | not measured | See saved methodology, full-schema accounting | Frozen catalog/scheduler seam for controlled comparisons | Release CLI has no such seam; require a separately reviewed harness | null | not measured / owner decision |
| B2 | waived | [Pi protocol](measurements.md#b2) | Pi 4 4 GB samples for off, idle, initial build, backlog, query and pressure/recovery | Pi inventory, runtime version, model hash and a 4096-token active slot are recorded; the cross-build checksum and execution are verified on the Pi; off dry run returned the correct signature through read_file; on disclosed repo_map but background admission was blocked by telemetry_unavailable and the model supplied an unattached alias; host PSI and memory controller restored; Hint reports known telemetry, eligible background work and one tracked root with discovered entries; focused retry called repo_map with valid primary-scope arguments and located LookupBeacon; post-turn status returned to eligible; instrumented pilot captured server timings and sample counts; both task answers failed and active-on before the request was unverified; resource files validated; both sides reached 85.7 °C and throttled, with starts about 20 °C apart; establish a common thermal start band and verify active-on before measurement | [Pi pilot](pi-pilot.txt) | F |
| Proposed numerical thresholds | not measured | Paired median throughput loss and additional simultaneous family RSS | At most 5% and 128 MiB, subject to review | Zero qualifying pairs; both values null | null | not measured / owner decision |
| Auto remains paused | pass | Inspect default index mode and owner disposition | No automatic admission without a validated profile | F/R report paused auto; release retains the resource_profile_unvalidated gate; owner explicitly required the pause | [Release record](release-verification.txt), [Phase 2](../../phase-2-memory-mcp.md#separate-acceptance-task--pimodel-evaluation-and-auto-mode) | F, R; T source inspection |
| Automatic-profile approval | blocked | Review B1/B2 after collection | Completed measurements and explicit owner approval | Collect qualifying pairs under the saved protocol, review thresholds, then request approval; release timing waiver does not enable auto | [Methodology](../../repo-map-evaluation.md) | not measured / owner decision |
| B1/B2 release timing waiver | waived | Owner confirmation on 2026-11-08 | Defer both measurement suites until after v0.4.0 | No qualifying pairs are available and the Pi requires stable thermal conditions; measurements remain pending and auto stays paused | Owner decision recorded here and in Phase 2; see the release disposition in #95 | not measured / owner decision |

The owner confirmed that a Pi run with a local model is optional for release acceptance.
Completed Pi resource measurements and owner review remain prerequisites for enabling `auto`.

Preliminary source SHA is `4a8645df20a9e19006775d1589f064fcd6443ae1`.
Preliminary binary SHA-256 is `817f4ff18954e82971a5ad37f1f1a19c53870c8f726591b922dd2780b8cee381`.
Built from a Git archive with Go 1.26.10 darwin/arm64 and `CGO_ENABLED=0 go build -o hint ./cmd/hint`.
No preliminary outcome counts as release evidence.

## Release disposition and remaining work

A1–A8 and #96 have the candidate-build results recorded above.
The initial A3 failure and A4 gaps are superseded by the pinned R follow-up, not erased from the historical log.
A complete manual rerun on T was not performed; the final tag includes later turn-lifecycle and MCP closed-pipe fixes and test stabilization.
No new waiver of that provenance limitation is implied.

B1/B2 are waived for the v0.4.0 release deadline by the owner on 2026-11-08, not passed or canceled.
The reason is the absence of qualifying pairs and the need for comparable, stable Pi thermal conditions.
The Pi local-model test is optional for release acceptance; measured model/Pi resource evidence and owner approval remain required for `auto`.
Both proposed threshold results (128 MiB additional RSS and 5% median inference degradation) are null.
The isolated schema/interference result is also null because the reviewed experimental seam is absent.
Follow the outstanding measurement checkboxes in [Phase 2](../../phase-2-memory-mcp.md#separate-acceptance-task--pimodel-evaluation-and-auto-mode); do not mark the full phase complete when closing release administration.

PR [#104](https://github.com/mrYush/hint/pull/104) synchronized the released file tree into `develop` at `54bd7fd9b48992d74c872d9047093de5b97ac842`.
Its tree equals T, but the GitHub merge commit has only the previous `develop` tip as parent: the PR was squash-merged and did not restore `main` ancestry.
PR [#103](https://github.com/mrYush/hint/pull/103) was closed without merging on 2026-11-08 as superseded for content synchronization.
Future release synchronization should preserve ancestry with a merge commit.

The published archives, checksums and Debian packages were observed; the release workflow's apt publication step passed.
Installation and upgrade through apt, Homebrew and Scoop were not exercised in this closeout.
Release attempt 1 failed in the multi-process SQLite contention test; attempt 2 passed without changing T.
The separately prepared test fix is not part of v0.4.0, and the successful retry does not establish that the intermittent failure was fixed.

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
