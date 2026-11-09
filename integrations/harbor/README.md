# Hint on Harbor

This adapter runs Hint's production one-shot CLI with its built-in tools, project instructions and context compaction.
Harbor owns the task environment, resource limits, timeouts, artifact transfer and verifier.
Every attempt starts with a fresh Hint home, configuration, session store, cache and index state.

## Install

Run from a clean checkout with the baseline commit in its Git history.
Install Docker with Compose, Go 1.26.10, and uv with CPython 3.13.15 available.
Harbor 0.25.0 requires Python 3.12 or newer.
This integration pins [Harbor 0.25.0](https://pypi.org/project/harbor/0.25.0/) and its transitive runtime dependencies.
The [tagged installed-agent contract](https://github.com/harbor-framework/harbor/blob/v0.25.0/src/harbor/agents/installed/base.py) calls `install()` from `setup()`.
Trial configuration runs in `run()` so that it stays independent of installation.
The supported range is Harbor ==0.25.0.
An existing 0.25.0 installation needs no upgrade.
Recheck the tagged lifecycle, auth and error-policy contracts before changing the pin.

```bash
cd /path/to/hint
root="$PWD"
integration="$root/integrations/harbor"
work="$root/.tmp/harbor"
mkdir -p "$work"
uv venv --python 3.13.15 "$work/venv"
uv pip install --python "$work/venv/bin/python" \
  -r "$integration/requirements.txt" "$integration"
py="$work/venv/bin/python"
harbor="$work/venv/bin/harbor"

GOTOOLCHAIN=go1.26.10 sh "$integration/build-hint.sh" "$work/bin"
"$harbor" --version
"$harbor" agent schema hint_harbor.agent:HintAgent --json
```

The build uses a Git archive of Hint commit `53264f88ad6900abab31c8e50e1281c0381ff45f`, stamped as `harbor-baseline`.
The adapter selects the binary using the container's `uname -m`, checks the build's SHA-256 before and after upload, and verifies the embedded commit.
The expected SHA-256 values for the documented toolchain are below.

| Binary | SHA-256 |
| --- | --- |
| linux/amd64 | `599f6ea8730db3652ba7cf13d3dd33a8cdfa9729e125810814b52ac7f6c51963` |
| linux/arm64 | `ebc0b423efbf9e6788d0cc993cbf71a66dedc815635e002599424c00347ae0d1` |

## Dataset and Docker

```bash
docker info --format 'arch={{.Architecture}} cpu={{.NCPU}} ram_bytes={{.MemTotal}}'
docker compose version
"$harbor" datasets download terminal-bench/terminal-bench-cpu-only@4.0.0 \
  --output-dir "$work/data"
dataset="$work/data/terminal-bench-cpu-only"
platform=linux/amd64
```

The runner expects the exported 63-task tree and reads its task configuration without mounting the downloaded solution or test tree in the agent container.
Use `linux/amd64` on x86-64 Linux or on Apple Silicon with Docker Desktop's amd64 emulation.
Use `linux/arm64` for ARM Linux images with matching task binaries.
The adapter has both binary paths, while the dataset's task-specific dependencies can still require amd64.
With 10 CPUs and about 7.75 GiB Docker RAM, start at concurrency one.
CPU-only tasks can still exceed available memory, and separate verification can require both the agent and verifier environments.

```bash
"$py" "$integration/benchmark.py" run "$dataset" "$work/jobs/oracle-wal" \
  --oracle --task wal-recovery-ordering --attempts 1 --concurrent 1 --platform "$platform"
```

## Model route and runs

Copy the [route configuration example](route.example.yaml) and fill in the exact model ID, Chat Completions base URL, context window, credential environment variable name and approved smoke budget.
Export the named variable from your secret manager before a real run.
The YAML rejects inline keys.
The base URL must omit credentials and query strings.
Hint accepts either the API root or its full `/chat/completions` endpoint.

```bash
route_config="$work/route.yaml"
cp "$integration/route.example.yaml" "$route_config"
# Edit route.yaml with the approved gateway settings.
```

For GLM, use `route: glm` and `reasoning_expectation: endpoint-default`.
For DeepSeek, use `route: deepseek` and `reasoning_expectation: non-thinking` only after verifying the gateway setting for the exact model ID.
The manifest records `reasoning_enforced: false` because the adapter sends no reasoning or output-token override.
Direct DeepSeek API and reasoning-enabled DeepSeek tool turns require the [provider dependency](#deepseek-provider-dependency).
Enforce the approved smoke budget at the provider.
The manifest records the amount with `smoke_budget_enforced: false` because Hint has no reliable local cost cap.

Use this Bash function to keep the one-task and full-suite configuration identical.
Each job needs a fresh output directory.
The runner pins retries to zero and preserves independent attempts.

```bash
hint_bench() {
  "$py" "$integration/benchmark.py" run "$dataset" "$@" \
    --concurrent 1 --platform "$platform" --binary-dir "$work/bin" \
    --route-config "$route_config"
}

# Resolve the configuration without inference or trial execution.
hint_bench "$work/jobs/plan-wal" --task wal-recovery-ordering --attempts 1 --dry-run
cat "$work/jobs/plan-wal/manifest.json"

# Run one paid attempt only after agreeing the smoke budget.
hint_bench "$work/jobs/hint-wal" --task wal-recovery-ordering --attempts 1

# Validate all 63 tasks and the 126-trial plan without calling the model.
hint_bench "$work/jobs/plan-full" --all --attempts 2 --dry-run

# Full paid evaluation, when authorized.
hint_bench "$work/jobs/hint-full" --all --attempts 2
```

Dry-run auth reporting uses a synthetic key and works without a real key in the environment.
It does not verify API access or model availability.

## Results and reruns

```bash
job="$work/jobs/hint-wal"
"$py" "$integration/benchmark.py" inspect "$job"
"$harbor" view "$work/jobs"

# Use the task directory from manifest.json for the failed trial.
failed_task=wal-recovery-ordering
hint_bench "$work/jobs/rerun-$failed_task" --task "$failed_task" --attempts 1
```

`manifest.json` records the requested dataset version, local task identities, digest-pinned agent and verifier images, model route, platform, attempts and retry policy.
Harbor's `lock.json` records resolved task digests, and each trial's `agent/effective.json` records the actual Hint binary and container architecture.
`summary.json` retains exceptions and missing attempts alongside verifier rewards.
Use Harbor's aggregate result for the repeated-attempt mean and pass@k, and the trial results for individual outcomes.
A completed agent process supplies no reward by itself.

The agent directory contains stdout JSON, stderr, the native session journal, execution timing and exit status, and Harbor-validated ATIF.
The session journal excludes the transient system preamble.
Effective metadata identifies the pinned preamble and tool source and records the native context settings.
Hint's turn limit uses Harbor's exhausted-budget policy.
Harbor 0.25.0 normalizes the exception message to a timeout, and `execution.json` records the original `error_kind`.
Token totals use only usage reported through Hint, with unavailable values and cost left null in execution metadata.
Hint tool calls run noninteractively as the task's agent user using `--yolo` inside Docker.
The process inherits the task environment, replaces Hint-specific variables and uses isolated XDG directories.
A task with a pre-existing `.hint/config.yaml` or `hint.yaml` fails explicitly to preserve the evaluation configuration.
The task image needs Bash and standard coreutils including chown, chmod, id, touch, sleep, uname, `sha256sum` and util-linux `setsid`.
Harbor timeouts cancel the invocation, and the adapter sends TERM followed by KILL to its process group and collects available logs before environment teardown.
Use this integration with trusted benchmark environments since the agent process needs access to the inference credential.

## Free regression

```bash
"$py" -m unittest discover -s "$integration/tests" -v
fake_jobs=$(mktemp -d "$work/fake.XXXXXX")
for mode in success auth timeout; do
  DOCKER_DEFAULT_PLATFORM="$platform" "$py" "$integration/tests/smoke.py" \
    "$work/bin" "$fake_jobs" --mode "$mode"
done
```

The fake provider runs on host loopback and uses `host.docker.internal` from Docker Desktop.
On Docker Engine for Linux, pass `--bind BRIDGE_IP --host BRIDGE_IP` to listen on the Docker bridge IP.
Find the default bridge gateway with `docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}'`.
The trial network must allow that host address.
The success case runs two tasks twice through Hint, checks the literal instruction, runs two native tools, and validates separate-verifier reward and session isolation.
The other cases exercise a 401 response and Harbor deadline cancellation.
All cases scan saved files for the fake key.

## DeepSeek provider dependency

The pinned Hint code reads reasoning deltas in [stream.go](../../internal/provider/openai/stream.go).
The outgoing [Chat Completions message](../../internal/provider/openai/convert.go) does not serialize `reasoning_content`.
[DeepSeek's thinking-mode contract](https://api-docs.deepseek.com/guides/thinking_mode/) requires that history for requests carrying tools, and its current API defaults to thinking.
A separate provider change on `fix/deepseek-reasoning-history` must implement explicit reasoning controls and preserve the required reasoning through multi-tool history before enabling direct DeepSeek evaluation.
Cover resumed and compacted sessions in the provider regression and verify an authorized multi-tool smoke against the direct endpoint.
The integration tracks this dependency in the [WP2R.3](../../docs/plan/phase-2r-review.md#wp2r3--harbor-benchmark-integration).

## Development

Run from `integrations/harbor` in the Harbor environment.

```bash
uv pip install --python "$py" -r requirements.txt '.[dev,docs]'
"$py" -m unittest discover -s tests -v
"$work/venv/bin/ruff" format --check .
"$work/venv/bin/ruff" check .
"$work/venv/bin/pyright"
"$work/venv/bin/sphinx-build" -W -b html docs docs/_build
```

Pyright reads Harbor's installed library source because Harbor 0.25.0 does not ship a `py.typed` marker.
