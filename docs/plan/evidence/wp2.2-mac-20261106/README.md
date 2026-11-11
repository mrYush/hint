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

## Metadata catalog scaling

Status: all three metadata measurements completed successfully on the developer Mac, 2026-11-06.
This is synthetic catalog evidence, not a million-file filesystem crawl, parser throughput result, Raspberry Pi result or inference-overhead measurement.

### Metadata method

The host is the same Apple M5 / Mac17,3 / 16 GiB machine used by the adjacent corpus measurements, running macOS 26.7 and Go 1.26.10 on arm64.
The [environment manifest](metadata-environment.json) records the compiled test-binary SHA-256, index source checksums, exact method and working-tree base.
The benchmark process is compiled once before measurement, so compiler RSS and compilation time are excluded.
The manifest preserves that binary's source checksums; additional frontier-lookup and regression-test changes made afterward do not alter the measured insertion/query paths.
The corpus timing runs had finished before these measurements; ordinary concurrent development activity on the host was not controlled.

Each run creates a fresh private SQLite catalog with 10,000, 100,000 or 1,000,000 synthetic file metadata records and one declaration per file.
The files have canonical paths `src/file-%07d.go` and function names `Function%07d`.
Their declared source size is 100 bytes; those source files are not created or parsed.
The fixture uses the six-byte root ID `r_test`, whereas production root handles are 45 bytes, so the observed database size is not a worst-case production storage estimate.
There are no obsolete source versions or concurrent snapshot pins in the build workload.

The total storage allowance is explicitly 8 GiB, replacing the ordinary 128 MiB default for this scale experiment.
SQLite still uses the production 2 MiB cache, 4096-byte pages, DELETE journaling, one connection and bounded write operations.
Approximately one third of the allowance admits database pages so rollback journal headroom remains reserved.
The temporary catalogs are removed by the test framework after each run.
No filesystem cache flush is performed.

After construction, a snapshot is pinned and the benchmark performs twenty name-prefix queries for the final generated function name.
Every query must examine exactly one raw record and return exactly one declaration, with a limit of 16 records and a 64 KiB logical record-read budget.
Go's `testing.B.Loop` excludes catalog setup from query timing and allocation counters and avoids rebuilding the fixture during calibration.
The earlier `StopTimer`/`ResetTimer` sequence was corrected before measurements because `ResetTimer` alone does not start a stopped timer.
The reported query result is the arithmetic mean of twenty queries, not p95 and not a complete agent-tool request.
It mixes the first query with nineteen subsequent queries against the same retained catalog and does not establish cold-query latency.

Build time comes from a separate monotonic timer around the production `PutSource` loop.
Catalog bytes are the database file size after construction, before pinning the query snapshot; this is not peak database-plus-journal disk usage.
The recorded peak RSS is macOS `/usr/bin/time -l`'s maximum resident set size for the compiled benchmark process, in bytes.
It is neither additional RSS relative to Hint nor a process-family ceiling; this benchmark starts no parser helper and runs no model.
Query bytes/allocations per operation are Go allocation counters, not RSS or physical disk I/O.
The zero block-I/O counters in the raw macOS output must not be interpreted as zero storage work.

### Metadata results

- ✅ 10,000-entry measured run completed successfully.
- ✅ 100,000-entry measured run completed successfully.
- ✅ 1,000,000-entry measured run completed successfully.

| Entries | Build time | Catalog bytes | Peak RSS bytes | Mean query ns/op | Query B/op | Query allocs/op |
|---:|---:|---:|---:|---:|---:|---:|
| 10,000 | 7.063 s | 22,433,792 | 25,313,280 | 37,669 | 9,615 | 170 |
| 100,000 | 67.716 s | 225,292,288 | 26,869,760 | 38,831 | 9,626 | 170 |
| 1,000,000 | 20m23.109s | 2,250,113,024 | 27,394,048 | 40,225 | 9,617 | 170 |

The successful [10,000-entry raw output](metadata-10000-measured.txt), [100,000-entry raw output](metadata-100000.txt) and [1,000,000-entry raw output](metadata-1000000.txt) include all reported counters and progress checkpoints.
[Machine-readable results](metadata-results.json) retain the raw benchmark metrics and process timings.
Build times in the table use the final progress timestamp rounded to milliseconds, because Go formats the separate `build-seconds` metric with fewer significant digits.
An earlier [sandboxed probe](metadata-10000.txt) completed its benchmark but `/usr/bin/time` could not read `kern.clockrate`, so the command exited unsuccessfully and produced no RSS evidence.
That probe is retained for transparency and is not the measured row above.

Across this fixture, catalog size grows with retained records while process peak RSS stays between 25.3 and 27.4 MB and selective-query allocation counts remain unchanged.
These are observed samples, not a universal memory ceiling or latency percentile.
The million-entry build took about 20 minutes; its per-entry construction time was slower than the smaller samples, so the smaller build times must not be extrapolated as a linear throughput promise.
The ordinary 128 MiB storage allowance cannot retain this million-entry fixture; this run explicitly configured 8 GiB and does not remove normal partial-index and quota behavior.
The query and catalog measurements do not establish large-directory traversal latency, parser cost, inference interference or Raspberry Pi acceptance.

### Metadata reproduction

From the repository root:

```bash
GOCACHE=/private/tmp/hint-wp22-code-cache GOPROXY=off \
  go test -c -o .tmp/wp22-index-scale.test ./internal/index
HINT_INDEX_BENCH_ENTRIES=10000 /usr/bin/time -l \
  .tmp/wp22-index-scale.test -test.run '^$' \
  -test.bench '^BenchmarkMetadata$' -test.benchtime=20x -test.benchmem -test.v
```

Repeat with `HINT_INDEX_BENCH_ENTRIES=100000` and `1000000`.
On macOS, the measurement command needs access to the resource counters used by `/usr/bin/time`; a restricted environment can deny those counters even when the benchmark itself passes.
Allow several gigabytes of temporary disk space for the million-entry catalog and its configured journal headroom.
Keep timed corpus, indexing and other storage-heavy measurements separate.

## Integration and workspace review

The 2026-11-06 automated component reviews inspected root/workspace, query/index and manager/resource seams before the later remediation.
The query/index reviewer was independent only of the workspace code; IR4 and IR6 were its own implementation/self-review.
The manager/resource reviewer inspected the workspace implementation and separately corrected manager findings.
These are attributed automated reviews, not independent human approval or final whole-repository acceptance.

| Finding | Disposition and regression evidence |
| --- | --- |
| IR1: historical boundary presented as live existence | Label the retained authority reservation as availability-unverified; `TestTreeHistoricalBoundaryIsNotLiveExistenceClaim`. |
| IR2: secondary preparation could not address exact opaque targets | Add mutually exclusive `entry_id` preparation and pass the relative mutation target; `TestRepoPrepareEntryIDPreservesExactTarget`. |
| IR3: routing tested values rather than field presence | Reject null and ambiguous path/entry fields; `TestRepoRoutingRejectsNullAndAmbiguousFields` and `TestRepoPrepareRejectsNullAndAmbiguousFields`. |
| IR4: fallback glob accumulated every match | Bound the retained heap to 201 results; `TestGlobWindowMatchesFullSortOracle` and `TestGlobWindowPreservesShortResultsAndTruncationBoundary`. |
| IR5: obsolete catalog versions lacked maintenance | Add bounded admitted maintenance while preserving active pins; `TestManagerMaintenanceRetainsPinsThenCollects`. |
| IR6: resumed maps omitted current scope validation | Reject removed or newly excluded scopes before emitting saved metadata; `TestMapCursorDoesNotRevealRemovedScope`. |
| W1: tool/catalog ignore policy diverged | Secondary ordinary roots use only their own Git metadata; primary roots preserve enclosing-Git policy without widening authority; `TestAttachedOrdinaryFolderDoesNotInheritAncestorGitIgnore` and `TestManagerPrimarySubdirectoryUsesEnclosingGitPolicy`. |
| W2: lazy Git probes could run repository fsmonitor hooks | Disable fsmonitor for policy commands; `TestRulesNeverRunRepositoryFSMonitorHook`. |
| W3: walk error callbacks leaked skip controls | Normalize `SkipDir` and `SkipAll` while retaining sibling traversal; `TestErrorCallbacksHonorSkipControls`. |
| M1: ignored directories hid force-added tracked descendants | Use a bounded tracked-descendant probe; `TestManagerGitIgnoreNULAndTrackedSemantics`. |
| M2: focused extraction could start during an emergency | Guard source admission, cancel active work and require a complete current healthy sample for recovery; `TestManagerFocusedEmergencyPreservesCatalog` and `TestManagerFocusedEmergencyCancelsActiveWorker`. |
| M3: a wide root could retain all directory streams | Retain one stream per root and eight globally with durable retries; tiny-root, blocked-child, retained-tail and stream-capacity regressions in `internal/repomap`. |

The leading-space entry-handle fixture passed; APFS rejected the raw-invalid-byte filename fixture, so that subcase was skipped rather than reported as a platform pass.
Targeted race tests, vet and pinned lint passed; the [focused transcript](integration-focused-tests.txt) preserves the integration command results.
The [original integration review](https://github.com/mrYush/hint/blob/1c45765d1126898cb467c9c847ba1f1b9b63cdb8/docs/plan/evidence/wp2.2-mac-20261106/integration-review.md) and [workspace review](https://github.com/mrYush/hint/blob/1c45765d1126898cb467c9c847ba1f1b9b63cdb8/docs/plan/evidence/wp2.2-mac-20261106/workspace-review.md) preserve reviewer attribution, source snapshot hashes, detailed inspection and limits in repository history.
The [remediation record](../wp2.2-remediation-20261107/README.md) supersedes the working-tree handoff and records final checks.
These component reviews did not measure Pi hardware, local-model usefulness, 8k/32k context acceptance or inference interference.
The [code-map contract](../../repo-map-contract.md) remains the runtime contract; the [manual evaluation](../../repo-map-evaluation.md) owns unperformed model/device measurements.
