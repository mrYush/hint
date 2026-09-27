# Raspberry Pi smoke test

> Status: **deferred past `v0.2.0-alpha`** (decided 2026-09-14) · Still required
> for `v0.2.0` and for ticking [WP0.10](phase-0-mvp-cli.md#wp010--ci-release-distribution)'s
> Pi 5 checkbox ([PLAN.md](../../PLAN.md#current-status))

## Goal

CI (WP0.10) only *compiles* `linux/arm64` — it never runs on it, since a
native ARM runner costs 3-10x the minutes of the one `ubuntu` job that
actually executes tests. This is the manual run that exercises a real
`linux/arm64` binary: the five
[acceptance criteria](phase-0-mvp-cli.md#acceptance-criteria) of Phase 0,
executed on a **Raspberry Pi 5**.

Pass/fail on every scenario below gates ticking WP0.10's Raspberry Pi
checkbox and cutting **`v0.2.0`**. It does **not** block `v0.2.0-alpha`.

For running `hint`'s offline fallback on a **Pi 4 4 GB** (different hardware
budget, different local stack), see
[`docs/raspberry-pi-4-4gb.md`](../raspberry-pi-4-4gb.md). That guide does
not close this checklist.

## Prerequisites

- A Raspberry Pi 5 running 64-bit Raspberry Pi OS (or another `linux/arm64`
  distro), reachable over SSH.
- An OpenAI-compatible cloud profile configured (default: api-bar.ru — see
  [WP0.2](phase-0-mvp-cli.md#wp02--config-profiles-and-sources--done)) for
  scenarios 1, 2, 4.
- [Ollama](https://ollama.com) installed and running on the Pi, with a small
  model pulled (e.g. `qwen2.5:7b`, matching the default `fallback_provider`
  in the example config), for scenario 3.
- Run once **with** ripgrep on `PATH` and once **without** it, since
  `glob`/`grep` (WP0.5) take different code paths either way — pick one full
  pass for the primary run and spot-check the other on scenario 1.

## 1. Build the release archive

From a checkout of `develop` at the commit being smoke-tested, on any
machine (cross-compilation is CGO-free, see
[docs/plan/risks.md](risks.md), R1):

```bash
cd hint
go install github.com/goreleaser/goreleaser/v2@latest   # once
export PATH="$(go env GOPATH)/bin:$PATH"               # go install lands here
goreleaser release --snapshot --clean
```

`--snapshot` builds every release target into `dist/` without publishing or
requiring a tag. Take the one archive needed:

```
dist/hint_<version>_linux_arm64.tar.gz
```

(Faster alternative for just the binary, no archive:
`GOOS=linux GOARCH=arm64 goreleaser build --single-target --snapshot --clean`.)

- [ ] Archive built, version noted: `___________`

## 2. Transfer and unpack on the Pi

```bash
scp dist/hint_*_linux_arm64.tar.gz pi@<host>:~/
ssh pi@<host>
tar xzf hint_*_linux_arm64.tar.gz
# Snapshot archives may unpack flat (binary + LICENSE + README in $PWD)
# or into a directory named like the archive stem — check both:
ls -la hint hint_*_linux_arm64 2>/dev/null || true
HINT="$HOME/hint"   # or $HOME/hint_*_linux_arm64/hint
uname -m            # expect aarch64
$HINT --version     # confirm it starts, prints version/commit/date
```

- [ ] Binary starts on `aarch64` and `--version` reports the expected commit

## 3. Configure a profile

Either export env vars or drop a project/global config
(see [WP0.2](phase-0-mvp-cli.md#wp02--config-profiles-and-sources--done)):

```bash
export API_BAR_KEY=...        # or OPENAI_API_KEY for the zero-config path
```

Verify both profiles resolve before running scenarios:

```bash
$HINT models                         # cloud profile: lists models
HINT_PROVIDER=local $HINT models     # ollama / local profile by name
```

- [ ] Both profiles (default + fallback) list models successfully

## 4. Acceptance scenarios

Run each from an empty scratch directory (`mkdir smoke && cd smoke`) unless
noted, so `.hint/`, sessions and `HINT.md` discovery start clean. Keep
`$HINT` as an absolute path to the binary.

### Scenario 1 — self-directed exploration (no manual context assembly)

```bash
$HINT -p "what files are in this project and what do they do"
```
Pass: the agent calls `list_dir`/`read_file` itself (visible on stderr) and
answers from that, without being handed file contents.

- [ ] Pass — notes: ___________

### Scenario 2 — edit with confirmation

```bash
$HINT "add --version flag handling to main.go"
```
Pass: the agent shows a diff (WP0.6 preview) and asks for confirmation
before `edit_file`/`write_file` runs.

- [ ] Pass — notes: ___________

### Scenario 3 — offline fallback

Disconnect the Pi from the internet (or block the cloud profile's host) with
Ollama still running locally, then repeat scenario 1 or 2 unchanged:

```bash
$HINT -p "what files are in this project and what do they do"
```
Pass: the router (WP0.3) fails over to the `fallback_provider` with a
stderr notice, no flag or command change needed.

- [ ] Pass — notes: ___________

### Scenario 4 — session continuity

```bash
$HINT -p "remember the number 42"
$HINT -c -p "what number did I ask you to remember?"
```
Pass: `-c` continues the prior session (WP0.7) and the second answer uses
the preserved context.

- [ ] Pass — notes: ___________

### Scenario 5 — this run, itself

Implicit in scenarios 1-4 once they pass on this binary: a
`GOOS=linux GOARCH=arm64` build runs the same scenarios as any other
platform, on a Raspberry Pi 5.

- [ ] Pass (i.e., scenarios 1-4 all passed on this Pi)

## Appendix — Raspberry Pi 4 (4 GB)

Not the Phase 0 / `v0.2.0` gate. Use
[`docs/raspberry-pi-4-4gb.md`](../raspberry-pi-4-4gb.md) for devices, use
cases, why `llama-server` instead of Ollama, and the launch recipe on 4 GB.

## 5. Record the result

After a green Pi 5 run (for `v0.2.0`, not for `v0.2.0-alpha`):

- [ ] Tick WP0.10's Raspberry Pi checkbox in
      [phase-0-mvp-cli.md](phase-0-mvp-cli.md#wp010--ci-release-distribution)
- [ ] Update the "Next" line in [PLAN.md](../../PLAN.md) past the Pi 5 run
- [ ] Tag `v0.2.0` on the release path in [CONTRIBUTING.md](../../CONTRIBUTING.md)
      (goreleaser's `prerelease: auto` / `skip_upload: auto` already apply to
      pre-releases such as `v0.2.0-alpha`; final tags need `HOMEBREW_TAP_TOKEN`)
- [ ] If a scenario failed: file it as a Phase 0 issue, do not tag `v0.2.0`
      until fixed and re-run
