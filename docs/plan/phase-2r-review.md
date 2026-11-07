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
