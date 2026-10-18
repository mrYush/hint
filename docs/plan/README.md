# Planning conventions

This directory holds the detailed development plan for `hint`. The top-level
roadmap lives in [`../../PLAN.md`](../../PLAN.md).

## Structure

| File | Contents |
|---|---|
| `why-hint.md` | Why the project is worth building next to the prior art: the claims it rests on, what they cost, and the conditions that would end it |
| `architecture.md` | Core architecture, package layout, key interfaces, references, platform/provider matrices |
| `subscription-access.md` | Design spike: how a subscription-gated model credential can be made useless outside the product (Q9, WP7.6) |
| `phase-0-mvp-cli.md` … `phase-7-feature-store.md` | One file per roadmap phase: goal, work packages, task checklists, acceptance criteria |
| `risks.md` | Cross-cutting risks and mitigations |
| `review-2026-09-28-go-practices.md` | Go-practices audit of `release/v0.2`: release blockers, must/should/nit findings with locations and fixes, the issue list they map to |
| `raspberry-pi-smoke-test.md` | Checklist for a Raspberry Pi **5** + Ollama smoke. Backlog as of 2026-10-11; does not gate `v0.2.0` |
| `evidence/pi-4-20261011-105151/` | Recorded Pi 4 `pi-bench` (commit `b2aee40`) that is the linux/arm64 hardware record for `v0.2.0`. Not a product guide |
| [`../raspberry-pi-4-4gb.md`](../raspberry-pi-4-4gb.md) | Product guide: offline fallback on Pi 4 4 GB — devices, use cases, why `llama-server`, launch recipe |

Product documentation under `docs/` is limited to launch instructions,
examples, rationale for chosen solutions, device coverage, and use cases.
Chronological field / incident logs for a later article are kept out of this
index and are not part of the product doc set.

## Source of truth and issue tracking

- **Files here are the source of truth** for scope and status. GitHub Issues
  mirror the work packages of the *active* phase (one issue per work package,
  titled `[Phase N][WPx.y] Title`) for day-to-day tracking and discussion.
- When an issue closes, tick the corresponding checkboxes in the phase file in
  the same PR (or a follow-up docs commit).
- When scope changes in a discussion or issue, port the decision back into the
  phase file — an unmerged decision does not exist.

## Status legend

Phase files use plain GitHub task lists:

- `- [ ]` — not started
- `- [x]` — done
- Append `(blocked: reason)` to a task that cannot proceed.

**Branch per work package.** Every `### WPx.y` heading is followed by a
``**Branch:** `dev/wpx.y-<short-name>` `` line, assigned when the work package
is written down, before work starts (naming per CONTRIBUTING "Git workflow").
The work package's status is read from that branch, not from a separate
field: no such branch — not started; branch exists — in progress; branch
merged into `develop` — done (append **done** to the heading with the merge
date; this may be automated on merge). One work package — one branch; do not
bundle small packages into a shared branch, merge them quickly instead.
Checkboxes inside the package remain the step-level view.

**Exception: scope that grew inside one branch.** A branch that, while in
progress, came to carry work beyond its package (another feature the
package turned out to need, or the plans of later packages) may be merged
as one PR instead of being split after review has started. Three
conditions: the extra scope is in the host package's checklist, so the
phase file shows what was delivered where; the PR description has a scope
note naming the exception and why splitting was not worth it; and the
phase file records the exception under the package. Later packages still
get their own branches; the exception is for scope found on the way, not
a way to plan two packages into one branch.

Each phase file header carries a status line: `Planned` → `In progress` →
`Released vX.Y`.

## Work package numbering

`WP<phase>.<n>` — e.g. `WP0.3` is the third work package of Phase 0. Numbers
are stable once assigned; new packages get the next free number even if
inserted logically earlier.
