# After v0.3.1 — implementation plan

> Status: steps 1–5 done; WP2.1 M4–M6 remain · Created: 2026-11-01
> Last updated: 2026-11-04 · Target: Phase 2 / `v0.4`
> Scope source: §7 of [release-v0.3.1.md](release-v0.3.1.md), [PLAN.md](../../PLAN.md), [phase-0](phase-0-mvp-cli.md) and [phase-2](phase-2-memory-mcp.md).
> This file is a plan, not a second scope document: if it disagrees with `PLAN.md` or a phase file, those win.

This plan covers the three items left after `v0.3.1`: the `v0.3.2` patch for [#32](https://github.com/mrYush/hint/issues/32), the UTF-8 truncation of [#41](https://github.com/mrYush/hint/issues/41), and the start of Phase 2 with the Q6 spike and WP2.1, the MCP client.
Steps 1–5 are done; the body below records the plan as it was written.

## State (2026-11-04)

- `develop` is at `ffc6a18`; `v0.3.2` is tagged on `main`, and the release documentation is final.
- #32 (PR [#82](https://github.com/mrYush/hint/pull/82)) and #41 (PR [#83](https://github.com/mrYush/hint/pull/83)) are merged and shipped in `v0.3.2`.
- Q6 is settled by [mcp-client-spike.md](mcp-client-spike.md); WP2.1 M0–M3 and M2 are merged in PR [#87](https://github.com/mrYush/hint/pull/87).

## Order

| Step | Branch | State | Result | Estimate |
|---|---|---|---|---|
| 1 | `fix/compaction-refresh` | done | PR [#82](https://github.com/mrYush/hint/pull/82), `Closes #32` | 1–2 days |
| 2 | `fix/utf8-truncation` | done | PR [#83](https://github.com/mrYush/hint/pull/83), `Closes #41` | 1–2 days |
| 3 | `release/v0.3.2` | done | PR [#84](https://github.com/mrYush/hint/pull/84) to `main`; tag `v0.3.2` | 0.5–1 day |
| 4 | `docs/q6-mcp-spike` | done | spike document and Q6 decision; see [mcp-client-spike.md](mcp-client-spike.md) | 2–3 days |
| 5 | `dev/wp2.1-mcp-client` | done | PR [#87](https://github.com/mrYush/hint/pull/87), `Refs #86`: M0–M3 and M2; M4–M6 remain in WP2.1 | 3–4 weeks for WP2.1 |

Steps 1–5 are done.
WP2.1 continues with M4–M6; Q6 no longer blocks it.

## Alignment with PLAN.md

[PLAN.md](../../PLAN.md) step 20 records the order: `v0.3.2` comes before Phase 2, B1 and B2 ship in that patch, and B3, M1 and M2 stay backlog.
#41 is in `v0.3.2` too ([PLAN.md](../../PLAN.md)).

- The runbook is an execution checklist and says that `PLAN.md` wins when the two disagree ([runbook header](release-v0.3.2.md)).
- Q6 is resolved by the [spike](mcp-client-spike.md); WP2.1 is the first work package of Phase 2 ([phase-2](phase-2-memory-mcp.md#wp21--mcp-client)).

Done since this plan was written:

1. The #41 backlog lines are in WP0.5, WP0.7 and WP0.8.
2. `docs/plan/release-v0.3.2.md` is written and committed.
3. The README, `PLAN.md` and Phase 0 status texts are updated.
4. Q6 is resolved by the [spike](mcp-client-spike.md).

Still open:

1. WP2.1 continues after PR [#87](https://github.com/mrYush/hint/pull/87) merged M0–M3 and M2 into `develop`: MCP resources and prompts (M4–M5), the live reference-server test and the final acceptance pass (M6) remain on a follow-up branch.

## v0.3.2: #32 — done

Implemented on `fix/compaction-refresh`; merged in PR [#82](https://github.com/mrYush/hint/pull/82).
The body below records the change as planned.

### Branch and pull request

- Branch from `develop`: `fix/compaction-refresh`.
- PR to `develop`, body carries `Closes #32`, what changed, why, and what was run.
  One PR because both fixes change the same loop state and both are in the issue's acceptance.

### B1: the compaction summary must survive `refreshPrefix`

Today [`compact`](../../internal/agent/compact.go#L113-L116) inserts the summary as an ordinary `RoleSystem` message directly after the prefix, and [`refreshPrefix`](../../internal/agent/loop.go#L149-L168) replaces the whole leading run of system messages with what `Preamble` returns.
`cmd/hint` always returns one message ([`rules.go`](../../cmd/hint/rules.go#L47-L85)), so the summary is dropped on the next request.
The same happens on the next turn, because a resumed conversation starts with the summary and the RPC session prepends the preamble in front of it ([`rpcengine.go`](../../cmd/hint/rpcengine.go#L488-L503)).

The recommended fix is the marker option from the issue, not the prefix-length option:

1. Add `Summary bool` with `json:"summary,omitempty"` to [`agentapi.Message`](../../pkg/agentapi/message.go#L190-L211), plus a `SummaryMessage(text string) Message` constructor.
2. In [`Message.Validate`](../../pkg/agentapi/message.go#L254-L288), reject `Summary` on a non-system role.
   An empty summary message is already rejected by the existing empty-message rule.
3. In [`compact`](../../internal/agent/compact.go#L113-L116), insert `agentapi.SummaryMessage(summary)`.
4. In [`splitForCompaction`](../../internal/agent/compact.go#L164-L199), make the prefix the leading run of system messages **before the first summary**.
   The old summary then belongs to the body and is replaced by the new one instead of accumulating.
5. In [`refreshPrefix`](../../internal/agent/loop.go#L149-L168), stop the replaced run at the first summary and leave it in place.
6. For sessions written before this change, mark the leading system messages as summaries in [`Session.Messages`](../../internal/session/session.go#L87).
   A stored session has no preamble: [`Recorder.stripPreamble`](../../internal/session/recorder.go#L106-L117) removes it before writing, so a leading system message in the file is compaction output.

The smaller alternative is to replace exactly `len(prefix)` leading messages and keep the summary after them.
It avoids the public type change, but it depends on a stable prefix count and leaves the old summary in the prefix, so repeated compactions accumulate summaries.
Use it only if a patch release must not touch `pkg/agentapi`; in that case open a follow-up issue for the accumulation before closing #32.

### B2: reset usage after any compaction

After a reactive compaction the loop does `iter--; continue` ([`loop.go`](../../internal/agent/loop.go#L88-L104)) without resetting `lastUsage` and `usageBaseline`.
The next [`occupancy`](../../internal/agent/loop.go#L189-L194) call slices `messages[usageBaseline:]` on the shorter history: a panic when the baseline is past the end, or an unnecessary second compaction when the bounds happen to hold.

1. Replace `iter--; continue` with an explicit inner retry loop around `streamOnce`.
   One outer iteration is one logical round; a retry after overflow does not consume `MaxIterations`.
2. Reset `lastUsage = agentapi.Usage{}` and `usageBaseline = 0` after every successful compaction, proactive or reactive.
3. Guard `occupancy`: when `baseline > len(messages)`, estimate the whole history instead of slicing.
   The reset remains the invariant; the guard keeps a future path from crashing.
4. Delete the stale comment about `iter--` and say in the loop comment why a retry is not a new round.

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

- `TestRunTurn_CompactionSummarySurvivesPreambleRefresh`: a counting preamble, a proactive compaction, one more tool round, then a final answer.
  The third request must still carry the summary message.
- `TestRunTurn_SummaryAtStartOfHistorySurvivesPreamble`: history shaped like a resumed session, `[preamble, SummaryMessage("earlier"), user]`.
- `TestCompact_ReplacesPreviousSummary`: the compactor receives the old summary in the body, and the result holds exactly one summary.
- `TestMessageSummaryValidate` and a JSON round-trip case in [`pkg/agentapi`](../../pkg/agentapi/json_test.go#L49-L65).
- `TestSessionMarksLoadedSummary`: a compaction record loads with `Messages()[0].Summary == true`.

B2:

- `TestRunTurn_ReactiveCompactionWithWindowDoesNotPanic`: two tool rounds with usage, then `ErrContextOverflow`, then a successful answer; exactly one compaction and `FinishStop`.
  A message-counting estimator with `MaxContextTokens: 9` and `CompactThreshold: 0.9` reproduces the old panic deterministically: the second tool round leaves `usageBaseline = 6`, and compaction shortens the history to five messages.
- `TestRunTurn_ReactiveCompactionDoesNotCompactTwice`: exactly one `EventCompaction`.
- `TestOccupancy_StaleBaselineFallsBackToEstimate`, if the guard stays.

Update `TestRunTurn_ProactiveCompaction`, `TestRunTurn_OverflowCompactsAndRetries` and `TestCompact_SummarizesBodyBetweenPrefixAndTail` for the new message shape.

### Documentation and acceptance

- [README.md](../../README.md#known-issues): drop items 1 and 2 from Known issues, renumber the rest, and add a `Fixed in v0.3.2` note: the summary was lost after `refreshPrefix`, and a reactive compaction after a context-overflow error could panic with `slice bounds out of range` when `context_window` was set.
  Both present since `v0.2.0-alpha`.
- [phase-0-mvp-cli.md](phase-0-mvp-cli.md#L261-L268): tick B1 and B2.
- [PLAN.md](../../PLAN.md): step 20 and the current status.
- [release-v0.3.1.md](release-v0.3.1.md): tick the `v0.3.2` item after the release.

Acceptance: the B1 and B2 tests fail on the old code and pass on the new; the full gate is green; the README no longer lists the two bugs; the PR says `Closes #32`.

Out of scope for this patch: S4, S5, S12 and S28 from #32.
They are separate hardening, and S28 (`WithLimits` zero values) changes the public `Limits` contract; keep it out of a patch.

## #41: one UTF-8-safe truncation helper — done

Implemented on `fix/utf8-truncation`; merged in PR [#83](https://github.com/mrYush/hint/pull/83).
The body below records the change as planned.

### Branch and pull request

- Branch from `develop`: `fix/utf8-truncation`.
- PR to `develop`, `Closes #41`.
- Merge after #32 to avoid conflicts in README and the phase file.

### Helper

New package `internal/text`, with no project dependencies and only `unicode/utf8`:

```go
// Cut returns the longest prefix of s of at most n bytes that ends on a
// UTF-8 rune boundary. n <= 0 returns ""; n >= len(s) returns s.
func Cut(s string, n int) string
```

`Cut` is a rune boundary, not a grapheme-cluster boundary: combining marks stay separate.
Document that.
The existing [`trimToLineEnd`/`trimToLineStart`](../../internal/tool/truncate.go#L54-L77) stay the reference for head-and-tail truncation; after `text.Cut` lands, `Truncate` may use it for the head slice without changing the marker format.

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

- [`internal/config/mask.go`](../../internal/config/mask.go#L17-L22) — API keys are ASCII by contract; this is masking, not truncation.
- `internal/console/lines.go`, `internal/tui/render.go` (`short`), `internal/diff`, `internal/tool/builtin/edit.go`, `internal/session/turns.go` — cuts at ASCII delimiters, on ids, or on slices of messages.
- `cmd/hint/rpccomplete.go` (`cutPrefixFold`) — already iterates over `range s`, so it cuts on rune boundaries.

### Tests and acceptance

- `internal/text/text_test.go`: a table with ASCII, Cyrillic, `→` and emoji; `n = 0`, negative, `n = len(s)`, and every byte inside a rune; assert the exact prefix and `utf8.ValidString`.
- `project`: `TestExcerptCutsRuneBoundary`, `TestCutLinesCutsRuneBoundary`, `TestReadBoundedCutsRuneBoundary`.
- `session`: `TestFirstLineOfCutsRuneBoundary` (same-package test) and/or a `Choose` listing test with a Cyrillic prompt.
- `builtin`: `TestReadTruncatesLineOnRuneBoundary`, `TestGrepTruncatesLineOnRuneBoundary`, `TestBashDescribeCutsCommandOnRuneBoundary`.
- `tool`: `TestDescribeCutsArgsOnRuneBoundary`.
- `shell`: `TestSummarizeArgsCutsOnRuneBoundary`.
- Assert both `utf8.ValidString` and the absence of `\uFFFD`; the latter is what `encoding/json` substitutes.
- Add the #41 backlog lines to the affected Phase 0 packages; they are ticked now.

Acceptance: no listed site cuts a valid UTF-8 string, ASCII output is unchanged, and the full gate is green.

## Release v0.3.2 — done

The release checklist is [release-v0.3.2.md](release-v0.3.2.md).
`v0.3.2` is tagged on `main`; the checklist remains the record.
Do not duplicate it here.

## Q6 spike: MCP client — done

Done 2026-11-03.
The result is [mcp-client-spike.md](mcp-client-spike.md): spec baseline `2026-07-28`, official `go-sdk` `v1.8.0`, transports and auth surface, 48 KiB budget and the WP2.1 decisions.
Q6 is resolved in [PLAN.md](../../PLAN.md).
No further research is needed; start WP2.1 from the spike and the Phase 2 decisions.

## WP2.1: MCP client — in progress

Progress (2026-11-04): M0–M3 and M2 are implemented on `dev/wp2.1-mcp-client` and merged in PR [#87](https://github.com/mrYush/hint/pull/87) — the client, config, manager, tool adapter, schema budget, lazy start on the first turn, the `ping` health check, and `hint mcp list|status|enable`.
M4–M6 remain and take a follow-up branch.

### Branch, issue and phase file

The issue is [#86](https://github.com/mrYush/hint/issues/86); the phase file carries the branch line and `In progress`.

1. Create `[Phase 2][WP2.1] MCP client` from the work-package template.
   The body repeats `**Branch:** dev/wp2.1-mcp-client`, the scope and the acceptance criteria.
2. Add the `**Branch:**` line to [phase-2](phase-2-memory-mcp.md#wp21--mcp-client) and move the phase status to `In progress`.
3. Branch from `develop`: `dev/wp2.1-mcp-client`.
   The first commit is the documentation change (branch line and Q6 decision), then code.
4. PR to `develop`, `Closes #N`.
   Agree on the PR granularity before starting: one large PR for all of WP2.1, or separate PRs for M0–M3 and M4–M5 under new work-package numbers.
   The repository's rule is one issue, one branch, one PR, so a split must be planned, not improvised after review starts.

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

- The global file holds `env`, `headers` and credentials.
  A project file may add a server but may not set `env`/`headers` or override a key; otherwise a cloned repository can exfiltrate the developer's variables.
- A project-added server starts a process or opens a network connection from a cloned repository, so it is disabled by default.
  Consent is keyed by the canonical project root, server name, and a SHA-256 of `transport`, `command`, `args`, `url` and `permission`.
  A change to any of those fields resets consent and keeps the server disabled until `hint mcp enable <name>` is run again.
- `permission` defaults to `execute`; `read` and `write` are explicit.
- `Config.Secrets()` includes each expanded `headers`/`env` value and every referenced `${VAR}` value separately, so a bare `Bearer ${TOKEN}` token is redacted.
  A stdio server receives only its declared `env` over a filtered environment, never hint's API keys.
- MCP tool output is data, not instructions.
  A resource named in the config may be loaded as an instruction file because the user chose it; a model-requested `resource_read` returns data only.
- Every MCP tool is wrapped in [`tool.WithLimits`](../../internal/tool/limits.go#L37-L90): timeout and truncation, like the built-ins.

### Context budget and tests

- Use the spike's measurement to set a schema cap.
  If the MVP cannot fit the tool schemas, cap descriptions and warn; full lazy disclosure is WP2.8 work, not a reason to block WP2.1.
- CI uses a Go fake MCP server over stdio: tools, resources, prompts, permission, timeout, restart, truncation and redaction.
- The live filesystem-server test is optional and skips when `npx` is absent; CI must not depend on the network.
- Run `go test -race ./internal/mcp/...` and `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...`.

### Estimate

- M0–M3 (walking skeleton and tools): 1.5–2 weeks.
- M4–M5 (resources and prompts): 1.5–2 weeks.
- M6, docs and hardening: 2–3 days.
- Whole WP2.1: about 3–4 weeks, consistent with the 8–10 weeks of Phase 2.

## Risks and open decisions

| Risk | Mitigation |
|---|---|
| The `Message.Summary` field changes the public contract | Optional JSON field; the repository already treats optional fields as non-breaking (`json_test.go`); add round-trip and validation tests. If a patch must not touch `pkg/agentapi`, use the prefix-length fallback and file the accumulation issue. |
| Old sessions have no marker | Mark leading system messages in `Session.Messages`; otherwise a session saved right after compaction loses its summary once on upgrade. |
| Repeated compaction accumulates summaries | The marker makes the old summary part of the body, so the new one replaces it. |
| #41 changes output | `Cut` returns the same bytes for ASCII; tests pin rune boundaries and the absence of `\uFFFD`. |
| MCP SDK churn or weight | Spike pins a version and measures the dependency; `internal/mcp` isolates it; thin client is the fallback. |
| A project file starts an arbitrary MCP server | Disabled by default; consent is keyed by project root and a hash of `transport`, `command`, `args`, `url` and `permission`, and resets on change. Credentials stay in the global file. |
| MCP schemas fill the prompt | Measure in the spike; cap or hide tools; full solution is WP2.8. |
| MCP schema numbers normalize through the SDK | Reject tools with `abs(v) >= 2^53`; document the decimal limitation; keep a raw `tools/list` fallback. |
| One huge WP2.1 PR | Decide the split before starting; a split needs new work-package numbers. |

Decisions taken:

1. B1 uses the `Message.Summary` marker, implemented in #32.
2. #41 is in `v0.3.2`, implemented in PR #83.
3. The `release-v0.3.1.md` merge-back edit is committed.

Still open:

1. WP2.1: one large PR or separate M0–M3 and M4–M5 work packages.

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

A pull request is not done until the full gate is green, the phase file and `PLAN.md` agree with the change, and the release notes name the fixed payload and the version it was present since.
