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

- **Go ≥ 1.26** (raised from 1.22 by WP1.4: `charm.land/bubbletea/v2`
  declares it; `go.mod` is the authority)
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

Today's code is small (`cmd/hint`, `internal/config`, `internal/provider`,
`internal/agent`, `internal/tool`, `internal/permission`, `internal/diff`,
`internal/session`, `internal/console`, `internal/project`,
`internal/debuglog`, `internal/rpc`). `cmd/hint` is a client of
`internal/rpc`: only the engine wiring (the `rpc*.go` files) may import the
agent loop, and a test enforces it. The target layout is
described in
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
- GitHub Issues mirror the work packages of the *active* phase, titled
  `[Phase N][WPx.y] …`. Comment on the relevant issue before starting
  sizeable work so effort isn't duplicated.
- Scope changes land in the phase file in the same PR as the code (or as a
  docs-only decision PR first). An agreement that exists only in a comment
  thread does not exist.
- Every work package names its branch (`**Branch:** ...` under the `### WPx.y`
  heading) when it is written down, before work starts; the package's status
  is read from that branch (absent / exists / merged into `develop`), see the
  status legend in [docs/plan/README.md](docs/plan/README.md).

### Opening an issue

Start every task with an issue, before the branch exists. Use the `Work
package` issue template for a planned task; a bug or a one-off change uses a
plain title. A work-package issue is titled `[Phase N][WPx.y] <imperative
title>` and its body starts with the branch it will use:

    **Branch:** `dev/wpx.y-<short-name>`

For a work package, the issue, the phase-file heading, the branch, and the
pull request carry the same name. A work-package branch that appears in
neither the issue nor the phase file is not ready to start.

Every issue also carries:

- **Scope** — what changes and what is explicitly out of scope, with a link
  to the phase file or the roadmap;
- **Done when** — acceptance criteria as checkboxes;
- **Links** — related issues and the phase file.

The `**Branch:**` line goes under the `### WPx.y` heading in the phase file
in the same change that creates the work package, as described in
[docs/plan/README.md](docs/plan/README.md).

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
  `dev/<short-name>`, `fix/<short-name>`, or `docs/<short-name>`
  (`dev/` and `feature/` are interchangeable; `dev/wp0.1-agentapi` naming a
  work package is the common case); short-lived, merged back into
  `develop` via PR (squash-merge by default, so a branch becomes one coherent
  commit; keep the PR title in the commit-subject style described below).
  Rebase or merge `develop` into your branch to stay current — but never
  rewrite history of a branch someone else may have checked out (no
  force-push after review has started; add commits instead).
- **Releases**: a `release/vX.Y` branch is cut from `develop` when the
  version's scope is complete. Only stabilization fixes and release chores
  land on it. It is then merged into `main` with a regular merge (`--no-ff`),
  the merge commit on `main` is tagged with an annotated `vX.Y.Z` tag
  (pre-releases like `v0.2.0-alpha` included), and the release branch is
  **merged back into `develop`** so stabilization fixes aren't lost, then
  deleted. The version plan per phase is in [PLAN.md](PLAN.md).
- **Release pipeline**: pushing the tag runs
  [`release.yml`](.github/workflows/release.yml). goreleaser
  ([`.goreleaser.yaml`](.goreleaser.yaml)) builds `{linux,darwin,windows}`
  × `{amd64,arm64}`, attaches the archives and `checksums.txt` to a GitHub
  Release (marked pre-release when the tag has a suffix such as `-alpha`),
  and for a final version pushes the Homebrew cask to
  [`mrYush/homebrew-tap`](https://github.com/mrYush/homebrew-tap). That
  push needs the `HOMEBREW_TAP_TOKEN` repository secret: a token with write
  access to the tap repository. Without it a final release fails at the
  cask step after the GitHub Release is already published; pre-releases
  skip the step. The Scoop manifest goes to
  [`mrYush/scoop-bucket`](https://github.com/mrYush/scoop-bucket) the same
  way, with the `SCOOP_BUCKET_TOKEN` secret. The same goreleaser run builds a `.deb` per Linux
  architecture (nFPM), and for a final version
  [`scripts/apt-repo.sh`](scripts/apt-repo.sh) turns them into a signed apt
  repository that the workflow force-pushes as one orphan commit to the
  `gh-pages` branch, served by GitHub Pages at
  `https://mryush.github.io/hint`. It needs the `APT_SIGNING_KEY`
  repository secret (ASCII-armored private key; `APT_SIGNING_PASSPHRASE` if
  the key has one). `gh-pages` is generated output: never edit it by hand.
  The apt step also runs after the GitHub Release is published, so a
  missing key fails it late. `goreleaser check` validates the config and
  `goreleaser release --snapshot --clean` builds everything into `dist/`
  without publishing.
- **Hotfixes**: `hotfix/<short-name>` branches off `main`, is merged into
  **both `main` (then patch-tagged) and `develop`**, and deleted.

### Branch names

| Prefix | Use | Example | Target |
|---|---|---|---|
| `dev/` | a work package | `dev/wp2.1-mcp-client` | `develop` |
| `feature/` | a feature outside a work package | `feature/openapi-tools` | `develop` |
| `fix/` | a bug fix | `fix/torn-session-tail` | `develop` |
| `docs/` | documentation only | `docs/v0.3.1-status` | `develop` |
| `release/` | a release stabilization | `release/v0.3.1` | `main` |
| `hotfix/` | a patch cut from `main` | `hotfix/tap-refresh` | `main` and `develop` |

The name is lowercase ASCII with hyphens, is short, and names the issue it
implements; `dev/` and `feature/` are interchangeable, but a work package
uses `dev/wpx.y-<short-name>`. One issue gets one branch; do not share a
branch between issues. Work branches start at `develop`; `release/` and
`hotfix/` follow the release and hotfix rules above. Delete the branch after
it is merged.

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
- `Require status checks to pass` — the CI jobs (`Lint`, `Test` and the six
  `Build <os>/<arch>` jobs of [`ci.yml`](.github/workflows/ci.yml)) should be
  added as required checks; that is a repository setting the admin flips
  after their first run on `develop`.

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
   practical. Scope that grew inside a branch follows the exception in
   [docs/plan/README.md](docs/plan/README.md) ("Branch per work package").
3. Write tests with the change, not after it. Phase 0 targets ≥ 70% coverage
   on the agent loop, tools, and permission logic, and golden tests guard the
   session wire format — a PR that lowers these gates needs a stated reason.
4. Run `go build ./...`, `go test ./...`, and `go vet ./...` before pushing.
   CI ([`ci.yml`](.github/workflows/ci.yml)) runs the same with the race
   detector, plus `gofmt -l`, `golangci-lint` with the repository's
   [`.golangci.yml`](.golangci.yml), and a cross-compilation of every
   release target. To see the lint gate locally install the `golangci-lint`
   version that `ci.yml` pins (the `version:` of the lint step; it is
   pinned only there) and run `golangci-lint run ./...`.
5. Open a pull request describing **what** changed and **why**; link the
   issue/work package it implements. Tick the corresponding checkboxes in the
   phase file in the same PR.

### Commits

- Subject: imperative English, under ~72 characters, no trailing period.
- Body: why the change is needed, wrapped around 72 columns; reference the
  issue with `Refs #123` while the work is partial. Do not rely on a commit
  trailer to close an issue — the pull-request body does that.
- One concern per commit. The branch may carry several, but the squash merge
  into `develop` makes the pull-request title the final subject, so write the
  title as a commit subject.
- After review starts, add commits; do not rewrite the branch.

### Pull requests

- Title: imperative English, under ~72 characters; it becomes the squash
  commit on `develop`.
- Body: **What**, **Why**, and a line `Closes #123` on its own line. GitHub
  closes the issue automatically when the PR merges into the default branch
  (`develop`). Use `Refs #123` instead when the PR does not complete the
  issue; do not close the issue by hand.
- If the PR completes more than one issue, put one `Closes #N` per line.
- Add a scope note when the branch carries work beyond its work package, per
  the exception in [docs/plan/README.md](docs/plan/README.md). Tick the
  phase-file checkboxes in the same PR.
- A PR that targets `main` is a `release/*` or `hotfix/*` PR; it does not use
  `Closes` for feature issues. A release closes its own issue, if it has one.

### Review comments

- Answer every review comment with a change or a reason. Do not resolve a
  conversation silently.
- Push follow-up commits; do not force-push after review starts.
- Request re-review after addressing the comments; branch protection dismisses
  an approval when new commits are pushed.

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
a public issue — follow [SECURITY.md](SECURITY.md), which also states what
each run mode hands over to the agent.

## Licensing

`hint` is [MIT-licensed](LICENSE); by contributing you agree your
contributions are your own work, provided under the same license. Do not
paste code from other projects into the tree.

## Questions

Open a GitHub issue (label `question`), or comment on the relevant
`[Phase N][WPx.y]` issue if it concerns planned work.
