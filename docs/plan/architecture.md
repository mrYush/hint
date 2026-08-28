# Architecture

> Status: reference document, updated as contracts evolve.

## Principle: core as a service, shells as clients

One Go core (agent runtime) exposes an API (JSON-RPC/gRPC over stdio or a
localhost socket). Every interface — CLI/TUI, desktop widget, iOS/iPadOS,
Android, Raspberry Pi — is a thin client of that core. The core talks to:

- Providers (LLM/VLM/STT/TTS/Embeddings) over OpenAI-compatible APIs
- Local models (Ollama, llama.cpp) as offline fallback
- MCP servers (third-party tools)
- Sensors/camera/microphone (captured by the client, streamed to the core)

The pattern is proven in the industry:

- **Pi** ([badlogic/pi-mono](https://github.com/badlogic/pi-mono)) — `--mode
  rpc`: any app talks to the agent as a subprocess via JSON commands; the
  `pi-agent-core` SDK embeds the agent into other applications.
- **OpenCode (sst)** ([sst/opencode](https://github.com/sst/opencode)) —
  explicit client/server architecture: core on the server, TUI/web/mobile
  clients connect to it.
- **Claude Code** — CLI, VS Code plugin, and web UI run on one agent backend
  with a single master loop.

## Target package layout

```
hint/
├── cmd/hint/main.go            # CLI, flags, wiring
├── internal/
│   ├── agent/                  # loop, turn, compaction, limits
│   ├── provider/               # modality interfaces
│   │   ├── openai/             # openai-compatible client (chat+stream+tools)
│   │   └── router/             # default/fallback routing
│   ├── tool/                   # Tool interface, registry
│   │   └── builtin/            # read,write,edit,ls,glob,grep,bash,todo
│   ├── permission/             # action classes, modes, allow-list
│   ├── session/                # JSONL store, resume, list
│   ├── project/                # HINT.md, auto-context, gitignore
│   └── config/                 # profiles, sources (extends the current package)
└── pkg/agentapi/               # PUBLIC types (Message, ToolCall, Event) — future SDK base
```

## Key interfaces (contract)

All modality interfaces are declared in the MVP even though only Chat is
implemented — this freezes the contract for later phases.

```go
// provider
type ChatProvider interface {
    Stream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}
type ChatRequest struct {
    Model    string
    Messages []Message
    Tools    []ToolSchema
}
type ChatEvent struct { // Delta | ToolCall | Usage | Done | Error
    Kind  EventKind
    Text  string
    Call  *ToolCall
    Err   error
}

// declared in MVP, implemented in phases 2–3+
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}
type Transcriber interface {
    Transcribe(ctx context.Context, audio io.Reader, opts STTOpts) (string, error)
}
type Speaker interface {
    Speak(ctx context.Context, text string, opts TTSOpts) (io.ReadCloser, error)
}
type VisionProvider interface {
    Describe(ctx context.Context, images []Image, prompt string) (string, error)
}

// tool
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage               // JSON Schema of the input
    Class() permission.Class               // read | write | execute
    Run(ctx context.Context, input json.RawMessage) (ToolResult, error)
}
```

## References and what we take from each

| Reference | Link | What we borrow |
|---|---|---|
| opencode (archived, MIT, Go) | [opencode-ai/opencode](https://github.com/opencode-ai/opencode) | Skeleton: agent loop, built-in tools, SQLite sessions, MCP client, Bubble Tea TUI, auto-compaction |
| Crush (Go) | [charmbracelet/crush](https://github.com/charmbracelet/crush) | Granular permission system, mid-session model switching, LSP support, per-project session contexts |
| Claude Code | [anthropics/claude-code](https://github.com/anthropics/claude-code) (docs) | Philosophy: one flat `while(tool_call)` loop, ~14 tools, TODO planning, depth-limited subagents, hooks/skills/plugins as 4 extension mechanisms, compaction at ~92% of the window |
| Aider (Python) | [Aider-AI/aider](https://github.com/Aider-AI/aider) | Repo map: tree-sitter + PageRank symbol ranking within ~1k tokens; edit formats (search/replace, udiff, whole-file) chosen per model strength; architect mode; git-first: every edit is a commit |
| Codex CLI (Rust) | [openai/codex](https://github.com/openai/codex) | Sandboxed command execution, approval modes (suggest/auto-edit/full-auto) |
| Gemini CLI (TS) | [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli) | Tool schemas, checkpointing, built-in MCP, GEMINI.md context format |
| Goose (Rust) | [block/goose](https://github.com/block/goose) | MCP-native extensibility, automation beyond code — closest reference for the sensor/device phases |
| OpenHands | [All-Hands-AI/OpenHands](https://github.com/All-Hands-AI/OpenHands) | Docker sandbox, event-stream architecture, unattended autonomous runs |
| Pi | [badlogic/pi-mono](https://github.com/badlogic/pi-mono) | RPC mode, session format (tree-structured JSONL with branching), packages: extensions/skills from npm/git/URL, trust model for project extensions |
| Forge (Rust) | [antinomyhq/forge](https://github.com/antinomyhq/forge) | Role separation: forge (edits) / sage (read-only research) / muse (plans) |
| Ollama (Go) | [ollama/ollama](https://github.com/ollama/ollama) | Both the offline-fallback provider and a model of Go architecture around local models |
| MCP Go SDK | [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk), [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) | Ready protocol for third-party tools instead of a custom plugin format |
| Bubble Tea | [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework |
| tree-sitter Go | [tree-sitter/go-tree-sitter](https://github.com/tree-sitter/go-tree-sitter) | Code parsing for the repo map |
| whisper.cpp / llama.cpp | [ggml-org/whisper.cpp](https://github.com/ggml-org/whisper.cpp), [ggml-org/llama.cpp](https://github.com/ggml-org/llama.cpp) | On-device STT and LLM for mobile and Raspberry Pi |
| MLC LLM | [mlc-ai/mlc-llm](https://github.com/mlc-ai/mlc-llm) | Alternative on-device runtime (iOS/Android GPU) |

Licensing rule: borrow code only from MIT/Apache projects (archived opencode,
Aider idea-porting, official SDKs). Crush (FSL) and Claude Code are pattern
sources only.

## Platform matrix

| Platform | Phase | Shell technology | Core | Offline models |
|---|---|---|---|---|
| Linux/PC, Mac, Windows (CLI/TUI) | 0–1 | Go (Bubble Tea) | native binary | Ollama |
| Raspberry Pi | 0, 3, 6 | same CLI + GPIO MCP | linux/arm64 binary | Ollama / llama.cpp (small models) |
| Mac (widget) | 4 | SwiftUI menu bar | `hint serve` (launchd) | Ollama |
| Windows/Linux (widget) | 4 | WinUI/Wails | `hint serve` | Ollama |
| iPhone / iPad | 5 | SwiftUI | gomobile xcframework | llama.cpp / MLC / Apple FM |
| Android | 5 | Kotlin/Compose | gomobile AAR | llama.cpp / MLC / AICore |

## Provider enrichment sequence

| Step (phase) | Modality | Cloud (openai-compatible) | Offline fallback |
|---|---|---|---|
| 0 | LLM chat + tools | `/v1/chat/completions` (OpenAI, OpenRouter, api-bar.ru and other aggregators) | Ollama `/v1` |
| 2 | Embeddings | `/v1/embeddings` | Ollama (nomic-embed etc.) |
| 3 | VLM | image content in chat | Ollama (llava/qwen-vl), llama.cpp mmproj |
| 3 | STT | `/v1/audio/transcriptions` | whisper.cpp |
| 3 | TTS | `/v1/audio/speech` | piper |
| 5 | On-device LLM | — | llama.cpp/MLC inside the mobile app |

## Note on api-bar.ru

The service's public documentation is not indexable (the site is an "AnyAPI
Admin" SPA), so the exact `base_url`, model list, and modality support
(embeddings/vision/audio) are pinned from the dashboard after registration.
This does not affect code — the profile is configured via config. The CI
integration-test matrix must include api-bar.ru alongside
OpenAI/OpenRouter/Ollama, especially for streaming and tool-calling (the most
common dialect-divergence points among aggregators).
