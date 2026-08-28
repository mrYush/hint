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

The project uses a **trunk-based** model, not classic GitFlow — there is no
`develop` or long-lived release branch:

- **`main` is the trunk** and must always build and pass tests. Nothing is
  committed to `main` directly; every change lands through a pull request.
- **Short-lived branches off `main`**: `feature/<short-name>`,
  `fix/<short-name>`, or `docs/<short-name>`. Rebase or merge `main` into
  your branch to stay current — but never rewrite history of a branch someone
  else may have checked out (no force-push after review has started; add
  commits instead).
- **Merging**: PRs are squash-merged by default, so a branch becomes one
  coherent commit on `main`; keep the PR title in the commit-subject style
  described below. A regular merge is acceptable for a branch whose
  individual commits are meaningful on their own (e.g. a multi-WP phase
  branch).
- **Releases are tags, not branches**: annotated tags `vX.Y[.Z]` (with
  pre-releases like `v0.1-alpha`) are cut from `main` and drive goreleaser
  (WP0.10). The version plan per phase is in [PLAN.md](PLAN.md).
- **Hotfixes** follow the same flow: `fix/…` branch off `main`, PR, then a
  patch tag. If a fix must land on top of an older release, branch from the
  tag (`release/vX.Y`) — this is the only case where a release branch exists,
  and it is deleted after the patch tag is cut.

## Making changes

1. Fork the repository (external contributors) or create a branch off `main`
   as described above.
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
