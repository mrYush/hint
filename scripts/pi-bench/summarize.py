#!/usr/bin/env python3
"""Render a pi-bench results directory as a Markdown summary on stdout.

Reads what pi-bench.sh wrote — env/env.txt, raw/results.jsonl,
hint/runs.jsonl with the matching hint/*.json answers and stderr,
metrics/*.txt counter snapshots and samples.csv — and prints the tables
the write-up needs. Missing pieces are reported, not fatal, so a partial
run (STEPS=raw, an interrupted bench) still summarizes.
"""

import csv
import json
import sys
from pathlib import Path

FAILOVER_NOTICE = 'falling back to "local"'

# llama-server counters whose before/after delta describes one hint run.
COUNTERS = {
    "prompt_tokens": "llamacpp:prompt_tokens_total",
    "prompt_s": "llamacpp:prompt_seconds_total",
    "predicted_tokens": "llamacpp:tokens_predicted_total",
    "predicted_s": "llamacpp:tokens_predicted_seconds_total",
}


def read_jsonl(path: Path) -> list[dict]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def read_counters(path: Path) -> dict[str, float]:
    """Parse the Prometheus text format, keeping unlabelled samples only."""
    out: dict[str, float] = {}
    if not path.exists():
        return out
    for line in path.read_text().splitlines():
        if line.startswith("#") or not line.strip():
            continue
        name, _, value = line.rpartition(" ")
        try:
            out[name.strip()] = float(value)
        except ValueError:
            continue
    return out


def fmt(value: float | None, digits: int = 1) -> str:
    return "–" if value is None else f"{value:.{digits}f}"


def env_section(out: Path) -> list[str]:
    env = out / "env" / "env.txt"
    lines = ["## Environment", ""]
    if not env.exists():
        return lines + ["env/env.txt missing", ""]
    lines += ["| key | value |", "|---|---|"]
    for row in env.read_text().splitlines():
        key, _, value = row.partition(": ")
        lines.append(f"| {key} | `{value}` |")
    return lines + [""]


def samples_section(out: Path) -> list[str]:
    path = out / "samples.csv"
    lines = ["## Clock, temperature, throttling during the run", ""]
    rows = list(csv.DictReader(path.open())) if path.exists() else []
    clocks = [int(r["arm_hz"]) for r in rows if r["arm_hz"].isdigit()]
    temps = []
    for r in rows:
        try:
            temps.append(float(r["temp_c"]))
        except ValueError:
            pass
    if not rows:
        return lines + ["no samples (vcgencmd missing or run too short)", ""]
    throttled = sorted({r["throttled"] for r in rows})
    lines += [
        f"- samples: {len(rows)}, every ~{_interval(rows)} s",
        f"- ARM clock: min {min(clocks) / 1e6:.0f} MHz, max {max(clocks) / 1e6:.0f} MHz" if clocks else "- ARM clock: n/a",
        f"- temperature: max {max(temps):.1f} °C" if temps else "- temperature: n/a",
        f"- `get_throttled` values seen: {', '.join(f'`{t}`' for t in throttled)}",
        "",
        "`0x0` means no under-voltage and no throttling; bit 0x1 is under-voltage now,"
        " 0x4 throttled now, 0x10000/0x40000 the same since boot.",
        "",
    ]
    return lines


def _interval(rows: list[dict]) -> str:
    if len(rows) < 2:
        return "?"
    return f"{(int(rows[-1]['epoch']) - int(rows[0]['epoch'])) / (len(rows) - 1):.1f}"


def raw_section(out: Path) -> list[str]:
    rows = read_jsonl(out / "raw" / "results.jsonl")
    lines = ["## Raw model speed (llama-server, prompt cache off)", ""]
    if not rows:
        return lines + ["not run", ""]
    lines += [
        "| prompt tokens | prompt tok/s | prompt time, s | generated tokens | gen tok/s | wall, s |",
        "|---:|---:|---:|---:|---:|---:|",
    ]
    for r in rows:
        lines.append(
            f"| {r['prompt_n']} | {fmt(r['prompt_per_second'], 2)} | {fmt(r['prompt_ms'] / 1000)} "
            f"| {r['predicted_n']} | {fmt(r['predicted_per_second'], 2)} | {fmt(r['wall_s'])} |"
        )
    return lines + [""]


def hint_section(out: Path) -> list[str]:
    runs = read_jsonl(out / "hint" / "runs.jsonl")
    lines = ["## hint turns", ""]
    if not runs:
        return lines + ["not run", ""]
    lines += [
        "| run | exit | wall, s | input tokens | cached | processed by server | server prompt s "
        "| prompt tok/s | output tokens | failover notice |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|---|",
    ]
    notes = []
    for run in runs:
        name = run["name"]
        answer = _answer(out / "hint" / f"{name}.json")
        usage = answer.get("usage") or {}
        before = read_counters(out / "metrics" / f"{name}.before.txt")
        after = read_counters(out / "metrics" / f"{name}.after.txt")
        delta = {
            key: after[metric] - before[metric]
            for key, metric in COUNTERS.items()
            if metric in after and metric in before
        }
        rate = None
        if delta.get("prompt_s"):
            rate = delta["prompt_tokens"] / delta["prompt_s"]
        stderr = (out / "hint" / f"{name}.stderr").read_text(errors="replace")
        notice = "yes" if FAILOVER_NOTICE in stderr else "no"
        lines.append(
            f"| {name} | {run['exit']} | {fmt(run['wall_s'])} | {usage.get('input_tokens', '–')} "
            f"| {usage.get('cached_input_tokens', '–')} | {fmt(delta.get('prompt_tokens'), 0)} "
            f"| {fmt(delta.get('prompt_s'))} | {fmt(rate, 2)} | {usage.get('output_tokens', '–')} | {notice} |"
        )
        if answer.get("error"):
            notes.append(f"- `{name}` error: `{json.dumps(answer['error'], ensure_ascii=False)}`")
        if answer.get("answer"):
            text = answer["answer"].strip().replace("\n", " ")
            notes.append(f"- `{name}` answer: {text[:300]}{'…' if len(text) > 300 else ''}")
    lines += [
        "",
        "*input tokens* and *cached* come from the usage hint reports (summed over the turn's"
        " requests); *processed by server* is the delta of llama-server's prompt counter, i.e."
        " the tokens the model actually had to read. The gap between the two is the prefix cache.",
        "",
    ]
    return lines + notes + [""]


def _answer(path: Path) -> dict:
    try:
        return json.loads(path.read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        return {}


def main() -> None:
    out = Path(sys.argv[1])
    lines = [f"# pi-bench {out.name}", ""]
    for section in (env_section, samples_section, raw_section, hint_section):
        lines += section(out)
    print("\n".join(lines))


if __name__ == "__main__":
    main()
