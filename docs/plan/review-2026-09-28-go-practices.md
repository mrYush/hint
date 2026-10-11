# Code review: Go practices audit (2026-09-28)

> Status: findings to file · Scope: the whole tree at `release/v0.2`
> (commit `4895d9a`, WP0.1–WP0.12 landed) · Method: five package-group
> reviews against a checklist of modern Go idioms, then every must-level
> finding re-verified by reading the code; items marked *reproduced* were
> confirmed with a throwaway test that was deleted afterwards.

`golangci-lint` (standard + exhaustive, errorlint, revive, godot, misspell,
unconvert, nilerr) reports 0 issues and `go test -race ./...` is green, so
nothing below is something the existing gate catches.

How to use this file: each finding has an ID, a severity, the work package
it belongs to, exact locations, and a suggested fix plus the test that
should pin it. File the blockers and the must items as GitHub issues titled
`[Phase 0][WPx.y] <title>` (see [README.md](README.md), "Source of truth and
issue tracking"); the should and nit items can be batched one issue per
package.

Severity:

- **blocker** — confirmed wrong behaviour in the default configuration
  that loses data, crashes the process, or makes a file unreadable.
  Backlog as of 2026-10-11: `v0.2.0-alpha` and `v0.2.0` may ship with
  them listed in the release notes.
- **must** — confirmed bug with a wrong user-visible result. Backlog as of
  2026-10-11; M1 and M2 do not block `v0.2.0`.
- **should** — a deviation from Go practice that will bite under a
  realistic condition (locale, load, platform, a future change).
- **nit** — style, naming, an idiom the toolchain now offers.

---

## Release blockers

### B1 — Compaction summary is erased by the next prefix refresh

| | |
|---|---|
| Severity | **blocker** |
| WP | WP0.4 (agent loop), WP0.12 (preamble) |
| Where | [`internal/agent/compact.go:115`](../../internal/agent/compact.go#L115), [`internal/agent/loop.go:160-170`](../../internal/agent/loop.go#L160-L170), [`cmd/hint/rules.go:93`](../../cmd/hint/rules.go#L93) |
| Verified | read + reproduced |

`compact` inserts the summary as a `RoleSystem` message directly after the
system prefix. On the next iteration `refreshPrefix` collects the *entire
leading run* of `RoleSystem` messages, compares it with what the
`Preamble` returns, and swaps the whole run out. The `cmd/hint` preamble
returns exactly one system message, so the run (prompt + summary) never
matches and the summary is dropped. `cmd/hint` always installs a Preamble
(`app.go:199`), so in production every compaction — proactive or the
retry after `ErrContextOverflow` — deletes the summarized history instead
of compressing it. The model continues with the prompt plus the tail only.

Fix: make the summary distinguishable from the prefix. Options, cheapest
first: (a) `refreshPrefix` remembers the length of the prefix it installed
and swaps only that many messages; (b) mark the summary with a dedicated
content part or a `RoleSystem` message flagged as compaction output that
`refreshPrefix` skips; (c) let `Preamble.Prefix` receive and return the
summary. Add a test in `internal/agent` that runs Preamble + compaction
together and asserts the summary survives the next request.

### B2 — Stale usage baseline after a reactive compaction: slice panic

| | |
|---|---|
| Severity | **blocker** |
| WP | WP0.4 |
| Where | [`internal/agent/loop.go:88-104`](../../internal/agent/loop.go#L88-L104), [`internal/agent/loop.go:191`](../../internal/agent/loop.go#L191) |
| Verified | read + reproduced |

When `streamOnce` reports `overflow`, the loop replaces `messages` with the
compacted history and does `iter--; continue`, but leaves `lastUsage` and
`usageBaseline` as they were. On the retry, `occupancy` evaluates
`messages[usageBaseline:]` against the shorter slice: `slice bounds out of
range` whenever `MaxContextTokens > 0`, the previous Stream returned usage,
and the compacted history is shorter than the old baseline. When the
bounds happen to hold, the stale `InputTokens` immediately triggers a
second, proactive compaction in the same iteration, violating the "one
attempt per iteration" rule stated in `compact.go:77`.
`TestRunTurn_OverflowCompactsAndRetries` sets `MaxContextTokens: 0` and
never reaches this path.

Fix: reset `lastUsage = agentapi.Usage{}` (and the baseline) after *any*
compaction, exactly as `refreshPrefix` already does; replace `iter--` with
an explicit inner retry loop so the invariant is local. Test with
`MaxContextTokens > 0` and a provider that reports usage then overflows.

### B3 — A complete last record without `\n` turns the session file unreadable

| | |
|---|---|
| Severity | **blocker** |
| WP | WP0.7 |
| Where | [`internal/session/session.go:167-178`](../../internal/session/session.go#L167-L178), [`internal/session/store.go:143-149`](../../internal/session/store.go#L143-L149) |
| Verified | read + reproduced |

The loader treats the last line as *torn* only when it fails to parse. If a
crash interrupts the write between the JSON and its `\n`, the record
parses, `torn` stays false, `validLen` includes the unterminated line,
`Open` skips `Truncate`, and the next `AppendMessage` writes the new record
immediately after the previous one on the same line. The next `Open` then
fails with `line N: invalid character '{' after top-level value` — a
recoverable crash becomes a permanently unreadable session, contrary to
the promise in `session/doc.go`.

Fix: the torn criterion is `!terminated`, not "did not parse". Either
ignore an unterminated last line even when it parses, or, before the first
write after load, append `\n` when `validLen` does not end in one. Add a
golden-style test: valid JSON without trailing newline → open → append →
reopen succeeds.

---

## Must

### M1 — `filepath.SkipAll` leaks out of `grep` on a single file

| | |
|---|---|
| Severity | must |
| WP | WP0.5 |
| Where | [`internal/tool/builtin/grep.go:226-241`](../../internal/tool/builtin/grep.go#L226-L241) |
| Verified | read + reproduced |

`visit` returns `filepath.SkipAll` once `maxGrepMatches` is exceeded. That
sentinel is meaningful only inside `filepath.WalkDir`; for a single-file
`path` the code calls `visit(target)` directly and hands the sentinel to
the model as an error result (`hay.txt: skip everything and stop the
walk`). `TestGrep_CapsMatches` covers the directory path only.

Fix: `errors.Is(err, filepath.SkipAll)` → `nil` on the single-file path,
or move "cap reached" out of `visit` into a flag. Add the single-file
> 200-matches case to the test.

### M2 — `readWhole` errors are all reported as "too large to diff"

| | |
|---|---|
| Severity | must |
| WP | WP0.5, WP0.6 |
| Where | [`internal/tool/builtin/fs.go:138-155`](../../internal/tool/builtin/fs.go#L138-L155), [`internal/tool/builtin/write.go:101-113`](../../internal/tool/builtin/write.go#L101-L113) |
| Verified | read + reproduced |

`readWhole` returns `info != nil` for every failure after `Stat`, including
a plain `os.ReadFile` error. `write_file.Describe` classifies "`err != nil
&& info != nil && !IsDir`" as *too large to diff* and asks the user to
confirm an overwrite of a file it could not read (reproduced with mode
`0o000`: summary `overwrite f.txt (1 bytes, was 4; too large to diff)`,
err nil). The user confirms without seeing that the file is unreadable.

Fix: sentinel errors (`errTooLarge`, `errIsDirectory`) from `readWhole`
and `errors.Is` at the call sites instead of classifying by the shape of
`(data, info, err)`. Test: unreadable file → Describe returns the read
error, no confirmation prompt.

---

## Should

### Error handling

- **S1 — `bash` attributes a parent deadline to its own timeout.**
  [`internal/tool/builtin/bash.go:155`](../../internal/tool/builtin/bash.go#L155).
  `errors.Is(ctx.Err(), context.DeadlineExceeded)` cannot tell the tool's
  timeout from the caller's; with a 300 ms parent context the result reads
  `[timed out after 1m0s; process killed]` (reproduced).
  [`internal/tool/limits.go:65-78`](../../internal/tool/limits.go#L65-L78)
  already uses `context.WithTimeoutCause` + `context.Cause`; do the same
  here and report a foreign deadline as `[canceled]` / a machinery error.
  WP0.5.
- **S2 — `edit_file.Describe` flattens a typed error into a string.**
  [`internal/tool/builtin/edit.go:169`](../../internal/tool/builtin/edit.go#L169)
  does `errors.New(res.Text())`, so `errors.Is(err, tool.ErrOutsideRoot)`
  is false for edit while [`write.go:97`](../../internal/tool/builtin/write.go#L97)
  keeps the chain; `describe_test.go:84` checks only write. `plan` should
  return `error`; `Run` wraps it into `ErrorResult`, not the reverse. WP0.5.
- **S3 — Permission gate swallows a Prompter failure.**
  [`internal/permission/gate.go:58-65`](../../internal/permission/gate.go#L58-L65)
  returns `(false, nil)` when the Prompter fails with a live context, so
  the model is told "the user did not approve" although nobody was asked.
  Contradicts the `Authorizer` contract in
  [`internal/agent/agent.go:66-69`](../../internal/agent/agent.go#L66-L69).
  Return the wrapped error (`%w`); fail-closed is preserved. WP0.6.
- **S4 — Cancellation reported as `ErrUnknown`.**
  [`internal/agent/iter.go:69`](../../internal/agent/iter.go#L69): a provider
  that closes its channel without a terminal event after ctx cancellation
  yields `FinishError` instead of `FinishCanceled` (reproduced). Check
  `ctx.Err()` before declaring a provider bug. WP0.4.
- **S5 — Provider events dereferenced without nil checks.**
  [`internal/agent/iter.go:53,55`](../../internal/agent/iter.go#L53): `*ev.Call`,
  `*ev.Usage`. Tools run under `recover`; the stream does not. Validate the
  event or nil-check → `EventError{ErrUnknown}`. WP0.4.
- **S6 — Typed nil in the one-shot exit path.**
  [`cmd/hint/turn.go:103-104`](../../cmd/hint/turn.go#L103-L104): `runErr =
  lastErr` where `lastErr *agentapi.Error` → non-nil interface holding a
  nil pointer, exit 1 with `hint: <nil>`. Latent today because
  `loop.go` always emits `EventError` before `FinishError`, but it relies
  on another package's discipline. Guard explicitly. WP0.9.
- **S7 — `${VAR}` that is unset silently expands to `""`.**
  [`internal/config/expand.go:9-14`](../../internal/config/expand.go#L9-L14),
  [`internal/config/load.go:372-380`](../../internal/config/load.go#L372-L380).
  A typo in `${API_BAR_KE}` produces "needs an API key" with no mention of
  the variable (pinned by `load_test.go:288-300`). Emit a `warnf` naming
  the variable. WP0.2.
- **S8 — `internal/config` has no sentinel or typed errors.**
  [`config.go:129,135`](../../internal/config/config.go#L129),
  [`load.go:621,631,664`](../../internal/config/load.go#L621); tests match
  with `strings.Contains` (`load_test.go:297,508,656,713,840`).
  `internal/session` already exports `ErrNoSessions`/`ErrUnknownSession`;
  config should do the same (`ErrNoProvider`, `ErrUnknownProfile`, or a
  `*ValidationError{Key}`) and tests should use `errors.Is/As`. WP0.2.
- **S9 — Error prefix stutter.** `config:` in
  [`load.go:56,268,272`](../../internal/config/load.go#L56) + `loading
  configuration: %w` in [`app.go:42`](../../cmd/hint/app.go#L42) + `hint:` in
  `main.go:76` → `hint: loading configuration: config: parsing …`. Per
  Go Code Review Comments a package prefix belongs only where nobody
  wraps the error. Drop one layer. WP0.2/WP0.9.
- **S10 — In-stream error chunks are always `ErrUnavailable`.**
  [`internal/provider/openai/stream.go:284-287`](../../internal/provider/openai/stream.go#L284-L287)
  ignores `wireError.Code`; a gateway that reports
  `context_length_exceeded` or 401 inside a 200 stream gets two retries
  and a failover instead of compaction or a user message. Route through
  `kindForStatus` / `isContextOverflow` (`status.go:55,79`). WP0.3.
- **S11 — `git` invocation depends on locale and loses the cause.**
  [`internal/project/git.go:42-55`](../../internal/project/git.go#L42-L55):
  "not a git repository" is matched as a substring of stderr, which git
  localizes (`LANG=ru_RU` → warning `.gitignore not applied` on every run
  outside a repo; not verified on a ru locale, none installed). No timeout
  above `ctx`; `fmt.Errorf("…: %s", msg)` drops `*exec.ExitError` /
  `context.DeadlineExceeded`. Set `LC_ALL=C`, or skip git when
  `project.go:158` already knows there is no `GitRoot`; use `%w`. WP0.8.

### Concurrency and context

- **S12 — Unconditional sends on the event channel.**
  [`internal/agent/loop.go:35`](../../internal/agent/loop.go#L35) and
  [`internal/agent/tools.go:147,166,182,189`](../../internal/agent/tools.go#L147):
  every `out <-` blocks forever if the consumer stops reading, even after
  ctx cancellation (reproduced: after `cancel()` the sender stays blocked).
  `RunTurn` does not document "drain until close"; tests work around it
  with `pump` (`schedule_loop_test.go:102-106`). Either
  `select { case out <- ev: case <-ctx.Done(): }` via an `emit` helper, or
  document the mandatory drain. WP0.4.
- **S13 — Data race on `timedOut`.**
  [`internal/provider/openai/stream.go:93-96,125`](../../internal/provider/openai/stream.go#L93-L125):
  written from a `time.AfterFunc` goroutine, read in `readStream`, the
  comment calls it "benign". The Go memory model has no benign races;
  `-race -count=20` is quiet only because `net/http`'s `bodyEOFSignal`
  mutex happens to order the accesses. Use `atomic.Bool` or a channel.
  WP0.3.
- **S14 — `ctx` is checked only for displayed lines in `read_file`.**
  [`internal/tool/builtin/read.go:144-149`](../../internal/tool/builtin/read.go#L144-L149):
  the `ctx.Err()` check sits after `continue`, so counting past `limit`
  in a 50 MB file ignores cancellation. Move the check above. WP0.5.
- **S15 — Windows shell path is broken and untested.**
  [`internal/tool/builtin/shell_windows.go:11`](../../internal/tool/builtin/shell_windows.go#L11):
  `exec.CommandContext("cmd.exe", "/C", script)` — Go quotes arguments
  per `CommandLineToArgvW`, which `cmd.exe` does not honour (`os/exec`
  docs: set `SysProcAttr.CmdLine` yourself); `killProcessTree` kills only
  the shell (no Job Object). All bash tests skip on Windows
  (`bash_test.go:16`). Fix via `CmdLine` + Job Object, or declare Windows
  unsupported for `bash` in README. WP0.5.
- **S16 — `TestBash_TimeoutKillsProcessGroup` does not test the kill.**
  [`internal/tool/builtin/bash_test.go:58`](../../internal/tool/builtin/bash_test.go#L58)
  only asserts `Run` returned within 5 s; `WaitDelay=2s` would release
  the pipe even if only the shell died. Observe the child: `sleep 30 &&
  touch marker` and assert the marker is absent. WP0.5.

### Persistence and files

- **S17 — No lock on the session file.**
  [`internal/session/store.go:115,151`](../../internal/session/store.go#L115):
  two `hint -c` in one directory interleave records of two histories into
  one file. Take an advisory lock (`syscall.Flock` / `x/sys/unix`, or an
  `O_EXCL` lock file) for the session's lifetime and return `ErrLocked`.
  WP0.7.
- **S18 — Summary cache key ignores budget and prompt; write is not atomic.**
  [`internal/project/summary.go:97-107`](../../internal/project/summary.go#L97-L107):
  key is the content SHA-256 only, so a different `maxBytes`, model or
  `summarySystemPrompt` serves a stale summary; `os.WriteFile` can be
  read half-written by a concurrent run. Add prompt version + budget to
  the key; write temp + `os.Rename`. WP0.8.
- **S19 — Golden file opened for writing by a test.**
  [`internal/session/session_test.go:137`](../../internal/session/session_test.go#L137)
  opens `testdata/golden_v1.jsonl` with `O_WRONLY|O_APPEND` (and would
  `Truncate` it if torn); fails in a read-only module cache. Copy to
  `t.TempDir()` or add a read-only `Store.Peek`. `ExampleRecorder`
  (`:541-544`) uses a fixed name in `os.TempDir()` with `O_EXCL`, ignores
  the `Create` error and leaks the directory — use `os.MkdirTemp`. WP0.7.
- **S20 — Byte slicing cuts UTF-8 runes.**
  [`internal/session/pick.go:96`](../../internal/session/pick.go#L96),
  [`internal/project/markdown.go:134`](../../internal/project/markdown.go#L134),
  [`internal/project/fit.go:153`](../../internal/project/fit.go#L153),
  [`internal/project/instructions.go:188`](../../internal/project/instructions.go#L188),
  [`internal/tool/builtin/read.go:152`](../../internal/tool/builtin/read.go#L152),
  [`internal/tool/builtin/grep.go:130`](../../internal/tool/builtin/grep.go#L130),
  [`internal/tool/builtin/bash.go:84`](../../internal/tool/builtin/bash.go#L84),
  [`cmd/hint/turn.go:218-220`](../../cmd/hint/turn.go#L218-L220). A Russian
  prompt in `hint sessions` or a Russian `HINT.md` under the budget ends
  in broken bytes (`json.Marshal` substitutes U+FFFD).
  [`internal/tool/truncate.go:56-63`](../../internal/tool/truncate.go#L56-L63)
  already does it right; extract `cutRunes(s, n)` and use it everywhere.
  WP0.5/WP0.7/WP0.8.
- **S21 — `Rule.Matches` recompiles globs on every touch; warnings overwrite.**
  [`internal/project/rules.go:63`](../../internal/project/rules.go#L63)
  calls `glob.Compile` per pattern per path although `readRule` (`:186`)
  already validated them; store compiled matchers in an unexported field.
  `:173,187` — `warn` is a single string, so only the last of several
  problems survives; use `[]string`. WP0.12.

### CLI, config, logging

- **S22 — Numeric and bool flags are strings.**
  [`internal/config/load.go:17-20`](../../internal/config/load.go#L17-L20),
  [`cmd/hint/main.go:199-203`](../../cmd/hint/main.go#L199-L203). The
  comment's justification ("cobra int flag cannot tell not-passed from
  0") is wrong: `cmd.Flags().Changed(name)` exists in pflag. Cost: manual
  `strconv` in config, `--help` shows `string`, config knows flag names
  for its error text. Typed flags + `Changed` → `*int`/`*bool`. WP0.9.
- **S23 — Flag errors print twice; exit codes are all 1.**
  [`cmd/hint/main.go:75-77,134-135`](../../cmd/hint/main.go#L75): `hint
  --bogus` prints cobra's `Error: unknown flag` + usage, then `hint:
  unknown flag`, because `SilenceErrors/SilenceUsage` are set inside
  `RunE`. Set them at construction (or `SetFlagErrorFunc`); use 2 for
  usage errors and 130 for SIGINT. WP0.9.
- **S24 — `run` reads the process environment directly and has no tests.**
  [`cmd/hint/app.go:40-68`](../../cmd/hint/app.go#L40-L68): `os.UserHomeDir`,
  `os.Getwd`, `os.LookupEnv`, `os.Args`, `time.Now`, `config.Load` instead
  of `LoadFrom`; only `assemble` is tested. `config.Options` already shows
  the pattern — lift an `environment{home, cwd, lookupEnv, args, now}` to
  `main`. The `UserHomeDir` error is swallowed in three places
  (`app.go:48-51`, `commands.go:495-497`, `load.go:50-53`); with an empty
  home `session.DefaultDir` (`store.go:32`) yields the *relative*
  `.local/share/hint/sessions`, silently inside the project. WP0.9.
- **S25 — Debug log: unbounded retention and full request bodies.**
  [`internal/debuglog/debuglog.go:261-275`](../../internal/debuglog/debuglog.go#L261-L275)
  creates a file per run, never pruned;
  [`internal/provider/openai/client.go:143`](../../internal/provider/openai/client.go#L143)
  logs the whole request body (the conversation and project files), only
  the key is masked. Prune by count or age in `Open`; document what the
  log contains. WP0.9.
- **S26 — Hand-rolled log sink instead of `log/slog`.**
  [`internal/provider/build.go:23`](../../internal/provider/build.go#L23),
  [`internal/provider/openai/client.go:46,66`](../../internal/provider/openai/client.go#L46):
  a `func(string)` threaded through three layers with `if c.log != nil`
  guards. Since Go 1.21 the standard is `*slog.Logger` (levels, structure,
  a discard handler). WP0.3.

### API design

- **S27 — A channel in the public SDK contract.**
  [`pkg/agentapi/provider.go:28`](../../pkg/agentapi/provider.go#L28):
  `Stream` returns `<-chan ChatEvent`, which fixes "goroutine per stream"
  and the rule "consumer must read or cancel, else leak" for every
  implementation and consumer (`stream.go:47-49`, `drain` in
  `router.go:259-264`). Go guidance: don't expose channels in an API. A
  pull iterator (`Next() bool / Event() / Err()`, like `bufio.Scanner`,
  `sql.Rows`) adapts to `iter.Seq` (Go 1.23) without a break. Given
  `doc.go:8` ("breaking changes here are exceptional") this is the one
  architectural decision worth revisiting before the API gains external
  users. WP0.1.
- **S28 — `WithLimits` zero values are unusable.**
  [`internal/agent/agent.go:129`](../../internal/agent/agent.go#L129):
  `WithLimits(Limits{})` gives `MaxIterations: 0` → immediate
  `ErrTurnLimit`; `CompactThreshold: 0` with a window → compaction every
  iteration; `New(nil, …)` is accepted and fails later in `llmCompactor`.
  Fill defaults per field inside `WithLimits` or validate in `New`. WP0.4.
- **S29 — Router test matrix silently skips a kind.**
  [`internal/provider/router/router_test.go:116-121`](../../internal/provider/router/router_test.go#L116-L121):
  `allKinds` lacks `ErrTurnLimit` (present in `pkg/agentapi/error.go:62`)
  and `TestKindTableIsComplete` only checks `Valid()` of the listed ones,
  so "fails loudly" in the comment is not true. Export the list of kinds
  from `agentapi` (or assert the count). WP0.3.

### Tests and determinism

- **S30 — `time.Sleep` as synchronization.**
  [`cmd/hint/main_test.go:519-525`](../../cmd/hint/main_test.go#L519),
  [`internal/console/lines_test.go:39,63,86`](../../internal/console/lines_test.go#L39),
  [`internal/permission/prompter_test.go:101,112`](../../internal/permission/prompter_test.go#L101).
  `TestLineReader_DiscardPending` passes vacuously if the goroutine is
  late by >50 ms. The rest of `internal/permission` uses rendezvous
  channels — do the same here.
- **S31 — No `t.Parallel()` anywhere** (0 of 356 tests) although nearly
  every test isolates through `t.TempDir()` / injected lookups. Cheap CI
  win; also surfaces hidden shared state.
- **S32 — Missing tests for the interactions above**: Preamble +
  compaction (B1), reactive compaction with usage (B2), unterminated but
  valid last line (B3), single-file grep over the cap (M1), unreadable
  file in `write_file.Describe` (M2), `Retry-After` as an HTTP date
  ([`status.go:143`](../../internal/provider/openai/status.go#L143), only
  the integer form is covered).

### Algorithms

- **S33 — Myers diff keeps a full copy of `v` per step.**
  [`internal/diff/diff.go:91`](../../internal/diff/diff.go#L91): `trace =
  append(trace, append([]int(nil), v...))` copies `2·limit+2` ints on each
  of up to `limit` steps: ~64 MB for `limit=2000` inside a permission
  prompt (`:34` acknowledges it). Keep only `v[-d..d]` per step, or
  switch to linear-space Myers ("middle snake"). WP0.6.
- **S34 — Scanner errors ignored in the ripgrep path.**
  [`internal/tool/builtin/grep.go:179-197`](../../internal/tool/builtin/grep.go#L179-L197):
  `sc.Err()` is never checked, so a ripgrep line > 4 MB (`ErrTooLong`)
  silently truncates the match list and the result looks complete.
  `grepFile:294` ignores `ErrTooLong` on purpose but says so; here use
  `bufio.Reader.ReadLine` or handle the error. WP0.5.

---

## Nits

Grouped by package. Each line: location — what — the idiom.

### `cmd/hint`, `internal/config`, `internal/console`, `internal/debuglog`

- `cmd/hint/turn.go:216` `const max = 120`, `internal/config/load.go:562`
  parameter `min int` — shadow the Go 1.21 builtins; revive's
  `redefines-builtin-id` is not enabled. Rename (`maxLen`, `minValue`).
- `cmd/hint/turn.go:47` double `append` → `slices.Concat`; `turn.go:66`
  `fmt.Fprint(stream, text)` → `io.WriteString`.
- `internal/config/load.go:562` `overlayInt(dst, key, envName, flagName,
  flagValue, min)` — six positional parameters, four of them strings;
  call sites (`:485,510,515,518`) are unreadable. Use a parameter struct.
- `internal/debuglog/debuglog.go:232,306` imports `internal/config` for
  the pure `Redact` function — inject `redact func(string) string` to
  remove the dependency of the log package on config.

### `internal/agent`, `internal/tool`, `internal/permission`

- `internal/tool/root.go:61-108` — TOCTOU between `Resolve` (symlink
  check) and the later open; unavoidable without `os.Root` (Go 1.24).
  Document it in the doc comment; prefer `Lstat` after open where cheap.
- `internal/tool/args.go:49-54` — manual `strings.Trim(s, "\"")` instead of
  `json.Unmarshal` into `string` (escapes not decoded);
  `ParseInt(…, 64)` into `int` truncates silently on 32-bit (the project
  targets Raspberry Pi) — use `strconv.IntSize`.
- `internal/tool/describe.go:91` parameter `max` shadows the builtin;
  `internal/agent/loop.go:149` `refreshPrefix(ctx, *[]Message, …)` — in/out
  through a pointer to a slice; returning `([]Message, bool)` is the
  idiom; `internal/agent/agent.go:184` `sort.Strings` → `slices.Sort`;
  `schemas()` is recomputed per Stream although `Agent` is immutable after
  `New`.
- `internal/agent/estimate.go:13` doc says "rune count", `:29` counts bytes
  (`len`). Bytes are the better token proxy — fix the comment.
- `internal/tool/truncate_test.go:22` `itoa` is a roundabout
  `fmt.Sprintf("%03d", i)`.

### `internal/tool/builtin`, `internal/diff`, `internal/glob`

- `internal/tool/builtin/fs.go:116` — `tmp.Chmod(0o644)` for a *new* file
  bypasses the umask (with `umask 077` the file becomes world-readable),
  unlike `os.WriteFile(…, 0o644)`. Create with
  `os.OpenFile(tmp, O_CREATE|O_EXCL, perm)`; for an existing file the
  current code is right.
- `internal/glob/glob.go:112` `sort.SliceStable` → `slices.SortStableFunc`
  + `cmp`; `builtin/instructions.go:217` `sort.Strings` → `slices.Sort`;
  `builtin/edit.go:275` double `append` → `slices.Concat`;
  `internal/diff/diff_test.go:125` `math/rand` → `math/rand/v2`.
- Parameters named `new` (`diff.go:46,193`, `edit.go:205`) and `max`
  (`edit.go:246`, `bash.go:78`) shadow builtins; `read.go:67` already uses
  the builtin `max`, so it is unavailable inside those functions.
- `builtin/bash.go:118` `cmd.Env = os.Environ()` is redundant (nil
  inherits). `Touches` is duplicated verbatim in `read.go:164`,
  `write.go:134`, `edit.go:467`. `helpers_test.go:94` comment names
  `sortedLines`, the function is `lineSet`.

### `internal/session`, `internal/project`

- `internal/session/store.go:246-248` `Store.List` skips a corrupt file
  silently although `Warnings` exists; each file is read whole for a
  listing (`summarize` → `load`). `:251` `sort.SliceStable` →
  `slices.SortStableFunc` + `time.Time.Compare`.
- `internal/project/instructions.go:88`, `rules.go:58`, `project.go:217` —
  three copies of `rel == ".." || HasPrefix(rel, "../")`; `isUnder`
  exists. `rules.go:147` `sort.Strings(names)` is redundant: `os.ReadDir`
  returns sorted entries.
- `internal/project/project.go:245` `var lookupGit` "for tests" — no test
  overrides it (external test package cannot) and `WithGit` already
  covers the case; it runs eagerly even when `WithGit` is passed.
  `instructions.go:128` `ReadInstructions` swallows `ctx`
  (`context.Background()`) and is used only by tests.
- `internal/project/summary.go:84-92` `DefaultSummaryCacheDir` reads
  `os.Getenv`/`os.UserHomeDir` directly while `session.DefaultDir(home,
  lookupEnv)` injects; untested.
- `internal/session/session.go:70`, `recorder.go:45` — the "copy" is
  shallow (`Content`/`ToolCalls` share backing arrays); `session.go:210`
  reports "is empty" when the first line is merely of an unknown kind;
  `fsync` only in `Close`, no directory fsync after `Create` — acceptable
  for a CLI, say so in `doc.go`.

### `pkg/agentapi`, `internal/provider`

- `pkg/agentapi/error.go:185-191` — `Is` equates any two `*Error` of the
  same Kind and needs a throwaway `NewError(kind, "")` as target. Either
  document "Is means same Kind" or rely on `KindOf`.
- `internal/provider/openai/client.go:102-106`,
  `internal/provider/models.go:44,102` — the `Transport` is cloned per
  `New` (a connection pool per profile and per `Models` call);
  `http.DefaultTransport.(*http.Transport)` panics when the transport is
  replaced (tracing, tests). Share one Transport at the composition root.
  `ollama/native.go:121` decodes without `LimitReader`, unlike
  `openai/models.go:55`.
- Idioms: `openai/stream.go:468` `sort.SliceStable` → `slices.SortStableFunc`;
  `router/router.go:26,276` `math/rand.Int63n` → `math/rand/v2` `rand.N`;
  `router.go:278-280` → `min(d, maxRetryDelay)`; `models.go:88`
  `fmt.Sprintf("%s%s")` → concatenation; `stream.go:430` → `"i" +
  strconv.Itoa`.
- Pass-through wrappers: `openai/status.go:16-18` `asAgentError` only
  forwards to `errors.As`; `router_test.go:66` `noSleep(t)` takes `t`
  only for `t.Helper()`.
- Tests: `openai/client_test.go:63-64` a single `r.Body.Read` may return a
  partial body — `io.ReadAll`; variable `cap` shadows the builtin;
  `agentapi/contract_test.go:188` `ev := ev` is redundant under `go 1.22`
  while `router.go:186` already relies on the 1.22 loop semantics — pick
  one convention.
- Internal helpers return `*agentapi.Error` (`openai/status.go:42,153`,
  `router.go:150`); `(*Error).WithProvider` on nil returns a typed nil
  (`error.go:155`). Every path is non-nil today (verified), but helpers
  should return `error` to keep the nil-interface trap out.

---

## Practices worth keeping (reference for new code)

- **Sealed interface + discriminated union**: the series-parallel schedule
  tree in [`internal/agent/schedule.go:15-28`](../../internal/agent/schedule.go#L15-L28);
  wire types with `Validate` and a forward-compat `ErrUnknownKind` in
  [`pkg/agentapi/message.go:65-74`](../../pkg/agentapi/message.go#L65-L74).
- **Consumer-side interfaces / dependency inversion**: `Compactor`,
  `Authorizer`, `Preamble`, `Estimator` in
  [`internal/agent/agent.go:47-84`](../../internal/agent/agent.go#L47-L84);
  `Ignorer`, `Overview`, `Summarizer` in `internal/project`;
  `ReadyChecker` in the router with an interface-upgrade type assertion.
- **Decorator + `Unwrap` convention** (after `errors.Unwrap`):
  [`internal/tool/limits.go:46-59`](../../internal/tool/limits.go#L46-L59).
- **Semaphore + WaitGroup + first-error + sibling cancel**, results by
  index without a mutex:
  [`internal/agent/tools.go:99-133`](../../internal/agent/tools.go#L99-L133).
- **`context.WithTimeoutCause` / `context.Cause`** to tell own timeout from
  a parent deadline: [`internal/tool/limits.go:65-78`](../../internal/tool/limits.go#L65-L78).
- **Sans-IO protocol decoder** for SSE, testable with string literals:
  [`internal/provider/openai/stream.go:217-235`](../../internal/provider/openai/stream.go#L217-L235).
- **Functional options with a test seam** (`withSleep`, `WithClock`,
  `WithIDs`): [`internal/session/store.go:46-67`](../../internal/session/store.go#L46-L67),
  [`internal/provider/openai/client.go:50-74`](../../internal/provider/openai/client.go#L50-L74).
- **Errors are values**: `Retryable`/`Fallbackable` as enum methods behind
  an exhaustive switch, table completeness pinned via `go/ast` in
  [`pkg/agentapi/error_test.go:63-92`](../../pkg/agentapi/error_test.go#L63-L92).
- **Atomic write** (temp in the same dir + `Sync` + `Rename`, mode kept):
  [`internal/tool/builtin/fs.go:96-135`](../../internal/tool/builtin/fs.go#L96-L135).
- **Plan/Apply**: `Describe` and `Run` computed from one `editPlan`, TOCTOU
  boundary documented: [`internal/tool/builtin/edit.go:44-125`](../../internal/tool/builtin/edit.go#L44-L125).
- **Environment injection**: `config.Options{HomeDir, WorkingDir,
  LookupEnv}` with nil-safe defaults
  ([`internal/config/load.go:41-46`](../../internal/config/load.go#L41-L46));
  `fs.FS` + `fstest.MapFS` for the overview walk.
- **Two-layer secret masking**: `fmt.Stringer` on the config
  ([`config.go:56-59`](../../internal/config/config.go#L56-L59)) plus a
  `redactor` `io.Writer` under the debug log, both covered by `TestTrace`.
- **Bounded I/O**: `io.LimitReader(limit+1)`, `bufio.Reader.ReadBytes`
  instead of `Scanner` and its 64 KB line limit, `BoundedBuffer` for
  subprocess output.

## Suggested issues

| Issue title | Items |
|---|---|
| `[Phase 0][WP0.4] Compaction summary survives prefix refresh; reset usage after compaction` | B1, B2, S28, S12, S4, S5 |
| `[Phase 0][WP0.7] Torn-tail detection by termination; session file lock` | B3, S17, S19 |
| `[Phase 0][WP0.5] grep SkipAll on single file; readWhole sentinels; bash timeout cause` | M1, M2, S1, S2, S14, S16, S34 |
| `[Phase 0][WP0.5] Windows bash: CmdLine quoting and Job Object, or declare unsupported` | S15 |
| `[Phase 0][WP0.6] Prompter errors propagate; diff memory` | S3, S33 |
| `[Phase 0][WP0.2] Typed config errors; warn on unset ${VAR}; typed flags` | S7, S8, S9, S22 |
| `[Phase 0][WP0.9] run() environment injection; exit codes; debug log retention` | S6, S23, S24, S25 |
| `[Phase 0][WP0.3] timedOut race; in-stream error kinds; slog; router kind table` | S13, S10, S26, S29 |
| `[Phase 0][WP0.8] git locale/timeout; summary cache key; rule glob cache` | S11, S18, S21 |
| `[Phase 0] UTF-8-safe truncation helper` | S20 |
| `[Phase 0] Tests: t.Parallel, remove time.Sleep sync, cover the interactions` | S30, S31, S32 |
| `[Phase 1][WP1.x] Pull iterator instead of a channel in ChatProvider.Stream` | S27 (schedule with the core-as-a-service work, when the API is next touched) |
| `[Phase 0] Nits batch` | the Nits section, one PR per package |
