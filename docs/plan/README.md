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

Each phase file header carries a status line: `Planned` → `In progress` →
`Released vX.Y`.

## Work package numbering

`WP<phase>.<n>` — e.g. `WP0.3` is the third work package of Phase 0. Numbers
are stable once assigned; new packages get the next free number even if
inserted logically earlier.
