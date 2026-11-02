# After v0.3.1 — implementation plan

> Status: Draft, not yet ratified · Created: 2026-11-01
> Target: `v0.3.2`, then Phase 2 / `v0.4`
> Scope source: §7 of [release-v0.3.1.md](release-v0.3.1.md#L176-L186),
> [PLAN.md](../../PLAN.md), [phase-0](phase-0-mvp-cli.md) and
> [phase-2](phase-2-memory-mcp.md). This file is a plan, not a second scope
> document: if it disagrees with `PLAN.md` or a phase file, those win.

This plan covers the three items left after `v0.3.1`: the `v0.3.2` patch
for [#32](https://github.com/mrYush/hint/issues/32), the UTF-8 truncation
of [#41](https://github.com/mrYush/hint/issues/41), and the start of
Phase 2 with the Q6 spike and WP2.1, the MCP client. It records the order,
the code and test changes, the release chores, and the documentation that
must change before the order becomes the project's own.

## State at writing

- `develop` and `release/v0.3.2` are at `5f95a57`, the merge of the
  `v0.3.1` release branch. The release branch was cut before its scope was
  complete; it currently carries no commits of its own.
- The working tree has an uncommitted documentation change in
  [release-v0.3.1.md](release-v0.3.1.md#L171): the merge-back box is
  ticked. It is unrelated to #32 and #41 and should not be mixed into
  their pull requests.
- The affected packages pass locally with a temporary `GOCACHE`. The full
  gate still has to run in CI on the pull requests.

## Order

| Step | Branch | Result | Estimate |
|---|---|---|---|
| 1 | `fix/compaction-refresh` | PR to `develop`, `Closes #32` | 1–2 days |
| 2 | `fix/utf8-truncation` | PR to `develop`, `Closes #41` | 1–2 days |
| 3 | `release/v0.3.2` | runbook, checks, PR to `main`, tag, merge back | 0.5–1 day |
| 4 | `docs/q6-mcp-spike` | spike document and Q6 decision | 2–3 days |
| 5 | `dev/wp2.1-mcp-client` | issue, branch line, first MCP slice | 3–4 weeks for WP2.1 |

Steps 1 and 2 are independent and can be written in parallel. Merge #32
first, then rebase or merge `develop` into #41 before its PR, because the
two changes touch the same README and phase-file paragraphs. If #41 does
not make `v0.3.2`, step 2 moves to `v0.3.3` and step 3 contains only #32.

## Alignment with PLAN.md

The order above follows §7 of the `v0.3.1` runbook. [PLAN.md](../../PLAN.md)
step 20 now records it: `v0.3.2` comes before Phase 2, B1 and B2 ship in
that patch, and B3, M1 and M2 stay backlog. #41 is in `v0.3.2` when it
lands before the tag.

- The runbook is an execution checklist and says that `PLAN.md` wins when
  the two disagree ([runbook header](release-v0.3.1.md#L6-L8)).
- Q6 → WP2.1 is consistent: Q6 blocks WP2.1, and WP2.1 is the first work
  package of Phase 2 ([PLAN.md](../../PLAN.md),
  [phase-2](phase-2-memory-mcp.md#L24-L35)).

Still open, and not part of the #32 change:

1. Add the #41 backlog line to WP0.5, WP0.7 and WP0.8 when that fix starts.
2. Write `docs/plan/release-v0.3.2.md` when the release branch is cut.
3. When WP2.1 starts, change the Phase 2 status to `In progress` and add
   its `**Branch:**` line.

This file stays a draft for those steps. `PLAN.md` remains the authority.

## v0.3.2: #32

### Branch and pull request

- Branch from `develop`: `fix/compaction-refresh`.
- PR to `develop`, body carries `Closes #32`, what changed, why, and what
  was run. One PR because both fixes change the same loop state and both
  are in the issue's acceptance.

### B1: the compaction summary must survive `refreshPrefix`

Today [`compact`](../../internal/agent/compact.go#L113-L116) inserts the
summary as an ordinary `RoleSystem` message directly after the prefix, and
[`refreshPrefix`](../../internal/agent/loop.go#L149-L168) replaces the
whole leading run of system messages with what `Preamble` returns.
`cmd/hint` always returns one message
([`rules.go`](../../cmd/hint/rules.go#L47-L85)), so the summary is dropped
on the next request. The same happens on the next turn, because a resumed
conversation starts with the summary and the RPC session prepends the
preamble in front of it
([`rpcengine.go`](../../cmd/hint/rpcengine.go#L488-L503)).

The recommended fix is the marker option from the issue, not the
prefix-length option:

1. Add `Summary bool` with `json:"summary,omitempty"` to
   [`agentapi.Message`](../../pkg/agentapi/message.go#L190-L211), plus a
   `SummaryMessage(text string) Message` constructor.
2. In [`Message.Validate`](../../pkg/agentapi/message.go#L254-L288),
   reject `Summary` on a non-system role. An empty summary message is
   already rejected by the existing empty-message rule.
3. In [`compact`](../../internal/agent/compact.go#L113-L116), insert
   `agentapi.SummaryMessage(summary)`.
4. In [`splitForCompaction`](../../internal/agent/compact.go#L164-L199),
   make the prefix the leading run of system messages **before the first
   summary**. The old summary then belongs to the body and is replaced by
   the new one instead of accumulating.
5. In [`refreshPrefix`](../../internal/agent/loop.go#L149-L168), stop the
   replaced run at the first summary and leave it in place.
6. For sessions written before this change, mark the leading
   system messages as summaries in
   [`Session.Messages`](../../internal/session/session.go#L87). A stored
   session has no preamble: [`Recorder.stripPreamble`](../../internal/session/recorder.go#L106-L117)
   removes it before writing, so a leading system message in the file is
   compaction output.

The smaller alternative is to replace exactly `len(prefix)` leading
messages and keep the summary after them. It avoids the public type
change, but it depends on a stable prefix count and leaves the old
summary in the prefix, so repeated compactions accumulate summaries. Use
it only if a patch release must not touch `pkg/agentapi`; in that case
open a follow-up issue for the accumulation before closing #32.

### B2: reset usage after any compaction

After a reactive compaction the loop does `iter--; continue`
([`loop.go`](../../internal/agent/loop.go#L88-L104)) without resetting
`lastUsage` and `usageBaseline`. The next [`occupancy`](../../internal/agent/loop.go#L189-L194)
call slices `messages[usageBaseline:]` on the shorter history: a panic
when the baseline is past the end, or an unnecessary second compaction
when the bounds happen to hold.

1. Replace `iter--; continue` with an explicit inner retry loop around
   `streamOnce`. One outer iteration is one logical round; a retry after
   overflow does not consume `MaxIterations`.
2. Reset `lastUsage = agentapi.Usage{}` and `usageBaseline = 0` after
   every successful compaction, proactive or reactive.
3. Guard `occupancy`: when `baseline > len(messages)`, estimate the whole
   history instead of slicing. The reset remains the invariant; the guard
   keeps a future path from crashing.
4. Delete the stale comment about `iter--` and say in the loop comment
   why a retry is not a new round.

Sketch:

```go
for {
    baseline := len(messages)
    res, err := a.streamOnce(ctx, messages, out)
    // handle err as today
    if !res.overflow {
        total = total.Add(res.usage)
        lastUsage = res.usage
        usageBaseline = baseline
        break
    }
    compacted, cerr := a.compact(ctx, messages, out)
    // handle cerr as today
    messages = compacted
    lastUsage = agentapi.Usage{}
    usageBaseline = 0
}
```

### Tests

B1:

- `TestRunTurn_CompactionSummarySurvivesPreambleRefresh`: a counting
  preamble, a proactive compaction, one more tool round, then a final
  answer. The third request must still carry the summary message.
- `TestRunTurn_SummaryAtStartOfHistorySurvivesPreamble`: history shaped
  like a resumed session, `[preamble, SummaryMessage("earlier"), user]`.
- `TestCompact_ReplacesPreviousSummary`: the compactor receives the old
  summary in the body, and the result holds exactly one summary.
- `TestMessageSummaryValidate` and a JSON round-trip case in
  [`pkg/agentapi`](../../pkg/agentapi/json_test.go#L49-L65).
- `TestSessionMarksLoadedSummary`: a compaction record loads with
  `Messages()[0].Summary == true`.

B2:

- `TestRunTurn_ReactiveCompactionWithWindowDoesNotPanic`: two tool rounds
  with usage, then `ErrContextOverflow`, then a successful answer; exactly
  one compaction and `FinishStop`. A message-counting estimator with
  `MaxContextTokens: 9` and `CompactThreshold: 0.9` reproduces the old
  panic deterministically: the second tool round leaves
  `usageBaseline = 6`, and compaction shortens the history to five
  messages.
- `TestRunTurn_ReactiveCompactionDoesNotCompactTwice`: exactly one
  `EventCompaction`.
- `TestOccupancy_StaleBaselineFallsBackToEstimate`, if the guard stays.

Update `TestRunTurn_ProactiveCompaction`,
`TestRunTurn_OverflowCompactsAndRetries` and
`TestCompact_SummarizesBodyBetweenPrefixAndTail` for the new message
shape.

### Documentation and acceptance

- [README.md](../../README.md#L676-L705): drop items 1 and 2 from Known
  issues, renumber the rest, and add a `Fixed in v0.3.2` note: the summary
  was lost after `refreshPrefix`, and a reactive compaction after a
  context-overflow error could panic with `slice bounds out of range`
  when `context_window` was set. Both present since `v0.2.0-alpha`.
- [phase-0-mvp-cli.md](phase-0-mvp-cli.md#L261-L268): tick B1 and B2.
- [PLAN.md](../../PLAN.md): step 20 and the current status.
- [release-v0.3.1.md](release-v0.3.1.md#L176-L186): tick the `v0.3.2`
  item after the release.

Acceptance: the B1 and B2 tests fail on the old code and pass on the new;
the full gate is green; the README no longer lists the two bugs; the PR
says `Closes #32`.

Out of scope for this patch: S4, S5, S12 and S28 from #32. They are
separate hardening, and S28 (`WithLimits` zero values) changes the
public `Limits` contract; keep it out of a patch.

## #41: one UTF-8-safe truncation helper

### Branch and pull request

- Branch from `develop`: `fix/utf8-truncation`.
- PR to `develop`, `Closes #41`.
- Merge after #32 to avoid conflicts in README and the phase file.

### Helper

New package `internal/text`, with no project dependencies and only
`unicode/utf8`:

```go
// Cut returns the longest prefix of s of at most n bytes that ends on a
// UTF-8 rune boundary. n <= 0 returns ""; n >= len(s) returns s.
func Cut(s string, n int) string
```

`Cut` is a rune boundary, not a grapheme-cluster boundary: combining marks
stay separate. Document that. The existing
[`trimToLineEnd`/`trimToLineStart`](../../internal/tool/truncate.go#L54-L77)
stay the reference for head-and-tail truncation; after `text.Cut` lands,
`Truncate` may use it for the head slice without changing the marker
format.

### Sites

| File | Today | Change |
|---|---|---|
| [`internal/session/pick.go`](../../internal/session/pick.go#L86-L101) | `s[:max]` | `text.Cut(s, max)` |
| [`internal/project/markdown.go`](../../internal/project/markdown.go#L125-L138) | `s[:maxFirstSentence]`, then `LastIndex(" ")` | cut first, then search the space in the cut string; `cut = len(head)` when no space |
| [`internal/project/fit.go`](../../internal/project/fit.go#L140-L154) | `s[:limit]` in three places | `head := text.Cut(s, limit)`; search `head`; return `head` |
| [`internal/project/instructions.go`](../../internal/project/instructions.go#L177-L191) | `string(data[:limit])` | `text.Cut(string(data), limit)`; keep `truncated` on `len(data) > limit` |
| [`internal/tool/builtin/read.go`](../../internal/tool/builtin/read.go#L149-L153) | `line[:maxLineLength]` | `text.Cut(line, maxLineLength)` |
| [`internal/tool/builtin/grep.go`](../../internal/tool/builtin/grep.go#L127-L132) | `text[:maxGrepLine]` | `text.Cut(text, maxGrepLine)` |
| [`internal/tool/builtin/bash.go`](../../internal/tool/builtin/bash.go#L78-L89) | `s[:max]` | `text.Cut(s, max)` |
| [`internal/shell/turn.go`](../../internal/shell/turn.go#L184-L192) | `s[:max]` | `text.Cut(s, max)` |
| [`internal/tool/describe.go`](../../internal/tool/describe.go#L90-L101) | `s[:max]` | `text.Cut(s, max)` |

Audit and leave alone, with the reason in the PR:

- [`internal/config/mask.go`](../../internal/config/mask.go#L17-L22) — API
  keys are ASCII by contract; this is masking, not truncation.
- `internal/console/lines.go`, `internal/tui/render.go` (`short`),
  `internal/diff`, `internal/tool/builtin/edit.go`,
  `internal/session/turns.go` — cuts at ASCII delimiters, on ids, or on
  slices of messages.
- `cmd/hint/rpccomplete.go` (`cutPrefixFold`) — already iterates over
  `range s`, so it cuts on rune boundaries.

### Tests and acceptance

- `internal/text/text_test.go`: a table with ASCII, Cyrillic, `→` and
  emoji; `n = 0`, negative, `n = len(s)`, and every byte inside a rune;
  assert the exact prefix and `utf8.ValidString`.
- `project`: `TestExcerptCutsRuneBoundary`,
  `TestCutLinesCutsRuneBoundary`, `TestReadBoundedCutsRuneBoundary`.
- `session`: `TestFirstLineOfCutsRuneBoundary` (same-package test) and/or
  a `Choose` listing test with a Cyrillic prompt.
- `builtin`: `TestReadTruncatesLineOnRuneBoundary`,
  `TestGrepTruncatesLineOnRuneBoundary`,
  `TestBashDescribeCutsCommandOnRuneBoundary`.
- `tool`: `TestDescribeCutsArgsOnRuneBoundary`.
- `shell`: `TestSummarizeArgsCutsOnRuneBoundary`.
- Assert both `utf8.ValidString` and the absence of `\uFFFD`; the latter
  is what `encoding/json` substitutes.
- Add the #41 backlog line to the affected Phase 0 packages and tick it
  in this PR.

Acceptance: no listed site cuts a valid UTF-8 string, ASCII output is
unchanged, and the full gate is green.

## Release v0.3.2

1. After #32 (and #41, if it is in this release) merges into `develop`,
   merge `develop` into `release/v0.3.2`. Do not develop on the release
   branch; do not rebase or force-push it.
2. Add `docs/plan/release-v0.3.2.md`, a short runbook:
   - scope: #32, plus #41 when it lands;
   - pre-flight: CI on `develop` green; secrets unchanged
     (`HOMEBREW_TAP_TOKEN` is valid until 2027-10-28,
     `SCOOP_BUCKET_TOKEN`, `APT_SIGNING_KEY`, `APT_SIGNING_PASSPHRASE`);
   - `goreleaser check` and `goreleaser release --snapshot --clean`;
     inspect archives, the two `.deb` packages, the cask and the Scoop
     manifest;
   - PR `release/v0.3.2` → `main` with a merge commit, not squash;
   - annotated tag `v0.3.2` on the merge commit, push the tag;
   - verify the GitHub Release, cask, Scoop, apt, `hint -v` from an
     archive, and `go install github.com/mrYush/hint/cmd/hint@v0.3.2`;
   - close #32 (and #41), tick §7 of the `v0.3.1` runbook, merge back into
     `develop`, delete the release branch.
3. Update README: the remaining Known issues, the Limits line, and the
   `Fixed in v0.3.2` note.
4. Update `PLAN.md`: step 20 done, `v0.3.2` is the latest release, next is
   Phase 2 / `v0.4`.
5. If #41 did not make it, say so in the release notes and keep #41 open
   for `v0.3.3`.

## Q6 spike: MCP client

### Branch and document

- Branch from `develop`: `docs/q6-mcp-spike`.
- New `docs/plan/mcp-client-spike.md` with `Status: Design spike`, the
  Q6 number, the date, the alternatives and their price. No branch line
  and no checkboxes: this is a design spike, like
  [subscription-access.md](subscription-access.md).
- Add the file to the design-spike table in
  [docs/plan/README.md](README.md).

### Questions the spike answers

1. **Spec baseline.** The current MCP specification and the supported
   versions; the methods WP2.1 needs: `initialize`,
   `notifications/initialized`, `ping`, `tools/list`, `tools/call`,
   `resources/list`, `resources/read`, `prompts/list`, `prompts/get`,
   cancellation and timeouts. Transports: stdio and Streamable HTTP
   ([transport specification](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)).
2. **SDK.** The baseline is the official
   [`modelcontextprotocol/go-sdk`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk):
   at the time of writing pkg.go.dev serves v1.8.0, Apache-2.0/MIT, a Go
   module. Check the Go version, CGO, context and timeout support, the
   in-memory and stdio transports, the test server, raw JSON Schema
   handling, error types and dependency size. The fallback is a thin
   JSON-RPC client or `mcp-go`.
3. **Catalog probe.** Eight to twelve public or reference servers
   (filesystem, git, fetch, memory, sequential thinking, GitHub and
   similar): connect, `tools/list`, count tools and schema bytes, call one
   read-only tool. Record transport, auth, quirks and failures.
4. **Context budget.** Measure the token cost of the tool schemas and
   decide whether WP2.1 needs a schema/description cap, hidden tools or
   lazy disclosure. This is the architecture's Q6 implication: MCP is an
   ecosystem bet, not a context-budget one.
5. **Decision.** A matrix of SDK versus thin client over spec coverage,
   transports, auth, dependencies and license, testability, maintenance
   and budget hooks. Record the chosen version and the rejected
   alternatives.
6. **Docs.** `PLAN.md` Q6 becomes resolved with a link to the spike;
   Phase 2 records the decision; `risks.md` gets an entry only if the
   spike leaves a real risk.

Acceptance: WP2.1 can start without choosing the SDK again, and every
argument has a measured price.

## WP2.1: MCP client

### Branch, issue and phase file

1. Create `[Phase 2][WP2.1] MCP client` from the work-package template.
   The body repeats `**Branch:** dev/wp2.1-mcp-client`, the scope and the
   acceptance criteria.
2. Add the `**Branch:**` line to
   [phase-2](phase-2-memory-mcp.md#L24-L35) and move the phase status to
   `In progress`.
3. Branch from `develop`: `dev/wp2.1-mcp-client`. The first commit is the
   documentation change (branch line and Q6 decision), then code.
4. PR to `develop`, `Closes #N`. Agree on the PR granularity before
   starting: one large PR for all of WP2.1, or separate PRs for M0–M3 and
   M4–M5 under new work-package numbers. The repository's rule is one
   issue, one branch, one PR, so a split must be planned, not improvised
   after review starts.

### Milestones

| Milestone | Work | Files | Check |
|---|---|---|---|
| M0. Skeleton | `internal/mcp`: `Client`, `ServerConfig`, `Manager`, `Tool` adapter; a fake MCP server | `internal/mcp/*.go` | connect/list/call/error/timeout/restart on the fake server |
| M1. Config | `mcp_servers:` in `internal/config`; schema, global/project merge, `${VAR}`, secrets | [load.go](../../internal/config/load.go#L205-L222), [schema.go](../../internal/config/schema.go) | merge, project without keys, missing variable, redaction |
| M2. Lifecycle and CLI | lazy start, ping/health, restart backoff, `hint mcp list` and `hint mcp status` | `internal/mcp`, [main.go](../../cmd/hint/main.go#L252), new `cmd/hint/mcp.go` | running/failed status, no startup block, restart |
| M3. Tools | `tools/list` → `<server>.<tool>`, `tools/call`, permission class, `tool.WithLimits`, registry | `internal/mcp`, `internal/tool`, `cmd/hint` | permission gate, read class, error result, truncation, collision |
| M4. Resources | config-named resources as instruction files; `resource_read` for model requests | `internal/mcp`, `internal/project`, `cmd/hint` | budget and outline, untrusted data, fake resource server |
| M5. Prompts | `/prompts`, `/<server>:<prompt>`, argument completion | `internal/mcp`, RPC/TUI | list/get/expand, schema completion |
| M6. Acceptance | integration test, docs, race, cross-build | all | WP2.1 acceptance criteria |

### Configuration shape

```yaml
mcp_servers:
  filesystem:
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    permission: read
    timeout: 30s
  github:
    transport: http
    url: https://api.githubcopilot.com/mcp/
    headers:
      Authorization: "Bearer ${GITHUB_TOKEN}"
    permission: execute
```

Rules:

- The global file holds `env`, `headers` and credentials. A project file
  may add a server but may not set `env`/`headers` or override a key;
  otherwise a cloned repository can exfiltrate the developer's variables.
- A project-added server starts a process or opens a network connection
  from a cloned repository. Gate its first start like an execute-class
  action, or keep project-added servers disabled by default until the
  user enables them. Record the decision in Phase 2.
- `permission` defaults to `execute`; `read` and `write` are explicit.
- `Config.Secrets()` includes `headers` and `env` values; `Redact` hides
  them. A stdio server receives only its declared `env` over a filtered
  environment, never hint's API keys.
- MCP tool output is data, not instructions. A resource named in the
  config may be loaded as an instruction file because the user chose it;
  a model-requested `resource_read` returns data only.
- Every MCP tool is wrapped in
  [`tool.WithLimits`](../../internal/tool/limits.go#L37-L90): timeout and
  truncation, like the built-ins.

### Context budget and tests

- Use the spike's measurement to set a schema cap. If the MVP cannot fit
  the tool schemas, cap descriptions and warn; full lazy disclosure is
  WP2.5 work, not a reason to block WP2.1.
- CI uses a Go fake MCP server over stdio: tools, resources, prompts,
  permission, timeout, restart, truncation and redaction.
- The live filesystem-server test is optional and skips when `npx` is
  absent; CI must not depend on the network.
- Run `go test -race ./internal/mcp/...` and
  `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`.

### Estimate

- M0–M3 (walking skeleton and tools): 1.5–2 weeks.
- M4–M5 (resources and prompts): 1.5–2 weeks.
- M6, docs and hardening: 2–3 days.
- Whole WP2.1: about 3–4 weeks, consistent with the 8–10 weeks of
  Phase 2.

## Risks and open decisions

| Risk | Mitigation |
|---|---|
| The `Message.Summary` field changes the public contract | Optional JSON field; the repository already treats optional fields as non-breaking (`json_test.go`); add round-trip and validation tests. If a patch must not touch `pkg/agentapi`, use the prefix-length fallback and file the accumulation issue. |
| Old sessions have no marker | Mark leading system messages in `Session.Messages`; otherwise a session saved right after compaction loses its summary once on upgrade. |
| Repeated compaction accumulates summaries | The marker makes the old summary part of the body, so the new one replaces it. |
| #41 changes output | `Cut` returns the same bytes for ASCII; tests pin rune boundaries and the absence of `\uFFFD`. |
| MCP SDK churn or weight | Spike pins a version and measures the dependency; `internal/mcp` isolates it; thin client is the fallback. |
| A project file starts an arbitrary MCP server | First start is confirmed; credentials stay in the global file. |
| MCP schemas fill the prompt | Measure in the spike; cap or hide tools; full solution is WP2.5. |
| One huge WP2.1 PR | Decide the split before starting; a split needs new work-package numbers. |

Decisions to confirm before implementation:

1. B1: the `Message.Summary` marker (recommended) or the minimal
   prefix-length fallback.
2. #41: in `v0.3.2` or a separate `v0.3.3`.
3. What to do with the uncommitted `release-v0.3.1.md` edit: a separate
   docs commit, a stash, or leave it for the release.
4. WP2.1: one large PR or separate M0–M3 and M4–M5 work packages.

## Verification

After each change:

```bash
gofmt -l .
go test ./...
go vet ./...
golangci-lint run ./...
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...
go test -race ./...
```

Before the tag:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

A pull request is not done until the full gate is green, the phase file
and `PLAN.md` agree with the change, and the release notes name the
fixed payload and the version it was present since.
