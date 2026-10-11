# pi-bench

A reproducible measurement of `hint` with a local `llama-server` on a
Raspberry Pi. It backs the numbers in
[`docs/raspberry-pi-4-4gb.md`](../../docs/raspberry-pi-4-4gb.md) and is
meant to be re-run after any change to the board, the model or hint itself.

## What it measures

| Step | What | Why |
|---|---|---|
| `raw` | Prompt-processing and generation speed of the bare model at 512 / 1024 / 2048 prompt tokens, prompt cache off | The ceiling everything else is bounded by |
| `turn` | One cold `hint -p` turn on the local profile | Real prompt size of hint (system prompt + tool schemas) and time to an answer |
| `cache` | A `-c` follow-up in the same session, then the same question in a new session | How much of hint's prompt llama-server serves from its prefix cache |
| `failover` | Default profile points at a refused port, fallback is local | Scenario 3: the router fails over with a stderr notice and no command change |

Clock, temperature, `get_throttled` and load average are sampled every
2 s throughout, so an under-voltage or thermal throttle is visible next to
the numbers it distorts.

The script starts its own `llama-server` (with `-np 1 --metrics`) so every
run begins with a cold cache and identical settings, and points hint at an
isolated XDG tree inside the results directory: the user's
`~/.config/hint`, sessions and logs are not read or modified. No cloud key
is needed — the "cloud" in the failover step is `127.0.0.1:9`.

## Requirements on the Pi

- `llama-server` built under `~/llama.cpp` and the GGUF in
  `~/llama.cpp/models/` (steps 2–3 of the Pi guide);
- a `linux/arm64` hint build that knows `header_timeout`;
- `curl`, `python3`, `git`, `timeout`; `vcgencmd` for the board readings;
- nothing listening on port 8080: stop a running `llama-server` first.

## Run

On the development machine:

```bash
GOOS=linux GOARCH=arm64 goreleaser build --single-target --snapshot --clean
scp dist/*linux_arm64*/hint pi@<host>:~/hint
scp -r scripts/pi-bench pi@<host>:~/
```

On the Pi, inside `tmux` (a full run takes from tens of minutes to hours
on a Pi 4):

```bash
~/pi-bench/pi-bench.sh
```

Results land in `~/pi-bench-results/<timestamp>/`; `summary.md` there is
the table to start from.

## Settings

All optional, as environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `HINT_BIN` | `~/hint` | hint binary |
| `LLAMA_DIR` | `~/llama.cpp` | llama.cpp checkout with `build/bin/llama-server` |
| `MODEL_PATH` / `MODEL_ALIAS` | Qwen2.5-1.5B-Instruct Q4_K_M | GGUF and the `-a` alias hint asks for |
| `THREADS` / `CTX` / `PORT` | `4` / `4096` / `8080` | `llama-server` `-t`, `-c`, `--port` |
| `HEADER_TIMEOUT` | `30m` | `header_timeout` of the local profile |
| `RUN_TIMEOUT` | `60m` | hard limit on one hint invocation |
| `RAW_SIZES` / `RAW_PREDICT` | `512 1024 2048` / `64` | raw-step prompt sizes and generated tokens |
| `STEPS` | `raw turn cache failover` | subset to run, e.g. `STEPS=raw` for a quick check |
| `OUT_ROOT` | `~/pi-bench-results` | where run directories are created |

## Results directory

```
summary.md          tables for the write-up (rebuild: python3 summarize.py <dir>)
bench.log           the console log of the run
samples.csv         epoch, ARM clock, temperature, get_throttled, load
env/                board, OS, versions, lscpu, llama-server /props
raw/                request, response and timings per prompt size
hint/               per run: JSON answer, stderr, runs.jsonl; debug/ traces
metrics/            llama-server /metrics before and after each hint run
server.log          llama-server output
work/               the three-file project hint was asked about
```

`hint/debug/` holds `--debug` traces with the full prompts — no secrets
(the only key is the placeholder `local`), but review before publishing.
