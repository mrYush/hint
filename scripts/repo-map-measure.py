#!/usr/bin/env python3
"""Run the opt-in Mac/Linux corpus harness and sample its process-family RSS."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import time


def measure(binary, corpus, output, run, mode):
    environment = dict(os.environ)
    environment["HINT_INDEX_CORPUS"] = str(corpus)
    stem = output / f"{run}-{mode}"
    environment["HINT_INDEX_CORPUS_REPORT"] = str(stem.with_suffix(".json"))
    environment.pop("HINT_INDEX_CORPUS_BASELINE", None)
    if mode == "baseline":
        environment["HINT_INDEX_CORPUS_BASELINE"] = "1"
    peak = samples = 0
    began = time.monotonic()
    with stem.with_suffix(".log").open("w") as log:
        process = subprocess.Popen(
            [str(binary), "-test.run=^TestCorpusEvaluation$", "-test.v"],
            env=environment, stdout=log, stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        try:
            while process.poll() is None:
                if time.monotonic() - began > 180:
                    raise TimeoutError("corpus evaluation exceeded 180 seconds")
                rows = subprocess.check_output(
                    ["ps", "-axo", "pid=,ppid=,rss="], text=True, timeout=5,
                )
                table = {}
                for row in rows.splitlines():
                    parts = row.split()
                    if len(parts) == 3:
                        pid, parent, rss = map(int, parts)
                        table[pid] = (parent, rss)
                family = {process.pid}
                while True:
                    descendants = {pid for pid, (parent, _) in table.items() if parent in family}
                    if descendants <= family:
                        break
                    family |= descendants
                peak = max(peak, sum(table.get(pid, (0, 0))[1] for pid in family) * 1024)
                samples += 1
                time.sleep(0.1)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
    result = {
        "run": run, "mode": mode, "returncode": process.returncode,
        "peak_process_family_rss_bytes": peak, "rss_samples": samples,
        "sampling_interval_seconds": 0.1,
        "elapsed_seconds": time.monotonic() - began,
        "test_binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
    }
    Path(str(stem) + "-resources.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result), flush=True)
    if process.returncode:
        raise RuntimeError("evaluation failed; inspect " + str(stem.with_suffix(".log")))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--corpus", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=3, choices=range(1, 11))
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    for run in range(1, args.runs + 1):
        for mode in ("baseline", "index"):
            measure(args.binary.resolve(), args.corpus.resolve(), args.output.resolve(), run, mode)


if __name__ == "__main__":
    main()
