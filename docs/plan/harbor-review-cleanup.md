# Harbor review cleanup — #115

**Branch:** `dev/harbor-review-cleanup`

Date: 2026-11-11.
Scope and status belong to [WP2R.3](phase-2r-review.md#complete-harbor-review-cleanup-115).
This record covers [#115](https://github.com/mrYush/hint/issues/115), following the adapter merged in [#108](https://github.com/mrYush/hint/pull/108).

## Implementation plan

1. Reconcile the issue's complete finding list against the merged adapter, its tests and the existing review dispositions.
   Record already fixed findings separately from changes required on this branch and intentional retained behavior.
2. Remove the unreachable outer `yaml.YAMLError` handler in `benchmark.py` because `read_route_config` already converts parser and schema errors into a sanitized `ValueError`.
   Preserve the inner handler and the tests that prevent credential disclosure.
3. Remove the optional branch around the credential overlay after the runner has either supplied a dry-run placeholder or required the selected live credential.
   Cover both paths with the actual adapter validation rather than mocking away authentication.
4. Keep shared route and auth validation in `HintAgent`, then release the temporary host-side agent after constructing the sanitized manifest and before starting Harbor.
   Verify its lifetime at the subprocess boundary with a non-secret test credential.
   This reduces unnecessary object lifetime; it does not claim memory erasure or change the environment-based authentication contract.
5. Remove the extra production parse of the ATIF JSON produced from the validated Harbor model.
   Keep journal validation, session matching, nonempty trajectories and all error accounting.
   Extend offline round-trip coverage to consecutive tool calls and observations, reasoning, compaction checkpoints, future journal kinds and unavailable usage.
6. Preserve reviewed behavior that already satisfies the contract, documenting the rationale below.
   Do not introduce broad renames, partial-dataset support, new schema restrictions or a second route-validation implementation.
7. Run Python unit tests, Ruff formatting/lint, Pyright and Sphinx with warnings treated as errors.
   Run Docker fake-provider success, authentication-failure and timeout scenarios on linux/amd64 using the pinned binaries.
   Run repository formatting, Go tests/race/vet, pinned golangci-lint and CGO-disabled linux/arm64 compilation.
8. Record the final results here and tick the matching WP2R.3 steps.
   A subsequent PR targets `develop` and carries `Closes #115`; after its merge, close the issue manually because the default branch is `main`.
   Commit, push, opening the PR, merge and issue closure are separate publication actions.

## Findings register

“Fixed in #108” means the change is already in `develop` as `c6e621a`.
“Fixed on this branch” describes the local implementation and does not imply publication or merge.
Grouped IDs share one disposition; the original review treated them as related complaints about the same boundary or readability choice.

| Finding | Disposition | Evidence or rationale |
| --- | --- | --- |
| F-06 | Fixed in #108 | `test_journal_tail_validation` and `test_journal_invalid_utf8` distinguish unfinished final JSON from complete invalid JSON, schema errors and interior corruption. |
| F-09 | Intentionally retained | The runner accepts the pinned 63-task export; task selection operates on that complete export and does not add partial-export support. `test_dataset_export_contract` covers empty, partial and oversized exports. |
| F-10 | Unsupported finding | An empty export fails the 63-task check before reaching `Task not found`; `test_dataset_export_contract` also verifies a missing named task in a complete export. |
| F-11 | Fixed in #108 | `test_summary_propagates_programming_errors` verifies that unexpected programming errors escape rather than becoming unreadable-result rows. |
| F-12 / D-06 | Fixed on this branch | Removed the unreachable outer YAML handler. `test_route_config_does_not_echo_inline_credentials` retains sanitized parser/schema errors at the configuration boundary. |
| T-02 / T-05 / T-08 / T-09 | Intentionally retained | Provider finish/error values and journal record/content kinds are external data that may grow; retain their strings and existing fallback/ignore behavior instead of narrowing every field to a closed enum. `test_future_provider_values` and `test_tool_trajectory_round_trip` cover these boundaries. The journal role remains restricted where conversion needs a known role. |
| T-06 | Intentionally retained | `BaseHTTPRequestHandler` dispatches `do_POST` dynamically; it does not declare that method for an `@override` annotation. |
| T-07 | Intentionally retained | The classmethod's inferred `cls` type preserves subclass typing; an explicit narrower annotation adds no guarantee. |
| T-10 | Intentionally retained | Existing test factories and cached-property substitution are confined to test fixtures. Replacing them does not fix production behavior and is not an acceptance gate. |
| A-06 | Intentionally retained | `RunArgs` gives `launch` a typed, independently testable input; matching the CLI fields is expected. |
| A-08 | Intentionally retained | Verify the binary immediately before execution, including the preinstalled-agent lifecycle; installation-time verification alone does not cover that boundary. |
| A-09 | Fixed on this branch | Removed the repeated production parse. `test_tool_trajectory_round_trip`, `test_partial_trace_and_unknown_usage` and session-selection tests validate the emitted JSON against Harbor's schema, including tool observations and unavailable metrics. The Docker smoke independently parses the saved trajectory. |
| D-03 | Intentionally retained | `select_binary` is a documented convenience entry point with architecture/checksum tests; its small size does not make it dead code. |
| D-05 | Fixed on this branch | Supply the required credential overlay directly after placeholder injection or live-key validation. `test_route_launch_releases_validation_agent` exercises both paths with the real adapter. |
| D-07 | Intentionally retained | The small `summarize` dispatch preserves the typed `agent` and `execution` result fields and per-artifact error accounting without a generic untyped abstraction. |
| N-01 / N-02 / N-03 / N-04 / N-05 / N-06 / N-07 / N-08 / N-09 | Intentionally retained | These readability suggestions do not identify a behavior failure. Preserve existing artifact names and public helpers; defer renames until the corresponding code needs a functional change. The adapter's `CancelledError` denotes a Hint result and is distinct from `asyncio.CancelledError`. |
| C-07 | Intentionally retained | Keep the Harbor job on every PR. Path filters are an optional optimization and are unnecessary for this small job; required-check behavior remains unchanged. |
| S-11 | Intentionally retained | The fake server defaults to loopback and uses a fake credential. Explicit bridge binding is needed for the documented Docker Engine workflow. |
| S-13 | Fixed on this branch | Reuse the existing auth validation, then release the temporary parent-side agent before subprocess execution. `test_route_launch_releases_validation_agent` verifies through a weak reference that the real validation agent is gone at the subprocess boundary in both live and dry-run paths. Environment auth remains necessary; no memory-erasure guarantee is implied. |
| S-14 | Intentionally retained | The operator selects the credential variable in trusted route YAML. A naming suffix would neither establish credential purpose nor preserve all valid configurations. |
| S-17 | Fixed in #108 | `test_digest_validation` covers exact 64-digit hexadecimal SHA-256 digests for both agent and verifier images. |
| Owner-requested removal of monetary budget declarations | Fixed in `3d86ab5` on this branch | Route YAML and new manifests no longer declare an unenforced amount or require a provider-budget confirmation. Trusted-environment confirmation is preserved. |

## Boundaries preserved

The runner keeps exact model IDs, explicit endpoints, native Hint tools/context behavior, independent attempts and zero automatic retries.
The verifier remains the source of rewards; process success does not invent a score.
Missing attempts, partial artifacts, exceptions and unavailable usage/cost remain explicit.
Credential handling is evaluated within the documented trusted-environment contract; isolation of untrusted tasks remains #111.
The real-model smoke in #107 and provider reasoning changes in #109 are separate acceptance work.
This cleanup does not claim either route compatibility or completion of #110–#114.

## Verification

Local verification completed on 2026-11-11.

| Check | Result |
| --- | --- |
| `python -m unittest discover -s tests -v` from `integrations/harbor` | All 29 tests passed, including the real validation-agent lifetime check for live/dry-run, multi-tool ATIF round-trip, future provider values and the pinned export contract. |
| `ruff format --check .` and `ruff check .` | Passed for all eight Python source files. |
| `pyright --pythonpath /private/tmp/hint-review-venv/bin/python` | Zero errors and warnings using the installed Harbor environment. |
| `sphinx-build -W -b html docs /private/tmp/hint-115-sphinx` | Passed with warnings treated as errors. |
| `gofmt -l .` | No unformatted Go files. |
| `go test ./...` and `go test -race ./...` | Passed for the whole repository. |
| `go vet ./...` | Passed. |
| `golangci-lint run ./...` | Passed with pinned v2.13.2; zero issues. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | Passed. |
| Docker fake-provider success on linux/amd64 | Four independent trials passed, each with reward 1.0 and no exceptions; the smoke checked tool calls, separate verification, session isolation, serialized ATIF, usage and credential redaction. |
| Docker fake-provider authentication failure on linux/amd64 | One trial produced the expected `AgentAuthenticationError`; artifact checks passed. |
| Docker fake-provider timeout on linux/amd64 | One trial produced the expected `AgentTimeoutError` and recorded cancellation; artifact checks passed. |

Python checks used the existing pinned Harbor environment at `/private/tmp/hint-review-venv` with this checkout on `PYTHONPATH`.
Docker checks used the documented smoke script with `DOCKER_DEFAULT_PLATFORM=linux/amd64` and the pinned binary directory `.tmp/review-107/bin`.
The six trial records are under `/private/tmp/hint-115-smoke.Zbhzhg/{success,auth,timeout}` on the validation host.
These temporary paths locate local evidence and are not required paths for reproducing the checks.
The [integration README](../../integrations/harbor/README.md#free-regression) provides the portable setup and commands.

All 33 finding IDs listed in #115 are covered by the register above.
No real inference endpoint was called by these checks.

Remote CI and merge are not yet verified for this cleanup branch.

## Publication handoff

The #115 implementation and its local checks are complete.
The branch also acquired the owner's unrelated grep documentation/test commit `de6fba2` during this work.
That commit was left intact and is outside the cleanup scope described above.
Before opening the PR, settle whether it should be carried as an explicitly documented scope exception or submitted separately, following the repository's branch rules without rewriting shared history.
Then publish the #115 changes through a reviewed PR to `develop`, verify remote CI and close #115 after merge.
