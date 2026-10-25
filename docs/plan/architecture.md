# Architecture

> Status: reference document, updated as contracts evolve.

## Principle: core as a service, shells as clients

One Go core (agent runtime) exposes an API over stdio or a localhost socket.
Every interface — CLI/TUI, desktop widget, iOS/iPadOS, Android, Raspberry Pi — is a thin client of that core.
`v0.3` is JSON-RPC 2.0 ([`docs/rpc.md`](../rpc.md)).
A gRPC adapter is added beside it, on the same engine and the same events, when the first client of `hint serve` is not Go in this module (Phase 4 native shells).
JSON-RPC stays for stdio and for Go clients.
Phase 5 embeds the core with gomobile and does not use either socket protocol.
The core talks to:

- Providers (LLM/VLM/STT/TTS/Embeddings) over OpenAI-compatible APIs
- Local models (Ollama, llama.cpp) as offline fallback
- MCP servers (third-party tools)
- Sensors/camera/microphone (captured by the client, streamed to the core)

## Target package layout

```
hint/
├── cmd/hint/                   # CLI shell: an RPC client of the core it serves in-process
│   └── rpcengine.go            # the one file that wires internal/agent into rpc.Engine
├── internal/
│   ├── rpc/                    # JSON-RPC server (socket, loopback TCP, stdio) and Client (WP1.1, WP1.3)
│   ├── provider/               # implementations of the modality interfaces
│   │   ├── openai/             # openai-compatible client (chat+stream+tools)
│   │   └── router/             # default/fallback routing
│   ├── tool/                   # Tool registry, schema generation
│   │   └── builtin/            # read,write,edit,ls,glob,grep,bash,todo
│   ├── permission/             # modes, allow-list, prompting
│   ├── diff/                   # line diff + unified rendering (permission previews, TUI)
│   ├── session/                # JSONL store, resume, list (WP0.7)
│   ├── console/                # the one stdin line reader: prompts, session picker, REPL
│   ├── project/                # HINT.md, auto-context, gitignore
│   ├── debuglog/               # the --debug trace: one file per run, secrets redacted on write
│   └── config/                 # profiles, sources, limits (extends the current package)
└── pkg/agentapi/               # PUBLIC contract — future SDK base:
                                #   wire types (Message, ContentPart, ToolCall,
                                #   ToolResult, ToolSchema, Usage, Error),
                                #   event streams (ChatEvent, Event),
                                #   interfaces (ChatProvider, Embedder,
                                #   Transcriber, Speaker, VisionProvider, Tool),
                                #   ActionClass
```

Note the split: the interfaces and the types they exchange live in
`pkg/agentapi`; `internal/provider` and `internal/tool` hold only
implementations and wiring. That is what lets a provider or a tool be written
outside this module. `ActionClass` lives in `pkg/agentapi` for the same
reason — a third-party tool has to be able to declare its own class, which it
could not do if the type stayed in `internal/permission`.

## Key interfaces (contract)

All modality interfaces are declared in the MVP even though only Chat is
implemented — this freezes the contract for later phases.

All of the below live in `pkg/agentapi` (implemented in WP0.1); see the
package's doc comment for the full contract.

```go
// conversation state — what a provider is given and what a session stores
type Message struct {
    Role       Role            // system | user | assistant | tool
    Content    []ContentPart   // text | thinking | image | audio
    ToolCalls  []ToolCall      // assistant only
    ToolCallID string          // tool only, links back to a ToolCall
}

// provider
type ChatProvider interface {
    Name() string
    Stream(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}
type ChatRequest struct {
    Model       string
    Messages    []Message
    Tools       []ToolSchema
    Temperature *float64       // pointer: 0 is a value, not "unset"
    MaxTokens   int
}
type ChatEvent struct { // text_delta | thinking_delta | tool_call | usage | done | error
    Kind         ChatEventKind
    Text         string
    Call         *ToolCall     // always complete; the provider assembles fragments
    Usage        *Usage
    FinishReason FinishReason
    Err          *Error
}

// agent — the stream a client (CLI, TUI, widget, mobile shell) renders.
// Separate from ChatEvent on purpose: a client must not have to know that a
// turn involved several provider calls, a fallback and a compaction.
// Produced by internal/agent.Agent.RunTurn (WP0.4): model responds, tool
// calls run and feed back, repeat until a final answer, a compaction, or
// the iteration/turn_limit cap.
type Event struct { // turn_start | text_delta | thinking_delta | message |
                    // permission | tool_start | tool_end | compaction |
                    // usage | turn_end | error
    Kind       EventKind
    Message    *Message
    Call       *ToolCall
    Result     *ToolResult
    Permission *PermissionRequest
    Compaction *Compaction
    Err        *Error
    // …
}

// failures, classified by what the caller should do about them
type Error struct {
    Kind     ErrorKind        // network | timeout | rate_limited | unavailable |
                              // auth | invalid_request | model_not_found |
                              // context_overflow | content_filtered | canceled |
                              // turn_limit | unknown
    Message  string
    Provider string
    // …
}
func (e *Error) Retryable() bool    // same provider again could work
func (e *Error) Fallbackable() bool // wider: auth and model_not_found too

// declared in MVP, implemented in phases 2–3+.
// No empty stubs: until the phase arrives there is simply no constructor.
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
    InputSchema() json.RawMessage          // JSON Schema of the input
    Class() ActionClass                    // read | write | execute
    Run(ctx context.Context, callID string, args json.RawMessage) (ToolResult, error)
}
```

Two rules the wire types follow, because the same values are persisted in
session files and shipped over RPC:

- **Sum types are structs with a `Kind` discriminator, not sealed
  interfaces.** A sealed interface is more type-safe in a `switch`, but it
  cannot be decoded by `encoding/json` without a hand-written `UnmarshalJSON`
  and a type registry — code that sits on the session-loading path and the
  RPC path at once. The tagged union costs the ability for `Kind` and payload
  to disagree, which each such type answers with a `Validate` method.
- **Every type round-trips losslessly through `encoding/json`**, and the
  field names of the core types are pinned by a test: renaming one breaks
  sessions on disk and RPC clients. Raw-JSON fields are `omitempty` so a nil
  value does not come back as the literal `null`.
- **Unknown `Kind` values are skippable, not fatal.** `Validate` reports them
  wrapped in the `ErrUnknownKind` sentinel, so a reader of a newer session
  file or RPC message distinguishes "written by a later version" from
  "malformed" with `errors.Is`. This is what lets a new `Kind` be added
  without a `WireVersion` bump.

## Implications noted for the plan

1. **MCP is an ecosystem bet, not a context-budget one (Q6).** WP2.1 should
   plan for a layer that compacts or lazily discloses MCP tool descriptions
   rather than pasting every server's schema into the preamble, in the same
   spirit as WP0.12.
2. **Two router policies worth adding to the offline story (Phase 2–3,
   `internal/provider`).** (a) A *data-minimising escalation* mode: the local
   model drives the loop and the frontier model is asked a text question
   without files or tool results — on a Raspberry Pi or a phone this is
   likely more realistic than running the whole loop on a 3B model. (b) A
   *preview before leaving the device* step on the cloud fallback path, so
   the user sees what a fallback would send. Both sit on the WP0.3 router and
   would make "offline as a first-class mode" (PLAN.md §1.4) a concrete
   guarantee instead of a positioning claim.
3. **Sandboxing is a layer we do not have yet.** WP0.6 gates *whether* a
   command runs; nothing constrains what it can touch once it does, so `bash`
   executes with the user's full permissions. The rule to adopt when this
   lands: no sandbox, no tools (`landlock`/`bwrap` on Linux, `sandbox-exec`
   on macOS).
4. **No evaluation harness is a visible gap.** Without a task bench we
   cannot tune the cloud → Ollama → on-device ladder honestly: we have no way
   to say where a local model actually stops being sufficient. Worth scoping
   before Phase 3.

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
