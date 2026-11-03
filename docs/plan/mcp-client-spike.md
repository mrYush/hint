# MCP client: the Q6 spike

> Status: **Design spike — nothing here is implemented.**
> Resolves Q6 in [`../../PLAN.md`](../../PLAN.md).
> Last updated: 2026-11-03.
> Scope: settle the MCP specification baseline, the Go SDK (or a thin client), the transport and authentication surface for WP2.1, and the context budget before WP2.1 starts.
> This branch is documentation only.

## The question

Q6 asks whether `hint` should build its own MCP client or validate the public MCP catalogs first.
The second half of that question is the real one: MCP is an ecosystem bet, and the shape of the bet is only knowable by connecting to real servers.
This spike does that — it reads the current specification, probes reference and public servers, measures what their tool descriptions cost in the prompt, and fixes the shape of WP2.1.
The answer is not "MCP is good"; it is "MCP is worth its cost, with this SDK and this budget."

## Decision

1. **SDK: pin the official [`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) at `v1.8.0`.**
   It is the only Go SDK that implements the current `2026-07-28` specification and still negotiates the earlier revisions.
2. **Spec: target `2026-07-28`, let the SDK negotiate the rest.**
   The SDK supports `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26` and `2024-11-05`, including the switch between the stateless model and the legacy `initialize` handshake.
   WP2.1 does not implement either handshake itself.
3. **Transports: stdio and Streamable HTTP.**
   Do not add the deprecated HTTP+SSE transport in WP2.1.
4. **Authentication: static headers from the global config.**
   OAuth 2.1 is a later package; WP2.1 supports a bearer token or API key supplied by the user, not a browser flow.
   Project files never carry a credential.
5. **Context: a default MCP schema budget of 48 KiB, 20 KiB per server.**
   Whole servers are hidden, with a warning, when the budget is exceeded; per-server `tools:` allow and deny lists are the escape hatch.
   Full lazy disclosure remains WP2.8 work.
6. **No thin client.**
   The dual-era, OAuth and multi-round-trip work is larger than the dependency.
   The official SDK already does it, builds CGO-free, and costs about 6 MB in the release binary.

The rest of this document is the evidence for those six statements.

## Specification baseline

The current revision is **`2026-07-28`**.
It is a substantial change from the `2025-06-18` revision that the earlier plan named:

- **MCP is stateless.**
  `initialize` and `notifications/initialized` are gone.
  Every request carries the protocol version, client capabilities and (optionally) client identity in `_meta`.
- **`server/discover` is mandatory for modern servers.**
  It advertises the supported versions, capabilities and identity.
  A client may call it first, or send another request and retry after an `UnsupportedProtocolVersionError`.
- **`ping` is gone.**
  A health check is `server/discover` or any successful request.
- **`subscriptions/listen` replaces the GET stream and `resources/subscribe`.**
  One long-lived request carries the change notifications the client opts in to.
- **Server-to-client requests use Multi Round-Trip Requests (MRTR).**
  Sampling, elicitation and roots arrive as `InputRequiredResult` payloads that the client answers by retrying the original request.
- **Sampling, roots and logging are deprecated** as of this revision ([SEP-2577](https://github.com/modelcontextprotocol/modelcontextprotocol/pull/2577)).
  WP2.1 should not add them.
- **Results carry `resultType`**, and list results carry `ttlMs` and `cacheScope`.
  The client can cache a tool list, but caching does not reduce its context cost.

The older revision still matters because servers in the wild use it.
The compatibility model is:

| Era | Revisions | Handshake | Session |
|---|---|---|---|
| Modern | `2026-07-28` | none; per-request `_meta` | no protocol session |
| Legacy | `2025-11-25` and earlier | `initialize` / `notifications/initialized` | `Mcp-Session-Id` on HTTP |

The SDK detects the era on `Client.Connect`: it probes `server/discover` on stdio, or sends a modern request on Streamable HTTP, and falls back to `initialize` when the response is not a recognized modern error.
The probe confirmed both paths: the seven reference servers negotiated `2026-07-28`, and the archived GitHub server negotiated `2024-11-05` and listed 26 tools.

### Methods WP2.1 needs

| Method | `2026-07-28` | WP2.1 use |
|---|---|---|
| `server/discover` | required | connect, version selection, health check |
| `tools/list` | required | registry, pagination |
| `tools/call` | required | tool execution |
| `resources/list` | required for resources | list configured and model-visible resources |
| `resources/templates/list` | optional | only if a server exposes templates |
| `resources/read` | required for resources | instruction files and `resource_read` |
| `prompts/list` | required for prompts | `/prompts` |
| `prompts/get` | required for prompts | `/<server>:<prompt>` |
| `completion/complete` | optional | prompt argument completion |
| `subscriptions/listen` | optional | refresh a changed tool list |
| `initialize`, `notifications/initialized`, `ping` | removed | legacy only; the SDK handles them |
| `resources/subscribe` | removed | legacy; no direct call in WP2.1 |
| `logging/setLevel` | deprecated | not used |
| `sampling/createMessage`, `roots/list`, `elicitation/create` | deprecated or MRTR | not advertised by WP2.1 |

Two corrections to the after-release plan follow from the table:

- The plan lists `initialize`, `notifications/initialized` and `ping` as methods WP2.1 needs.
  They are not part of the current revision.
  The SDK negotiates them for legacy servers; `hint` does not call them directly.
- The plan says prompt arguments are completed "from the prompt's schema".
  MCP prompts expose an argument list (`name`, `description`, `required`), not a JSON Schema; suggestions come from `completion/complete`.
  The TUI should complete from those two surfaces.

### Transports

**stdio** is one JSON-RPC message per line over the child process's stdin and stdout.
The server may log to stderr; stderr is not an error signal.
Shutdown closes stdin and waits, escalating to terminate after a grace period.
The SDK implements this as `CommandTransport`, including the child lifecycle.

**Streamable HTTP** is one HTTP POST per JSON-RPC message.
The server answers with `application/json` or a request-scoped `text/event-stream`; the client must accept both.
In `2026-07-28` there is no GET stream, no protocol session, no `Last-Event-ID`, and closing the response stream is the cancellation signal.
Requests carry `MCP-Protocol-Version`, `Mcp-Method`, and `Mcp-Name` where applicable.
Servers must validate `Origin` and should bind to localhost when local; clients should authenticate every connection.

The deprecated HTTP+SSE transport (`2024-11-05`) is not in WP2.1.
The SDK still ships an SSE client for compatibility, but adopting it would add a second transport for no current server in the catalog probe.

### Authentication

MCP authorization is OAuth 2.1 at the transport layer: a 401 with `WWW-Authenticate` and a protected-resource-metadata URL, authorization server discovery, PKCE, and resource indicators.
Dynamic client registration is deprecated in favour of Client ID Metadata Documents.
None of that is free, and none of it is needed to prove the WP2.1 tool surface.

WP2.1 supports the static form the plan already names: `headers` in the global config, with `${VAR}` expansion and redaction.
That covers a GitHub PAT, a Linear API key, a Context7 key, and any server that accepts a bearer token.
OAuth is a separate package and can be added behind the same `internal/mcp` seam with the SDK's `auth` and `oauthex` packages later.

The probe measured the wall this creates:

- Sentry (`https://mcp.sentry.dev/mcp`) returned `401` with `WWW-Authenticate: Bearer realm="OAuth", resource_metadata="https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp"`.
- Linear (`https://mcp.linear.app/mcp`) returned the same shape with `scope="read write"`.
- GitHub (`https://api.githubcopilot.com/mcp/`) returned `403 Forbidden`.
  A GitHub CLI OAuth token (`gho_…`, `repo`/`gist`/`read:org`) was not accepted; the remote server wants a token authorized for the MCP server, not just any GitHub token.
  That is a configuration quirk to surface in the error message, not a reason to block WP2.1.

### Tool descriptors and trust

A tool is `name`, `description`, `inputSchema`, optional `outputSchema`, optional `annotations`, and display metadata.
WP2.1 sends only the first three to the model; the rest are either provider-irrelevant or untrusted.
`outputSchema` is not a provider tool parameter, `annotations` are explicitly untrusted by the specification unless the server is trusted, and `title` / `icons` are display-only.

Two schema facts matter for `hint`:

- MCP allows any JSON Schema 2020-12 keyword, including `$ref` and `$defs`.
  The provider contract in [`internal/tool/schema.go`](../../internal/tool/schema.go) avoids references because no provider resolves them.
  The probe found `anyOf` but no `$ref`/`$defs`.
  WP2.1 should pass schemas through and warn on references; dereferencing is a later enhancement, not a reason to rewrite every schema now.
- A server's `readOnlyHint` is not a permission decision.
  The config declares the class; the default is `execute`.
  Deriving `read` from a server's annotation would let a server talk its way into the permission layer.

## SDK evaluation

### The candidate

`github.com/modelcontextprotocol/go-sdk` **v1.8.0**, published 2026-09-04 (tag release 2026-09-14).
Its own version table is the decisive fact:

| SDK version | Latest spec | Supported specs |
|---|---|---|
| v1.7.0+ | `2026-07-28` | `2026-07-28`, `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05` |
| v1.4.0–v1.6.1 | `2025-11-25` | `2025-11-25`, `2025-06-18`, `2025-03-26`, `2024-11-05` |
| v1.2.0–v1.3.1 | `2025-11-25` (partial) | same, with client OAuth and sampling gaps |
| v1.0.0–v1.1.0 | `2025-06-18` | `2025-06-18`, `2025-03-26`, `2024-11-05` |

The current SDK is the only one in that table that covers the current revision without dropping the installed base.
That matters because the catalog probe found both eras in active use: seven reference servers on `2026-07-28`, and an archived GitHub server on `2024-11-05`.

### What the dependency costs

Measured on 2026-11-03 with Go 1.26.10, macOS arm64:

| Measure | Value |
|---|---|
| Module requirement | `go 1.25.0` |
| Modules in the build graph | 12 external (13 including the probe) |
| SDK source on disk | 2.6 MB |
| Module cache including tools | 62 MB |
| Linked modules in a stripped client | 9 |
| Stripped client binary | 7.6 MB, versus 1.6 MB for a hello-world |
| Current `hint` binary (stripped) | 18 MB; the SDK adds about 6 MB |
| CGO | not required |
| Cross-build | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./probe` passed |
| Licences | Apache-2.0 for new code, MIT for earlier contributions |

The module cache number is a developer-machine number, not a release artifact.
The release artifact number is the binary: about 6 MB, or a third of the current 18 MB.
That is acceptable for a CLI that already carries a TUI and a Markdown renderer, and it does not change the CGO-free core rule.

The SDK's module graph includes `google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate`, `golang-jwt/jwt`, `golang.org/x/oauth2`, `golang.org/x/time`, `golang.org/x/tools`, and `golang.org/x/sync`.
The linked client uses the JSON, schema, URI-template, OAuth2, rate-limit and sync packages; `x/tools` and the JWT package are not pulled into the final link of a client-only build.

### What the API gives WP2.1

- `mcp.NewClient` + `Client.Connect(ctx, transport, nil)` creates a `ClientSession`; `Connect` performs discovery, version selection and the legacy fallback.
- `ClientSession` exposes `Tools`, `CallTool`, `Resources`, `ReadResource`, `Prompts`, `GetPrompt`, `Complete`, `Close` and `Wait`.
  The iterators (`Tools`, `Resources`, `Prompts`) follow `nextCursor` automatically.
- `CommandTransport` runs a subprocess over stdio, with a configurable terminate grace period.
  The `exec.Cmd` is ours, so WP2.1 can pass a filtered environment.
- `StreamableClientTransport` takes an `http.Client`, so per-server headers are a `RoundTripper`; `OAuthHandler` is there when OAuth lands.
  The client must always reject a cross-origin redirect when credentials are configured: `CheckRedirect` compares the scheme and host with the configured endpoint and returns an error before the redirected request is sent.
  Independently, the header `RoundTripper` is origin-aware and injects `Authorization` only when the request has the same scheme and host as the configured endpoint; stripping headers in `CheckRedirect` alone is not enough because the `RoundTripper` runs afterwards.
  A 302 to another host is covered by a test, and a direct `RoundTripper` test asserts that a different origin never receives the header; a redirect that re-sends a token to a different origin is a credential leak.
- `mcp.NewInMemoryTransports` and `mcp.NewServer` make a Go fake MCP server cheap for the WP2.1 tests; no `npx` is needed in CI.
- Every call takes a `context.Context`; protocol errors are `*jsonrpc.Error`, and tool-level errors are `CallToolResult.IsError`.
- `ClientOptions.Capabilities` defaults to advertising roots.
  WP2.1 must set `&mcp.ClientCapabilities{}` — it does not implement roots, sampling or elicitation, and should not claim them.
- The client-side `Tool.InputSchema` is `any` (a decoded map), not raw bytes.
  JSON structure survives, and so do strings, booleans and null.
  Numbers are decoded as `float64`: integers at or above 2^53 in magnitude and high-precision decimals can change when `hint` re-encodes the schema.
  WP2.1 scans for `float64` values with `abs(v) >= 2^53` and rejects that tool with "unsupported numeric precision" instead of sending a silently rounded schema.
  The bound is deliberately conservative: `9007199254740993` decodes to `9007199254740992` (2^53), so an exact 2^53 is rejected too; after decode the two are indistinguishable.
  High-precision decimals that round below the bound remain a documented limitation.
  This is not a byte-identical passthrough; WP2.1 tests the rejection and does not promise exact numeric literals.
  A future raw `tools/list` path is the fallback if a real server depends on exact numbers; it is not worth a thin client for WP2.1.

### Alternatives

| Option | Spec coverage | Transports | Cost | Verdict |
|---|---|---|---|---|
| Official `go-sdk` v1.8.0 | `2026-07-28` + all legacy | stdio, Streamable HTTP, legacy SSE | 12 dependencies, ~6 MB binary, Apache/MIT | **Chosen** |
| `mark3labs/mcp-go` v1.5.0 | `2025-11-25` + legacy | stdio, SSE, Streamable HTTP | comparable deps, MIT | Rejected: no `2026-07-28`, no MRTR/subscriptions model |
| Thin JSON-RPC client | whatever we write | stdio + HTTP | no dependency, weeks of work | Rejected: dual-era, OAuth, MRTR, pagination and SSE are exactly the work the SDK already tests |
| `mcp-golang`, `go-mcp` | behind the official SDK | varies | extra dependency | Not evaluated beyond the version check; the official SDK covers the need |

The `mcp-go` rejection is not a quality judgement.
It is a version fact: v1.5.0 was published on 2026-10-30 and still targets `2025-11-25`.
A client built on it would either fail against modern servers or need its own `server/discover` / `_meta` layer, which defeats the reason to use an SDK.

The thin-client fallback remains the insurance policy, not the plan.
If the SDK's v1 API becomes unusable, `internal/mcp` is the boundary: the rest of `hint` sees `agentapi.Tool`, not `mcp.Tool`.
The fallback would still be a large piece of work, which is why the spike records it as a fallback rather than a parallel option.

### Pin and upgrade policy

- Add `github.com/modelcontextprotocol/go-sdk v1.8.0` to `go.mod` in WP2.1; do not use `@latest` or a floating branch.
- Keep the import inside `internal/mcp` (and the tests).
  The agent loop, the registry and `pkg/agentapi` do not import the SDK.
- Upgrade only with a specification review and the probe rerun.
  The SDK's release notes describe transport hardening and version negotiation; those are exactly the parts a silent upgrade can change.
- Do not vendor.
  The core must still build with `CGO_ENABLED=0`, and a vendored tree would hide the dependency cost from review.

## Catalog probe

### Method

A Go probe built on SDK v1.8.0 connected to each endpoint, listed tools with the `Tools` iterator, optionally called one read-only tool, and printed the tool descriptors as JSON.
A second probe listed resources and prompts and read the first of each.
The measurement then serialized each tool in the exact OpenAI wire shape:

```json
{"type":"function","function":{"name":"<server>.<tool>","description":"…","parameters":{…}}}
```

and counted UTF-8 bytes, `o200k_base` tokens and `cl100k_base` tokens.
The tool name carries the WP2.1 `<server>.<tool>` prefix.
The counts exclude the system prompt, the conversation and the built-in tools; the built-in baseline is measured separately.

The run was 2026-11-03 on macOS arm64 with Go 1.26.10, Node 26.13, npm 11.20 and `uvx`.
It was a one-shot probe, not CI: authentication-gated servers were not exercised with a browser flow, and the `fetch` server's tool call hit a sandbox `npm install` failure.
Both are recorded below rather than smoothed over.

Thirteen endpoints were probed; twelve are in the table.
Linear (`https://mcp.linear.app/mcp`) is the extra: it returned the same OAuth 401 shape as Sentry and added no new information.

### Results

The token column is `o200k_base`; `cl100k_base` differs by less than 2% on this corpus.

| Server | Transport | Auth | Protocol | Tools | Tokens | Read-only call |
|---|---|---|---:|---:|---:|---|
| filesystem | stdio | none | `2026-07-28` | 14 | 1,782 | `read_text_file`, 33 B |
| git | stdio | none | `2026-07-28` | 12 | 1,243 | `git_status`, 144 B |
| fetch | stdio | none | `2026-07-28` | 1 | 234 | `fetch` failed |
| memory | stdio | none | `2026-07-28` | 9 | 1,034 | `read_graph`, 39 B |
| sequentialthinking | stdio | none | `2026-07-28` | 1 | 876 | `sequentialthinking`, 123 B |
| time | stdio | none | `2026-07-28` | 2 | 279 | `get_current_time`, 123 B |
| everything | stdio | none | `2026-07-28` | 22 | 2,908 | `get-sum`, 24 B |
| DeepWiki | Streamable HTTP | none | `2026-07-28` | 3 | 278 | `read_wiki_structure`, 1,278 B |
| Context7 | Streamable HTTP | optional key | `2026-07-28` | 2 | 1,077 | `resolve-library-id`, 1,688 B |
| Cloudflare docs | Streamable HTTP | none | `2026-07-28` | 2 | 270 | search, 24,342 B |
| Sentry | Streamable HTTP | OAuth | — | 0 | 0 | `401` |
| GitHub | Streamable HTTP | OAuth/PAT | — | 0 | 0 | `403` |

Totals:

| Set | Tools | Tokens | Bytes |
|---|---:|---:|---:|
| Seven reference servers | 61 | 8,356 | 38,290 |
| Three reachable public servers | 7 | 1,625 | 7,385 |
| Ten reachable servers | 68 | 9,981 | 45,675 |
| Built-in tools, for comparison | 8 | 1,589 | 7,479 |
| Ten servers + built-ins | 76 | 11,570 | 53,154 |

### What the probe found

1. **The current revision is already deployed.**
   All seven reference servers negotiated `2026-07-28`, not a legacy revision.
   DeepWiki, Context7 and Cloudflare docs also negotiated it, even though a direct `initialize` request to DeepWiki and Context7 returns `2025-06-18`.
   That is the SDK's `server/discover` path doing its job.
   A client must not infer a server's era from an `initialize` response alone.
2. **Legacy is alive.**
   The archived `@modelcontextprotocol/server-github@2025.4.8` negotiated `2024-11-05`, listed 26 tools, and cost 3,773 tokens.
   The SDK's fallback is not theoretical; it is the difference between connecting and not connecting.
3. **One server can dominate the budget.**
   `sequentialthinking` costs 876 tokens for one tool; Context7 costs 1,077 for two; the legacy GitHub server costs 3,773 for 26.
   A per-server cap is more useful than a single global average.
4. **Descriptions, not schemas, are the expensive half.**
   Across the ten reachable servers, descriptions are about 3,536 tokens of the 9,981; schemas and names are 6,445.
   The most expensive descriptors are long natural-language instructions, not large JSON objects.
5. **Full MCP descriptors would be 43% more expensive.**
   The SDK's tool object also carries `title`, `outputSchema`, `annotations` and icons.
   Sending the full object would cost 14,242 tokens instead of 9,981.
   `agentapi.ToolSchema` already has the right three fields; WP2.1 must map into it and drop the rest.
6. **Auth is the wall, not the protocol.**
   Sentry and Linear return a clean OAuth challenge; GitHub returns `403` even with a GitHub CLI token.
   WP2.1 with static headers can use a PAT/API key, but a browser OAuth flow is a separate package.
7. **Output size is real.**
   Cloudflare documentation search returned 24,342 bytes in one call.
   The existing `tool.WithLimits` 50 kB cap is the right first defence; the MCP adapter should use it, not its own ad hoc truncation.
8. **Resources and prompts work through the same SDK.**
   `everything` exposed 7 resources and 4 prompts; `resources/read` returned 2,032 bytes and `prompts/get` returned a message.
   `memory` exposed a `memory://knowledge-graph` resource and no prompts; `prompts/list` returned `Method not found`, which the adapter must turn into "this server has no prompts", not a fatal connect error.

### Quirks recorded for WP2.1

- **filesystem** ships both `read_file` (deprecated) and `read_text_file`.
  Both are exposed; the model may call either.
  The WP2.1 adapter should not rename server tools; the server owns its names.
- **git** exposes write tools (`git_add`, `git_commit`, `git_checkout`, `git_reset`, `git_create_branch`).
  A config default of `execute` is the right ceiling; a `read` class would be a lie.
- **fetch** lists one tool but its call failed because the Python server shells out to `npm install` for Readability in this sandbox.
  The failure was a tool error, not a connection error; the adapter must keep that distinction.
- **memory** returns no prompts; `prompts/list` is a protocol error.
  A server that does not implement a feature is not a broken server.
- **everything** is the best WP2.1 fixture: tools, resources, prompts, elicitation, logging and list-change exercises in one process.
  It is also the most expensive reference server at 2,908 tokens.
- **Cloudflare docs** returns 24 kB for a single search; the 50 kB cap fits, but the model sees a large result.
  WP2.1's resource/output budgets must be shared with the existing tool limits, not duplicated.
- **GitHub remote** rejected the CLI OAuth token.
  The error must say which credential to use (a PAT or an OAuth app authorized for the MCP server), not just "Forbidden".
- **Linear** is not in the measured table because it is the same OAuth challenge as Sentry; its `/mcp/readonly` endpoint is a useful later target.

## Context budget

### The numbers

| Measure | Value |
|---|---:|
| Ten reachable servers, 68 tools | 9,981 tokens / 45,675 B |
| Seven reference servers, 61 tools | 8,356 tokens / 38,290 B |
| Built-in tools, 8 tools | 1,589 tokens / 7,479 B |
| All of the above | 11,570 tokens / 53,154 B |
| Full MCP descriptors, 68 tools | 14,242 tokens |
| Descriptions only (ten servers) | 3,536 tokens |
| Names and schemas only (ten servers) | 6,445 tokens |
| OpenAI wrapper overhead | 340 tokens over the minimal object |
| Per tool: min / median / mean / p95 / max | 49 / 112 / 147.5 / 268 / 875 |
| Top ten tools | 3,714 tokens, 37% of the total |

The context share is the part that decides the policy:

| Window | Ten servers | Ten servers + built-ins |
|---|---:|---:|
| 32k | 30.5% | 35.3% |
| 128k | 7.8% | 9.0% |
| 200k | 5.0% | 5.8% |

On a 128k cloud model the measured set is a 9% tax — real, but not a reason to reject MCP.
On a 32k local model it is a third of the window before the system prompt, the rules and the conversation.
That is the case the budget exists for, and it is the case the project cares about (the Pi and the small local models).
The architecture's "MCP is an ecosystem bet, not a context-budget one" is therefore half right: the ecosystem is real, and the budget is still a first-class constraint.

### What can be trimmed

- **Drop `outputSchema`, `annotations`, `title` and icons.**
  They are not provider tool parameters.
  This is the 14,242 → 9,981 difference.
- **Strip `$schema`/`$id` from the input schema.**
  It saves 850 tokens (9,981 → 9,131) because most servers repeat the 2020-12 URL.
  The built-in schema builder already omits it.
  Stripping `title` as well saves another 257 tokens, but `title` is sometimes the only short description of a nested field; do not strip it by default.
- **Cut descriptions to 200 characters.**
  It saves 1,686 tokens (9,981 → 8,295), but it turns `sequentialthinking` from 876 to 331 tokens by deleting the instructions that make the tool usable, and Context7 from 1,077 to 620.
  This is a last resort for a server over its cap, not a global default.
- **Lazy disclosure.**
  Tools are the fixed cost because the model must know they exist.
  Resources and prompts are already on demand: `everything`'s resource was read for 2,032 bytes, and prompts are expanded only when the user invokes one.
  Full tool lazy disclosure (a search/load meta-tool, or Cloudflare's Code Mode shape) is WP2.8, not a reason to block WP2.1.

Cloudflare's own numbers are the extreme end of the same curve: its API server exposes 2,594 native tools (about 1.17M tokens) or two Code Mode tools (about 1,000 tokens).
The lesson is not "always use Code Mode"; it is that a catalog can grow without bound and the client needs a budget before it needs a clever loader.

### The policy

**Default MCP schema budget: 48 KiB, measured on the compact OpenAI wire form; per-server cap: 20 KiB.**
The ten-server set is 44.6 KiB and the seven reference servers are 37.4 KiB, so the default fits the measured catalog without trimming.
The legacy GitHub server is 16.6 KiB, under the per-server cap; the next server that grows past 20 KiB has to be allow-listed.

At roughly 4.6 bytes per token, 48 KiB is about 10.5k tokens; the built-ins take the total to about 11.6k.
The byte budget is deliberate: `hint` has no tokenizer and should not grow one for this.
It already uses byte budgets in [`internal/tool/limits.go`](../../internal/tool/limits.go) (50 kB ≈ 12k tokens), so the same units and the same estimator apply.

When the budget is exceeded:

1. **Hide whole servers, in config order, until the set fits.**
   Do not send a half-truncated schema for a tool that remains visible; a model that sees half a parameter list will call it wrongly.
2. **Warn once at startup and in `hint mcp status`, naming the hidden servers and the budget.**
   A silent drop is worse than a refused tool.
3. **Offer `tools:` allow and deny lists per server.**
   A user who needs one tool from a 2,000-tool server names it; the rest stays hidden.
4. **Do not truncate descriptions by default.**
   `description_max` is an explicit per-server opt-in for a server that is otherwise worth keeping.
5. **Revisit in WP2.8.**
   The budget is a guard, not the final disclosure design.
   A search/load meta-tool or per-turn selection is the full answer.

The budget must not become a reason to skip resources or prompts.
They are loaded on demand and do not sit in every request; their limits are the existing instruction and tool-output budgets.
The 32k-window cost is recorded as R10 in [`risks.md`](risks.md).

## What WP2.1 must build

This is the shape the spike fixes; it is not an implementation in this branch.

### Package boundary

`internal/mcp` holds `ServerConfig`, `Client`, `Manager` and the `agentapi.Tool` adapter.
The SDK import stays there.
The registry sees a normal `agentapi.Tool`; `pkg/agentapi` does not grow an MCP type.
The manager owns connections, restart and the schema budget; the adapter owns one tool's name, description, schema, class and `Run`.

### Configuration

Keep the shape in [`after-v0.3.1.md`](after-v0.3.1.md#configuration-shape), with four additions the probe forces:

```yaml
mcp_servers:
  filesystem:
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    permission: read
    timeout: 30s
    tools:
      allow: ["read_text_file", "list_directory"]  # optional allow-list
  github:
    transport: http
    url: https://api.githubcopilot.com/mcp/
    headers:
      Authorization: "Bearer ${GITHUB_TOKEN}"
    permission: execute
    tools:
      deny: ["create_repository"]                 # optional deny-list
```

Rules already fixed elsewhere and repeated because MCP makes them sharper:

- The global file holds `env` and `headers`; a project file may add a server but may not set or override them.
- A project-added server that starts a process is an execute-class action.
  It is **disabled by default**; `hint mcp enable <name>` records the user's consent outside the repository.
  The record is keyed by the canonical project root, the server name, and a SHA-256 of canonical JSON over `transport`, `command`, `args`, `url` and `permission`.
  Any change to those fields after `git pull` invalidates the record, and the server stays disabled until `hint mcp enable <name>` is run again.
  `hint mcp status` reports "consent changed" and the field that changed; a test edits `args` after enabling and asserts the server does not start.
  A cloned repository must not be able to run a command just by being opened.
  A server in the global file is enabled by its presence.
- `permission` defaults to `execute`; `read` and `write` are explicit.
  Server annotations never change it.
- `Config.Secrets()` includes each fully expanded `headers`/`env` value and every referenced `${VAR}` value separately.
  A header `Authorization: "Bearer ${TOKEN}"` therefore contributes both `Bearer <token>` and `<token>`, so an error that prints only the bare token is still redacted.
  Redaction is tested with that header and an error string containing only the expanded value.
- A stdio server receives only its declared environment, never `hint`'s API keys; the `exec.Cmd.Env` is built by the manager.

### Lifecycle and CLI

- **Lazy start.**
  `hint mcp list` reads config and shows "configured"; the first `tools/list` or `tools/call` connects.
  `hint mcp status` performs `server/discover` (modern) or the SDK's equivalent legacy probe and reports the negotiated version, server name, tool count and budget.
- **Health and restart.**
  `ClientSession.Wait` detects a dead child.
  The manager restarts with backoff, re-lists tools and re-applies the budget.
  Do not auto-retry a write/execute tool whose call may have reached the server before the disconnect; report the ambiguity to the model.
- **Timeouts.**
  Every call gets the configured timeout as a context deadline.
  The existing `tool.WithLimits` wrapper still applies, so an MCP tool gets the same timeout and output cap as a built-in.
- **Shutdown.**
  Close the session, close stdin, wait for the child, then terminate after the SDK's grace period.
  Do not leak a process per restart.

### Names, permissions and output

- **Name:** `<server>.<tool>`, stable across restarts.
  Validate the result against the provider's tool-name rules and the registry's duplicate check; a collision is a startup error, not a last-wins overwrite.
- **Permission:** the config class.
  `read` tools may run concurrently; `write` and `execute` serialize like built-ins.
  An MCP tool with no declared class is `execute`.
- **Schema fidelity:** keep the schema as JSON and reject a tool whose decoded numbers have `abs(v) >= 2^53` rather than send a rounded schema.
  The public `agentapi.ToolSchema` comment now says that an MCP adapter may re-encode and that numeric literals are not byte-identical; a raw `tools/list` path is the fallback if a server needs more.
- **Output:** convert `TextContent` to `agentapi.PartText`; `StructuredContent` to compact JSON text when there is no text content; `EmbeddedResource` text inline under the output cap; `ResourceLink` as a URI line.
  Images and audio wait for Phase 3, with an explicit error rather than a silent empty result.
- **Errors:** `CallToolResult.IsError` becomes `ToolResult.IsError` so the model sees it and can react.
  A protocol error becomes an `ErrorResult` too, except a canceled context, which aborts the turn.
  The wrapper must not turn a timeout into a successful empty result.
- **Untrusted data:** every tool result and every model-requested resource is data.
  Only a resource the user named in config may be loaded as an instruction file, and even then through the same budget and outline rules as `.hint/rules`.

### Resources and prompts

- **Resources:** a config-named resource URI is fetched at startup/session start and loaded like an instruction file, with the instruction budget, outline and `instructions` tool.
  A model-requested `resource_read` returns data only, capped like a tool result, and is never replayed as system text.
  Blob contents are base64; decode only when the MIME type is text and the budget allows it; otherwise report the MIME type and size.
- **Prompts:** `/prompts` lists `name`, `description` and arguments; `/<server>:<prompt>` fetches the prompt and expands its messages into the turn.
  Argument completion uses the prompt's `arguments` plus `completion/complete`, not a JSON Schema.
  Embedded resources and images in a prompt need the same part conversion as tool results; images can wait for Phase 3.
- **Trust:** a prompt the user invokes is user intent, but its text still comes from a third-party server.
  Keep it out of the system preamble; it expands as a user turn, exactly like a slash command.

### Tests

- A Go fake MCP server over `mcp.NewInMemoryTransports` covers tools, resources, prompts, `IsError`, protocol errors, timeouts, a missing feature and a legacy `initialize`-only server.
  CI must not need `npx` or the network.
- A live filesystem-server test is optional and skips when `npx` is absent; a live DeepWiki test is optional and skips without network.
  The repo's existing CI rule is that the network is not a dependency.
- A redirect test serves a 302 from the configured origin to a second origin and asserts the second origin never sees `Authorization`; a same-origin redirect keeps the header.
- A direct `RoundTripper` test sends a request to a different origin and asserts no credential header; the same request to the configured origin gets one.
- A consent test enables a project server, changes `args` (and separately `command`, `url` and `permission`), and asserts the server is disabled and `status` names the changed field until it is enabled again.
- A redaction test configures `Authorization: "Bearer ${TOKEN}"` and asserts both the expanded header and the bare token are in `Config.Secrets()` and replaced in an error string that contains only the bare token.
- A schema-fidelity test serves both `9007199254740992` and `9007199254740993` and asserts both are rejected with "unsupported numeric precision"; a fractional number below the bound remains a JSON number.
- Run `go test -race ./internal/mcp/...`, the full `go test ./...`, and `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...`.
- A budget test registers ten fake servers with known descriptor sizes and asserts the hidden set, the warning and the `status` output.

### Acceptance for WP2.1

WP2.1 can start without choosing the SDK again when:

1. A real reference server contributes tools, a resource and a prompt under the permission layer: `everything` for all three, `filesystem` for the tool path.
2. A modern HTTP server (DeepWiki or Cloudflare docs) and a legacy server (a fake `initialize`-only server, or the archived GitHub package when `npx` is available) both connect through the same manager.
3. The ten-server measured set fits the 48 KiB budget; an over-budget set hides whole servers and reports them.
4. `hint mcp list|status` shows configured, running, failed and hidden.
5. The process/stdio lifecycle, timeout, truncation and redaction tests pass under `-race`, and the CGO-free cross-build is green.

## Deferred, not forgotten

- **OAuth 2.1 browser flow is deferred.**
  It is needed for Sentry, Linear, GitHub and any server that does not accept a static token.
  It is a separate package with a token store (keyring, `0600` fallback), PKCE, resource metadata and issuer binding.
  WP2.1's static headers are not a substitute.
- **Dereferencing `$ref`/`$defs` is deferred.**
  Pass-through plus a warning now; dereference or reject at registration once a real server needs it.
- **Tool-list change notifications and live registry refresh are deferred.**
  The SDK handler exists; WP2.1 can refresh before the next turn.
  Do not mutate the registry mid-turn.
- **Elicitation and MRTR are out of scope.**
  WP2.1 advertises no client capabilities, so a server that asks for input fails with an actionable error.
  Wiring elicitation into the permission UI is a later feature, not a hidden capability claim.
- **Progress notifications, the tasks extension, sampling, roots and logging are out of scope.**
  Sampling, roots and logging are deprecated.
- **Lazy tool disclosure is WP2.8.**
  The budget is the WP2.1 guard.
- **HTTP+SSE is deprecated and not in WP2.1.**

## References

Specification, revision `2026-07-28` (accessed 2026-11-03):

- [Specification overview](https://modelcontextprotocol.io/specification/2026-07-28.md)
- [Key changes](https://modelcontextprotocol.io/specification/2026-07-28/changelog.md)
- [Versioning and compatibility](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning.md)
- [stdio transport](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio.md)
- [Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http.md)
- [Authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization.md)
- [Tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools.md)
- [Architecture overview](https://modelcontextprotocol.io/docs/learn/architecture.md)

SDK:

- [pkg.go.dev](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk)
- [v1.8.0 module](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)
- [v1.8.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)
- [Client features](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/client.md)
- [Base protocol and transports](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/protocol.md)
- [Rough edges](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/rough_edges.md)

Servers and alternatives:

- [Reference servers](https://github.com/modelcontextprotocol/servers)
- [Example servers](https://modelcontextprotocol.io/examples)
- [mcp-go v1.5.0](https://github.com/mark3labs/mcp-go)
- [DeepWiki MCP](https://mcp.deepwiki.com/mcp)
- [Context7 MCP](https://context7.com/docs/resources/all-clients)
- [Cloudflare MCP servers](https://developers.cloudflare.com/agents/model-context-protocol/cloudflare/servers-for-cloudflare/)
- [Sentry MCP](https://mcp.sentry.dev/)
- [Linear MCP](https://linear.app/docs/mcp)
- [GitHub remote MCP](https://github.com/github/github-mcp-server/blob/main/docs/host-integration.md)

## Appendix: raw totals

Per server, in the OpenAI wire shape with `<server>.<tool>` names.
`o200k` is the primary count; `cl100k` was within 2% on every row.

| Server | Tools | o200k | cl100k | Bytes |
|---|---:|---:|---:|---:|
| filesystem | 14 | 1,782 | 1,749 | 8,647 |
| git | 12 | 1,243 | 1,209 | 5,362 |
| fetch | 1 | 234 | 230 | 1,083 |
| memory | 9 | 1,034 | 991 | 4,800 |
| sequentialthinking | 1 | 876 | 868 | 4,090 |
| time | 2 | 279 | 279 | 1,233 |
| everything | 22 | 2,908 | 2,875 | 13,075 |
| DeepWiki | 3 | 278 | 266 | 1,265 |
| Context7 | 2 | 1,077 | 1,077 | 5,025 |
| Cloudflare docs | 2 | 270 | 271 | 1,095 |
| **Ten reachable** | **68** | **9,981** | **9,815** | **45,675** |
| Built-ins | 8 | 1,589 | 1,579 | 7,479 |
| Legacy GitHub (`2024-11-05`) | 26 | 3,773 | — | 16,998 |

The per-tool distribution is min 49, median 112, mean 147.5, p95 268 and max 875 tokens.
The top ten tools account for 3,714 tokens (37%); the remaining 58 account for 6,267.

The strip experiments on the ten-server set:

| Change | Tokens | Saving |
|---|---:|---:|
| Baseline | 9,981 | — |
| Drop `$schema`/`$id` | 9,131 | 850 (8.5%) |
| Drop `$schema`/`$id`/`title` | 8,874 | 1,107 (11.1%) |
| Description capped at 200 chars | 8,295 | 1,686 (16.9%) |

The 200-character cap is not recommended as a default; it is listed to show what the last resort buys and what it costs in tool usability.
