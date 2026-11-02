# Raspberry Pi smoke test

> Status: **done** (accepted 2026-11-02). Did **not** gate `v0.2.0-alpha`,
> `v0.2.0`, or `v0.3.0`; those tags stand. The measured linux/arm64 log
> for `v0.2.0` remains the Pi 4 bench
> [`evidence/pi-4-20261011-105151/`](evidence/pi-4-20261011-105151/summary.md)
> ([PLAN.md](../../PLAN.md#current-status)). No separate Pi 5 run log is
> stored in this repository.

## Goal

CI (WP0.10) only *compiles* `linux/arm64` — it never runs on it, since a
native ARM runner costs 3-10x the minutes of the one `ubuntu` job that
actually executes tests. This is the manual run that exercises a real
`linux/arm64` binary: the five
[acceptance criteria](phase-0-mvp-cli.md#acceptance-criteria) of Phase 0,
executed on a **Raspberry Pi 5**.

Pass/fail on every scenario below was the original WP0.10 hardware gate.
As of 2026-10-11 it did **not** block `v0.2.0-alpha` or `v0.2.0`.
The smoke is accepted as done on 2026-11-02. The scenario boxes below
record that acceptance; they were not filled from a stored run log.

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

- ✅ Archive built, version noted: accepted 2026-11-02; version not recorded here

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

- ✅ Binary starts on `aarch64` and `--version` reports the expected commit — accepted 2026-11-02

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

- ✅ Both profiles (default + fallback) list models successfully — accepted 2026-11-02

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

- ✅ Pass — accepted 2026-11-02

### Scenario 2 — edit with confirmation

```bash
$HINT "add --version flag handling to main.go"
```
Pass: the agent shows a diff (WP0.6 preview) and asks for confirmation
before `edit_file`/`write_file` runs.

- ✅ Pass — accepted 2026-11-02

### Scenario 3 — offline fallback

Disconnect the Pi from the internet (or block the cloud profile's host) with
Ollama still running locally, then repeat scenario 1 or 2 unchanged:

```bash
$HINT -p "what files are in this project and what do they do"
```
Pass: the router (WP0.3) fails over to the `fallback_provider` with a
stderr notice, no flag or command change needed.

- ✅ Pass — accepted 2026-11-02

### Scenario 4 — session continuity

```bash
$HINT -p "remember the number 42"
$HINT -c -p "what number did I ask you to remember?"
```
Pass: `-c` continues the prior session (WP0.7) and the second answer uses
the preserved context.

- ✅ Pass — accepted 2026-11-02

### Scenario 5 — this run, itself

Implicit in scenarios 1-4 once they pass on this binary: a
`GOOS=linux GOARCH=arm64` build runs the same scenarios as any other
platform, on a Raspberry Pi 5.

- ✅ Pass (scenarios 1-4 accepted on this Pi, 2026-11-02)

## Appendix — Raspberry Pi 4 (4 GB)

The `v0.2.0` hardware record is the Pi 4 `pi-bench` run
`20261011-105151`
([summary](evidence/pi-4-20261011-105151/summary.md),
[runs.jsonl](evidence/pi-4-20261011-105151/runs.jsonl)).
The launch recipe for that board stays
[`docs/raspberry-pi-4-4gb.md`](../raspberry-pi-4-4gb.md).

## 5. Record the result

Accepted as done on 2026-11-02.

- ✅ Tick WP0.10's Raspberry Pi checkbox in
      [phase-0-mvp-cli.md](phase-0-mvp-cli.md#wp010--ci-release-distribution--done)
- ✅ PLAN.md no longer lists the Pi 5 smoke as backlog. **Next** was
      already Phase 2 (`v0.4`).
- ✅ ~~Tag `v0.2.0` on the release path in [CONTRIBUTING.md](../../CONTRIBUTING.md).~~
      Tagged 2026-10-11 without this run; `v0.3.0` followed on 2026-10-25.
- ✅ No failed scenario was filed. A tag already published is not reopened.
