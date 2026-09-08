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
│   ├── agent/                  # loop, turn, compaction, limits, tool schedule (WP0.11)
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

## Prior art: does an existing tool already implement the whole mechanic?

> Research pass: 2026-08-29; refreshed 2026-09-08 (Perplexity Portable
> Computer). Revisit before Phase 4 (mobile/desktop convergence) and before
> deciding Q2 (product naming) in PLAN.md — this space moves fast.

Short answer: **no single existing tool implements the full mechanic** — the
combination of a Go core, camera/VLM and mic/STT/TTS as first-class declared
interfaces, device sensors (incl. Raspberry Pi GPIO) exposed as client-hosted
MCP servers, and a signed/trust-gated third-party catalog does not exist yet
in one project. But four actively developed projects each already implement
a large, *different* subset of the mechanic, in production or public beta —
worth tracking, and worth reusing from where licenses allow.

| Project | Closest to our plan on | Key gaps vs. our plan |
|---|---|---|
| [FutureOS](https://www.v2ex.com/t/1235540) ([write-up](https://www.80aj.com/2026/08/19/futureos-ai-agent-rust/)) | **Core-as-a-service, almost exactly as designed**: one local gRPC backend (`127.0.0.1:50051`), with terminal TUI, desktop, mobile (Android+iOS), CLI, and IM bots (Feishu/DingTalk) as thin clients of the same agent/session/memory. Sessions are JSONL with git-like branching (`/fork`, `/tree`) — arguably ahead of our WP0.7 plan, closer to Pi's tree-structured format. "Trust before capability" tiered-sandbox permission model. 140+ providers incl. local deployment (offline fallback). | Rust, not Go. No confirmed MCP client. No VLM/camera, no STT/TTS as core interfaces. No sensor/GPIO abstraction, no Raspberry Pi story. No formal signed package catalog. |
| [Goose](https://goose-docs.ai/) ([review](https://theaiagentindex.com/agents/goose)) — donated by Block to the Agentic AI Foundation / Linux Foundation, Apache 2.0 | **Most mature MCP ecosystem** (70+ built-in extensions; any MCP server that works with Claude Desktop works with Goose — validates our WP2.1 bet on MCP over a custom plugin format). Multi-provider incl. Ollama (offline fallback). CLI + desktop app. Mobile exists: [`goose-ios`](https://github.com/dhanji/goose-ios) is a thin remote client in the App Store (tunnels back to your desktop agent — **not** on-device/offline), [`goose-android-agent`](https://github.com/block/goose-mobile) is an experimental PoC that automates the whole device (closer to computer-use than to "agent with perception"); a strictly-client Android port is only planned. See the [mobile apps announcement](https://block.github.io/goose/blog/2026/01/20/goose-mobile-apps/). | No native VLM/camera tool, no STT/TTS in core. No sensor/GPIO abstraction. No formal trust/signing model for extensions — an in-app MCP marketplace is only [proposed](https://github.com/block/goose/issues/6648), current discovery is a community [Skills Marketplace](https://github.com/block/goose/discussions/2075) and a static [extensions directory](https://mintlify.wiki/block/goose/concepts/extensions). Rust, not Go. |
| [OpenHuman](https://github.com/tinyhumansai/openhuman) ([explainer](https://www.mager.co/blog/2026-05-25-openhuman-explainer/), [deep dive](https://pyshine.com/OpenHuman-Personal-AI-Super-Intelligence/)) | **Closest to Phases 3+4 today**: voice built into the core (Whisper STT, ElevenLabs TTS), native system-tray widget on macOS/Windows/Linux (Tauri) — near-equivalent of our planned Phase 4 desktop widget. Long-term memory goes further than our WP2.3 plan: a hierarchical memory tree built from 110+ OAuth-connected services. | Desktop-first, not core-as-a-service — no CLI-first / multi-shell architecture the way FutureOS and Goose have it. No native mobile apps. No camera/VLM. No sensor/GPIO or Raspberry Pi angle. Rust+Tauri, GPL3 (copyleft — code cannot be borrowed under our MIT/Apache-only rule). |
| [Perplexity Portable Computer](https://www.perplexity.ai/hub/products/portable-computer) ([analysis](https://venturebeat.com/infrastructure/perplexity-partners-with-nvidia-to-launch-portable-computer-a-fully-local-ai-agent-with-zero-token-costs), [deep dive](https://www.layer3labs.io/guides/perplexity-portable-computer-explained), [technical summary](https://www.marktechpost.com/2026/08/25/perplexity-ships-portable-computer-on-nvidia-dgx-spark-local-harness-os-enforced-sandbox-and-zero-per-token-cost-for-local-steps/)) — proprietary, subscription-gated, announced 2026-08-25 | **Offline as a first-class mode, pushed further than our plan.** The whole loop — orchestrator LLM, subagent LLMs, planner, tool router, task scheduler, local document index — runs on the user's own machine, and the cloud is the *exception*: an opt-in "advisor" call that carries the question text only, never local files or tool results, and returns text guidance with nothing executable. A PII classifier shows what would leave the device before it does. Tool execution always sits in an OS-level sandbox that **disables itself rather than running unprotected** when the sandbox is unavailable — the fail-closed rule we apply to the permission prompter (WP0.6), one layer lower. Instructions and skills load on demand instead of living in the prompt: the same progressive-disclosure shape as WP0.12 rung 4, arrived at independently. Connectors (Gmail, Drive, GitHub, Slack) were deliberately rewritten *from MCP servers into compact CLI tools* to save context budget. Shipped as an appliance: one `apt` package carries model, patched vLLM, harness, sandbox and connectors together. | Not portable in our sense: it needs ≥24 GB VRAM (RTX 3090+, 32 GB recommended) or a $4,699 NVIDIA DGX Spark, Linux at launch with Windows promised for 2026-09 and no Apple silicon planned — the opposite end of the hardware range from our Raspberry Pi target. A single desktop application, not core-as-a-service: no thin clients, and early users report no way to reach the machine from a phone or from the cloud. Closed and non-extensible ("a small set of core tools"), no MCP client, no third-party catalog. Text and files only — no VLM/camera, no STT/TTS, no sensors. Model and harness are co-designed (a post-trained PPLX 27B alongside Qwen 3.8 27B), which buys benchmark points at the cost of exactly the provider-agnostic property `ChatProvider` exists to protect. All published numbers are vendor-reported and include Pi as a baseline (82.6% vs. 77.6% on their own 53-task bench); no independent results yet. |

Also checked and ruled out as a closer match: [Open Interpreter's `01` project](https://01.openinterpreter.com/) ([repo](https://github.com/openinterpreter/01)) — the closest in *spirit* to "personal multimodal agent" (voice-first, iOS/Android apps, ESP32 device support), but it is pre-1.0, explicitly [lacks basic safeguards](https://github.com/openinterpreter/01/issues) yet, is not coding-agent-first, and has no core-as-a-service/RPC design or Go/single-binary story.

### What this means for the plan

1. **The core architectural bet is de-risked, not novel.** "One core, thin clients on every platform" (Section 1.2 / this document's opening) is now a validated, shipping pattern (FutureOS, Goose), not a speculative design choice unique to us.
2. **The unique combination still holds.** Nobody ships VLM+STT+TTS as day-one-declared interfaces *and* GPIO/sensor MCP servers *and* a signed third-party catalog *and* a single Go binary across desktop+mobile+Raspberry Pi. That combination (Section 1.4) remains the actual differentiator — not the core-as-a-service idea by itself.
3. **Build-vs-adopt is worth an explicit look before Phase 2.** Goose's MCP integration is mature enough that Phase 2 (WP2.1) could validate its client against real-world MCP servers already cataloged by Goose's community, instead of discovering integration issues from scratch. This does not change the Go-core decision (Goose is Rust; embedding it does not serve the Raspberry Pi/mobile/no-cgo goals), but it lowers integration risk for WP2.1.
4. **MCP is an ecosystem bet, not a context-budget one (Q6).** Portable Computer kept the connector targets and threw the protocol away, converting MCP servers into compact CLI tools purely to stop tool schemas from eating the window. Goose still argues for MCP on reach. Both can be true: WP2.1 should plan for a layer that compacts or lazily discloses MCP tool descriptions rather than pasting every server's schema into the preamble, in the same spirit as WP0.12.
5. **Two router policies worth adding to the offline story (Phase 2–3, `internal/provider`).** (a) A *data-minimising escalation* mode: the local model drives the loop and the frontier model is asked a text question without files or tool results — on a Raspberry Pi or a phone this is likely more realistic than running the whole loop on a 3B model. (b) A *preview before leaving the device* step on the cloud fallback path, so the user sees what a fallback would send. Both sit on the WP0.3 router and would make "offline as a first-class mode" (PLAN.md §1.4) a concrete guarantee instead of a positioning claim.
6. **Sandboxing is a layer we do not have yet.** WP0.6 gates *whether* a command runs; nothing constrains what it can touch once it does, so `bash` executes with the user's full permissions. Portable Computer's rule — no sandbox, no tools — is the shape to copy when this lands (`landlock`/`bwrap` on Linux, `sandbox-exec` on macOS); Codex CLI and OpenHands are the reference implementations already in the table above.
7. **No evaluation harness is now a visible gap.** Every competitor publishes a task bench (Portable Computer's 53-task Local Knowledge Work Bench, Terminal Bench 2.1, BrowseComp), and Pi shows up as a baseline in them. Without one we cannot tune the cloud → Ollama → on-device ladder honestly: we have no way to say where a local model actually stops being sufficient. Worth scoping before Phase 3.
8. **Revisit cadence:** this space is moving fast — FutureOS, OpenHuman and Portable Computer all surfaced within roughly six months of each other in 2026, and Portable Computer was announced on 2026-08-25 — before both the product spec (2026-08-28) and the first research pass (2026-08-29), which still missed it. Re-run this scan before committing to Phase 4/5 architecture decisions (Q2/Q3/Q4 in PLAN.md).

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
