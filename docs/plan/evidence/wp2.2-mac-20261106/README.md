# WP2.2 local corpus measurements

Status: local component evidence, 2026-11-06.
These measurements do not validate the automatic resource profile, Raspberry Pi operation, or inference overhead.
The user will run the separate [Pi/model methodology](../../repo-map-evaluation.md).

## Corpus and method

The host is an Apple M5, Mac17,3, arm64, with 16 GiB physical memory.
The [machine-readable report](measurements.json) records the Go version, working-tree status, test-binary hash, individual measurements and RSS samples per run.
The corpus contains 163 real source files, 248,811 physical lines and 226,780 nonblank lines; comments are not removed from either count.
The [manifest](corpus.json) pins archive and individual-file SHA-256 digests rather than relying on mutable tags alone.

| Project | Version | Selection | Files | Physical lines |
|---|---|---|---:|---:|
| Requests | v2.32.3 | `src/requests/*.py` | 18 | 5,642 |
| Serde | v1.0.219 | `serde/src/**/*.rs` | 19 | 16,176 |
| TypeScript | v5.9.3 | `src/compiler/**/*.ts` | 77 | 191,840 |
| Express | 4.21.2 | `lib/**/*.js` | 11 | 4,140 |
| Wazero | v1.10.1 | First non-test `internal/**/*.go` paths in lexical order until 30,000 lines | 38 | 31,013 |

The harness verifies every source digest, creates a private SQLite catalog and invokes the production supervised extraction worker through the manager's focused admission path.
Cold means an empty catalog and fresh helper process; warm means the same catalog and unchanged sources.
The filesystem cache is not flushed.
There is no model running as part of this test, and concurrent development activity on the host was not controlled.
Consequently these are reproducible engineering measurements, not a low-noise hardware benchmark or a background-scheduler throughput claim.

RSS is the sampled sum for the test process and its descendant helper, observed every 100 ms through `ps` PID/PPID/RSS fields.
The indexing-off comparison creates the catalog and verifies the corpus but does not extract code.
Additional RSS is the difference between paired sampled family peaks, not a hard memory ceiling or an allocation counter.
Shorter peaks may fall between samples.
The helper uses a 64 MiB Go soft memory target and `GOGC=50`; its WASM linear-memory ceiling is separately 64 MiB.
Neither setting is an operating-system RSS limit.

## Results

Three final repetitions after the worker GC adjustment produced:

| Measurement | Observed range |
|---|---:|
| Cold catalog construction | 4.595–4.660 s |
| Warm source verification | 0.220–0.224 s |
| Warm extractor invocations | 0 |
| Additional sampled process-family RSS | 92.03–93.98 MiB |
| Exact `Session` lookup p95, 20 queries per repetition | 2.123–2.255 ms |
| Serialized lookup result | 982 bytes, one declaration |
| Catalog size | Approximately 30.9 MB |
| Catalog coverage | 160 indexed files, 3 omitted, 0 pending |

The initial default-GC screening reached approximately 137.5 MiB additional sampled RSS, exceeding the proposed 128 MiB target on this host.
The isolated-helper GC adjustment reduced that observed peak without a visible cold-time penalty in this small sample.
Both the initial observation and the adjusted screening run remain in the report; the three final repetitions do not erase the initial result.
This is evidence for the local implementation choice, not proof that the proposed Pi/inference acceptance targets pass.

The omitted files are significant and are not counted as successfully parsed:

| File | Reason |
|---|---|
| `typescript/src/compiler/checker.ts` | `oversized`, above the 1 MiB source limit |
| `typescript/src/compiler/types.ts` | `parse_failed` with the pinned grammar |
| `typescript/src/compiler/transformers/utilities.ts` | `parse_failed` with the pinned grammar |

The corpus therefore does not establish complete TypeScript 5.9 grammar coverage.
The code map reports these omissions explicitly; ordinary authorized file access remains available.

## Reproduction

Run from the repository root with Go 1.26 and Python 3.
The preparation script downloads only pinned public archives, verifies their checksums and copies the manifest's selected files without executing source code.
The output directories must not already exist.

```bash
python3 scripts/repo-map-corpus.py \
  --manifest docs/plan/evidence/wp2.2-mac-20261106/corpus.json \
  --output .tmp/wp22-corpus-local
GOCACHE=/private/tmp/hint-repomap-gocache go test -c \
  -o .tmp/wp22-repomap.test ./internal/repomap
python3 scripts/repo-map-measure.py \
  --binary .tmp/wp22-repomap.test \
  --corpus .tmp/wp22-corpus-local \
  --output .tmp/wp22-corpus-measurements --runs 3
```

Both scripts were exercised against a newly recreated corpus on this Mac.
The opt-in Go harness is skipped during ordinary test runs.
The scripts measure focused local indexing; use the separate methodology for real background on/off and local-model comparisons.

## Resource adapter observations

The macOS adapter was exercised through `TestLocalResourceSamplerSmoke` against real counters.
It observed approximately 5.14 GB available memory, 736.66 GB free disk, 6% CPU activity and a 0.0017 aggregate I/O-time ratio in that sample.
These instantaneous values are neither thresholds nor capacity recommendations.

Linux uses bounded reads of `MemAvailable` and CPU/I/O PSI plus filesystem free space.
For fully visible cgroup v2 hierarchies, it takes the minimum headroom across host and ancestor `memory.max`/`memory.high` constraints and the maximum host/group CPU and I/O pressure.
Legacy/hybrid hierarchies, hidden ancestors, malformed counters and more than 32 levels keep optional work paused instead of substituting host memory for a container budget.
The cgroup fixture tests follow the [kernel's hierarchy and memory-interface contract](https://docs.kernel.org/admin-guide/cgroup-v2.html); Linux hardware measurements remain pending.
macOS uses bounded `vm_stat`, a one-second current `iostat` CPU sample, memory-pressure sysctl, filesystem free space and deltas of the I/O Registry's total read/write times.
Missing counters, changing devices, counter resets and stale samples do not become zero pressure.
The macOS I/O signal sums operation time over elapsed wall time conservatively; overlapping operations may saturate it, so it is not a literal disk utilization percentage.
The implementation avoids `iostat`'s optional latency fields, whose missing values can appear as zero in [Apple's implementation](https://github.com/apple-oss-distributions/system_cmds/blob/main/iostat/iostat.c).
Platforms without a telemetry adapter, including Windows in this version, leave optional background work paused and retain bounded focused access.

The experimental manual profile pauses at 512 MiB memory/disk headroom, 0.75 CPU or 0.5 I/O, and resumes above 768 MiB headroom and below 0.5 CPU/0.2 I/O after ten seconds of recovery and a fifteen-second cooldown.
Sampling is every five seconds, with a fifteen-second maximum sample age and a three-second sampler deadline.
Sampling is skipped while indexing is off, while inference runs, and while auto mode lacks a validated profile.
Explicit `on` permits this finite experimental profile; default `auto` remains paused until measured acceptance justifies automatic operation.
