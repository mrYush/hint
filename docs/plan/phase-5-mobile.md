# Phase 5 — Mobile: iPhone / iPad / Android

> Status: Planned · Target release: **v0.6** (TestFlight / closed Google Play beta) · Estimate: 10–16 weeks (part-time)
> Depends on: Phase 1 (agentapi contract), Phase 3 (VLM/STT/TTS)
> Can run in parallel with: Phase 6

## Goal

The agent runtime inside a mobile app — no server required. The Go core is
embedded via **gomobile bind** (xcframework for iOS, AAR for Android) with
thin native shells.

## Work packages

### WP5.1 — gomobile embedding

- [ ] `pkg/agentapi` surface adapted to gomobile constraints (exported types must be bind-compatible; wrap channels into callback interfaces)
- [ ] Build pipeline: Go core → xcframework (iOS) and AAR (Android) in CI
- [ ] cgo-dependent features (tree-sitter repo map) excluded via build tags; verify the mobile core builds and runs
- [ ] Storage paths adapted to app sandboxes (sessions, config, memory DB)

### WP5.2 — iOS/iPadOS shell (SwiftUI)

- [ ] One target for iPhone/iPad; iPad gets split-view chat + artifacts
- [ ] Chat UI over the event stream (delta/tool_call/permission_request)
- [ ] Camera/mic capture in the native layer, frames/audio handed to the core → VLM/STT
- [ ] Background execution rules respected (turns finish or checkpoint on suspension)

### WP5.3 — Android shell (Kotlin/Compose)

- [ ] Same feature set as WP5.2
- [ ] Foreground-service strategy for long turns

### WP5.4 — On-device models

The same `ChatProvider` interface — the core never knows the model is local.

- [ ] Spike A (fast path): llama.cpp via existing bindings, 1–4B models (Qwen, Gemma, Phi)
- [ ] Spike B: MLC LLM (GPU acceleration)
- [ ] Decision recorded (PLAN.md Q4); one runtime shipped, the other kept behind an interface
- [ ] Apple: track Foundation Models / MLX integration as it matures
- [ ] Model weights downloaded post-install (never bundled) with resumable downloads and integrity checks

### WP5.5 — Store compliance

- [ ] Early guideline review: weight downloads, background work, sensor access
- [ ] Mobile tool set: `bash` replaced by a safe set (app files, shortcuts/intents)
- [ ] Privacy manifests / data-safety forms
- [ ] Fallback channel: TestFlight / sideload if review stalls

## Acceptance criteria

1. Airplane mode: a full multi-turn dialogue with tool use works on-device.
2. Point the camera at an object, ask about it, get a VLM answer.
3. The same session file semantics as desktop (export/inspect a session).
4. TestFlight build and Google Play internal-testing build pass review.
