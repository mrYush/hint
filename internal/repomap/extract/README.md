# Declaration extraction

`Extract` consumes already-authorized complete source bytes and never reads a source path from disk.
It returns an all-or-nothing declaration result with an explicit omission reason on malformed, oversized, timed-out or work/output-limited input.
`MaxOutputBytes` counts the complete JSON encoding of `Result`.
The policy extracts Go package-level functions, methods, types, variables and constants through `go/ast`.
The non-Go policy extracts named functions, methods, classes, interfaces, type aliases, Rust types/items/modules and JavaScript/TypeScript variable declarations encountered in declaration containers.
Executable function bodies and arbitrary initializer expressions are not recursively searched for local declarations.
Python assignment targets, destructuring patterns, anonymous callables without a declaration binding and macro-generated symbols are outside this initial declaration policy.
The policy is syntactic and does not resolve names or infer dependencies.
Function bodies are omitted from signatures, including callable bodies inside default arguments.

## Runtime and artifacts

The selected non-Go adapter is `github.com/zema1/wasitter v0.1.0` with `github.com/tetratelabs/wazero v1.10.1`.
The five embedded modules are downloaded from the matching [wasitter v0.1.0 release](https://github.com/zema1/wasitter/releases/tag/v0.1.0) and verified against its SHA256SUMS.
The grammar versions are Python and JavaScript v0.25.0, TypeScript and TSX v0.23.2, and Rust v0.24.2.
The release registry and third-party license notices identify their upstream repositories and source archive hashes.
The release modules combine the upstream Tree-sitter C runtime, grammar and wasitter ABI bridge; ordinary web-tree-sitter grammar modules are not substitutes.
The checksum regression test pins the actual module bytes, not merely their filenames.
The third-party notices and release registry are included in the embedded filesystem alongside the modules; release packaging should also make the notices available as a readable attachment.
There are no runtime downloads or user-controlled grammar search paths.

The package-level `Extract` creates and closes a runtime for one job; a serialized `Engine` retains at most one language runtime for repeated files, closing it on language changes, failed execution or explicit resource-pressure shutdown.
Each runtime has a 64 MiB guest linear-memory ceiling, context interruption and a two-second maximum local job deadline.
The effective deadline is the earlier caller deadline or that local maximum.
The guest limit is not a bound on compiler allocations, host wrappers or total process RSS.
WorkUnits counts visited extraction nodes, not native parser instructions.
The parser's full-source work is bounded by admission size and the supervised helper deadline.
The standard Go parser is not context-preemptible and requires the same supervised helper boundary.
Cancellation never means that a caller may free a worker lease before the process has stopped.

## Validation

Tests cover all six language routes, malformed sources, source coordinates, Unicode names, signatures, enclosing context, nested callable-body redaction, minified/decorated sources, budgets, cancellation and artifact checksums.
`BenchmarkExtract` measures complete fresh-runtime extraction of 100 generated declarations per language, including compilation and output construction.
This is deliberately a cold helper workload rather than an incremental-editor benchmark.
The development comparison against gotreesitter v0.56.0 lives in the session's `.tmp/wp2.2-parser-compare` evidence; its measured tradeoffs do not establish Raspberry Pi performance.
`BenchmarkWarmEngine` measures complete extraction after initializing the single cached language runtime, without reusing an old syntax tree.

## Local Mac screening evidence, 2026-11-06

The implementation was tested on darwin/arm64, Apple M5, Go 1.26.10, with golangci-lint 2.13.2.
The comparison pinned gotreesitter v0.56.0 at commit `ac4e027e02525e9d1d005c54ed5e54979271e4ed` and wasitter v0.1.0 at commit `d4ae75f0856a9a2dc7e29a6b82aac2f088be010f`.
Both candidates parsed the same five handwritten language fixtures and rejected corresponding malformed inputs.
Their named-node S-expressions matched exactly after removing upstream field labels, both for the small fixtures and for 500 repeated copies per language, totaling 8,000 generated lines across the five files.
This is parser-shape evidence for those fixtures, not proof of parity on arbitrary programs or semantic correctness.
Hint's independent output fixtures additionally check declaration names, enclosing context, exact representative signatures, byte/line ranges and body exclusion.

| Local measurement | gotreesitter v0.56.0 | wasitter v0.1.0 |
| --- | --- | --- |
| Language initialization, small fixtures | Approximately 5–17 ms | Approximately 63–84 ms |
| Warm parse plus full S-expression, 37.5–62.5 KB generated files, 20 iterations | Approximately 10–15 ms | Approximately 15–20 ms |
| Stripped cgo-free minimal executable, normal build, excluding separately loaded WASM assets | 28,554,386 bytes | 5,028,866 bytes |
| Same gotreesitter executable with five-language subset build tags | 10,152,530 bytes | Not applicable |
| Five selected WASM modules | Not applicable | 6,000,823 bytes |

The minimal formatting-only executable was 1,641,362 bytes using the same `-trimpath -ldflags='-s -w'` build flags.
The wasitter executable above loads external module files, so its deployable comparison must include the six-million-byte module payload; the product embeds that payload.
The gotreesitter subset result is slightly smaller than that combined payload, but requires additional build tags that ordinary builds do not automatically acquire.
The selected adapter trades higher cold initialization cost for the upstream runtime/grammar implementation, explicit guest-memory/interruption controls and predictable ordinary-build embedding.
It is not selected as the fastest parser.

An additional generated stress run used 6,000 copies per language, totaling 96,000 lines across five 450–750 KB files.
The gotreesitter run configured a 64 MiB parser budget, 200,000-node/iteration thresholds, depth 1,024 and a 500 ms parse timeout, and all five files reported an explicit node-limit stop.
The wasitter comparison used its default runtime and a one-second per-parse context, completed those inputs, and did not apply the product's separate declaration/output limits.
Peak child RSS reported by Python's `resource.getrusage` was 269,828,096 bytes for gotreesitter and 291,487,744 bytes for wasitter in separate runs.
Those unequal completion/resource policies and full-tree string construction make this a stress/omission diagnostic, not a fair speed or peak-memory ranking.
Neither figure establishes the proposed 128 MiB product target, and native or guest memory settings must not be confused with total RSS.

Complete fresh-runtime extraction of 100 generated declarations measured approximately 69–74 ms for non-Go languages and 0.68 ms for Go in a three-iteration screening run.
After adding the persistent single-language engine, the corresponding five-iteration warm extraction measurements were Python 7.31 ms, JavaScript 4.97 ms, TypeScript 7.30 ms, TSX 4.97 ms and Rust 5.41 ms.
Warm allocated bytes per operation ranged from approximately 0.89 MB to 1.88 MB; allocations are not retained RSS.
These short development runs were not an exclusive-machine thermal-controlled acceptance experiment and must be rerun using the reproducible benchmarks for performance decisions.
Raw comparison programs and JSONL are retained locally in `.tmp/wp2.2-parser-compare`; the reproducible product benchmarks and checksum/output tests are tracked in this package.
Raspberry Pi and local-model 8k/32k acceptance are deliberately pending the owner's manual run described in `docs/plan/repo-map-evaluation.md`.
