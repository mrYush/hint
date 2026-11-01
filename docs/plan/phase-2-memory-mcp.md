# Phase 2 — Memory, MCP, A2A, open integration surfaces, code maturity

> Status: Planned · Target release: **v0.4** · Estimate: 8–10 weeks (part-time)
> Depends on: Phase 1
> Exit bar: core parity with a mature coding agent, plus `hint` reachable by and able to reach other agents.

## Goal

Open the agent to the outside in every direction it can be extended — tools
(MCP servers, HTTP APIs described by OpenAPI), other agents (A2A, both as
caller and as callee), instructions (MCP prompts and resources next to the
rule files) and knowledge (local databases, document indexes) — make it
effective on large codebases (repo map), give it long-term memory (vector
store), plus deeper git integration and smarter compaction.

Every surface goes through the same two gates the architecture already
fixes: the permission layer (a class per tool, confirmation for writes and
commands) and the untrusted-input rule (what comes back from a tool, a
server, an agent or a document is data, never instructions). Added
2026-10-18 together with WP2.6 and WP2.7.

## Work packages

### WP2.1 — MCP client

SDK — [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk).

- [ ] `mcp_servers:` section in config (stdio and streamable HTTP transports), layered like every other key: global file, project `.hint/config.yaml`, with the project able to add servers and never a key
- [ ] Discovered MCP tools registered in the tool registry alongside built-ins, under `<server>.<tool>` names
- [ ] Permission wrapper around every MCP tool (class declared in config, default `execute`)
- [ ] MCP tool output treated as untrusted input (no instruction-following from tool results — architecture-level rule)
- [ ] MCP **resources** as instruction and knowledge sources: a resource the config names is loaded like an instruction file (budget, outline, `instructions` tool), one the model asks for is read on demand through a `resource_read` tool
- [ ] MCP **prompts** as reusable procedures: `/prompts` lists them, `/<server>:<prompt>` expands one into the turn, with its arguments completed from the prompt's schema in the TUI command table
- [ ] Lifecycle: lazy start, health check, restart on crash, timeout budget; `hint mcp list|status` to see it
- [ ] Integration test with a reference MCP server (e.g. filesystem server) for tools, resources and prompts

### WP2.2 — Repo map

Requires tree-sitter (cgo) → build-tag isolation, see risks.

- [ ] go-tree-sitter parsing → def/ref tags per file
- [ ] File graph + PageRank ranking of symbols
- [ ] Map rendered into a 1–2k token budget, included in the system prompt
- [ ] SQLite cache keyed by file mtime
- [ ] `cgo`-free fallback: no repo map, warning once (core must still build without cgo)
- [ ] Benchmark: map build time on a 100k-line repo

### WP2.3 — Vector memory

- [ ] First `Embedder` implementation: openai-compatible `/v1/embeddings` + Ollama
- [ ] Storage: sqlite-vec
- [ ] `memory_search` tool + automatic fact capture (opt-in)
- [ ] Division of labor: repo map for code, vectors for documents/notes/history
- [ ] Privacy: memory store is per-user, local, excluded from logs

### WP2.4 — Git integration

- [ ] `git` tool (status/diff/log/commit under the permission system)
- [ ] Optional auto-commit of agent edits (config flag, off by default)
- [ ] Dirty-tree guard: warn before the first write into a dirty repo

### WP2.5 — Compaction v2

- [ ] Layered: microcompact tool outputs → summarize turns → hard truncation
- [ ] Compaction records preserved in the session file (already a record kind)
- [ ] Regression tests: long multi-tool sessions stay under the window with no loss of task-critical facts (golden scenarios)

### WP2.6 — A2A: `hint` among other agents

Protocol — [A2A](https://a2a-protocol.org) (Agent2Agent, a Linux Foundation project): JSON-RPC over HTTP, an Agent Card for discovery, tasks with a lifecycle and streamed updates.
Which side goes first and which Go implementation to build on is open question Q10 in [PLAN.md](../../PLAN.md#open-questions); the spike settles it before any code.

- [ ] Spike: read the current spec, run the reference samples, decide client-first or server-first and the implementation to build on; record the decision here
- [ ] **`hint serve` as an A2A server.** An Agent Card at the documented well-known path describing `hint` (skills: the directory it serves, the tools it has); a task is a turn of a session; streamed task updates carry the same events the RPC protocol does; permission questions surface as `input-required` states that the calling agent — or the user through a shell attached to the same session — answers.
      The A2A adapter is one more client of the core, beside the JSON-RPC server and the shells; it does not get its own agent loop
- [ ] **`hint` as an A2A client.** `agents:` section in config naming remote agents by their card URL; each becomes a tool (`delegate_<name>`) under the permission system, class `execute`; the remote agent's answer is untrusted input like any tool result
- [ ] Authentication from the card's security schemes: bearer token from `${VAR}`, mutual TLS later; no credential ever in a project file
- [ ] Transport limits: a budget per delegated task (time, tokens where reported) and cancellation that propagates to the remote task
- [ ] Integration test: two `hint serve` processes in two directories, one delegating a read-only question to the other through A2A, with a recorded fixture for CI
- [ ] Decision to record: what of a delegated task lands in the session file (the request, the final answer, the remote task id — not the remote agent's intermediate output)

Decisions (2026-11-01):

- **WP2.6 settles the seam for agents that speak A2A; it does not settle delegation to a *harness*.**
  An A2A agent owns its own loop, credentials and permissions and can be asked about its state; a harness is a process with its own loop and no protocol to ask it anything.
  That shape is Q11 in [PLAN.md](../../PLAN.md#open-questions) — a `Runner` beside `Tool`, or A2A only — not a checkbox here.
- **The order is the `bash` sandbox layer, then `hint`-in-`hint` over `-p --output json` in a worktree, then A2A (WP2.6), then a subprocess adapter for a foreign CLI.**
  Chosen over "A2A first" and over "wrap a foreign CLI in a bash-like tool": a child harness is less observable than a command the user confirmed and would run outside the file confinement of `tool.Root`.
  The price is that delegation does not ship before the sandbox layer exists.
- **The name is `runner`/`delegate`, not `harness`.**
  `harness` already means the evaluation bench ([architecture](architecture.md#implications-noted-for-the-plan)) and the coding-agent category ([why-hint](why-hint.md#why-start-with-a-solved-problem)).

### WP2.7 — Open integration surfaces beyond MCP and A2A

The ways an operator connects tools, instructions and knowledge without writing Go. Each is a small adapter onto what the core already has (the tool registry, the instruction files, the permission gate); none adds a second model loop.

- [ ] **HTTP APIs described by OpenAPI.** `openapi_tools:` in config: a spec URL or file plus an allow-list of operations; each operation becomes a tool with its parameters as the schema, read-class for `GET`, `execute` otherwise, credentials from `${VAR}`; responses truncated to the tool-output budget
- [ ] **Local databases as knowledge.** A read-only `sql` tool over SQLite and PostgreSQL connection strings from config (`databases:`), `SELECT` only, a row and byte budget, the schema offered to the model as an instruction outline; write access stays out until there is a sandbox story (see WP1.7's limits)
- [ ] **Document indexes.** Directories and URL lists in config indexed into the WP2.3 vector store on a schedule or on demand (`hint index`), so `memory_search` answers from the team's docs, not only from past sessions
- [ ] **Instruction sources beyond the repository.** A `rules:` list of extra rule directories and URLs (a team's shared rule pack, a company-wide `HINT.md`), fetched with a cache and a signature or pinned hash, read with the same budget and outline rules as `.hint/rules`
- [ ] **Procedure files (skills).** A `.hint/skills/<name>.md` file with front matter is a slash command: `/name` expands its body into the turn, its declared arguments complete in the TUI; shared the same way as rules
- [ ] One `hint integrations` command that lists every configured surface with its status, and one place in the docs that explains them side by side

Decisions to make when the work starts, recorded here: the config shape shared by all surfaces (a common `name`, `permission_class`, `budget` block), and whether a surface declared in a project file may add credentials (it may not; the global file holds them, the project file names the `${VAR}`).

## Acceptance criteria

1. A third-party MCP server from config contributes tools that the agent
   uses under the permission system, a resource it serves is read as an
   instruction file, and a prompt it serves runs as a slash command.
2. On a large repo, the agent answers "where is X implemented?" using the
   repo map without reading every file.
3. `memory_search` recalls a fact stored in a previous session.
4. With auto-commit on, every applied edit is a well-formed git commit.
5. Another agent fetches `hint serve`'s Agent Card and completes a task
   through A2A, answering a permission question on the way; `hint`
   delegates a question to a remote agent named in its config and uses
   the answer under the permission system.
6. An OpenAPI-described API and a read-only database appear as tools from
   config alone, each under its declared permission class.
