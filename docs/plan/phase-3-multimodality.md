# Phase 3 — Multimodality: VLM, attachments, terminal images, STT, TTS

> Status: Planned · Target release: **v0.5** · Estimate: 6–8 weeks for WP3.1–WP3.4 (part-time); WP3.5–WP3.6 are re-estimated with the phase
> Depends on: Phase 2 (provider maturity), Phase 2R (post-v0.4 review), Phase 1 (TUI and RPC client)
> Platforms unchanged; Raspberry Pi becomes a full voice device (mic+speaker).

## Goal

Give the agent eyes and a voice: implement the `VisionProvider`, `Transcriber`, and `Speaker` interfaces declared in the MVP, with cloud implementations and local fallbacks, and wire them into the CLI/TUI.

The same phase lets a conversation carry what the eyes see.
Images and arbitrary documents can be attached from a local path, a URL or the clipboard, and the terminal draws the images it can: real pixels on a graphics-capable terminal, coloured half-blocks everywhere else, at least in the TUI.
This attachment and terminal-image scope is added after `v0.4.0` (2026-11-07); it is not a Phase 2 deliverable and does not change `v0.4`.
It takes the user-facing shape of the terminal-image work in Hermes Agent v0.23, not its implementation: `hint` already has the `agentapi` media parts and its own core/client split, so the work is a client-side renderer plus a provider and session path, not a second agent.
Phase 2R runs immediately after `v0.4.0` and before this phase; it may revise this file, the release scope and the acceptance criteria.

## Work packages

### WP3.1 — VLM (vision)

- [ ] `VisionProvider` via the same openai-compatible chat (image content parts): the array-of-parts form with `{"type":"image_url","image_url":{"url":...}}` for an inline data URL or a remote URL, `detail` passed through where the dialect accepts it, and the plain-string form kept for a message with no media.
- [ ] A profile says whether it takes native image parts; a text-only profile routes the image through `VisionProvider` and sends its description, or refuses the attachment with a clear error.
  A model name is not treated as a capability declaration.
- [ ] Image inputs in CLI: `hint -p @img.png "what is this"` (Pi syntax); WP3.5 generalizes the reference to documents, URLs and the TUI.
- [ ] `screenshot` tool (desktop): capture display → hand to VLM (permission class `read`, but confirm on first use per session)
- [ ] Offline fallback: Ollama (llava/qwen-vl); llama.cpp mmproj documented
- [ ] Router extended: modality-aware fallback (a profile may support chat but not vision)
- [ ] MCP image tool results become a `PartImage` for a provider that can carry an image in a tool result; otherwise the core adds the image as a labelled follow-up user message, not as the current "hint cannot show yet" error and not as a silent drop.
- [ ] Tests: request conversion pins the string form for text-only messages and the parts form whenever a message carries an image, including the remote-URL and inline-data cases, the text-only describe path and the refusal path.

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

### WP3.5 — Image and document attachments

**Branch:** `dev/wp3.5-attachments`

An attachment is user input, not a tool call: the user chooses a path, a URL or a clipboard image, and the turn's user message carries it.
The shell expands a local path or clipboard into bytes; the core validates, extracts and stores them, and resolves a URL itself, so `--connect` to a remote core never asks that core to read the client's filesystem.
The one-shot CLI, the REPL and the TUI share the input spelling, not three implementations of it.
`internal/attach` owns validation, extraction and the attachment store; the RPC and provider layers consume its values.
This is not WP2.2's document catalog: one user-chosen document is extracted for one turn and is not added to the shared index or the durable notes.

- [ ] Input surface in both shells: `@path` and `@https://...` tokens in a question, with quoting for spaces, plus `/attach <path|url>` for something chosen before the question; `-p` accepts `--attach` as the non-interactive form and may repeat it.
  A bare URL in prose stays text; only the `@` token or `/attach` sends it.
- [ ] A pending-attachment line in the TUI lists name, kind and size; `ctrl+c` clears the pending set first and the input second, and the transcript keeps the attachment under the user's question.
- [ ] Clipboard: `/paste` attaches an image when the clipboard holds one; `ctrl+v` stays text-first and falls back to an image only when no text came through, so a normal paste is unchanged.
  macOS uses `osascript` or `pngpaste`, Linux `xclip` or `wl-paste`, WSL2 and Windows `powershell.exe`; a missing helper is a notice, not a failed turn.
  SSH has no clipboard bridge, and the docs say to use a path or a URL there.
- [ ] Resolution: regular files only, symlinks resolved, size and pixel caps, media type sniffed from content rather than trusted from the extension, and a readable error for a directory, a missing file or a corrupt image.
- [ ] URLs: `http` and `https` only, bounded redirects and timeouts, a byte cap, and no credentials or cookies.
  Loopback and private addresses are refused by default; an explicit config opt-in is required to attach one, so a remote document cannot turn the core into an SSRF probe.
- [ ] Documents: text and source formats are read as bounded UTF-8; DOCX, ODT and EPUB are extracted with `archive/zip` plus `encoding/xml`; PDF uses either a pure-Go module or a `pdftotext` helper, decided when the work starts and recorded here.
  An unsupported binary is named in a notice and is not silently dropped.
  OCR is out of scope.
- [ ] Extracted document text becomes `agentapi.PartText` with a header naming the file, media type and extracted size, and is labelled as untrusted data the way a tool result is; the system prompt says an attachment is data, not an instruction.
- [ ] Image parts use `agentapi.Image` with the bytes inline and a media type, or a remote URL only when the profile accepts URLs; the provider conversion builds the `image_url` object and never puts an attachment in a tool role that cannot carry it.
- [ ] An attachment reference survives the session: bytes are stored content-addressed outside the JSONL and the message carries a stable reference, so `session.list` does not decode image bytes and a fork keeps the attachment.
  The exact reference shape (`Image.Ref` or a `hint-attachment:` URL) is decided before the first commit; either choice gets a JSON round-trip test and a missing-reference error.
- [ ] Session and RPC: `turn.start` gains an optional `attachments` list, and `hello` advertises the capability; a client that needs an attachment refuses to send to an older core instead of letting it answer without the image.
  The list is bounded and each item carries a name, media type and base64 data or a URL.
- [ ] Budgets: per-attachment and per-turn byte caps, a maximum count, image downscaling only when above the cap and with an explicit notice, and extracted text truncated with a visible marker.
  Attachments count toward the context window and compaction like any other content.
- [ ] Secret boundary: a file the secret policy protects (the config, a key file) is refused or confirmed, and no attachment bytes or URL query strings are written to `--debug` logs.
  Paths may be logged only as the user wrote them.
- [ ] Docs: a user page for paths, URLs, clipboard, supported formats and the SSH limitation; `config.example.yaml` gains the limits.
- [ ] Tests: table-driven resolution for each format and cap, a fake clipboard reader, a URL server fixture with redirect and private-address cases, an RPC round-trip, a session reload and fork, and a provider conversion with text-only and native-vision profiles.

Decisions to make when the work starts, recorded here: the attachment reference shape, the PDF extractor, and whether the store is per-session or shared with garbage collection.
The safe default for the store is a per-session directory copied on fork, so deleting a session deletes its bytes and no reference counting is needed; the cost is duplicate bytes on fork.

### WP3.6 — Images in the terminal

**Branch:** `dev/wp3.6-terminal-images`

The shell draws the images the conversation already carries, so the model and the renderer share one attachment rather than a second copy.
`internal/termimage` owns protocol detection, encoders and the half-block fallback; the TUI and the REPL call it, and only the shell decides what the local terminal can show.

- [ ] Detection from the client's environment only: `KITTY_WINDOW_ID`, `TERM`, `TERM_PROGRAM`, `WEZTERM_PANE`, `ITERM_SESSION_ID`/`LC_TERMINAL`, `KONSOLE_VERSION`, `VTE_VERSION` and VS Code's `TERM_PROGRAM=vscode`; no DA1 query that can hang a pipe.
  `display.inline_images` overrides the guess with `auto|kitty|iterm|sixel|unicode|off`.
- [ ] Protocols: kitty Unicode placeholders for the TUI (kitty and Ghostty; WezTerm speaks the kitty APC but paints placeholders as tofu, so it uses half-blocks in the TUI and iTerm2 in the REPL), iTerm2 OSC 1337 for the classic REPL, sixel for a sixel terminal, and truecolor half-blocks as the always-available fallback.
  VS Code's terminal defaults to half-blocks and takes a pinned protocol only.
- [ ] Decode `image/png`, `image/jpeg`, `image/gif` and `image/webp` with the standard library or a justified module; animated input uses its first frame, and a corrupt or unsupported file degrades to a named placeholder.
- [ ] Layout: fit into a column and row budget, preserve aspect, never upscale, snap to the terminal cell box, and bound decoded pixels before allocation.
  Cell pixel size comes from the window ioctl where the platform offers it and falls back to 8×16; a missing size must not panic.
- [ ] TUI: an image entry occupies measured placeholder cells, so the viewport, selection and copy paths keep working.
  The kitty transmit escape is sent once per image id and re-sent after a resize or a full repaint; the placeholder cells carry the id colour.
  The contract is a test with a fake terminal, not only a manual look.
- [ ] The classic REPL reserves blank rows for an iTerm2 or sixel image, draws over them with cursor save and restore, and keeps the escapes out of the replayed scrollback; a non-TTY writes the file name only.
- [ ] Tool-produced images use the same path: the `screenshot` result of WP3.1 and an MCP image result get a name and are drawn after the tool entry, without a second image store.
- [ ] Resize, scroll, theme switch and a long transcript must not leak escape bytes into copied text, the status line or the session file; `display.inline_images: off` and a non-TTY produce the same readable placeholder.
- [ ] Tests: golden bytes for each encoder, a table of detection environments, width and height with no-upscale cases, a corrupt image, an oversized image, a non-TTY and `off`; a TUI test that the placeholder rows measure as their cell width.
- [ ] Manual matrix documented in the PR: kitty, Ghostty, WezTerm, iTerm2 (REPL), Konsole (REPL), VS Code terminal, a plain xterm fallback, tmux passthrough and an SSH session.

Decisions to make when the work starts, recorded here: whether iTerm2 and sixel in the TUI are in scope or REPL-only, and how the kitty transmit is sequenced against Bubble Tea v2's renderer.
The current expectation is kitty and half-blocks in the TUI, with iTerm2 and sixel in the REPL, because a line-based renderer cannot reserve unmeasured rows for an OSC 1337 image without controlling the whole screen.

## Acceptance criteria

1. `hint -p @screenshot.png "why does this dialog show an error"` answers from the image on a native-vision profile; a text-only profile gets a description or a clear refusal.
2. In the TUI, an image attached by path, URL or clipboard is drawn in the transcript on kitty/Ghostty and as half-blocks elsewhere; after a restart the same image is still part of the session.
3. A PDF, DOCX or text document attached by path or URL is answered from its extracted text; an unsupported binary names itself in a notice.
4. In the TUI, push-to-talk records a question, the answer streams as text and (with `--speak`) as audio.
5. `hint --voice` sustains a hands-free multi-turn dialogue on a Raspberry Pi with only Ollama + whisper.cpp + piper (no network).
