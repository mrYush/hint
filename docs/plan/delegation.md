# Delegation: `hint` among other harnesses

> Status: **Design spike — nothing here is implemented.** Not scheduled.
> Tracked as Q11 in [`../../PLAN.md`](../../PLAN.md).
> This is a concept, not a scope document: if it disagrees with `PLAN.md` or
> the phase files, those win. It may move to another repository or become an
> article; nothing below is a commitment until a work package names a branch.

## The question

Should `hint` be able to run other agent harnesses — a second `hint`, Claude
Code, Codex CLI, a remote A2A agent — as delegated subtasks? And if so, what
is it then: a tool that happens to call them, or a conductor that owns the
task, the permissions, the budget and the review?

The difference matters. A tool call returns text; a delegated harness returns
a change. The first is a feature; the second is a product.

## Terms

- **Harness** — an agent loop with its own prompt, tools and policy: today's
  `hint`, Claude Code, Codex CLI, an A2A agent.
- **Runner** — what executes a harness and reports on it: a subprocess, a
  container, a remote endpoint. A harness thinks; a runner carries it.
- **Conductor** — `hint`'s role here: choose a harness, launch it under a
  ceiling, watch it, collect the change, put it through review.
- **Task** — one delegated unit: a prompt, a working directory, a budget and
  a permission ceiling.

The word *harness* is already used twice in this repository: the evaluation
bench ([architecture](architecture.md#implications-noted-for-the-plan)) and
the coding-agent category
([why-hint](why-hint.md#why-start-with-a-solved-problem)). The seam below is
called `Runner` for that reason.

## Three shapes, one of them ours

1. **Local conductor.** `hint` runs harnesses on the user's machine, with the
   user's credentials, under the user's permission mode. In scope.
2. **Hosted runner.** A multi-tenant service executing harnesses for many
   users: isolation, quotas, billing. Out of scope — it is the "managed
   service" non-goal in [why-hint](why-hint.md#non-goals-stated-first) with a
   scheduler attached, and therefore a separate product if it ever exists.
3. **Protocol peer.** `hint` as an A2A client and server (WP2.6). In scope,
   and the first real interoperation: the same questions, with a standard on
   both ends.

There is a fourth, easy to forget: **`hint` as a harness that someone else
manages**. A conductor that cannot be conducted is a bad citizen, and the
first runner to support should be `hint` itself. It is also the cheapest: the
binary already answers `-p --output json`, and a worktree is one
`git worktree add` away.

## Why `hint`

Not because launching a subprocess is hard. Because the parts around it
already exist:

- the permission gate (`internal/permission`) gives a ceiling to cap a child
  with, and the run modes to express it;
- the provider router gives the choice of which model, or which harness, is
  enough for the task;
- sessions give an audit trail: request, result, artifact, cost;
- the RPC core gives one place for a `run.*` surface, for shells and for A2A;
- the diff layer already renders a patch for a human decision — which is what
  a delegated change needs.

What is missing is the part that makes it safe: **a sandbox**. `bash` today
runs with the user's full permissions and outside `tool.Root`'s confinement
([architecture](architecture.md#implications-noted-for-the-plan), R8 in
[risks](risks.md)). A child harness is worse: it is less observable than a
command the user confirmed. Hence the order in Q11 — sandbox first.

## What "manage" means

A conductor does seven things. A version missing any of them is a launcher,
not a manager.

1. **Register.** A `harnesses:` config block: id, command or endpoint,
   declared capabilities, a maximum action class, an env allowlist, a
   workspace mode (`worktree`, `read-only`, `copy`).
2. **Choose.** Which harness for this task: capability, cost, locality. First
   explicit — the model proposes, the user confirms — automatic only after
   the explicit path has data.
3. **Launch.** Under a worktree, with a scrubbed environment, a budget and a
   permission ceiling. Never the live tree for a write-capable child.
4. **Supervise.** Stream its output, relay or deny its permission questions,
   enforce the ceiling, cancel on budget or user request, kill the process
   group on timeout.
5. **Collect.** A structured result, not a transcript: status, summary, the
   patch, the files touched, usage, a reference to the transcript.
6. **Review.** The patch is the product. It goes through the same diff and
   permission path as an edit the model made itself: accept, reject or edit
   per file.
7. **Record and clean up.** The session stores the request, the result and
   the artifact reference, not the child's conversation. The worktree is
   removed after the result is accepted or discarded.

## The seam: `Runner`, not `Tool`

`Tool` is one call returning text
([tool.go](../../pkg/agentapi/tool.go)). A delegated harness is a lifecycle:
it starts, emits, asks, costs, and can be cancelled. Folding it into `Tool`
loses exactly the parts that make it safe.

Sketch — not a proposal to add it now, only the shape this document assumes:

```go
// Runner executes one harness for one task.
type Runner interface {
    Name() string
    Capabilities() Capabilities // maximum action class, modalities, context
    Run(ctx context.Context, task Task) (<-chan RunEvent, error)
    Cancel(runID string) error
}

type Task struct {
    Prompt    string
    Workspace string // a worktree, not the live tree
    Ceiling   PermissionCeiling
    Budget    Budget // wall clock, iterations, optional tokens
    Env       []string // an allowlist, never the parent's environment
}

type Result struct {
    Status     RunStatus // done | failed | cancelled | budget
    Summary    string
    Patch      []byte // unified diff, may be empty
    Files      []string
    Usage      Usage
    Transcript string // a path under the debug log, redacted
}
```

Why not reuse `Event`: a client must not have to know that one turn was
actually three harnesses. `RunEvent` is what the conductor consumes; the
existing `Event` stream is what it produces for its own shells.

## Permissions, secrets, sandbox

- **The ceiling is outer.** A child never gets a wider mode than the `hint`
  run that started it. `--yolo` does not propagate; if it did, the gate
  would be advisory.
- **The child's questions come back.** A runner either relays a permission
  request into the same `permission` event the shell already renders, or
  denies it. Silence is not an answer.
- **Secrets do not cross.** The child gets an explicit allowlist, never the
  parent's environment. WP1.7's rule — no key reaches a tool subprocess —
  applies to a runner, and the child's output is scrubbed like any tool
  result.
- **No sandbox, no delegation.** The rule "no sandbox, no tools", one level
  up: until a child can be confined (`landlock`/`bwrap`, `sandbox-exec`, a
  container), a write-capable harness is not launched.

## Workspace and result

- One worktree per run, `git worktree add` from the repository `hint` was
  started in. A read-only task may share the live tree; anything that can
  write gets its own.
- One write-capable run per worktree at a time; read-only runs may overlap,
  like the read-class tool schedule.
- The child may commit inside its worktree; the conductor still presents the
  patch, because the user reviews the change, not the child's history.
- A rejected patch discards the worktree; an accepted one applies to the
  parent tree through the normal edit path.

## Scheduling and budget

Costs are not comparable across harnesses: one bills tokens, another a
subscription, a third nothing but wall clock. The budget is therefore wall
clock and iterations first, tokens where the harness reports them, and money
never as a hard unit. A run that exceeds its budget is cancelled, not
truncated — a half-applied patch is worse than none.

## Routing

The interesting product is not "can run Claude Code". It is "picks the
cheapest sufficient executor and keeps the user in control of what any of
them may touch". But routing is a policy over a cost model that does not
exist yet, so it starts explicit: a `delegate` tool and a slash command where
the user names the harness; the model may propose one, the permission class
is `execute`, the user confirms. Automatic selection waits for data.

## Trust and the catalog

A harness is executable code with an agent inside. The Phase 7 registry
([phase-7](phase-7-feature-store.md)) is the natural place for a harness
package, but the bar is higher than for a tool: a manifest that declares
capabilities, a pinned version or commit, a signature or hash, and a
human-readable statement of what it may do. A catalog entry with no sandbox
story is a no.

## Observability

- `hint runs` lists past runs: harness, task, status, cost, patch size,
  session.
- The child's transcript is stored apart from the session, under the debug
  log and redacted, referenced by id.
- The session records what was asked and what came back; a reader can
  reconstruct the delegation without the child's internals.

## Failure modes

The list that decides the design:

- child hangs or floods output — timeouts and output caps;
- child asks and nobody relays — deny by default;
- child writes outside its worktree — sandbox, not path checks;
- child spawns a child — a depth cap, and A2A loops get a task id;
- cost runaway — a budget and cancellation, not a warning;
- version drift in a foreign CLI — an adapter contract pinned by version,
  and a "not supported" error rather than a guess;
- terms of service of a hosted harness — the user's own credentials, never
  `hint`'s, and no resale.

## Slices

Ordered by dependency; each is useful on its own.

- **S0 — a spike, no core code.** Launch `hint -p --output json` in a
  `git worktree`, parse the result, measure. Proves the contract shape with
  no new package.
- **S1 — `hint` as the first runner.** A `delegate` tool, class `execute`,
  worktree, ceiling, budget, patch in the result. No foreign CLI.
- **S2 — A2A runner.** The same interface over WP2.6.
- **S3 — one foreign adapter.** A generic subprocess runner with a declared
  output contract; one harness supported, pinned.
- **S4 — `harnesses:` config, `hint runs`, the review path.**

The sandbox layer gates S1 and S3; S0 is a spike and S2 is remote.

## Non-goals

- A hosted multi-tenant runner.
- Reselling or proxying another product's subscription.
- Reimplementing a harness's own UX inside `hint`.
- Automatic routing before the explicit path has data.
- Running a harness that `hint` cannot cap.

## Open questions

- `Runner` beside `Tool`, or a `run.*` RPC method first?
- Does the result carry a patch, or does the child commit and `hint` reviews
  a branch?
- Do runners hold long-lived sessions, or is every task a fresh process?
- Does `hint` need a capability descriptor of its own — the A2A card today,
  a `hint describe` later?
- Which sandbox primitive is first: `bwrap`, `sandbox-exec`, or a container
  image?
- Where does this document live long-term: `docs/plan/`, the feature-store
  design, or another repository?
