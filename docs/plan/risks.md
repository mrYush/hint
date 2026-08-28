# Cross-cutting risks and mitigations

| # | Risk | Phases affected | Mitigation |
|---|---|---|---|
| R1 | **cgo on mobile / for tree-sitter** breaks cross-compilation | 2, 5 | Keep cgo dependencies behind build tags; the cgo-free core must always build. Repo map degrades to "absent" without cgo; whisper.cpp/piper run as subprocesses, not linked. |
| R2 | **Reference licensing** — copying from incompatibly licensed projects | all | Study approaches everywhere; borrow code only from MIT/Apache (archived opencode, Aider idea-porting, official SDKs). Crush (FSL) and Claude Code are pattern sources only. |
| R3 | **OpenAI-compatible dialect drift** (tools/streaming fields differ across vLLM/Ollama/OpenRouter/api-bar.ru) | 0, 2, 3 | Integration tests against a provider matrix in CI. For api-bar.ru specifically, verify streaming, tool-calling, and multimodal endpoints empirically — there is no public documentation. |
| R4 | **App-store policies** (model-weight downloads, background work, sensor access) | 5, 6 | Early guideline review; weights downloaded post-install; `bash` replaced by a safe tool set on mobile; fallback channel — TestFlight/sideload. |
| R5 | **Feature-store tool security** | 7 (design starts in 2) | Permission manifests + sandbox by default + catalog review. External input (MCP tool output, files) is untrusted at the architecture level — the Claude Code lesson. |
| R6 | **Scope drag** — 8 phases with one part-time developer | all | Every phase is an independently useful release; phase files keep non-goals explicit; only the active phase is mirrored to GitHub Issues. |
| R7 | **Contract churn in `pkg/agentapi`** after clients exist | 1+ | Contract-first (WP0.1), versioned RPC handshake (WP1.2), golden wire-format tests; semver discipline from Phase 7. |
