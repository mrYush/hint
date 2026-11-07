# Measurement runbook

Follow the [saved methodology](../../repo-map-evaluation.md).
Freeze immutable metadata before collection, including release/build hashes, corpus manifest, tool and index configuration, runtime/model artifact and template hashes, model seed support, cache policy and thermal band.
Store append-only run, request, sample, index and score JSONL with joined run IDs.
Use UTC and monotonic timestamps.
Keep raw traces private pending a credential and source-content review.

## B1

Create two experiments at effective capacities 8192 and 32768.
Verify actual runtime slots and effective context with runtime startup/slot telemetry and a truncation probe before pairing.
Hint's `--context-window` is only its client budget.
Choose and verify a 1024-token output reserve/cap for both sides through effective client and runtime settings before starting; if the pinned profile cannot supply or demonstrate it, revise the frozen plan before collection.
Use one model, fixed generation settings, model seed 95 where supported, and record null with reason where unsupported.
Use the same documented warm prompt/filesystem cache policy throughout each experiment and discard one full warm-up pair per task.
Use sequential pair order off/on for odd repetitions and on/off for even repetitions, in task order P1 through N4.
Run ten pairs per task and context for median-only interpretation or twenty if interpreting p95.
Record requested and effective mode, and retain paused and failed outcomes as separate states.
Do not run B1 alongside B2 or another benchmark.

For each side create a fresh fixture, hash its files, start a fresh terminal process and send the exact prompt after the relevant setup.
From its primary directory run `"$BIN" --plain --debug --provider local --context-window "$CAPACITY" --index-max-bytes "$INDEX_BYTES"`.
Attach `second` identically on both sides for P3.
Select `/repos index off` or `/repos index on` and capture status before and after.
Use identical predeclared thermal and cache conditions within pairs.

| Task | Class | Exact prompt | Rubric |
| --- | --- | --- | --- |
| P1 | Positive | Locate the API that prefixes an arbitrary string with beacon:. Give file, symbol and signature. | alpha.go, LookupBeacon, correct signature and location. |
| P2 | Positive | Which methods are defined on Catalog? Give their defining file and signatures. | methods.go, Open and Close, correct receiver and signatures. |
| P3 | Positive | Find Shared in the second root and distinguish it from the one in primary. Give both paths and return strings. | second beta.go returns second; primary alpha.go returns primary. |
| P4 | Positive | Discover Mutable, read its current source, then change its current prefix from fresh: to final:. Leave the current parameter list unchanged. | Tool trace proves read after discovery; diff touches only the return prefix in methods.go and preserves the two-parameter signature. |
| P5 | Positive | Locate the declaration PhantomMissing and explain how certain you are about its absence, given the available coverage. | No invented source, respects partial/omitted coverage and uses live fallback where appropriate. |
| N1 | Negative | In settings.txt replace the exact line label=before with label=after. Make no other changes. | Exact file content and only that changed file; any map call unnecessary. |
| N2 | Negative | Read alpha.go directly and quote the package declaration. | package alpha with read trace; any map call unnecessary. |
| N3 | Negative | Reply exactly ACCEPTANCE. | Exact final text; any map call unnecessary. |
| N4 | Negative | Given that the answer is 42, reply with the answer only. | Exact final text 42; any map call unnecessary. |

For P4 prewarm the old signature, then perform the same dirty rewrite in both modes before the measured prompt using `sed 's/Mutable(s string)/Mutable(s string, _ bool)/;s/old:/fresh:/' methods.go > methods.new && mv methods.new methods.go`.
Score the on trajectory for beneficial discovery and the off trajectory for correctness/current-source evidence; map absence in off is expected.
For P5 keep malformed.go and omitted.txt and freeze a reproducibly partial on fixture before collection; verify partial coverage in the raw tool result, and classify that cell separately if the intended coverage cannot be established.
Capture correctness, useful current sources and unnecessary exposure, invalid/unnecessary/repeated calls and missed beneficial calls from tool traces.
Capture every provider request's schema hash/bytes/tokens, prompt and output usage, TTFT, throughput and timing via verified runtime telemetry or reviewed instrumentation.
A debug transcript can establish tool calls, but request metrics absent from it need runtime telemetry or null with reason.
Capture first visible token separately and use a family sampler on the measurement device.
Keep schema-only and same-catalog interference results null without the reviewed seam.

## B2

On the Pi, from the evidence directory copied locally, collect `sh pi-inventory.sh > pi-inventory.txt`.
Return that file plus the cooling, ambient temperature, power supply, storage sharing and local server/model settings, without credentials.
Build from the confirmed SHA with `BUILD=$(sh build.sh /path/to/hint "$SHA")` and return `"$BUILD/build.txt"`.
Use the local inference profile and startup flags from B1 with the verified Pi capacity, plus the actual storage allowance and index thresholds.
Retain only the capacities the Pi really supports and use null plus reason for unsupported metrics and cells.
Before each run prepare an identical corpus revision and record eligible files/bytes, manifest and free space.
Use the [pinned corpus instructions](../wp2.2-mac-20261106/README.md) for a larger corpus.
Do not share the derived index with ordinary Hint processes.
Use a dedicated cache location with `XDG_CACHE_HOME` on Linux.

Start Hint in a terminal using `printf 'Hint exec PID=%s\n' "$$"; exec "$BUILD/hint" --plain --debug --provider local --context-window "$CAPACITY" --index-max-bytes "$INDEX_BYTES"` inside a subshell from the corpus root.
In another terminal run `python3 pi-sample.py "$RUN_ID" "$HINT_PID" "$MODEL_PID" "$XDG_CACHE_HOME/hint/index" >> resources.jsonl`.
Start sampling before the condition begins and stop with Ctrl-C after the ending status snapshot.
Use a new run ID for each condition and record timestamps, thermals, throttling, provider metrics and status.
Keep separate model and Hint family data and use cgroup counters if available for helpers shorter-lived than the sample interval.

| Condition | Operation |
| --- | --- |
| Off | Select `/repos index off`, record status and run the frozen inference task. |
| Idle | Select `/repos index on` with a completed index, no edits, then run the same inference task. |
| Initial build | With all processes closed, choose a fresh disposable cache directory, select on, sample discovery and run the same inference task on the frozen schedule. |
| Backlog | Restore the prebuilt cache and corpus, then apply a frozen edit/rename/delete batch while sampling, record queue and recovery, and run the same inference task. |
| Query | Restore the prebuilt state, send the frozen P1 and P2 prompts for the chosen corpus, and record retrieval timing, freshness and correctness. |
| Pressure/recovery | Use a separately frozen bounded pressure procedure on the dedicated Pi, capture admission reason and queue, release the pressure and capture resumed progress. Keep the pressure procedure pending until device headroom and tooling are known. |

Use at least ten valid off/on pairs after warm-up for each comparable resource condition, or twenty when interpreting p95.
Report the midpoint median of per-pair throughput losses `(off - on) / off`.
Report additional peak simultaneously sampled Hint/helper family RSS relative to comparable off, in MiB.
Preserve all pauses, throttling, failures and OOM events, and exclude paused samples from active-on counts with a recorded reason.
Derive CSV from JSONL, retain raw values, report counts, and use nearest-rank p95 only with the required sample count for interpretation.
