# Journal — Raspberry Pi 4 (4 GB) local fallback run

> **Not product documentation.** Source material for a later article
> (“how it went”): chronological incident log of what broke, what we tried,
> and what fixed it. Prefer appending new entries over rewriting old ones.
> Narrative polish belongs in the article, not here.
>
> Product launch guide: [`raspberry-pi-4-4gb.md`](raspberry-pi-4-4gb.md).
> Pi 5 acceptance checklist:
> [`plan/raspberry-pi-smoke-test.md`](plan/raspberry-pi-smoke-test.md).
>
> Do not treat this file as part of the shipped doc set until the article
> is intentionally published.

## Context

| | |
|---|---|
| Host | `boson` (SSH), hostname `higgs` |
| Hardware | Raspberry Pi 4 class, `aarch64`, Cortex-A72 |
| RAM / swap | 3.7 GiB / 2.0 GiB (measured idle) |
| Disk | ~106 GiB free on `/` |
| Goal | Stand up `hint` + a local OpenAI-dialect model for scenario 3 (offline fallback). **Not** the WP0.10 / `v0.1-alpha` gate (that requires a Pi 5). |
| Dates | 2026-09-11 … 2026-09-13 |

## Incident log

### J1 — Stale release archive in `dist/`

**Symptom.** `hint/dist` already had
`hint_0.1.0-SNAPSHOT-09d5963_linux_arm64.tar.gz`. Tempting to `scp` it and
skip a rebuild.

**Cause.** Archive commit `09d5963` is WP0.9. `develop` had moved to
`9ffb1b0` with WP0.10–WP0.12. PLAN.md deferred the Pi run until those
packages landed. A green run on `09d5963` would not describe the binary
we would tag.

**Fix.** Rebuild with `goreleaser release --snapshot --clean` (or
`--single-target`) from the commit under test. Do not ship the host
`hint/hint` binary either — wrong GOARCH.

**Status.** Decision recorded; rebuild still pending when this journal
entry was written.

---

### J2 — Missing prerequisites on the Pi (`rg`, Ollama)

**Symptom.**

```text
which rg || true   → rg not found
ollama --version   → command not found
```

**Cause.** Fresh board relative to the smoke-test prerequisites.

**Fix (first pass).**

```bash
sudo apt-get update
sudo apt-get install -y ripgrep
# then official Ollama ARM64 install script
curl -fsSL https://ollama.com/install.sh | sh
```

`rg` stayed: needed for WP0.5's ripgrep code path (full pass with `rg`,
spot-check scenario 1 without it).

Ollama was later **parked** — see J4.

**Status.** Fixed. `ripgrep 14.1.1` confirmed.

---

### J3 — Model class: 7B and stock Gemma 4 do not fit 4 GB

**Symptom.** Example config / plan text points at `qwen2.5:7b`. User
preferred Gemma 4 as “compact”.

**Cause.** Working RAM after OS ≈ 3.0–3.2 GiB. Weights alone:

| Candidate | Approx. size | Fits? |
|---|---|---|
| `qwen2.5:7b` Q4 | ~4.7 GB | No |
| `gemma4` / `e4b` | 9.6 GB | No |
| `gemma4:e2b` | 7.2 GB | No (“E2B” is effective params; PLE inflates the file) |
| `gemma4:e2b-it-qat` | 4.3 GB | Borderline / swap |
| `Qwen2.5-1.5B` Q4_K_M | ~1.1 GB | Yes (first pick) |
| `Qwen2.5-3B` Q4_K_M | ~1.9 GB | Next if tools fail |

**Fix.** Drop to ~1.5B Q4_K_M for this board. Cap context at 2048
(`-c 2048`). Scenario 3 tests the **router**, not 7B quality.

**Status.** Decision locked for this run.

---

### J4 — Ollama is the wrong serving stack on 4 GB

**Symptom.** Ollama 0.34.0 installed and running as systemd on
`127.0.0.1:11434`. Matches WP0.3's `ollama-native` path on a Pi 5 /
desktop.

**Cause.** Ollama wraps the same llama.cpp kernels in a Go daemon + model
manager (~100–300 MB). On 4 GB that overhead is unaffordable. LiteRT-LM
rejected separately: no HAT on Pi 4, Python runtime fights R1 (no-cgo).

**Fix.**

```bash
sudo systemctl disable --now ollama
```

Serve with `llama.cpp`'s `llama-server` → `/v1` → hint's existing
`kind: openai` client. No new provider kind.

**Status.** Fixed by policy. Package may remain on disk; daemon must not
be resident while `llama-server` runs.

---

### J5 — Config: `kind: openai` fallback without `api_key`

**Symptom.** Early appendix YAML had only:

```yaml
providers:
  - name: local
    kind: openai
    base_url: http://localhost:8080/v1
    model: <model>-Q4_K_M
fallback_provider: local
```

**Cause.** `config.validateUsable` requires an API key on every selected
`kind: openai` profile, including fallback. `llama-server` does not check
`Authorization`. Zero-config from `API_BAR_KEY` alone synthesizes only a
cloud profile — nothing to fail over to.

**Fix.** Full default + fallback pair; dummy key on local:

```yaml
providers:
  - name: api-bar
    kind: openai
    base_url: https://api-bar.ru/route/openai
    api_key: ${API_BAR_KEY}
    model: gpt-4o
  - name: local
    kind: openai
    base_url: http://127.0.0.1:8080/v1
    api_key: local
    model: Qwen2.5-1.5B-Instruct-Q4_K_M
default_provider: api-bar
fallback_provider: local
```

Preflight local with `HINT_PROVIDER=local ./hint models` (bare
`./hint models` only proves the cloud).

Also: bind `127.0.0.1` not `0.0.0.0`; pass `--jinja` so tool_calls exist;
pass `-a` so `model:` can be the short name (see J12). Without `-a`,
`/v1/models` advertises the GGUF path (J11).

**Status.** Fixed in checklist appendix and recipe docs.

---

### J6 — `cmake --build … -j2` (default `all`): tests fail to link

**Symptom.** Build reached ~61–62%, then:

```text
Linking CXX executable ../bin/test-tokenizer-0
/usr/bin/ld: ../bin/libllama-common.so.0.4.0: undefined reference to
  `gbnf_format_literal…`
  `json_schema_to_grammar…`
  `common_schema_info…`
  …
gmake: *** [Makefile:146: all] Error 2
```

Same for `test-recurrent-state-rollback`.

**Not the cause.** OOM. `free -h` showed ~3.4 GiB available, swap unused.
`-j2` was fine.

**Cause.** `cmake --build` without `--target` builds default `all` —
libraries **and** tests. On current master, shared `libllama-common.so`
was incomplete for those test binaries (post shared-`libllama-common`
split). Smoke does not need llama.cpp tests.

**Fix (partial).**

```bash
cmake -B build … -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF
cmake --build build --config Release -j2 --target llama-server
```

**Status.** Avoided the test link path; exposed J7.

---

### J7 — Shared `llama-server`: unresolved `server-chat` symbols

**Symptom.** With `--target llama-server` but still shared libs, build
reached 100% linking the executable and failed:

```text
Linking CXX executable ../../bin/llama-server
/usr/bin/ld: ../../bin/libllama-server-impl.so: undefined reference to
  `server_chat_convert_anthropic_to_oai(…)`
  `server_chat_msg_diff_to_json_oaicompat(…)`
  `server_chat_convert_responses_to_chatcmpl(…)`
  `convert_transcriptions_to_chatcmpl(…)`
collect2: error: ld returned 1 exit status
```

**Cause.** After the server-chat refactor, `server-chat.cpp` lives in
static `server-context`, while `llama-server-impl` is a shared library
when `BUILD_SHARED_LIBS` is on. The `.so` was produced with unresolved
symbols that the final executable link does not pull from
`libserver-context.a`. Same family as J6: incomplete shared-library link
on current master, not a missing source file on our side.

**Fix.**

```bash
cd ~/llama.cpp
rm -rf build   # required: wipe partial shared objects
cmake -B build -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=ON \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF
cmake --build build --config Release -j2 --target llama-server
```

Documented in llama.cpp as the static-build path
(`-DBUILD_SHARED_LIBS=OFF`).

**Status.** Fixed. 2026-09-13 rebuild ended:

```text
[100%] Linking CXX static library libllama-server-impl.a
[100%] Linking CXX executable ../../bin/llama-server
[100%] Built target llama-server
```

Verify next: `./build/bin/llama-server --version`.

---

### J8 — Docs drifted from dialogue commands

**Symptom.** Appendix Setup still had placeholders (`apt install` without
ripgrep/curl, `-j4`, `0.0.0.0`, YAML without `api_key`, no concrete GGUF)
while the dialogue and article recipe had moved on.

**Fix.** Synced appendix + article recipe to the working command set,
including J6/J7 flags. Article status table updated for the link failures
and static rebuild.

**Status.** Done for the build recipe. Remaining run steps (GGUF, serve,
hint archive, scenarios) still open.

---

### J9 — GGUF download succeeded

**Action.** After J7 static build:

```bash
mkdir -p models
curl -L --fail -o models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf
```

**Result.** `curl` followed the HF redirect (first ~1 KB HTML/meta, then the
blob): **1065 MB** in ~1m49s at ~9.8–10 MB/s. Exit 0 (`--fail` did not trip).

**Status.** Done. File on disk:
`~/llama.cpp/models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf`.

---

### J10 — `llama-server` listening

**Action.**

```bash
./build/bin/llama-server \
  -m ./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  --jinja -t 4 -c 2048 --host 127.0.0.1 --port 8080
```

**Result.** Success within ~7s of process start:

```text
model loaded
listening on http://127.0.0.1:8080
```

`n_threads = 4`, `n_ctx_slot = 2048`, `n_slots = 4`, `kv_unified = true`.

**Warnings (non-blocking, note for the article).**

| Log | Meaning for this run |
|---|---|
| CORS `*` and no API key | Risk if bound publicly; we use `127.0.0.1` only. hint still sends dummy `api_key: local` for its own validator. |
| control-looking token `</s>` type overridden | GGUF metadata quirk; server overrides and continues. |
| default port will become `:9931` later | Forward-looking notice; recipe stays on `8080` until we change it. |

**Status.** Done. Keep this SSH session open. Next: `curl` from a second session, then hint config.

---

### J11 — `GET /v1/models` OK; model id is the GGUF path

**Action** (second SSH session):

```bash
curl -s http://127.0.0.1:8080/v1/models
```

**Result.** OpenAI-shaped list. Relevant fields from `data[0]`:

| Field | Value |
|---|---|
| `id` / `aliases` | `./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf` |
| `owned_by` | `llamacpp` |
| `meta.n_ctx` | 2048 (matches `-c`) |
| `meta.n_params` | 1777088000 (~1.78B) |
| `meta.size` | 1111370240 (~1.04 GiB resident weights) |
| `meta.ftype` | `Q4_K - Medium` |

**Implication for hint config.** The profile `model:` must match what the
server advertises. Without `-a` that was the GGUF path (this entry). Fixed
by **J12** (`--alias`).

**Status.** Superseded by J12 for the preferred `model:` string. Dialects
endpoint itself was fine.

---

### J12 — `--alias` sets a short OpenAI model `id`

**Problem.** Without `-a`, `data[].id` was the GGUF path
(`./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf`), awkward in hint YAML.

**Fix.** Restart with llama-server's alias flag (documented: custom value for
model `id` via `--alias`):

```bash
./build/bin/llama-server \
  -m ./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  -a Qwen2.5-1.5B-Instruct-Q4_K_M \
  --jinja -t 4 -c 2048 --host 127.0.0.1 --port 8080
```

**Result.** `curl -s http://127.0.0.1:8080/v1/models` now returns:

```text
data[].id == "Qwen2.5-1.5B-Instruct-Q4_K_M"
```

`-m` stays the filesystem path; only the API-facing name changes. Multiple
aliases: comma-separated (`-a name1,name2`).

**Status.** Done. hint config `model:` can be the short name again.

---

### J13 — goreleaser archive unpacks flat; zsh glob fails

**Symptom.** After `scp` of `hint_0.1.0-SNAPSHOT-9ffb1b0_linux_arm64.tar.gz`
and `tar xzf …`:

```text
cd hint_*_linux_arm64
zsh: no matches found: hint_*_linux_arm64
cd hint
cd: not a directory: hint
```

`ls` showed `hint`, `LICENSE`, `README.md` in `$HOME` next to the `.tar.gz`.

**Cause.** This archive extracts the binary and docs into the **current
directory**, not into a subdirectory named like the archive stem. `hint` is
the executable file. zsh `nomatch` then rejects the missing directory glob.

**Fix.** Use the binary in place:

```bash
HINT="$HOME/hint"
$file $HINT   # expect ELF aarch64
$HINT --version
```

Docs that say `cd hint_*_linux_arm64` need a note for flat archives.

**Status.** Workaround in use. Docs still say the directory form in places.

---

### J14 — Both profiles list models; local needs server up

**Action** (from `~/smoke`, snapshot `9ffb1b0`):

```bash
HINT="$HOME/hint"
export API_BAR_KEY=…          # do not commit or paste into chat/logs
$HINT models
HINT_PROVIDER=local $HINT models
```

**Results.**

| Command | Outcome |
|---|---|
| `$HINT models` | OK — 132 models from profile `api-bar` |
| local while server down | `dial tcp 127.0.0.1:8080: connect: connection refused` |
| local after `llama-server` up | OK — `Qwen2.5-1.5B-Instruct-Q4_K_M` from `http://127.0.0.1:8080/v1` |

**Warning (expected, not a failure):**

```text
warning: fallback_provider "local" equals default_provider and was ignored
```

`HINT_PROVIDER=local` makes `default_provider` = `local` for that run. Config
still names `fallback_provider: local`, so the loader drops fallback (same
name twice cannot fail over to itself). Scenario 3 still uses default
`api-bar` + fallback `local` when `HINT_PROVIDER` is unset.

**Security.** An API key was pasted into an interactive shell that later
appeared in a shared terminal transcript. Rotate `API_BAR_KEY` on the
gateway; prefer `read`/`export` from a local file not copied into chat.

**Status.** Preflight for both profiles done. Next: scenarios 1–4.

---

### J15 — Failover works; local rejects oversized request (2048 ctx)

**Action.** Scenario 3 style: cloud `base_url` broken in config (or network
unreachable). `llama-server` up with `-c 2048`.

```text
hint: provider "api-bar" failed (network), falling back to "local"
hint: local: invalid_request: request (2389 tokens) exceeds the available
  context size (2048 tokens), try increasing it (400)
```

**Cause.** Failover itself is fine (router notice fired). The local turn
payload — system preamble + tool schemas + user message — is **2389**
tokens. `llama-server` was started with `-c 2048`, so it returns HTTP 400
before generation. Not a model-quality or tool-calling failure.

Earlier cloud scenarios 1 / 2 / 4 on this binary succeeded (list_dir on
empty `~/smoke`, session `-c` recalled 42, write_file with confirmation).

**Fix options (pick by RAM).**

1. Restart server with a larger context, e.g. `-c 4096` (still modest on
   4 GB with 1.5B Q4; watch `free -h` / swap).
2. Align the local profile: `context_window: 4096` in config so hint sizes
   compaction to the same budget (does not replace raising `-c` on the
   server — the hard limit is llama-server).
3. Shrink what hint sends (instruction budget / overview) — secondary;
   tool schemas alone are heavy for a 2K window.

**Status.** Open until re-run with `-c >= 4096` (or equivalent) passes a
local completion after failover.

---

### J16 — Scenario 3 pass after `-c 4096` (slow, weak answer OK)

**Action.** Cloud unreachable (bad `base_url` / network). `llama-server`
with larger context. During the wait, `htop` showed all 4 cores ~100% on
`llama-server` (~1.3 GiB RES, swap 0) — prefill, not a hang.

```text
hint: provider "api-bar" failed (network), falling back to "local"
I'm sorry, I can't remember the number 42. …
```

**Verdict.**

| Check | Result |
|---|---|
| Router notice on stderr | Pass |
| Failover without flag change | Pass |
| Local completion returns | Pass (after long CPU-bound wait) |
| Answer quality / “remember 42” | Fail as agent behaviour — 1.5B refuses / confuses; not a router bug |

**Notes for the article.** On Pi 4, “hung” after failover is usually
prefill of a ~2.4k-token tool preamble. Scenario 3 acceptance is the
failover path, not Qwen-1.5B as a memory agent. Use a tiny prompt
(`say ok`) to smoke latency; keep the checklist prompt for the real gate.

**Status.** Scenario 3 closed for this Pi 4 appendix run.

---

### J17 — Local after failover: tools, confirm, `-c`; board ID

**Hardware (from the Pi).**

```text
/proc/device-tree/model → Raspberry Pi 4 Model B Rev 1.1
/proc/cpuinfo Revision  → c03111          # 4 GB variant
free -h after turns     → ~623 MiB used, ~3.1 GiB available, swap 0
```

**Actions (cloud still broken; `llama-server` up).**

1. `$HINT -p "remember the number 42"` — failover notice; weak refusal (as J16).
2. `$HINT -p "… create a txt file with hello word"` — failover → diff preview →
   `Allow?` → `write_file` on `hello.txt` → completion. Failover notice also
   reappeared **after** the tool result (each LLM call in the turn retries
   primary first).
3. `$HINT -c -p "… create instruction.md …"` — session continued
   (`fc37cedb1fd1dd30`, 4 messages) → same failover → create + confirm →
   file on disk.

**Verdict.** On this board, offline fallback is usable for agentic edit
flows, not only one-shot chat. Repeated `falling back to "local"` lines in
one turn are expected with the current router (primary probed every
request). Memory headroom after the run was comfortable with 1.5B Q4 and
elevated context.

**Status.** Pi 4 appendix goals met: scenario 3 + local tools/session smoke.

---

## Working recipe (build + serve, as of J16)

```bash
# on the Pi, as pi
sudo apt-get update
sudo apt-get install -y ripgrep cmake build-essential libcurl4-openssl-dev
sudo systemctl disable --now ollama   # if previously installed

git clone --depth 1 https://github.com/ggml-org/llama.cpp.git ~/llama.cpp
cd ~/llama.cpp
rm -rf build
cmake -B build -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=ON \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF
cmake --build build --config Release -j2 --target llama-server
./build/bin/llama-server --version

mkdir -p models
# … download GGUF (J9) …

./build/bin/llama-server \
  -m ./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  -a Qwen2.5-1.5B-Instruct-Q4_K_M \
  --jinja -t 4 -c 4096 --host 127.0.0.1 --port 8080   # 2048 was too small: J15
```

## Next entries to append

- [x] GGUF download / disk / checksum notes — J9 (size via curl: 1065 MB; no separate checksum yet)
- [x] First `llama-server` listen — J10 (`model loaded`, `127.0.0.1:8080`)
- [x] `curl /v1/models` from a second SSH session — J11 (id is GGUF path)
- [x] `--alias` short model id — J12 (`Qwen2.5-1.5B-Instruct-Q4_K_M`)
- [x] hint snapshot `9ffb1b0` on Pi — J13 (flat tar; `~/hint`)
- [x] Config + both `./hint models` paths — J14 (local needs server; fallback warning OK)
- [x] Scenarios 1–4 outcomes — cloud 1/2/4 earlier; scenario 3 + local tools/`-c` J16–J17
- [x] Board identity — Pi 4 Model B Rev 1.1, rev `c03111`, 4 GB (J17)
- [ ] Rotate API key if it leaked into a shared transcript
- [x] Document default `-c 4096` + `-np 1` in the Pi 4 recipe — done in `raspberry-pi-4-4gb.md`; multi-failover-per-turn noted in J17
- [ ] Any further link/runtime failures after serve

## Index for the future article

Use this journal as the “what went wrong” spine. The polished “how to run
hint + a local net on Pi 4 4 GB” narrative should pull:

1. Hardware floor (C1) vs Phase 0 gate (Pi 5).
2. Why not Ollama / LiteRT-LM / 7B / stock Gemma 4 on this board.
3. Adapter via OpenAI dialect + dummy `api_key`.
4. The two cmake link failures (J6, J7) and why static `--target llama-server`
   is the Pi 4 recipe, not a generic desktop `cmake --build`.
5. Default model `id` is the GGUF path (J11); use `-a` / `--alias` for a short
   name in hint YAML (J12).
6. Snapshot tar may unpack flat (J13); `HINT_PROVIDER=local` suppresses
   fallback with a warning when names collide (J14).
