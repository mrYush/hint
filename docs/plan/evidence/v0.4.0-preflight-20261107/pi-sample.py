"""Read numeric Linux process and resource counters without arguments or environments."""

import datetime
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time


def read(path):
    try:
        return Path(path).read_text().strip()
    except OSError:
        return None


def counters(path, keys):
    data = read(path)
    values = {}
    for line in (data or "").splitlines():
        key, *fields = line.replace(":", " ", 1).split()
        if key in keys and fields:
            values[key] = int(fields[0])
    return {key: values.get(key) for key in keys}


def process_table():
    rows = {}
    for directory in Path("/proc").glob("[0-9]*"):
        stat = read(directory / "stat")
        if stat is None:
            continue
        fields = stat.rsplit(")", 1)[1].split()
        rows[int(directory.name)] = fields
    return rows


def family(rows, root, start):
    if root not in rows or rows[root][19] != start:
        return None
    members = {root}
    while True:
        expanded = members | {pid for pid, fields in rows.items() if int(fields[1]) in members}
        if expanded == members:
            break
        members = expanded
    records = []
    for pid in sorted(members):
        fields = rows[pid]
        path = Path("/proc") / str(pid)
        pss = counters(path / "smaps_rollup", ["Pss"])["Pss"]
        io = counters(path / "io", ["read_bytes", "write_bytes"])
        records.append(dict(pid=pid, start_ticks=int(fields[19]),
                            rss_bytes=int(fields[21]) * os.sysconf("SC_PAGE_SIZE"),
                            pss_bytes=None if pss is None else pss * 1024,
                            cpu_seconds=(int(fields[11]) + int(fields[12])) / os.sysconf("SC_CLK_TCK"),
                            major_faults=int(fields[9]), **io))
    return {
        "processes": records,
        "family_rss_bytes": sum(row["rss_bytes"] for row in records),
        "family_pss_bytes": (sum(row["pss_bytes"] for row in records)
                             if all(row["pss_bytes"] is not None for row in records) else None),
    }


def throttle():
    if shutil.which("vcgencmd") is None:
        return None
    try:
        return subprocess.check_output(["vcgencmd", "get_throttled"], text=True,
                                       stderr=subprocess.DEVNULL, timeout=1).strip()
    except (OSError, subprocess.SubprocessError):
        return None


def main():
    run, hint_pid, model_pid, index_path = sys.argv[1:]
    roots = {"hint": int(hint_pid), "model": int(model_pid)}
    rows = process_table()
    starts = {role: rows[pid][19] for role, pid in roots.items()}
    index = Path(index_path)
    deadline = time.monotonic()
    while True:
        rows = process_table()
        try:
            index_bytes = sum(path.stat().st_size for path in index.glob("code-v2.sqlite*"))
            disk_free = shutil.disk_usage(index if index.exists() else index.parent).free
        except OSError:
            index_bytes = disk_free = None
        print(json.dumps(dict(
            run_id=run, utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
            monotonic_ns=time.monotonic_ns(),
            families={role: family(rows, pid, starts[role]) for role, pid in roots.items()},
            meminfo_kib=counters("/proc/meminfo", ["MemAvailable", "SwapTotal", "SwapFree"]),
            vmstat=counters("/proc/vmstat", ["pgmajfault", "pswpin", "pswpout"]),
            psi={name: read(f"/proc/pressure/{name}") for name in ("cpu", "memory", "io")},
            thermal_millic={str(path.parent): read(path)
                           for path in Path("/sys/class/thermal").glob("thermal_zone*/temp")},
            throttling=throttle(), disk_free_bytes=disk_free, index_bytes=index_bytes,
        )), flush=True)
        deadline += 1
        time.sleep(max(0, deadline - time.monotonic()))


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        pass
