# Offline fallback on Raspberry Pi 4 (4 GB)

How to run `hint` with a local OpenAI-compatible model on a **4 GB Raspberry Pi 4**
(or any similar `linux/arm64` board with ~4 GiB RAM).

This is a **product guide**: devices, use cases, why this stack, launch recipe,
and examples. It is **not** the Phase 0 acceptance gate — that remains a
**Raspberry Pi 5** run with Ollama
([smoke-test checklist](plan/raspberry-pi-smoke-test.md)).

## Devices and use cases

| Device | Fits this guide? | Typical use |
|---|---|---|
| Raspberry Pi 4 Model B, **4 GB** | Yes (primary target) | Desk / lab board: cloud primary, local failover when the net drops |
| Pi 4 **2 GB** | Marginal | Only with a smaller GGUF and lower `-c`; expect swapping and poor tokens/s |
| Pi 4 **8 GB** | Yes, with headroom | Same recipe; optional larger Q4 (e.g. ~3B) once 1.5B tool quality is not enough |
| Pi 5 **4–8 GB** | Prefer Ollama + plan defaults | Phase 0 / `v0.2.0` gate uses Pi 5 + Ollama (`qwen2.5:7b` class) |
| Other `aarch64` SBCs (~4 GiB) | Yes if CPU-only | Same memory math; retune `-t` to core count |

**When to use this setup**

- You want scenario-3 behaviour: same `hint` command, cloud fails, local
  profile answers, no flag change.
- The board is always-on (or often available) next to a flaky uplink.
- You accept a **small** instruct model: good enough for short tools /
  confirmations, not a substitute for a cloud coding model.

**When not to**

- You need the official Phase 0 / `v0.2.0` hardware checkbox → Pi 5 checklist.
- You insist on stock Ollama + 7B on 4 GB → it does not fit the working set.
- You need strong agent quality offline → use a larger host, or stay on cloud.

## Why this stack

Two things run on the board:

1. **`hint`** — CGO-free `linux/arm64` CLI (cross-built; do not compile Go on the Pi).
2. **A local Chat Completions server** — so the existing `kind: openai` client
   talks to localhost with no new provider kind.

The binding constraint is **RAM**, not ISA. After OS + `hint` + the server,
plan on roughly **3.0–3.2 GiB** for weights + KV-cache on a 4 GB Pi 4.

| Choice | Why it wins here | Rejected alternative | Cost of the alternative |
|---|---|---|---|
| `llama-server` (llama.cpp) | OpenAI `/v1` dialect; low RAM; no extra daemon | Ollama | Extra resident process; 7B-class defaults do not fit 4 GB |
| ~1.5B Q4_K_M GGUF (e.g. Qwen2.5-1.5B) | Weights ~1.1 GiB; room for KV + OS | Stock Gemma 4 / 7B Q4 | Weights alone exceed or starve the budget |
| `kind: openai` + `api_key: local` | Same client as cloud; validator requires a non-empty key | `kind: ollama` | Needs Ollama’s native ready probe; wrong stack on 4 GB |
| Alias `-a` matching `model:` | `/v1/models` `id` stays short and stable | Path-as-id | Config and `HINT_PROVIDER=local models` become fragile |
| Context `-c 4096` | Tool + system preamble exceeds 2048 tokens | `-c 2048` | Prefill / request failures mid-agent loop |
| `--host 127.0.0.1` | Only local `hint` should call the server | `0.0.0.0` | Open model API on a reachable board |
| Static cmake (`BUILD_SHARED_LIBS=OFF`, tests off, `--target llama-server`) | Reliable link on current llama.cpp master | Shared `all` build | Link failures unrelated to OOM |
| Cross-build `hint` with goreleaser | Keeps Pi RAM for the model | Build Go on the Pi | Wastes the memory budget |

Local serving speaks the same OpenAI HTTP dialect as the cloud profile, so
the router (`fallback_provider`) stays provider-agnostic: only YAML and the
process behind the socket change. A dedicated `kind: llamacpp` would name
the stack more clearly, but adds another client for no protocol gain here.

```
hint (linux/arm64)
  └─ provider.Chat
       ├─ primary:  openai → cloud (e.g. api-bar)
       └─ fallback: openai → http://127.0.0.1:8080/v1
                              └─ llama-server + Qwen2.5-1.5B Q4_K_M
```

Router policy (compressed): retry primary on network / timeout / 429 / 5xx;
fail over only before the first user-visible event; cancellation never
retries. Scenario 3’s pass signal is the stderr failover notice plus a
completed turn with **no** command change.

## Memory budget

| Piece | Approx. |
|---|---|
| Raspberry Pi OS, ssh, services | 0.4–0.6 GiB |
| `llama-server` overhead | 0.1–0.2 GiB |
| Qwen2.5-1.5B Q4_K_M weights | ~1.1 GiB |
| KV-cache at 4096 context | larger than at 2048; stay out of swap |
| `hint` during a turn | tens of MiB |
| Headroom / page cache | remainder of ~3.7 GiB |

Swap is crash safety, not throughput. If the working set pages to SD/eMMC,
tokens/s have already failed.

## Recipe

Commands assume you are on the Pi as a normal user, and that you will copy
a current `linux/arm64` hint archive from a development machine.

### 1. Packages

```bash
sudo apt-get update
sudo apt-get install -y ripgrep cmake build-essential libcurl4-openssl-dev
rg --version
```

If Ollama was installed earlier and is still enabled, park it so it does not
compete for RAM:

```bash
sudo systemctl disable --now ollama
```

### 2. Build `llama-server`

```bash
git clone --depth 1 https://github.com/ggml-org/llama.cpp.git ~/llama.cpp
cd ~/llama.cpp
rm -rf build
cmake -B build -DCMAKE_BUILD_TYPE=Release -DGGML_NATIVE=ON \
  -DBUILD_SHARED_LIBS=OFF \
  -DLLAMA_BUILD_TESTS=OFF -DLLAMA_BUILD_EXAMPLES=OFF
cmake --build build --config Release -j2 --target llama-server
./build/bin/llama-server --version
```

Expect tens of minutes on a Pi 4. If the compiler OOMs, use `-j1`.
`GGML_NATIVE=ON` targets Cortex-A72 without DotProd/i8mm/SVE — set
throughput expectations accordingly.

### 3. Fetch the GGUF

```bash
mkdir -p ~/llama.cpp/models
curl -L --fail -o ~/llama.cpp/models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf
```

### 4. Run the server

```bash
cd ~/llama.cpp
./build/bin/llama-server \
  -m ./models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf \
  -a Qwen2.5-1.5B-Instruct-Q4_K_M \
  --jinja -t 4 -c 4096 --host 127.0.0.1 --port 8080
```

In a second session:

```bash
curl -s http://127.0.0.1:8080/v1/models
```

`--jinja` is required for tool-calling chat templates. `-c 4096` leaves
room for hint’s system/tool preamble (2048 is too small for a full agent
turn). Optional: `-np 1` if you never need concurrent slots.

### 5. hint config on the Pi

```bash
mkdir -p ~/.config/hint
cat > ~/.config/hint/config.yaml <<'EOF'
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
EOF
```

Zero-config from `API_BAR_KEY` alone synthesizes only a cloud profile.
Without this file there is nothing to fail over to. Keep `model:` equal to
the `-a` alias.

### 6. Build and copy hint (development machine)

```bash
cd hint
go install github.com/goreleaser/goreleaser/v2@latest   # once
export PATH="$(go env GOPATH)/bin:$PATH"
goreleaser release --snapshot --clean
scp dist/hint_*_linux_arm64.tar.gz pi@<host>:~/
```

On the Pi:

```bash
tar xzf hint_*_linux_arm64.tar.gz
# Snapshot archives often unpack flat into $PWD (hint + LICENSE + README),
# not into a hint_*_linux_arm64/ directory.
HINT="$HOME/hint"
uname -m            # aarch64
$HINT --version
export API_BAR_KEY=...   # do not paste secrets into shared logs
$HINT models
HINT_PROVIDER=local $HINT models
```

### 7. Examples

From a scratch directory, use an absolute path to the binary:

```bash
HINT="$HOME/hint"
mkdir -p ~/smoke && cd ~/smoke

# Cloud path — agent should call list_dir / read_file itself
$HINT -p "what files are in this project and what do they do"

# Edit with confirmation
$HINT "add a short comment to README explaining this is a smoke dir"

# Offline fallback — block or disconnect the cloud; same command
$HINT -p "what files are in this project and what do they do"

# Session continuity
$HINT -p "remember the number 42"
$HINT -c -p "what number did I ask you to remember?"
```

Offline pass: `llama-server` stays up, stderr shows the router failover
notice, the turn completes on `local`. Prefill on a Pi 4 A72 can look idle
while all cores sit at 100% — wait before concluding a hang.

## Limits to expect

- A 1.5B model may refuse or mishandle tools even when serving is correct.
  That is a **model-class** limit, not proof that failover is broken. Confirm
  with `HINT_PROVIDER=local $HINT models` and the stderr failover line first.
- Active cooling matters; an uncooled A72 invalidates any timing anecdote.
- For the `v0.2.0` gate and the plan’s default Ollama example, use a
  Raspberry Pi 5 and
  [`plan/raspberry-pi-smoke-test.md`](plan/raspberry-pi-smoke-test.md).
