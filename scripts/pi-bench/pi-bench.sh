#!/usr/bin/env bash
# pi-bench.sh — measure hint with a local llama-server on a Raspberry Pi.
#
# Runs on the Pi. Starts its own llama-server (so every run is cold and
# configured the same), samples clock/temperature/throttling in the
# background, and records four experiments into one results directory:
#
#   1. raw    — prompt-processing and generation speed of the bare model
#               over /v1/chat/completions, at several prompt sizes, with
#               the prompt cache disabled;
#   2. turn   — one hint turn against the local profile (cold cache);
#   3. cache  — a follow-up turn in the same session (-c) and a fresh
#               session with the same question, to see how much of the
#               prompt the server serves from its prefix cache;
#   4. failover — default profile points at an unreachable "cloud", the
#               router must fall back to the local profile unchanged.
#
# hint runs against an isolated XDG tree inside the results directory, so
# the user's ~/.config/hint, sessions and logs are neither read nor
# touched. Everything is configured through environment variables; see
# README.md next to this file.

set -euo pipefail

# ---- settings (override via environment) ----------------------------------

HINT_BIN=${HINT_BIN:-$HOME/hint}
LLAMA_DIR=${LLAMA_DIR:-$HOME/llama.cpp}
MODEL_PATH=${MODEL_PATH:-$LLAMA_DIR/models/Qwen2.5-1.5B-Instruct-Q4_K_M.gguf}
MODEL_ALIAS=${MODEL_ALIAS:-Qwen2.5-1.5B-Instruct-Q4_K_M}
PORT=${PORT:-8080}
THREADS=${THREADS:-4}
CTX=${CTX:-4096}
HEADER_TIMEOUT=${HEADER_TIMEOUT:-30m}
# Upper bound on one hint invocation, so a stuck run cannot hold the bench.
RUN_TIMEOUT=${RUN_TIMEOUT:-60m}
RAW_SIZES=${RAW_SIZES:-"512 1024 2048"}
RAW_PREDICT=${RAW_PREDICT:-64}
SAMPLE_EVERY=${SAMPLE_EVERY:-2}
STEPS=${STEPS:-"raw turn cache failover"}
OUT_ROOT=${OUT_ROOT:-$HOME/pi-bench-results}

QUESTION_1=${QUESTION_1:-"what files are in this project and what do they do"}
QUESTION_2=${QUESTION_2:-"which file would you change to add a --version flag, and why?"}

# ---- helpers ---------------------------------------------------------------

# elapsed START END prints END-START in seconds; bc is not on every Pi image.
elapsed() { awk -v a="$1" -v b="$2" 'BEGIN { printf "%.3f", b - a }'; }

BASE_URL="http://127.0.0.1:$PORT"
RUN_ID=$(date +%Y%m%d-%H%M%S)
OUT="$OUT_ROOT/$RUN_ID"

log() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*" | tee -a "$OUT/bench.log" >&2; }
die() { log "error: $*"; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# vc prints one vcgencmd reading, or "n/a" off a Raspberry Pi.
vc() {
	if have vcgencmd; then vcgencmd "$@" 2>/dev/null | sed 's/^[^=]*=//'; else echo n/a; fi
}

# metrics saves the server's Prometheus counters under the given name.
metrics() {
	curl -sf "$BASE_URL/metrics" >"$OUT/metrics/$1.txt" || echo "# /metrics unavailable" >"$OUT/metrics/$1.txt"
}

# run_hint NAME ARGS... runs hint in the scratch project with the isolated
# XDG tree, capturing stdout (JSON), stderr, wall time and the server
# counters before and after.
run_hint() {
	local name=$1
	shift
	metrics "$name.before"
	local start end rc=0
	start=$(date +%s.%N)
	(
		cd "$WORK"
		env -u HINT_PROVIDER -u HINT_FALLBACK_PROVIDER -u HINT_API_KEY -u HINT_API_URL -u HINT_MODEL \
			-u HINT_CONTEXT_WINDOW -u HINT_DEBUG \
			XDG_CONFIG_HOME="$XDG/$CONFIG_SET" XDG_DATA_HOME="$XDG/data" \
			XDG_STATE_HOME="$XDG/state" XDG_CACHE_HOME="$XDG/cache" \
			timeout "$RUN_TIMEOUT" "$HINT_BIN" --debug --output json "$@"
	) >"$OUT/hint/$name.json" 2>"$OUT/hint/$name.stderr" || rc=$?
	end=$(date +%s.%N)
	metrics "$name.after"
	printf '{"name":"%s","exit":%d,"wall_s":%s,"args":%s}\n' \
		"$name" "$rc" "$(elapsed "$start" "$end")" \
		"$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1:]))' "$@")" >>"$OUT/hint/runs.jsonl"
	log "  $name: exit $rc, $(elapsed "$start" "$end") s"
}

# write_config SET DEFAULT [FALLBACK] writes $XDG/SET/hint/config.yaml with
# the local profile and an unreachable "cloud" profile. Port 9 (discard)
# refuses connections on a stock Pi, which the router treats as a network
# failure: retry, then fail over.
write_config() {
	local set=$1 default=$2 fallback=${3:-}
	mkdir -p "$XDG/$set/hint"
	{
		cat <<EOF
providers:
  - name: local
    kind: openai
    base_url: $BASE_URL/v1
    api_key: local
    model: $MODEL_ALIAS
    context_window: $CTX
    header_timeout: $HEADER_TIMEOUT
  - name: cloud-down
    kind: openai
    base_url: http://127.0.0.1:9/v1
    api_key: unused
    model: gpt-4o
default_provider: $default
EOF
		if [ -n "$fallback" ]; then echo "fallback_provider: $fallback"; fi
	} >"$XDG/$set/hint/config.yaml"
}

# ---- preflight -------------------------------------------------------------

mkdir -p "$OUT"/{raw,hint,metrics,env}
log "results: $OUT"

for tool in curl python3 git timeout; do have "$tool" || die "$tool not found"; done
[ -x "$HINT_BIN" ] || die "hint binary not found at $HINT_BIN (set HINT_BIN)"
[ -x "$LLAMA_DIR/build/bin/llama-server" ] || die "llama-server not built under $LLAMA_DIR"
[ -f "$MODEL_PATH" ] || die "model not found: $MODEL_PATH"
if curl -s -o /dev/null "$BASE_URL/health"; then
	die "something already listens on :$PORT — stop the running llama-server first (the bench starts its own)"
fi

# ---- environment snapshot --------------------------------------------------

{
	echo "run_id: $RUN_ID"
	echo "date: $(date -Is)"
	echo "board: $(tr -d '\0' 2>/dev/null </proc/device-tree/model || echo unknown)"
	echo "kernel: $(uname -srm)"
	echo "os: $(. /etc/os-release && echo "$PRETTY_NAME")"
	echo "cpu_max_mhz: $(lscpu | awk -F: '/max MHz/ {gsub(/ /,"",$2); print $2}')"
	echo "mem_total: $(free -h | awk '/^Mem:/ {print $2}')"
	echo "governor: $(cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo n/a)"
	echo "arm_clock_idle: $(vc measure_clock arm)"
	echo "temp_idle: $(vc measure_temp)"
	echo "throttled_at_start: $(vc get_throttled)"
	echo "core_volts: $(vc measure_volts core)"
	echo "hint: $("$HINT_BIN" --version 2>&1 | head -1)"
	echo "llama_server: $("$LLAMA_DIR/build/bin/llama-server" --version 2>&1 | grep -m1 -i version || echo unknown)"
	echo "llama_commit: $(git -C "$LLAMA_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
	echo "model: $MODEL_PATH ($(du -h "$MODEL_PATH" | cut -f1))"
	echo "server_args: -t $THREADS -c $CTX -np 1 --jinja --metrics"
	echo "header_timeout: $HEADER_TIMEOUT"
} >"$OUT/env/env.txt"
lscpu >"$OUT/env/lscpu.txt"
log "throttled at start: $(vc get_throttled), arm clock $(vc measure_clock arm)"

# ---- background sampler ----------------------------------------------------

echo "epoch,arm_hz,temp_c,throttled,load1" >"$OUT/samples.csv"
(
	while :; do
		printf '%s,%s,%s,%s,%s\n' "$(date +%s)" "$(vc measure_clock arm)" \
			"$(vc measure_temp | tr -d "'C")" "$(vc get_throttled)" \
			"$(cut -d' ' -f1 /proc/loadavg)" >>"$OUT/samples.csv"
		sleep "$SAMPLE_EVERY"
	done
) &
SAMPLER=$!

# ---- llama-server ----------------------------------------------------------

log "starting llama-server"
"$LLAMA_DIR/build/bin/llama-server" -m "$MODEL_PATH" -a "$MODEL_ALIAS" \
	--jinja -t "$THREADS" -c "$CTX" -np 1 --metrics \
	--host 127.0.0.1 --port "$PORT" >"$OUT/server.log" 2>&1 &
SERVER=$!

cleanup() {
	kill "$SAMPLER" 2>/dev/null || true
	kill "$SERVER" 2>/dev/null || true
	wait "$SERVER" 2>/dev/null || true
}
trap cleanup EXIT

load_start=$(date +%s)
until curl -sf "$BASE_URL/health" >/dev/null; do
	kill -0 "$SERVER" 2>/dev/null || die "llama-server exited; see $OUT/server.log"
	sleep 1
done
echo "model_load_s: $(($(date +%s) - load_start))" >>"$OUT/env/env.txt"
curl -sf "$BASE_URL/props" >"$OUT/env/props.json" || true
log "server ready in $(($(date +%s) - load_start)) s"

# ---- scratch project and isolated hint config -----------------------------

WORK="$OUT/work"
XDG="$OUT/xdg"
mkdir -p "$WORK"
cat >"$WORK/README.md" <<'EOF'
# greeter

A tiny command-line tool that prints a greeting.
EOF
cat >"$WORK/main.go" <<'EOF'
package main

import (
	"flag"
	"fmt"
)

func main() {
	name := flag.String("name", "world", "who to greet")
	flag.Parse()
	fmt.Println(greet(*name))
}
EOF
cat >"$WORK/greet.go" <<'EOF'
package main

func greet(name string) string { return "hello, " + name }
EOF
git -C "$WORK" init -q && git -C "$WORK" add -A && git -C "$WORK" -c user.name=bench -c user.email=bench@localhost commit -qm init

write_config local-only local
write_config failover cloud-down local

step() { [[ " $STEPS " == *" $1 "* ]]; }

# ---- 1. raw model speed ----------------------------------------------------

if step raw; then
	log "raw: sizes $RAW_SIZES, $RAW_PREDICT predicted tokens each"
	for size in $RAW_SIZES; do
		python3 - "$size" "$MODEL_ALIAS" "$RAW_PREDICT" "$RUN_ID" >"$OUT/raw/req-$size.json" <<'EOF'
import json, sys
size, model, predict, nonce = int(sys.argv[1]), sys.argv[2], int(sys.argv[3]), sys.argv[4]
sentence = "The quick brown fox jumps over the lazy dog. "  # ~10.7 tokens with the Qwen2.5 vocab
text = f"[{nonce}-{size}] " + sentence * max(1, round(size / 10.7))
print(json.dumps({
    "model": model,
    "messages": [{"role": "user", "content": text + "\nSummarize in one sentence."}],
    "max_tokens": predict,
    "ignore_eos": True,      # llama-server extension: always generate max_tokens
    "cache_prompt": False,   # llama-server extension: measure a cold prompt
    "temperature": 0,
}))
EOF
		start=$(date +%s.%N)
		curl -sf "$BASE_URL/v1/chat/completions" -H 'Content-Type: application/json' \
			-d @"$OUT/raw/req-$size.json" >"$OUT/raw/resp-$size.json" || die "raw request $size failed"
		end=$(date +%s.%N)
		python3 - "$size" "$(elapsed "$start" "$end")" "$OUT/raw/resp-$size.json" >>"$OUT/raw/results.jsonl" <<'EOF'
import json, sys
t = json.load(open(sys.argv[3]))["timings"]
print(json.dumps({"target": int(sys.argv[1]), "wall_s": float(sys.argv[2]), **t}))
EOF
		log "  $size: $(python3 -c 'import json,sys; t=json.loads(open(sys.argv[1]).readlines()[-1]); print("%d prompt tok @ %.2f/s, %d gen tok @ %.2f/s" % (t["prompt_n"], t["prompt_per_second"], t["predicted_n"], t["predicted_per_second"]))' "$OUT/raw/results.jsonl")"
	done
fi

# ---- 2–3. hint turn and prompt cache ---------------------------------------

CONFIG_SET=local-only
if step turn; then
	log "turn: cold one-shot on the local profile"
	run_hint turn1 -p "$QUESTION_1"
fi
if step cache; then
	step turn || run_hint turn1 -p "$QUESTION_1"
	log "cache: follow-up in the same session"
	run_hint turn2-continue -c -p "$QUESTION_2"
	log "cache: same question, new session"
	run_hint turn1-new-session -p "$QUESTION_1"
fi

# ---- 4. failover -----------------------------------------------------------

if step failover; then
	CONFIG_SET=failover
	log "failover: default profile unreachable, fallback local"
	run_hint failover -p "$QUESTION_1"
	if grep -q 'falling back to "local"' "$OUT/hint/failover.stderr"; then
		log "  failover notice present"
	else
		log "  failover notice MISSING"
	fi
fi

# ---- collect ---------------------------------------------------------------

cp -r "$XDG/state/hint/log" "$OUT/hint/debug" 2>/dev/null || true
echo "throttled_at_end: $(vc get_throttled)" >>"$OUT/env/env.txt"
python3 "$(dirname "$0")/summarize.py" "$OUT" >"$OUT/summary.md"
log "done: $OUT/summary.md"
