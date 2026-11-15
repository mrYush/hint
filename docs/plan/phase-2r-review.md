# Phase 2R — Post-v0.4 global review and roadmap reset

> Status: Planned · Target release: **v0.4.1 (conditional)** · Estimate: 2–3 weeks (part-time)
> Runs immediately after the `v0.4.0` tag and before Phase 3.
> Depends on: Phase 2 (`v0.4.0`) and its release gate.

## Goal

Stop adding features immediately after `v0.4.0` and look at the whole product: code, architecture, market, user experience and efficiency.
Fix what is broken, decide what to build next with evidence, and rewrite the roadmap.
The phase produces a `v0.4.1` patch when the review accepts fixes, or an explicit decision to skip the tag, followed by the next version of `PLAN.md`.
It is an inserted review, not a renumbered phase: `2R` sits between Phase 2 and Phase 3 (decided 2026-11-07).

The review is timeboxed.
A finding that cannot be fixed inside the box becomes a scored roadmap item, not an open-ended rewrite.

## Work packages

### WP2R.1 — Code, architecture and efficiency audit

**Branch:** `dev/wp2r.1-audit`

Read the tree as a reviewer would, not as its author.
The audit covers correctness, the package graph, the public contract, tests, performance, security and cross-platform behavior.
Every finding gets evidence, severity and a disposition; a finding without a test or a dated accept decision is not closed.

- [ ] Package graph and boundaries: the shell does not reach `internal/agent`, `internal/tool/builtin` or `internal/project`; `pkg/agentapi` remains the only public contract; no cycles; the new `attach` and `termimage` packages fit their planned boundary; existing graph tests cover the rule.
- [ ] Architecture: the core/service split, RPC compatibility and optional methods, provider and router seams, tool and permission layers, session format, config, workspace and index boundaries, and the cost of the future ACP and mobile shells.
- [ ] Go quality: error wrapping and `errors.Is`/`errors.As`, context cancellation, goroutine lifetimes, races, deadlines, resource cleanup, panic paths, UTF-8 and integer boundaries, file and symlink handling, and build tags.
- [ ] Tests: table-driven coverage of every new branch, `-race`, fuzzing for the JSONL session, config, RPC framing, SSE parsing and MCP decoding, golden wire and session tests, and the six-target cross-compile.
- [ ] Efficiency baseline: startup, TUI frame, prompt tokens per task, tool calls per task, retries, session load and list, peak RSS on a large repository and on the Pi 4; pin the fixture, commit and model profile.
- [ ] Performance evidence: `go test -bench`, `pprof` and `-race` where they answer a question; record the commands and the machine.
- [ ] Security: permission bypasses, secret handling, file-tool confinement, `bash`, MCP trust, prompt injection, attachment and URL handling once it lands, debug-log redaction and supply chain.
- [ ] Documentation truth: README, help, docs and comments that no longer match the code; stale claims and known issues.
- [ ] Findings register: severity, evidence, affected files, disposition (`fix in v0.4.1`, `roadmap`, `won't fix` with a dated reason), and a test for every fix.
- [ ] Fix high-severity and cheap high-impact findings; keep `gofmt -l`, `go test ./...`, `go test -race`, `go vet`, `golangci-lint` and the cross-build green.
- [ ] Write the audit report as `docs/plan/review-2026-11-post-v0.4.md` (or an evidence directory) and compare it with the 2026-09-28 Go-practices review.
- [ ] Decide `v0.4.1`: tag it when the accepted fixes are worth a patch and the release notes say why, or record the decision to skip it and carry the findings to `v0.5`.
- [ ] Release verification when tagging: all gates above, the Pi 4 smoke where the change touches it, and the release note.

### WP2R.2 — Product research, quality scorecard and roadmap reset

**Branch:** `dev/wp2r.2-roadmap`

Find out what users will want next, how the market is moving and what `hint` should do about it.
The output is not a market report; it is a scored roadmap and a product definition that the next two releases can execute.

- [ ] Market matrix with dated sources: at least eight products across agent CLIs, terminal agents, editor agents, local runtimes and protocols; start from Claude Code, OpenAI Codex CLI, Gemini CLI, Aider, opencode, Cline, Zed/ACP and Hermes Agent, and justify additions or removals; use release notes, changelogs, docs and issue trackers rather than memory.
- [ ] Change dynamics: what shipped in the last 6–12 months, release cadence and issue velocity, what became table stakes, what is still hard and what users complain about; Hermes v0.23 is one data point, not the template.
- [ ] User signals: mine the project's issues and discussions, dogfood the product, and run five short interviews if they are available; tag jobs-to-be-done and friction, and do not overfit to the loudest issue.
- [ ] Product principles and non-goals: quality, convenience, efficiency, local-first, privacy, no lock-in and one core with many shells; use them as the scoring function.
- [ ] Trend radar with three horizons (now, next, later), a confidence for each bet and a falsifiable signal that would change the decision; include a "stop doing" list.
- [ ] Quality scorecard: task success, time to first useful answer, correction rate, tool calls and prompt tokens per task, cost per task, latency, peak RSS, crash and error rate, permission prompts and first-run success.
- [ ] Benchmark suite: 10–20 reproducible fixture tasks on a pinned repository, model and profile; record the `v0.4.0` baseline, set `v0.5` targets and add a deterministic regression gate where CI can carry it.
- [ ] UX audit: onboarding, provider and key setup, model choice, error messages, help, session resume, attach and paste flows, cancellation, recovery, accessibility and keyboard-only use, install and update; fix high-friction items or schedule them.
- [ ] Roadmap reset: re-score every phase and work package with reach, impact, confidence and effort plus a trend-fit judgement; drop, defer or split what no longer earns its place.
- [ ] Rewrite `PLAN.md`, the phase files, this directory's README, `architecture.md` and `risks.md` in one coherent change; keep the phase numbering stable and record `2R` as an insertion.
- [ ] Every work package in the rewritten plan gets a branch line, acceptance criteria and a measurable outcome; stale status and tentative proposals are removed or explicitly parked.
- [ ] Finish [`adaptive-tuning.md`](adaptive-tuning.md): the term, definition, signals, preference model, guardrails, metrics, evaluation and roadmap home; resolve Q12 and do not ship code in `2R`.
- [ ] Owner review: present the findings, the scorecard, the next two releases and the ATE definition; the owner accepts or returns them before Phase 3 starts.
- [ ] Record the decisions and rejected alternatives in a dated review note, including what the review deliberately did not change.

### WP2R.3 — Harbor benchmark integration

**Branch:** `feature/harbor-adapter`

[Issue #107](https://github.com/mrYush/hint/issues/107) adds a maintained Harbor adapter and a reproducible Terminal-Bench CPU-only workflow.
This companion package supplies external evaluation evidence for WP2R.2 and keeps its fixture and scorecard scope separate.

- ✅ Pin Harbor and Hint, run the production headless loop in isolated Docker trials, and preserve Harbor's verifier and task settings.
- ✅ Record effective configuration, artifacts, ATIF, available usage and exceptions, with a deterministic tool-using Docker regression.
- [ ] Validate real model routes with an explicitly selected scope and number of attempts.
  GLM and DeepSeek gateway runs require operator-confirmed model IDs and endpoints.
- ✅ Add offline Python CI for option validation, partial log recovery, result accounting, static typing and API documentation.
- [ ] Unblock direct DeepSeek in a separate provider change with explicit reasoning controls and reasoning history on tool turns.
  The [provider dependency](../../integrations/harbor/README.md#deepseek-provider-dependency) tracks the local scope for `fix/deepseek-reasoning-history`.

#### Remove declarative monetary budgets (2026-11-11)

The owner requested removal of monetary budget declarations because the adapter neither calculates costs nor enforces spending limits.
This focused part of [#115](https://github.com/mrYush/hint/issues/115) uses its assigned branch, `dev/harbor-review-cleanup`.

- ✅ Remove the declared amount and provider budget confirmation from route configuration, live-run validation and new manifests.
- ✅ Update the reproduction instructions and regression coverage while retaining trusted-environment confirmation.

#### Complete Harbor review cleanup (#115)

**Branch:** `dev/harbor-review-cleanup`

The owner requested completion of [#115](https://github.com/mrYush/hint/issues/115) on 2026-11-11.
The cleanup was merged into `develop` in [PR #118](https://github.com/mrYush/hint/pull/118) on 2026-11-11 (`1c45765`).
The findings register below preserves the complete review dispositions and their evidence.
This follow-up completes review cleanup without absorbing the real-route acceptance in #107 or the separate #109–#114 work.

- ✅ Remove unreachable YAML exception handling and the redundant credential-overlay branch.
- ✅ Release the runner's temporary validation agent before starting Harbor while reusing the existing route and auth validation.
- ✅ Remove repeated production ATIF parsing and verify serialized tool trajectories against Harbor's schema in regression tests.
- ✅ Record a disposition and evidence or rationale for every finding named in #115.
- ✅ Run Python, Docker fake-provider and repository checks, then prepare the branch for review into `develop`.

##### Findings register

All 33 finding IDs listed in #115 are covered below.
Grouped IDs share a disposition and rationale.

| Finding | Disposition | Evidence or rationale |
| --- | --- | --- |
| F-06 | Fixed in #108 | `test_journal_tail_validation` and `test_journal_invalid_utf8` distinguish unfinished final JSON from complete invalid JSON, schema errors and interior corruption. |
| F-09 | Intentionally retained | The runner accepts the pinned 63-task export; task selection operates on that complete export and does not add partial-export support. `test_dataset_export_contract` covers empty, partial and oversized exports. |
| F-10 | Unsupported finding | An empty export fails the 63-task check before reaching `Task not found`; `test_dataset_export_contract` also verifies a missing named task in a complete export. |
| F-11 | Fixed in #108 | `test_summary_propagates_programming_errors` verifies that unexpected programming errors escape rather than becoming unreadable-result rows. |
| F-12 / D-06 | Fixed in #118 | Removed the unreachable outer YAML handler. `test_route_config_does_not_echo_inline_credentials` retains sanitized parser/schema errors at the configuration boundary. |
| T-02 / T-05 / T-08 / T-09 | Intentionally retained | Provider finish/error values and journal record/content kinds are external data that may grow; retain their strings and existing fallback/ignore behavior instead of narrowing every field to a closed enum. `test_future_provider_values` and `test_tool_trajectory_round_trip` cover these boundaries. The journal role remains restricted where conversion needs a known role. |
| T-06 | Intentionally retained | `BaseHTTPRequestHandler` dispatches `do_POST` dynamically; it does not declare that method for an `@override` annotation. |
| T-07 | Intentionally retained | The classmethod's inferred `cls` type preserves subclass typing; an explicit narrower annotation adds no guarantee. |
| T-10 | Intentionally retained | Existing test factories and cached-property substitution are confined to test fixtures. Replacing them does not fix production behavior and is not an acceptance gate. |
| A-06 | Intentionally retained | `RunArgs` gives `launch` a typed, independently testable input; matching the CLI fields is expected. |
| A-08 | Intentionally retained | Verify the binary immediately before execution, including the preinstalled-agent lifecycle; installation-time verification alone does not cover that boundary. |
| A-09 | Fixed in #118 | Removed the repeated production parse. `test_tool_trajectory_round_trip`, `test_partial_trace_and_unknown_usage` and session-selection tests validate the emitted JSON against Harbor's schema, including tool observations and unavailable metrics. The Docker smoke independently parses the saved trajectory. |
| D-03 | Intentionally retained | `select_binary` is a documented convenience entry point with architecture/checksum tests; its small size does not make it dead code. |
| D-05 | Fixed in #118 | Supply the required credential overlay directly after placeholder injection or live-key validation. `test_route_launch_releases_validation_agent` exercises both paths with the real adapter. |
| D-07 | Intentionally retained | The small `summarize` dispatch preserves the typed `agent` and `execution` result fields and per-artifact error accounting without a generic untyped abstraction. |
| N-01 / N-02 / N-03 / N-04 / N-05 / N-06 / N-07 / N-08 / N-09 | Intentionally retained | These readability suggestions do not identify a behavior failure. Preserve existing artifact names and public helpers; defer renames until the corresponding code needs a functional change. The adapter's `CancelledError` denotes a Hint result and is distinct from `asyncio.CancelledError`. |
| C-07 | Intentionally retained | Keep the Harbor job on every PR. Path filters are an optional optimization and are unnecessary for this small job; required-check behavior remains unchanged. |
| S-11 | Intentionally retained | The fake server defaults to loopback and uses a fake credential. Explicit bridge binding is needed for the documented Docker Engine workflow. |
| S-13 | Fixed in #118 | Reuse the existing auth validation, then release the temporary parent-side agent before subprocess execution. `test_route_launch_releases_validation_agent` verifies through a weak reference that the real validation agent is gone at the subprocess boundary in both live and dry-run paths. Environment auth remains necessary; no memory-erasure guarantee is implied. |
| S-14 | Intentionally retained | The operator selects the credential variable in trusted route YAML. A naming suffix would neither establish credential purpose nor preserve all valid configurations. |
| S-17 | Fixed in #108 | `test_digest_validation` covers exact 64-digit hexadecimal SHA-256 digests for both agent and verifier images. |
| Owner-requested removal of monetary budget declarations | Fixed in #118 | Route YAML and new manifests no longer declare an unenforced amount or require a provider-budget confirmation. Trusted-environment confirmation is preserved. |

##### Verification

Local verification on 2026-11-11 passed all 29 Python unit tests, Ruff formatting and lint checks, Pyright, and Sphinx with warnings treated as errors.
Repository checks passed: `gofmt -l .`, `go test ./...`, `go test -race ./...`, `go vet ./...`, golangci-lint v2.13.2 and `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`.
Docker fake-provider checks on linux/amd64 passed four successful trials with reward 1.0 and no exceptions, plus one authentication-failure trial and one timeout trial with the expected exception types and cancellation state.
The Docker checks covered native tools, separate verification, session isolation, saved ATIF, usage accounting and credential redaction.
These checks used a local fake provider; real-model acceptance remains in #107 and the documented isolation boundary remains in #111.
The [integration README](../../integrations/harbor/README.md#free-regression) contains the reproduction commands.


#### Review remediation plan (2026-11-11)

The following review fixes remain in `feature/harbor-adapter` under #107.
They implement the session-validation subset of #110 and the CI timeout subset of #113.

1. ✅ F-04/F-05 — Match journal headers to stdout session IDs, reject ambiguous matches and empty successful trajectories, and retain sanitized partial evidence with explicit collection status.
2. ✅ F-06 — Recover only an incomplete final JSON record without a line terminator, and reject complete malformed records and schema errors.
3. ✅ F-11/S-17 — Propagate unexpected summary programming errors and require a complete SHA-256 image digest.
4. ✅ CLI/C-06 — Explain dry-run-only route flags in CLI help and bound the Harbor CI job to 15 minutes.
5. ✅ Validate the regressions, run repository checks and Docker fake-provider scenarios, and review the final diff.


#### Scope exception — repomap test fixture (#116)

At the owner's request, `feature/harbor-adapter` also carries the focused test fix for [#116](https://github.com/mrYush/hint/issues/116), which blocked PR #108.
This follows the existing exception for scope discovered during review and avoids a separate branch for the small test-only correction.
The PR description should carry this scope note when updated.

- ✅ Isolate the manually driven scheduler and choose the blocked child from the exact first directory page consumed by the fixture.
- ✅ Preserve the three-step assertion and the requirement that the parent stream remains open.
- ✅ Verify repeated race/coverage runs, Linux execution and all repository Go checks.

The original test passed 100 macOS race/coverage repetitions and 10 Linux arm64 repetitions.
Forcing the prequeued child into the second enumeration page reproduced `state=pending` even with the background scheduler stopped.
Rediscovery increments the queued revision, so rotating the older blocked item intentionally leaves the newer revision in place.
The fix controls the fixture's discovery order while retaining production queue behavior.

Validation passed with 100 targeted race/coverage repetitions on macOS arm64, 100 targeted Linux arm64 container repetitions, the full race/coverage suite, ordinary tests, vet, golangci-lint and Linux arm64 compilation.
Remote CI verification remains pending publication of this correction.

## Outputs

- A code and architecture findings register with dispositions and a `v0.4.1` decision.
- A product research note with dated sources, a competitor matrix, a trend radar and a stop-doing list.
- A reproducible quality and efficiency scorecard with a `v0.4.0` baseline and `v0.5` targets.
- A rewritten roadmap with scored packages, the next two releases in detail and the `2R` insertion recorded.
- A settled Adaptive Tuning Effect design and a roadmap home for its first stage.
- A release decision: `v0.4.1` tagged, or explicitly skipped.

## Non-goals

- A rewrite of the core or a general refactor without a finding behind it.
- New user-facing features beyond the fixes the review accepts.
- Model training, fine-tuning or a cloud personalization service.
- A formal security certification or an external audit, though the findings may recommend one.
- A market report that does not change a decision.
- Replacing the phase structure or renumbering the later releases.

## Acceptance criteria

1. The audit report names every finding with severity, evidence and a disposition; every high-severity finding is fixed with a test or accepted with a dated reason.
2. The product research note is dated, cites its sources, names the trend bets and the stop-doing list, and the roadmap reflects them.
3. The scorecard baseline is reproducible on a pinned fixture, model and profile, and the `v0.5` targets are measurable.
4. Every package in the rewritten roadmap has a branch line, acceptance criteria and a measurable outcome; the next two releases are specified.
5. `adaptive-tuning.md` settles the term, the signal and consent model, the guardrails, the metrics and the roadmap home; Q12 is resolved.
6. `v0.4.1` is tagged only when the accepted fixes warrant it, and the decision is recorded either way.

## Review method

- Timebox: 2–3 weeks, with a short daily log and one decision record at the end.
- Evidence over opinion: every finding and every trend claim names a file, a command, a URL and a date.
- One change at a time: fixes land in `WP2R.1`, the plan rewrite lands in `WP2R.2`, and a plan edit never hides a code change.
- The owner is the reviewer of the roadmap and the ATE definition; the audit findings can be reviewed in the PR.
- If the timebox closes with unfinished findings, ship the high-severity fixes, park the rest as scored items and say so in the record.

## Decisions to make when the work starts

- Whether `v0.4.1` ships or the review only feeds `v0.5`.
- The fixture repository and the model/profile pair for the scorecard.
- Whether to invite an external reviewer for the architecture audit.
- The roadmap home for the first ATE stage.
- Whether the audit report lives in `docs/plan/` or `docs/plan/evidence/`.
