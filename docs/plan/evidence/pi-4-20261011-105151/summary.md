# pi-bench 20261011-105151

## Environment

| key | value |
|---|---|
| run_id | `20261011-105151` |
| date | `2026-10-11T10:51:51+03:00` |
| board | `Raspberry Pi 4 Model B Rev 1.1` |
| kernel | `Linux 6.18.34+rpt-rpi-v8 aarch64` |
| os | `Debian GNU/Linux 13 (trixie)` |
| cpu_max_mhz | `1500.0000` |
| mem_total | `3.7Gi` |
| governor | `ondemand` |
| arm_clock_idle | `1500345728` |
| temp_idle | `50.1'C` |
| throttled_at_start | `0x0` |
| core_volts | `0.8688V` |
| hint | `hint version 0.1.0-SNAPSHOT-b2aee40 (commit b2aee40, built 2026-10-10T12:02:12Z)` |
| llama_server | `version: 0.4.0-dev (build 1, commit df03399)` |
| llama_commit | `df03399` |
| model | `/home/pi/llama.cpp/models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf (1.1G)` |
| server_args | `-t 4 -c 4096 -np 1 --jinja --metrics` |
| header_timeout | `30m` |
| model_load_s | `28` |
| throttled_at_end | `0xe0000` |

## Clock, temperature, throttling during the run

- samples: 689, every ~2.1 s
- ARM clock: min 600 MHz, max 1500 MHz
- temperature: max 85.7 °C
- `get_throttled` values seen: `0x0`, `0x80000`, `0x80008`, `0xe0000`, `0xe0006`, `0xe0008`

`0x0` means no under-voltage and no throttling; bit 0x1 is under-voltage now, 0x4 throttled now, 0x10000/0x40000 the same since boot.

## Raw model speed (llama-server, prompt cache off)

| prompt tokens | prompt tok/s | prompt time, s | generated tokens | gen tok/s | wall, s |
|---:|---:|---:|---:|---:|---:|
| 538 | 7.46 | 72.1 | 64 | 3.34 | 91.0 |
| 1019 | 6.89 | 147.9 | 64 | 2.68 | 171.4 |
| 1969 | 5.53 | 356.1 | 64 | 1.97 | 388.2 |

## hint turns

| run | exit | wall, s | input tokens | cached | processed by server | server prompt s | prompt tok/s | output tokens | failover notice |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---|
| turn1 | 0 | 554.3 | 4896 | 2430 | 2466 | 464.0 | 5.31 | 163 | no |
| turn2-continue | 0 | 54.7 | 2639 | 2614 | 25 | 6.9 | 3.61 | 86 | no |
| turn1-new-session | 0 | 64.7 | 4896 | 4847 | 49 | 13.2 | 3.71 | 94 | no |
| failover | 0 | 76.2 | 4896 | 4847 | 49 | 12.7 | 3.86 | 108 | yes |

*input tokens* and *cached* come from the usage hint reports (summed over the turn's requests); *processed by server* is the delta of llama-server's prompt counter, i.e. the tokens the model actually had to read. The gap between the two is the prefix cache.

- `turn1` answer: The files in this project are:  1. **README.md**    This is a README file which could contain instructions, instructions, or a summary of the project.  2. **greet.go**    This is a Go file for implementing the greeting functionality. It likely contains functions to create greeting messages.  3. **ma…
- `turn2-continue` answer: To change a file to add a `--version` flag, I would need to know the specific file and line number where this change should occur. Since the project's structure and specific files are not specified, I can't pinpoint the exact file or line to modify. If you have more context or the specific file you'…
- `turn1-new-session` answer: The files currently in this project are: - `README.md` - `greet.go` - `main.go`  `README.md` is the project's README, which is the starting point for the project's instructions and documentation. The `greet.go` and `main.go` files are likely source code files where the project's functionality is imp…
- `failover` answer: The files currently in the project are as follows:  - **README.md**: This is a plain text file that likely contains project instructions or documentation. - **greet.go**: This is a Go file that contains a simple greeting function. - **main.go**: This is a Go file that likely contains the entry point…
