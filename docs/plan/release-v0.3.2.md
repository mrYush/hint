# Release v0.3.2 — runbook

> Status: in progress · Created: 2026-11-02 · Target: `v0.3.2`
> Scope: [`PLAN.md` §20](../../PLAN.md#L286-L293), B1/B2 in
> [WP0.4](phase-0-mvp-cli.md#wp04--agent-loop--done), and S20 in
> [WP0.5](phase-0-mvp-cli.md#wp05--built-in-tools--done),
> [WP0.7](phase-0-mvp-cli.md#wp07--sessions--done) and
> [WP0.8](phase-0-mvp-cli.md#wp08--project-context--done). This is an
> execution checklist, not a second scope document; `PLAN.md` and the
> phase file win if they disagree.

`v0.3.2` is a patch of `v0.3.1`: the agent-loop fixes of
[#32](https://github.com/mrYush/hint/issues/32) and the UTF-8 truncation
of [#41](https://github.com/mrYush/hint/issues/41). Phase 2 is explicitly
out of scope.

## Current state (2026-11-02)

- ✅ `develop` is at `075cce6`, the merge of PR
      [#83](https://github.com/mrYush/hint/pull/83); `release/v0.3.2` is at
      `f4f3b6d` with the release documentation. PR
      [#82](https://github.com/mrYush/hint/pull/82) is merged too; CI on
      `075cce6` is green.
- ✅ The release branch carries #32 and #41 and nothing from Phase 2.
- ✅ README Known issues and Limits, `PLAN.md` step 20 and the Phase 0
      status line are updated for `v0.3.2`.
- ✅ Commit and push the first release documentation commit on
      `release/v0.3.2` (`f4f3b6d`).
- [ ] Commit and push the docs audit: `AGENTS.md`, `CONTRIBUTING.md`,
      `docs/plan/README.md`, `after-v0.3.1.md`, `release-v0.3.1.md`, the
      final `PLAN.md` status and the Phase 0 closing note.
- [ ] Open the release PR `release/v0.3.2` → `main`.
- [ ] Tag `v0.3.2`.
- The status documents already carry the post-tag state and name
  `v0.3.2`; the tag and the published artifacts are the only remaining
  steps. If the tag is aborted, revert those status lines.

## 0. Pre-flight

### Repository

- ✅ `release/v0.3.2` is checked out and contains the latest `develop`
      (`075cce6`).
- ✅ CI on `075cce6` is green: `Lint`, `Test`, all six build jobs and
      GitGuardian.
- ✅ `go test ./...`, `go vet ./...` and `gofmt -l .` pass locally.
- ✅ `goreleaser check` passes.
- ✅ `goreleaser release --snapshot --clean` passes. It used a temporary
      `GOCACHE` because the default Go build cache is not writable in the
      local sandbox. A snapshot takes its version from the last tag:
      `0.3.1-SNAPSHOT-075cce6`; that is expected.

### Secrets

- ✅ `HOMEBREW_TAP_TOKEN` is set and `hint-homebrew-tap` expires
      **2027-10-28** ([#71](https://github.com/mrYush/hint/issues/71)).
- ✅ `SCOOP_BUCKET_TOKEN` is set with Contents: Read and write on
      `mrYush/scoop-bucket`.
- ✅ `APT_SIGNING_KEY` and `APT_SIGNING_PASSPHRASE` are set; `v0.3.1`
      used them.
- ✅ `gh secret list --repo mrYush/hint` shows all four.

### Snapshot artifacts

- ✅ Six archives: darwin and linux as `.tar.gz`, windows as `.zip`, each
      amd64 and arm64.
- ✅ Two `.deb` packages, linux amd64 and arm64.
- ✅ `dist/homebrew/Casks/hint.rb` has `postflight_steps` with the
      `xattr` step inside `on_macos`, no old `postflight` block.
- ✅ `dist/scoop/hint.json` names `64bit` and `arm64`.

## 1. Release branch

- Commit the README, `PLAN.md`, Phase 0 and this runbook changes on
  `release/v0.3.2`. Commit subject imperative English; only release
  chores, no code changes.
- Push `release/v0.3.2`.
- Open a PR `release/v0.3.2` → `main` with a **merge commit** as the merge
  method. The body says what the patch contains and carries:

  ```
  Closes #32
  Closes #41
  ```

  Because the PR targets the default branch `main`, these lines close the
  issues when the release PR merges. If GitHub still leaves one open,
  close it with a comment linking this runbook and the merge commit.

## 2. Dry run before the merge

Already executed on `075cce6`; repeat only if a code commit lands after it.

- [ ] `goreleaser check`
- [ ] `goreleaser release --snapshot --clean`
- [ ] Inspect `dist/`: six archives, two `.deb` packages,
      `checksums.txt`, the cask and the Scoop manifest.
- [ ] `dist/homebrew/Casks/hint.rb` has the macOS `postflight_steps`.
- [ ] `dist/scoop/hint.json` has both architectures.

## 3. Merge and tag

- [ ] Merge PR `release/v0.3.2` → `main` with **Create a merge commit**,
      not squash and not rebase.
- [ ] On the merge commit on `main`:
      `git tag -a v0.3.2 -m "hint v0.3.2"`.
- [ ] `git push origin main` if the merge is not already there.
- [ ] `git push origin v0.3.2` — this triggers `release.yml`.
- [ ] Watch the workflow: goreleaser, cask, Scoop, apt.
- If cask, Scoop or apt fails after the GitHub Release is published:
  fix the secret and re-run the workflow on the same tag.
  `replace_existing_artifacts: true` allows asset replacement. Do not
  move or delete a published tag; cut a new patch instead.

## 4. Verify the published release

- [ ] GitHub Release `v0.3.2`: six archives, two `.deb` packages,
      `checksums.txt`; the changelog has the #32 and #41 commits and no
      merge commits.
- [ ] Homebrew tap: `Casks/hint.rb` is `0.3.2` and keeps
      `postflight_steps`.
- [ ] Scoop bucket: `hint.json` is `0.3.2` and names both architectures.
- [ ] apt: the `stable` index names `0.3.2`; on a clean Debian/Ubuntu,
      `apt update && apt install hint` succeeds and `hint -v` prints
      `0.3.2`.
- [ ] macOS: `brew install --cask mrYush/tap/hint`, then `hint -v`.
- [ ] Windows: `scoop install hint`, or validate the published
      `hint.json` when no Windows machine is available.
- [ ] `go install github.com/mrYush/hint/cmd/hint@v0.3.2` and `hint -v`.
- [ ] An archive from the Release: `hint --version` prints `0.3.2`.

## 5. Close and merge back

- [ ] #32 and #41 are closed by the release PR; if not, close them
      manually with a comment linking the release.
- [ ] Confirm `PLAN.md` already says step 20 is done, `v0.3.2` is the
      latest release and the next item is Phase 2 / `v0.4`. If the tag is
      aborted, revert those status lines before merging back.
- [ ] Merge `release/v0.3.2` back into `develop` and delete the branch.

The project-status documents are already final on this branch: README,
`PLAN.md`, the Phase 0 header and the B1/B2/S20 lines. Only the execution
checkboxes above remain; there is no separate post-release docs commit.

## Exit criteria

`v0.3.2` is released when:

1. the annotated tag is on `main` and the release workflow is green;
2. the GitHub Release has all six archives, both `.deb` packages and
   `checksums.txt`;
3. the cask, the Scoop manifest and the apt index all name `0.3.2` and
   install;
4. `hint -v` reports `0.3.2` from at least one archive and one package
   manager;
5. #32 and #41 are closed;
6. `release/v0.3.2` is merged back into `develop`;
7. `PLAN.md` names `v0.3.2` as the latest release.
