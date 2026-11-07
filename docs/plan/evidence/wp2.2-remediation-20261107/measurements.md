# Affected local measurement reruns

Date: 2026-11-07; code revision `748459b`.
The [environment manifest](measurement-environment.json) records commands, test-binary hashes and the corpus-manifest hash.
The host remains the Apple M5 developer Mac; this is not Pi/model acceptance or a filesystem-scale benchmark.

## Corpus

The same checksum-pinned 163 files / 248,811 physical lines and production supervised extraction path were exercised in three baseline/index pairs.
Source admission, cached-source verification, schema/storage metadata and coverage output changed, so these paths were remeasured.
The fixture was copied outside the Hint checkout to `/private/tmp`; each run verifies every source digest before extraction.
Filesystem caches were not flushed, and ordinary host activity was not controlled.

| Measurement | New observed range |
|---|---:|
| Cold catalog construction | 4.657–4.713 s |
| Warm source verification | 0.230–0.232 s |
| Warm extractor calls | 0 |
| Additional sampled process-family RSS | 91.72–92.39 MiB |
| Exact `Session` query p95, 20 requests/run | 2.332–2.475 ms |
| Serialized result | 1,027 bytes, one declaration |
| Catalog size | 35,708,928–35,753,984 bytes |
| Coverage | 160 indexed, 3 omitted, 0 pending |

The three omissions remain one oversized TypeScript source and two parse failures; no refresh failures were recorded.
The response is larger than the historical 982-byte result because reason coverage is now populated.
Catalog size is also larger than the historical approximately 30.9 MB observation; changed per-source policy metadata and schema mean the old storage number must not be carried forward as current.
These small uncontrolled samples do not demonstrate a throughput improvement or regression in isolation.

Raw index results: [run 1](corpus/1-index.json), [run 2](corpus/2-index.json), [run 3](corpus/3-index.json).
Paired sampled RSS: [run 1 index](corpus/1-index-resources.json) / [baseline](corpus/1-baseline-resources.json), [run 2 index](corpus/2-index-resources.json) / [baseline](corpus/2-baseline-resources.json), [run 3 index](corpus/3-index-resources.json) / [baseline](corpus/3-baseline-resources.json).
Additional RSS subtracts the paired baseline peak from the index process-family peak, sampled every 100 ms with `ps`; short peaks can be missed.
No model ran, so this is neither inference interference nor a background-scheduler throughput measurement.

The first attempt used the existing corpus under Hint's ignored `.tmp` directory and correctly failed with access denial under the primary root's enclosing Git policy.
Its [failure log](fixture-rejected/1-index.txt) and [resource record](fixture-rejected/1-index-resources.json) are retained and excluded from successful timing ranges.
The product's ignore policy was not relaxed to make the benchmark pass.
The earlier corpus location therefore cannot be reused blindly with the current primary-root policy.

## Catalog smoke at 10,000 records

The [raw benchmark](metadata-10000.txt) uses the existing `BenchmarkMetadata`, twenty selective-prefix queries, an 8 GiB configured allowance and the production bounded store.
The executable was built before timing; compiler memory is excluded.

| Metric | Observed value |
|---|---:|
| Catalog construction | 6.554 s |
| Database bytes after construction | 22,458,368 |
| Process peak RSS | 26,148,864 bytes |
| Mean selective query | 37,535 ns/op |
| Query allocations | 9,629 B/op; 170 allocs/op |

This checks the changed storage/coverage path at one representative size; the ordered query access path is unchanged.
The old 100k/1m experiments were not rerun and remain explicitly historical, not measurements for this revision.
Actual lock contention, frontier exhaustion, delayed retries and recovery are exercised by the new regression tests instead of being inferred from synthetic metadata timings.
Long wide/deep filesystem traversal remains in the separately proposed cleanup/quota follow-up.
