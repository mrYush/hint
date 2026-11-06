# Manual repo-map evaluation on Raspberry Pi

This is a reproducible acceptance protocol, not a report of completed Raspberry Pi or model experiments.
The owner will run the Pi 4 and local-model checks manually after reviewing the implementation.
Mac parser measurements below are screening evidence and cannot satisfy this acceptance gate.
The proposed 128 MiB additional indexing RSS and five-percent median inference-degradation targets remain proposals until measured and reviewed.
An implementation that keeps automatic indexing disabled because its resource profile is unvalidated is not an accepted automatic profile.

## Freeze the experiment before running it

Record the Hint source commit and the working-tree diff because the implementation under review may be uncommitted.
Include a content-hash manifest for new source and embedded artifact files that are not represented by a tracked-file diff.
Record the SHA-256 of the actual executable, Go version, build flags and dependency versions.
Use a Raspberry Pi 4 with 4 GB RAM and record board revision, OS image/release, kernel, architecture, firmware, power supply, governor, cooling, ambient temperature and swap/zram configuration.
Record storage model, connection type, filesystem, free space and whether the model, source corpus and index share the same device.
Record the complete local inference configuration: server/runtime version, model artifact hash, quantization, tokenizer/chat template, GPU or CPU backend, thread count, batch settings and generation parameters.
Record the effective model context capacity and verify that 8k and 32k requests actually use the selected capacity rather than a server default or silent truncation.
If the selected model/server cannot run 32k on this device, record that condition as unsupported instead of substituting another device or presenting a smaller context as 32k.
Use identical model weights and generation settings across indexing conditions within each context experiment.
Pin corpus revisions or archive hashes, eligible file counts/bytes, language distribution, ignored paths, dirty/untracked edits, nested-root boundaries and document-extractor settings.
Keep optional OCR and unrelated background services off unless they are a separately declared condition.
Record every enabled Hint tool and the exact automatic-indexing profile, including job/input/output/worker/cache limits, pause/resume thresholds, telemetry cadence, dwell and cooldown.

The following commands are ordinary environment checks, not a new Hint benchmark CLI:

```sh
uname -a
getconf LONG_BIT
cat /proc/meminfo
cat /proc/swaps
lsblk -o NAME,MODEL,TRAN,SIZE,FSTYPE,MOUNTPOINTS
go version
git rev-parse HEAD
git diff --stat
git ls-files --others --exclude-standard
```

Save the full diff locally as part of the review evidence, inspecting it for credentials before sharing.
Record missing platform utilities explicitly rather than inventing values.

## Build and parser checks

From the repository root, these commands exercise existing packages:

```sh
mkdir -p .tmp/repo-map-evaluation
CGO_ENABLED=0 go build -o .tmp/repo-map-evaluation/hint ./cmd/hint
sha256sum .tmp/repo-map-evaluation/hint
go test ./internal/repomap/extract ./internal/repomap/worker
go test ./internal/repomap/extract -run '^$' -bench 'Benchmark(Extract|WarmEngine)' -benchmem -count=5
```

Use the same executable for the paired product runs.
On a non-Linux development machine, use the platform's SHA-256 utility and record that substitution.
The [Mac evidence](evidence/wp2.2-mac-20261106/README.md) includes a checksum-pinned real corpus and scripts that can be reused for component measurements on the Pi.
Those focused component runs do not exercise a model or background admission.

## Product commands available for the manual run

Configure the local inference profile first using the existing setup/configuration interface and verify its model server's actual context capacity.
For a configured profile named `local`, start a fresh interactive evaluation process from the chosen fixture root:

```sh
.tmp/repo-map-evaluation/hint --plain --provider local --context-window 8192 --index-max-bytes 1073741824
```

Use `32768` instead of `8192` for the separate 32k experiment, only if the selected server/device supports it.
The Hint flag sizes its context budget; it does not reconfigure the model server.
Choose a storage allowance appropriate to the corpus and record it rather than copying the example's 1 GiB without checking available disk.
The following are real REPL commands, entered between completed turns:

```text
/repos index
/repos index off
/repos index on
/repos add ../second-fixture second
/repos tree second
/repos index
```

Use a fresh process and session for each paired task, restore its fixture first, and explicitly select the intended mode before entering the saved prompt.
Do not rely on a mode from an earlier process; startup resets it to `auto`.
An `on` run uses the published finite experimental envelope and is not proof that automatic operation is suitable for the Pi.
Current `auto` reports `resource_profile_unvalidated`, so an unchanged default run cannot count as the active-indexing condition.
Capture status before/after each run and distinguish resource-paused, incomplete and completed coverage.

Current product `off` and `on` also change `repo_map` disclosure; their direct comparison measures the combined product effect.
It cannot isolate background interference from schema cost.
The independent same-catalog experiment below requires a reviewed harness/configuration seam that freezes tool disclosure while controlling scheduler admission; that seam is not exposed as a public CLI flag in this version.
Until that experiment exists, report the combined measurement and keep the isolated schema/interference acceptance item open rather than labeling it controlled.
No automatic-profile acceptance result should be inferred from the component corpus harness.

## Conditions and run order

Evaluate 8k and 32k separately, with a fixed output-token reserve that leaves room for the full tool catalog and framing.
For resource isolation, first compare the same tool catalog with indexing off versus the candidate automatic-indexing profile.
For schema overhead, separately compare the baseline catalog with the otherwise identical catalog containing `repo_map`, keeping indexing state fixed.
Do not conflate the cost of exposing the tool, the cost of invoking it, and background indexing interference.

| Condition | Starting index state | Purpose |
| --- | --- | --- |
| Off | Indexing disabled; preserve declared tool availability | Model and live-tool baseline |
| Idle | Existing completed index; no deliberate changes | Retained runtime/cache and idle-maintenance cost |
| Initial build | Fresh disposable derived index | Discovery, parsing, disk and interference cost |
| Update backlog | Identical prebuilt index plus a fixed edit/rename/delete script | Incremental progress and pause/resume behavior |
| Query | Identical prebuilt index and predetermined map queries | Foreground retrieval latency and correctness |
| Pressure/recovery | Controlled pressure scenario and explicit release | Admission, hysteresis, checkpointing and eventual resume |

Never delete durable agent notes or user configuration when resetting a disposable index.
Restore each task's source fixture before every run, including dirty/untracked files, so a previous agent answer cannot make the next run easier.
Run at least ten paired repetitions per resource condition after a discarded warm-up, alternating AB/BA order or using a recorded randomization seed.
Use at least twenty repetitions when interpreting p95; retain the raw samples because small-sample p95 is unstable.
Do not run another benchmark concurrently.
Wait for the same predeclared thermal range before each pair and record waiting time rather than selectively dropping slow runs.
Report failures, cancellations, OOM events and throttled runs as outcomes; do not omit them from summaries without an explicit, predeclared exclusion rule.

Separate model prompt-cache states from filesystem cache states.
For warm prompt-cache runs, use a documented server cache policy and verify cache-hit counters if available.
For cold prompt-cache runs, use a documented reset or restart and report the resulting model-loading cost separately.
For filesystem-cold tests, use a controlled reboot or documented cache reset on a dedicated device and keep the procedure identical across conditions.
Do not claim a cold cache solely because Hint restarted, and do not mix cold and warm samples in one latency summary.

## Measurements

Measure task-to-first-visible-token and provider-request-to-first-token separately, since tools and scheduling can occur before a model request.
Record prefill duration and prompt-token counts when the inference runtime provides them.
Compute generation throughput from actual generated-token count divided by its documented generation interval; do not estimate tokens from characters.
Record total task duration, request count, tool-call count, retries, fallbacks, compactions, context overflows, cancellation latency and indexing progress.
Measure median and p95 TTFT, generation throughput and task duration separately for every context/cache/condition cell.

Track Hint and all indexing/helper processes, as well as the model server separately, using stable process identities that include process start time to avoid PID-reuse errors.
Sample process-family RSS at a fixed interval and sum simultaneously observed RSS values; the sum of independent per-process lifetime peaks is not a simultaneous family peak.
Shared pages may be counted repeatedly by RSS, so report PSS or cgroup memory where available and keep the metric definition unchanged across conditions.
Record peak family RSS, CPU seconds, read/write bytes, major faults, swap activity, disk free space and peak temporary/index disk usage.
Use process/cgroup lifetime counters to complement sampling because very short-lived helpers can disappear between samples.
Do not interpret Go heap statistics, guest linear-memory limits or SQLite cache settings as process-family RSS.

On Linux, the following observable sources are suitable for a sampler, with unavailable fields recorded as null:

| Source | Fields or interpretation |
| --- | --- |
| `/proc/<pid>/stat`, `/proc/<pid>/status`, `/proc/<pid>/io` | Process start identity, CPU, RSS/faults and I/O counters |
| `/proc/<pid>/smaps_rollup` | PSS/shared/private accounting when permitted |
| `/proc/meminfo`, `/proc/vmstat` | Available memory, swap and major-fault pressure |
| `/proc/pressure/cpu`, `/proc/pressure/memory`, `/proc/pressure/io` | PSI values when supported |
| `/sys/class/thermal/thermal_zone*/temp` | Temperature readings and associated zone identity |
| Isolated cgroup v2 counters, when available | Family memory peak and CPU/I/O accounting |

Where installed on the Pi, record `vcgencmd get_throttled` before and after each run and sample `vcgencmd measure_temp` or the thermal sysfs data during it.
Record both current and sticky throttling flags so a recovered device does not hide an earlier thermal or power event.
The sampler must not read source file contents, environment secrets or provider authorization headers.

## Full-schema accounting

Capture the exact serialized tool catalog and framing used for every provider request, including main turns, completion, compaction and retry/fallback requests.
Record a schema/catalog hash and schema byte count per request, plus token count from the matching tokenizer or measured provider-usage delta.
Store credentials-free request metadata; full source-bearing prompts require deliberate local retention and redaction before sharing.
A schema appearing once in a session log does not prove that its cost was counted on every model request.
For the cold first request, account for the full catalog before provider usage is available.
For later requests, avoid adding schema tokens twice when the provider's prompt-token usage already includes them.
Report unavailable tokenizer/cache accounting explicitly; a bytes-divided-by-four estimate is an approximation, not a measured token count.
Compare the catalog with and without `repo_map` at both context capacities, including prompt-processing latency, useful remaining context, overflow and extra compactions.

## Paired task set and scoring rubric

Before running the agent, save exact task prompts, fixture hashes, expected relevant files/declarations and a task-success rubric.
Positive cases should include finding an unfamiliar API, locating methods across files, choosing among repeated names in different roots, and an edit that requires a current source read after map discovery.
Include dirty edits, stale signatures, omitted/malformed sources and a partial-index negative query so success requires interpreting coverage correctly.
Negative cases should include a fully specified one-file change, a direct named-file read, a non-code request, and a task solvable from already supplied context.
Mark ambiguous cases before execution and score them separately rather than reclassifying them after seeing tool use.
Use identical task order, starting sessions, instructions, tools other than the experimental difference, model settings and output reserve for each pair.
Use a recorded seed where supported, but still repeat runs because deterministic seeds do not guarantee identical tool trajectories.

For every run, score final task correctness, current-source evidence, unnecessary source exposure, useful declarations/files per delivered token, tool calls, latency and failed/repeated retrieval attempts.
An edit is not correct merely because the final text looks plausible; run the fixture's predetermined assertion or test and verify the actual changed files.
A map call on a preclassified negative task is unnecessary unless the predeclared rubric permits it; record any exception and its justification without changing the task class.
Report negative tasks with at least one unnecessary map call divided by all negative tasks.
Also report unnecessary map calls divided by all tool calls, invalid map arguments, useless repeated calls after paused/partial results, missed beneficial map calls and correctness regressions.
Do not reduce this evaluation to retrieval recall or a single average latency.

## Raw artifacts and summary tables

Keep one immutable metadata JSON file per experiment and append-only JSONL for events and request/task results.
Export CSV summaries from the same JSONL; do not manually edit the summary values.
Use monotonic time for durations and UTC wall time for cross-file correlation.

| Record | Required fields |
| --- | --- |
| Experiment metadata | `experiment_id`, executable/source/diff hashes, device/OS/storage/cooling, model/runtime/artifact/template hashes, sampler version, run-order seed, corpus hashes, profile and tool-catalog hashes |
| Run | `run_id`, `pair_id`, repetition, condition, task ID/class, context capacity, cache states, thermal start/end, start/end timestamps, outcome, exclusion reason if any |
| Provider request | run/request IDs, purpose, attempt, provider/runtime, schema hash/bytes/tokens/method, prompt/output/cache tokens, TTFT, prefill/generation durations, throughput, error/cancel/fallback |
| Resource sample | run ID, timestamp, process identity/role, family RSS/PSS, CPU cumulative seconds, I/O cumulative bytes, major faults, available memory, PSI, swap, temperature, throttling, free/temporary disk |
| Index/map event | run ID, root/generation, eligible/indexed/omitted counts when known, work state/reason, queue depth, effective budgets, cursor stop reason, query/validation bytes, freshness and response bytes |
| Task score | run/task IDs, success assertion, evidence freshness, useful coverage/tokens, total/unnecessary/invalid/repeated tool calls, final correctness and rubric notes |

Preserve absent measurements as null with an availability reason, never as zero.
Publish counts and distributions with median and p95; retain individual pairs so thermal drift and outliers can be inspected.
Use the usual midpoint median for even sample counts and nearest-rank p95, recording the aggregation definition with the results.
Compute the paired throughput degradation as `(throughput_off - throughput_on) / throughput_off`, and report TTFT/task-latency changes separately.
State whether the five-percent proposal is applied to the median of paired changes or to the change in condition medians; use the chosen definition consistently.
Report additional simultaneous process-family RSS relative to the comparable off condition, including helpers and retained caches.
Only the owner-reviewed numerical profile and completed measurements can close the Pi acceptance item.
