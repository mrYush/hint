# Phase 3 — Multimodality: VLM, STT, TTS

> Status: Planned · Target release: **v0.4** · Estimate: 6–8 weeks (part-time)
> Depends on: Phase 2 (provider maturity), Phase 1 (TUI for voice UX)
> Platforms unchanged; Raspberry Pi becomes a full voice device (mic+speaker).

## Goal

Give the agent eyes and a voice: implement the `VisionProvider`,
`Transcriber`, and `Speaker` interfaces declared in the MVP, with cloud
implementations and local fallbacks, and wire them into the CLI/TUI.

## Work packages

### WP3.1 — VLM (vision)

- [ ] `VisionProvider` via the same openai-compatible chat (image content parts)
- [ ] Image inputs in CLI: `hint -p @img.png "what is this"` (Pi syntax)
- [ ] `screenshot` tool (desktop): capture display → hand to VLM (permission class `read`, but confirm on first use per session)
- [ ] Offline fallback: Ollama (llava/qwen-vl); llama.cpp mmproj documented
- [ ] Router extended: modality-aware fallback (a profile may support chat but not vision)

### WP3.2 — STT (speech to text)

- [ ] `Transcriber` → openai-compatible `/v1/audio/transcriptions`
- [ ] Local fallback: whisper.cpp as a subprocess (no cgo)
- [ ] Push-to-talk voice input in the TUI
- [ ] Audio capture abstraction per platform (portaudio-free: OS-native CLI helpers or a pure-Go capture lib — spike first)

### WP3.3 — TTS (text to speech)

- [ ] `Speaker` → `/v1/audio/speech`
- [ ] Local fallback: piper (subprocess)
- [ ] `--speak` flag: read answers aloud (final text only, not tool chatter)

### WP3.4 — Voice-dialogue pipeline

- [ ] mic → STT → agent → TTS in one interactive mode (`hint --voice`)
- [ ] Barge-in: speaking interrupts TTS playback
- [ ] Latency budget measured and documented (target: first audible response < 3 s on cloud profile)
- [ ] Raspberry Pi validation with USB mic + speaker

## Acceptance criteria

1. `hint -p @screenshot.png "why does this dialog show an error"` answers from
   the image.
2. In the TUI, push-to-talk records a question, the answer streams as text and
   (with `--speak`) as audio.
3. `hint --voice` sustains a hands-free multi-turn dialogue on a Raspberry Pi
   with only Ollama + whisper.cpp + piper (no network).
