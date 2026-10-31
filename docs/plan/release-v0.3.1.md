# Release v0.3.1 — runbook

> Status: in progress · Created: 2026-10-31 · Target: `v0.3.1`
> Scope and release status: [`PLAN.md` §19](../../PLAN.md#L276) and
> [`WP0.14`](phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows).
> This file is an execution checklist, not a second scope document. If it
> disagrees with `PLAN.md` or the phase file, those win. Mark each item as
> it is executed; the closing change in §6 updates the remaining boxes.

`v0.3.1` is a patch of `v0.3`: the WP0.14 index leftovers, the release
leftovers of [#50](https://github.com/mrYush/hint/issues/50), the gateway
message fix of [#57](https://github.com/mrYush/hint/issues/57), and the
status review of `PLAN.md`. The UTF-8 truncation of
[#41](https://github.com/mrYush/hint/issues/41) and Phase 2 are explicitly
out of scope.

## Current state (2026-10-31)

- ✅ `develop` at `92438a0`; PR [#74](https://github.com/mrYush/hint/pull/74)
      merged; CI green on the merge commit.
- ✅ Gateway message fix in the tree (`internal/provider/openai/status.go`);
      the README note says "fixed in `v0.3.1`".
- ✅ `retract v0.1.0` in `go.mod` and `SECURITY.md` (the first two items of
      [#50](https://github.com/mrYush/hint/issues/50)).
- ✅ `scoops` and the cask `hooks.post.install_steps` in
      [`.goreleaser.yaml`](../../.goreleaser.yaml).
- ✅ `v0.3.1` status review in `PLAN.md` and `docs/plan/phase-0-mvp-cli.md`.
- ✅ [#57](https://github.com/mrYush/hint/issues/57) closed on 2026-10-31;
      the fix is in `develop` and ships in `v0.3.1`.
- [ ] `main` is at `86cd1ab` with no `v0.3.1` tag; this branch is cut from
      `develop` at `92438a0`, and §4 cuts the tag on `main`.

## 0. Pre-flight

### Repository

- ✅ `git checkout develop && git pull --ff-only`; `git status` is clean.
- ✅ CI on `develop` is green: `Lint`, `Test`, and all six build jobs.
- ✅ `go test ./...` and `go vet ./...` pass locally (or CI is the only gate when no local Go toolchain is installed).
- ✅ `gofmt -l .` prints nothing.
- ✅ `goreleaser check` passes on the release commit (see §3 for the dry run).

### Secrets

- ✅ `HOMEBREW_TAP_TOKEN` is renewed before the tag. The fine-grained token `hint-homebrew-tap` expires **2027-10-28** ([#71](https://github.com/mrYush/hint/issues/71)).
  - ✅ Create a fine-grained token `hint-homebrew-tap`, expiration one year, repository access only `mrYush/homebrew-tap`, Contents:
        Read and write.
  - ✅ `gh secret set HOMEBREW_TAP_TOKEN --repo mrYush/hint` (stdin).
  - ✅ Delete the old `pwriter-cursor` token.
  - ✅ Record the new expiry date in #71, then close it.
- ✅ `SCOOP_BUCKET_TOKEN` exists and has Contents: Read and write on `mrYush/scoop-bucket`. Without it the release fails at the Scoop step after the GitHub Release is published.
- ✅ `APT_SIGNING_KEY` and `APT_SIGNING_PASSPHRASE` are still set; `v0.3.0`
      used them. A missing key fails the apt step late.
- ✅ `gh secret list --repo mrYush/hint` shows all four secrets (it shows
      names and update dates, not expiry or scope).

### External repositories and GitHub

- ✅ Remove the root formula `hint.rb` (`0.1.0`) from
      `mrYush/homebrew-tap`; keep `Casks/hint.rb`. Without the removal,
      `brew install` without `--cask` keeps installing the old application.
- ✅ Edit the `v0.1.0` GitHub Release: state that it is the earlier,
      unrelated application, superseded by `v0.2.0`, and retracted in
      `go.mod`; keep the tag and the Release. This is the last item of
      [#50](https://github.com/mrYush/hint/issues/50).
- ✅ Confirm the default branch and layout of `mrYush/scoop-bucket`; a
      manifest at the repository root is valid (Scoop falls back to the root
      when no `bucket/` directory exists).

## 1. Documentation before the tag

- ✅ `README.md` → Known issues: add `v0.3.1` to the list of releases that
      still ship the five open bugs. The fixed gateway item stays a separate
      "fixed in `v0.3.1`" note, not a numbered open item.
- ✅ `README.md` → Limits: say `v0.3.1` (unchanged from `v0.3.0`).
- ✅ `PLAN.md` → Current status: the Scoop config and the cask
      `install_steps` are in the tree; the tag confirms them. The root
      formula removal is a manual tap action.
- ✅ `PLAN.md` §19 links this runbook.
- ✅ Mark the release as in progress in `PLAN.md` when the release branch is
      cut.
- [ ] Keep the phase file checkboxes open until the release artifacts are
      verified; tick them in the post-release docs change (§6).

## 2. Release branch

GitFlow: a release branch is cut from `develop`, stabilized, merged into
`main` with a regular merge, tagged, and merged back.

- [ ] `git checkout develop && git pull --ff-only`
- ✅ `git checkout -b release/v0.3.1`
- ✅ Only documentation and release chores on this branch; no feature code.
- [ ] Commit with an imperative English subject; push the branch.
- [ ] Open a PR `release/v0.3.1` → `main`; wait for CI.
- [ ] Merge with **Create a merge commit** (not squash, not rebase).

## 3. Dry run before the tag

This is the first release that uses the cask `install_steps` and the
`scoops` block, so validate the generated files before publishing.

- [ ] Use goreleaser v2.19 or newer (`install_steps` is newer than the
      Ruby `install` hook).
- [ ] `goreleaser check`
- [ ] `goreleaser release --snapshot --clean`
- [ ] Inspect `dist/homebrew/Casks/hint.rb`: version `0.3.1`,
      `postflight_steps`, no old `postflight` block.
- [ ] Inspect `dist/scoop/hint.json`: version `0.3.1`, `64bit` and `arm64`
      entries.
- [ ] Inspect `dist/*.deb` (amd64 and arm64) and the six archives.

## 4. Tag and publish

- [ ] `git checkout main && git pull --ff-only`
- [ ] `git tag -a v0.3.1 -m "hint v0.3.1"` on the release merge commit.
- [ ] `git push origin main` if the merge is not already on the remote.
- [ ] `git push origin v0.3.1` — this triggers `release.yml`.
- [ ] Watch the workflow: goreleaser, cask, Scoop, apt.
- [ ] If cask, Scoop, or apt fails after the GitHub Release is published:
      fix the secret and re-run the workflow on the same tag.
      `replace_existing_artifacts: true` allows the assets to be replaced.
      Do not move or delete a published tag; cut a new patch instead.

## 5. Verify the published release

- [ ] GitHub Release `v0.3.1`: six archives, two `.deb` packages,
      `checksums.txt`; the changelog has the gateway and console fixes and
      no merge commits.
- [ ] Homebrew tap: `Casks/hint.rb` is `0.3.1` with `postflight_steps`; the
      root `hint.rb` is gone.
- [ ] Scoop bucket: `hint.json` is `0.3.1` and names both architectures.
- [ ] apt: the `stable` index names `0.3.1`; on a clean Debian/Ubuntu,
      `apt update && apt install hint` succeeds and `hint -v` prints
      `0.3.1`.
- [ ] macOS: `brew install --cask mrYush/tap/hint`, then `hint -v`.
- [ ] Windows: `scoop install hint` (or validate the generated manifest
      when no Windows machine is available).
- [ ] `go install github.com/mrYush/hint/cmd/hint@v0.3.1` and `hint -v`.
- [ ] An archive from the Release: `hint --version` prints `0.3.1`.

## 6. Close and merge back

- [ ] Close [#50](https://github.com/mrYush/hint/issues/50) with the release
      and the verification; [#57](https://github.com/mrYush/hint/issues/57)
      is already closed (2026-10-31), and #71 was closed when the token was
      renewed in §0.
- [ ] Tick the WP0.14 boxes in
      [`docs/plan/phase-0-mvp-cli.md`](phase-0-mvp-cli.md#wp014--install-indexes-for-debian-and-windows).
- [ ] Update `PLAN.md`: `v0.3.1` is the latest release, §19 is done, and the
      next item is Phase 2 / `v0.4`.
- [ ] Merge `release/v0.3.1` back into `develop` and delete the branch.
- [ ] Post-release docs change: any status text that could only be final
      after the tag, plus the remaining runbook checkboxes. The README
      known-issues header and limits were done in §1.

## 7. After v0.3.1

- [ ] `v0.3.2`: fix [#32](https://github.com/mrYush/hint/issues/32) — the
      compaction summary must survive `refreshPrefix`, and the usage
      baseline must be reset after a reactive compaction. Add the missing
      tests.
- [ ] Include or follow with
      [#41](https://github.com/mrYush/hint/issues/41): one UTF-8-safe
      truncation helper at every byte-slicing site.
- [ ] Then Phase 2 / `v0.4`: close the Q6 spike and start WP2.1, the MCP
      client.

## Exit criteria

`v0.3.1` is released when:

1. the annotated tag is on `main` and the release workflow is green;
2. the GitHub Release has all assets;
3. the cask, the Scoop manifest, and the apt index all name `0.3.1` and
   install;
4. `hint -v` reports `0.3.1` from at least one archive and one package
   manager;
5. #50, #57, #71, and the WP0.14 boxes are closed;
6. `release/v0.3.1` is merged back into `develop`;
7. `PLAN.md` names `v0.3.1` as the latest release.
