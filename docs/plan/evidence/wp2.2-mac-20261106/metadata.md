# WP2.2 metadata catalog scaling

Status: all three metadata measurements completed successfully on the developer Mac, 2026-11-06.
This is synthetic catalog evidence, not a million-file filesystem crawl, parser throughput result, Raspberry Pi result or inference-overhead measurement.

## Method

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

## Results

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

## Reproduction

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
