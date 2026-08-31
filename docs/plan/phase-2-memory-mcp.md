# Phase 2 — Memory, MCP, code maturity

> Status: Planned · Target release: **v0.3** · Estimate: 6–8 weeks (part-time)
> Depends on: Phase 1
> Exit bar: core parity with archived opencode / early Crush.

## Goal

Open the tool ecosystem via MCP, make the agent effective on large codebases
(repo map), and give it long-term memory (vector store), plus deeper git
integration and smarter compaction.

## Work packages

### WP2.1 — MCP client

SDK — [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk).

- [ ] `mcp_servers:` section in config (stdio and HTTP transports)
- [ ] Discovered MCP tools registered in the tool registry alongside built-ins
- [ ] Permission wrapper around every MCP tool (class declared in config, default `execute`)
- [ ] MCP tool output treated as untrusted input (no instruction-following from tool results — architecture-level rule)
- [ ] Lifecycle: lazy start, health check, restart on crash, timeout budget
- [ ] Integration test with a reference MCP server (e.g. filesystem server)

### WP2.2 — Repo map

Reference — Aider. Requires tree-sitter (cgo) → build-tag isolation, see risks.

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

Reference — Aider git-first.

- [ ] `git` tool (status/diff/log/commit under the permission system)
- [ ] Optional auto-commit of agent edits (config flag, off by default)
- [ ] Dirty-tree guard: warn before the first write into a dirty repo

### WP2.5 — Compaction v2

Reference — Claude Code pipeline.

- [ ] Layered: microcompact tool outputs → summarize turns → hard truncation
- [ ] Compaction records preserved in the session file (already a record kind)
- [ ] Regression tests: long multi-tool sessions stay under the window with no loss of task-critical facts (golden scenarios)

## Acceptance criteria

1. A third-party MCP server from config contributes tools that the agent
   uses under the permission system.
2. On a large repo, the agent answers "where is X implemented?" using the
   repo map without reading every file.
3. `memory_search` recalls a fact stored in a previous session.
4. With auto-commit on, every applied edit is a well-formed git commit.
