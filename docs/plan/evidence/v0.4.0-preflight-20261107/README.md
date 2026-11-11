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
| A1 | pass | [A1](#a1) | Both roots, scoped locations and revision, membership invalidates old cursors | Both roots listed; beta.go mapped under second at revision 2; remove and add changed the revision to 4 and the old cursor returned stale_cursor | [Release smoke](release-verification.txt) | F |
| A2 | pass | [A2](#a2) | Nested boundary bars primary access, explicit child and primary work | Parent scope rejected nested map, read and edit; explicit child mapped HiddenFromParent and primary mapped LookupBeacon; the same-SHA Pi tree showed the nested boundary | [Release smoke](release-verification.txt), [Pi pilot](pi-pilot.txt) | F |
| A3 | pass | [A3](#a3) | Default paused, on exposes map, off hides map, restart resets, busy status readable and mutations refused | F: mode switching and reset passed, busy status failed. R: live TUI status became readable during a turn; mode changes remained refused | [Release smoke](release-verification.txt) | F, R |
| A4 | pass | [A4](#a4) | Freshness and omissions correct, definitive invalidation stale, transient retry works, partial empty page inconclusive | F: same-size restored-mtime edit was reread, deleted source suppressed and authority change returned stale_cursor. R: an empty incomplete page continued to Good and Last; a transient symlink-loop error left the exact cursor retryable, then returned Last. Component tests separately cover injected metadata faults | [Release smoke](release-verification.txt) | F, R |
| A5 | pass | [A5](#a5) | Root loss and recovery, old cursor invalidated | Root loss returned root_unavailable; restored root returned beta.go; the retained cursor returned stale_cursor | [Release smoke](release-verification.txt) | F |
| A6 | pass | `CGO_ENABLED=0 go test ./internal/repomap -run '^TestToolDisclosureUsesEffectiveOutputLimit$' -count=1 -v` | Effective limits below 1024 suppress disclosure and preserve diagnostics/file tools | Issue-authorized substitution; all seven cases passed on the release source | [Release A6](release-verification.txt) | F |
| A7 | pass | [A7](#a7) | New primary-only, resume restores, fork preserves historical reservations, offline roots inactive | New session had primary only; resume restored second; missing second was inactive and warned; TUI fork from turn 1 restored primary and the historical detached reservation | [Release smoke](release-verification.txt) | F |
| A8 | pass | `sh a8.sh "$BIN" "$FIXTURE/primary"` on macOS with profile local and fresh fixture | Validation before git or destination creation as applicable, failed clone remains unattached and sanitized | Release CLI reproduced the expected errors and filesystem effects with no agent and a closed loopback port; clone component tests also passed | [Release A8](release-verification.txt) | F |
| #96 inclusion | pass | Compare identity implementation and `TestSystemPrompt` from F to T | Identity implementation included | Identity prompt and its tests are unchanged; release squash merging prevents using original feature-commit ancestry as the final-tag proof | [#96](https://github.com/mrYush/hint/issues/96), [Release record](release-verification.txt) | T |
| #96 fresh session | pass | Empty fixture, `hint --provider hub --no-session -p 'кто ты?'` | Hint, mrYush and source URL | Live response identified hint, mrYush and the source URL | [Release answer](release-verification.txt) | F |
| B1 8192 | waived | [Paired protocol](#b1) | At least ten valid pairs after warm-up with task quality, map-call and performance evidence | No qualifying pairs or verified runtime capacity/accounting; freeze model and verify effective window | null | not measured / owner decision |
| B1 32768 | waived | [Paired protocol](#b1) | Separate verified 32768 condition, same minimum | No qualifying 32768 pairs or verified runtime capacity/accounting | null | not measured / owner decision |
| B1 isolated schema and interference | not measured | See saved methodology, full-schema accounting | Frozen catalog/scheduler seam for controlled comparisons | Release CLI has no such seam; require a separately reviewed harness | null | not measured / owner decision |
| B2 | waived | [Pi protocol](#b2) | Pi 4 4 GB samples for off, idle, initial build, backlog, query and pressure/recovery | Pi inventory, runtime version, model hash and a 4096-token active slot are recorded; the cross-build checksum and execution are verified on the Pi; off dry run returned the correct signature through read_file; on disclosed repo_map but background admission was blocked by telemetry_unavailable and the model supplied an unattached alias; host PSI and memory controller restored; Hint reports known telemetry, eligible background work and one tracked root with discovered entries; focused retry called repo_map with valid primary-scope arguments and located LookupBeacon; post-turn status returned to eligible; instrumented pilot captured server timings and sample counts; both task answers failed and active-on before the request was unverified; resource files validated; both sides reached 85.7 °C and throttled, with starts about 20 °C apart; establish a common thermal start band and verify active-on before measurement | [Pi pilot](pi-pilot.txt) | F |
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

## Smoke procedures

Set `BIN` to the absolute path of the recorded executable and `FIXTURE=$(sh fixtures.sh)` to a fresh scratch tree for each scenario.
Run from `"$FIXTURE/primary"` with `"$BIN" --plain --provider local --debug`, using a real terminal and a tool-capable local profile.
Use the TUI for A7 and where concurrent input is needed in A3.
Capture terminal and tool traces privately first, then retain reviewed evidence with tool arguments/results, relevant catalog, fixture operations and observed status.
Use the exact cursor returned by the trace in place of each placeholder.
Capture actual tool errors rather than inferring them from the model's answer.

### A1

Enter `/repos index on` and `/repos add ../second-fixture second`.
Prompt: “Call repo_map with repo second and limit 1. Then call repo_list. Report the map scope and source locations, both root IDs and the membership revision.”
Remove second with `/repos remove second`, reattach it with the same add command, then prompt: “Call repo_map with only cursor <OLD_CURSOR>, once. Report the exact tool error.”
Compare root IDs and revision before and after.

### A2

Enter `/repos index on`.
Prompt: “Call repo_list and inspect the primary tree. Call repo_map on primary, then on subtree nested in primary. Try read of nested/hidden.go and try editing it through primary. Report each actual result.”
Enter `/repos add ./nested nested`.
Prompt: “Call repo_map on nested, then call repo_map on primary for LookupBeacon. Report the source locations.”
Approve only the disposable fixture edit attempt, if asked.

### A3

Enter `/repos index`, then `/repos index on`.
Prompt: “Call repo_map for LookupBeacon and report the result.”
Enter `/repos index off`.
Prompt: “Use the available tools to locate LookupBeacon and read alpha.go. Report whether repo_map was available.”
Restart and enter `/repos index`.
Start a real streaming turn and submit `/repos index` and `/repos index off` before it ends.
Capture turn timing, both status results and actual provider catalogs.

### A4

Enter `/repos index on`.
Prompt: “Call repo_map for Mutable and report the freshness and signature. Call repo_map for the whole primary root with limit 1 and save the cursor.”
In a second terminal in the fixture primary, run `cp -p methods.go methods.stamp; sed 's/old:/new:/' methods.stamp > methods.go; touch -r methods.stamp methods.go; rm methods.stamp`.
Prompt: “Call repo_map for Mutable again and show the signature and freshness; then read methods.go and show its current return expression.”
Repeat with a signature change and deletion, and capture raw versions and membership/policy/generation evidence.
Use membership change to establish a definitive invalidation; generation advancement alone can preserve a retained cursor under the contract.
For transient I/O, make a relevant continuation source temporarily unreadable as the non-root fixture user, capture the actual transient error, restore permissions and retry the same cursor.
If that platform/path cannot induce the required transient error, record that subcase as blocked with the attempted reproduction.
For partial empty coverage, retain a returned empty page with `query_complete: false` from a bounded search with filtered/invalidated candidates and verify continuation; increase the fixture size if necessary and record its hash.
Do not substitute a complete empty page.

### A5

Enter `/repos index on` and `/repos add ../second-fixture second`.
Prompt: “Call repo_map with repo second and limit 1 and return the cursor.”
Rename from the fixture parent with `mv second-fixture second-hidden`.
Prompt: “Call repo_map with repo second and report the exact outcome.”
Restore with `mv second-hidden second-fixture`.
Prompt: “Call repo_map with repo second. Then call repo_map with only cursor <OLD_CURSOR>. Report both exact outcomes.”

### A7

Use the TUI and preserve session IDs with `"$BIN" sessions` from the fixture primary.
Attach `/repos add ../detached detached`, detach with `/repos remove detached`, and send “Reply exactly FIRST.”
Attach `/repos add ../second-fixture second`, then send “Reply exactly SECOND.”
Run `/new` then `/repos`.
Restart with `"$BIN" --session <FIRST_SESSION_ID>` and enter `/repos`.
In the TUI message browser press Esc from empty input, select FIRST, and press `f`.
Inspect `/repos` and the historical detached-root reservation.
Rename second away, resume the original again, and capture inactive-root diagnostics.

### Identity

In the empty `"$FIXTURE/identity"` directory run `"$BIN" --provider local --no-session -p 'кто ты?'`.
Use the selected live profile and retain the answer with its model/runtime and executable hashes.

## Measurement runbook

Follow the [saved methodology](../../repo-map-evaluation.md).
Freeze immutable metadata before collection, including release/build hashes, corpus manifest, tool and index configuration, runtime/model artifact and template hashes, model seed support, cache policy and thermal band.
Store append-only run, request, sample, index and score JSONL with joined run IDs.
Use UTC and monotonic timestamps.
Keep raw traces private pending a credential and source-content review.

### B1

Create two experiments at effective capacities 8192 and 32768.
Verify actual runtime slots and effective context with runtime startup/slot telemetry and a truncation probe before pairing.
Hint's `--context-window` is only its client budget.
Choose and verify a 1024-token output reserve/cap for both sides through effective client and runtime settings before starting; if the pinned profile cannot supply or demonstrate it, revise the frozen plan before collection.
Use one model, fixed generation settings, model seed 95 where supported, and record null with reason where unsupported.
Use the same documented warm prompt/filesystem cache policy throughout each experiment and discard one full warm-up pair per task.
Use sequential pair order off/on for odd repetitions and on/off for even repetitions, in task order P1 through N4.
Run ten pairs per task and context for median-only interpretation or twenty if interpreting p95.
Record requested and effective mode, and retain paused and failed outcomes as separate states.
Do not run B1 alongside B2 or another benchmark.

For each side create a fresh fixture, hash its files, start a fresh terminal process and send the exact prompt after the relevant setup.
From its primary directory run `"$BIN" --plain --debug --provider local --context-window "$CAPACITY" --index-max-bytes "$INDEX_BYTES"`.
Attach `second` identically on both sides for P3.
Select `/repos index off` or `/repos index on` and capture status before and after.
Use identical predeclared thermal and cache conditions within pairs.

| Task | Class | Exact prompt | Rubric |
| --- | --- | --- | --- |
| P1 | Positive | Locate the API that prefixes an arbitrary string with beacon:. Give file, symbol and signature. | alpha.go, LookupBeacon, correct signature and location. |
| P2 | Positive | Which methods are defined on Catalog? Give their defining file and signatures. | methods.go, Open and Close, correct receiver and signatures. |
| P3 | Positive | Find Shared in the second root and distinguish it from the one in primary. Give both paths and return strings. | second beta.go returns second; primary alpha.go returns primary. |
| P4 | Positive | Discover Mutable, read its current source, then change its current prefix from fresh: to final:. Leave the current parameter list unchanged. | Tool trace proves read after discovery; diff touches only the return prefix in methods.go and preserves the two-parameter signature. |
| P5 | Positive | Locate the declaration PhantomMissing and explain how certain you are about its absence, given the available coverage. | No invented source, respects partial/omitted coverage and uses live fallback where appropriate. |
| N1 | Negative | In settings.txt replace the exact line label=before with label=after. Make no other changes. | Exact file content and only that changed file; any map call unnecessary. |
| N2 | Negative | Read alpha.go directly and quote the package declaration. | package alpha with read trace; any map call unnecessary. |
| N3 | Negative | Reply exactly ACCEPTANCE. | Exact final text; any map call unnecessary. |
| N4 | Negative | Given that the answer is 42, reply with the answer only. | Exact final text 42; any map call unnecessary. |

For P4 prewarm the old signature, then perform the same dirty rewrite in both modes before the measured prompt using `sed 's/Mutable(s string)/Mutable(s string, _ bool)/;s/old:/fresh:/' methods.go > methods.new && mv methods.new methods.go`.
Score the on trajectory for beneficial discovery and the off trajectory for correctness/current-source evidence; map absence in off is expected.
For P5 keep malformed.go and omitted.txt and freeze a reproducibly partial on fixture before collection; verify partial coverage in the raw tool result, and classify that cell separately if the intended coverage cannot be established.
Capture correctness, useful current sources and unnecessary exposure, invalid/unnecessary/repeated calls and missed beneficial calls from tool traces.
Capture every provider request's schema hash/bytes/tokens, prompt and output usage, TTFT, throughput and timing via verified runtime telemetry or reviewed instrumentation.
A debug transcript can establish tool calls, but request metrics absent from it need runtime telemetry or null with reason.
Capture first visible token separately and use a family sampler on the measurement device.
Keep schema-only and same-catalog interference results null without the reviewed seam.

### B2

On the Pi, from the evidence directory copied locally, collect `sh pi-inventory.sh > pi-inventory.txt`.
Return that file plus the cooling, ambient temperature, power supply, storage sharing and local server/model settings, without credentials.
Build from the confirmed SHA with `BUILD=$(sh build.sh /path/to/hint "$SHA")` and return `"$BUILD/build.txt"`.
Use the local inference profile and startup flags from B1 with the verified Pi capacity, plus the actual storage allowance and index thresholds.
Retain only the capacities the Pi really supports and use null plus reason for unsupported metrics and cells.
Before each run prepare an identical corpus revision and record eligible files/bytes, manifest and free space.
Use the [pinned corpus instructions](../wp2.2-mac-20261106/README.md) for a larger corpus.
Do not share the derived index with ordinary Hint processes.
Use a dedicated cache location with `XDG_CACHE_HOME` on Linux.

Start Hint in a terminal using `printf 'Hint exec PID=%s\n' "$$"; exec "$BUILD/hint" --plain --debug --provider local --context-window "$CAPACITY" --index-max-bytes "$INDEX_BYTES"` inside a subshell from the corpus root.
In another terminal run `python3 pi-sample.py "$RUN_ID" "$HINT_PID" "$MODEL_PID" "$XDG_CACHE_HOME/hint/index" >> resources.jsonl`.
Start sampling before the condition begins and stop with Ctrl-C after the ending status snapshot.
Use a new run ID for each condition and record timestamps, thermals, throttling, provider metrics and status.
Keep separate model and Hint family data and use cgroup counters if available for helpers shorter-lived than the sample interval.

| Condition | Operation |
| --- | --- |
| Off | Select `/repos index off`, record status and run the frozen inference task. |
| Idle | Select `/repos index on` with a completed index, no edits, then run the same inference task. |
| Initial build | With all processes closed, choose a fresh disposable cache directory, select on, sample discovery and run the same inference task on the frozen schedule. |
| Backlog | Restore the prebuilt cache and corpus, then apply a frozen edit/rename/delete batch while sampling, record queue and recovery, and run the same inference task. |
| Query | Restore the prebuilt state, send the frozen P1 and P2 prompts for the chosen corpus, and record retrieval timing, freshness and correctness. |
| Pressure/recovery | Use a separately frozen bounded pressure procedure on the dedicated Pi, capture admission reason and queue, release the pressure and capture resumed progress. Keep the pressure procedure pending until device headroom and tooling are known. |

Use at least ten valid off/on pairs after warm-up for each comparable resource condition, or twenty when interpreting p95.
Report the midpoint median of per-pair throughput losses `(off - on) / off`.
Report additional peak simultaneously sampled Hint/helper family RSS relative to comparable off, in MiB.
Preserve all pauses, throttling, failures and OOM events, and exclude paused samples from active-on counts with a recorded reason.
Derive CSV from JSONL, retain raw values, report counts, and use nearest-rank p95 only with the required sample count for interpretation.
