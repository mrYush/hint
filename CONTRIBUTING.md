# Contributing to hint

Thanks for your interest in `hint`! This document covers how to set up a
development environment, the project conventions, and how changes get planned,
reviewed, and released.

`hint` is evolving from a one-shot CLI into a cross-platform personal agent —
see [PLAN.md](PLAN.md) for the roadmap and [docs/plan/](docs/plan/) for the
detailed per-phase plans. Please skim these before proposing significant
changes: a feature that conflicts with the target architecture (for example,
logic that bypasses the future `pkg/agentapi` contract) will be asked to be
reworked.

## Development setup

Requirements:

- Go ≥ 1.20 today; the MVP (Phase 0) raises this to **Go ≥ 1.22**
- No cgo — the core must always build with `CGO_ENABLED=0` (cgo-dependent
  features live behind build tags; see `docs/plan/risks.md`, R1)
- An API key for an OpenAI-compatible service, or a local
  [Ollama](https://github.com/ollama/ollama) for offline work

Build and run:

```bash
git clone https://github.com/mrYush/hint.git
cd hint
go mod tidy
go build -o hint ./cmd/hint
./hint --help
```

Run the tests:

```bash
go test ./...
go vet ./...
```

Cross-compilation must stay trivial. If your change builds on your machine
but breaks `GOOS=linux GOARCH=arm64 go build ./...`, it will not be merged.

## Project layout

Today's code is small (`cmd/hint`, `internal/config`, `internal/llm`,
`internal/context`). The target layout is described in
[docs/plan/architecture.md](docs/plan/architecture.md); new code should move
toward it, not away from it. Two rules worth calling out now:

- **`pkg/agentapi` is the public contract.** Everything exported from it is
  future SDK surface: document it, test its JSON round-trips, and treat
  breaking changes as exceptional.
- **`internal/` is private.** Do not import it from outside the module, and
  do not promote packages out of it without a plan discussion.

## How work is planned

- Files under [docs/plan/](docs/plan/) are the **source of truth** for scope
  and status; [PLAN.md](PLAN.md) holds the roadmap. Conventions (work-package
  numbering, status legend, issue mirroring) are in
  [docs/plan/README.md](docs/plan/README.md).
- GitHub Issues mirror the work packages of the *active* phase (currently
  Phase 0, issues titled `[Phase 0][WP0.x] …`). Comment on the relevant issue
  before starting sizeable work so effort isn't duplicated.
- Scope changes land in the phase file in the same PR as the code (or as a
  docs-only decision PR first). An agreement that exists only in a comment
  thread does not exist.

## Git workflow

The project uses a **GitFlow** model with two permanent branches:

- **`main` — releases only.** Every commit on `main` corresponds to a
  released (or about-to-be-released) state; nothing is committed to it
  directly. Release tags live here.
- **`develop` — working code.** The permanent integration branch: all
  day-to-day work lands here through pull requests, and it must always build
  and pass tests.

Flow:

- **Work branches off `develop`**: `feature/<short-name>`,
  `fix/<short-name>`, or `docs/<short-name>`; short-lived, merged back into
  `develop` via PR (squash-merge by default, so a branch becomes one coherent
  commit; keep the PR title in the commit-subject style described below).
  Rebase or merge `develop` into your branch to stay current — but never
  rewrite history of a branch someone else may have checked out (no
  force-push after review has started; add commits instead).
- **Releases**: a `release/vX.Y` branch is cut from `develop` when the
  version's scope is complete. Only stabilization fixes and release chores
  land on it. It is then merged into `main` with a regular merge (`--no-ff`),
  the merge commit on `main` is tagged with an annotated `vX.Y[.Z]` tag
  (pre-releases like `v0.1-alpha` included) which drives goreleaser (WP0.10),
  and the release branch is **merged back into `develop`** so stabilization
  fixes aren't lost, then deleted. The version plan per phase is in
  [PLAN.md](PLAN.md).
- **Hotfixes**: `hotfix/<short-name>` branches off `main`, is merged into
  **both `main` (then patch-tagged) and `develop`**, and deleted.

## Branch protection and review policy

Both permanent branches (`main` and `develop`) are covered by a repository
ruleset. What it enforces:

- **A pull request is required** — no direct pushes to either branch.
- **One approving review**, and it must come from a **code owner**
  (`Require review from Code Owners`). Anyone may leave a review, but only a
  code owner's approval unblocks the merge.
- **Stale approvals are dismissed** when new commits are pushed
  (`Dismiss stale pull request approvals when new commits are pushed`) — an
  approval only covers the code it was given on.
- **Conversations must be resolved** before merging
  (`Require conversation resolution before merging`).
- **Deletions are restricted** and **force pushes are blocked** on both
  branches.
- **Merge methods**: merge commits, squash, and rebase are all allowed,
  because the workflow needs both — squash for `feature/*` → `develop`, and a
  real merge commit for `release/*` and `hotfix/*` → `main`.

Deliberately **not** enabled, and why:

- `Require linear history` — release and hotfix branches merge into `main`
  with `--no-ff`, which linear history would forbid.
- `Require approval of the most recent reviewable push` — it demands that
  someone *other than the pusher* approves. While there is a single code
  owner that locks the owner out of their own pull requests. Turn it on once
  there are at least two code owners.
- `Require status checks to pass` — there is no CI yet; add the build/test
  jobs as required checks when WP0.10 lands.

Because GitHub does not let anyone approve their own pull request, the
repository admin is on the ruleset's **bypass list (for pull requests only)**.
That keeps the sole maintainer able to merge their own work, while every
contributor's pull request still needs a code owner's approval.

### Who can approve

Approval rights are not a repository role — they are the combination of:

1. **Write access** to the repository (Settings → Collaborators). Approvals
   from users without write access are not counted by the ruleset.
2. **Being listed in [`.github/CODEOWNERS`](.github/CODEOWNERS)** for the
   paths the pull request touches.

To grant approval rights, add the login to `CODEOWNERS` in a pull request; to
revoke them, remove the line. Rights can be scoped to part of the tree — for
example `/docs/ @mrYush @some-writer` lets that person approve only pull
requests limited to `docs/`. The last matching line in the file wins.

Since `CODEOWNERS` lives in the repository, changing who may approve is
itself a reviewed, auditable pull request. Note that GitHub reads the file
from the pull request's **base branch**, so a change to it takes effect only
after it is merged into `develop`/`main`.

## Making changes

1. Fork the repository (external contributors) or create a branch off
   `develop` as described above. Pull requests target `develop` (only
   `release/*` and `hotfix/*` PRs target `main`).
2. Keep PRs focused: one work package, one fix, or one document per PR where
   practical.
3. Write tests with the change, not after it. Phase 0 targets ≥ 70% coverage
   on the agent loop, tools, and permission logic, and golden tests guard the
   session wire format — a PR that lowers these gates needs a stated reason.
4. Run `go build ./...`, `go test ./...`, and `go vet ./...` before pushing
   (CI will run the same plus lint once WP0.10 lands).
5. Open a pull request describing **what** changed and **why**; link the
   issue/work package it implements. Tick the corresponding checkboxes in the
   phase file in the same PR.

Commit messages: imperative mood, a subject line under ~72 characters, and a
body explaining the reasoning when it isn't obvious. English only.

## Code style

- `gofmt` (enforced) and idiomatic Go; prefer the standard library over new
  dependencies — every new module in `go.mod` needs a justification in the PR.
- Errors are wrapped with context (`fmt.Errorf("…: %w", err)`), never
  swallowed.
- **Never log secrets.** API keys and tokens must pass through the masking
  helper before reaching any log or debug output.
- User-facing strings, comments, and docs are in English.

## Security-sensitive areas

Two rules from the architecture apply to every contribution:

- **External input is untrusted.** Output of tools, MCP servers, and files
  read from disk must never be treated as instructions to the agent or
  interpolated into privileged operations.
- **The permission system is not optional.** Any new tool must declare an
  action class (`read` / `write` / `execute`) and go through the permission
  layer; file tools stay confined to the working directory by default.

If you believe you've found a security vulnerability, please do **not** open
a public issue — contact the maintainer privately (see the repository owner's
GitHub profile) and allow time for a fix.

## Borrowed code and licensing

The plan deliberately studies other agents (opencode, Crush, Claude Code,
Aider, Pi, …). The rule, from [docs/plan/risks.md](docs/plan/risks.md) R2:

- Code may be borrowed **only** from MIT/Apache-licensed projects, with
  attribution in the commit message.
- FSL and proprietary projects (e.g. Crush, Claude Code) are pattern
  references only — no copied code.

`hint` itself is [MIT-licensed](LICENSE); by contributing you agree your
contributions are provided under the same license.

## Questions

Open a GitHub issue (label `question`), or comment on the relevant
`[Phase N][WPx.y]` issue if it concerns planned work.
