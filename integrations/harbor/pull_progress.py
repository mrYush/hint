"""Queue-wide progress for Docker pulls on Unix terminals."""

import errno
import fcntl
import math
import os
import pty
import re
import select
import shutil
import struct
import subprocess
import sys
import termios
import time
from collections import deque

from image_index import INDEX, SizeIndex

ANSI = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
LAYER = re.compile(r"^([0-9a-f]{12,64}):\s*(.*)$")
DOWNLOADED = re.compile(r"^Downloading\s+(?:\[[^\]]*\]\s*)?([\d.]+)\s*([kKMGT]?B)")
UNITS = {"B": 1, "KB": 1000, "MB": 10**6, "GB": 10**9, "TB": 10**12}


class WindowRate:
    """Keep a time-based sliding window of cumulative transferred bytes."""

    def __init__(self, *, seconds: int, now: float) -> None:
        self.seconds = seconds
        self.samples: deque[tuple[float, int]] = deque([(now, 0)])

    def sample(self, *, now: float, total: int) -> float:
        """Calculate bytes per second over the rolling interval."""
        if self.samples[-1][0] == now:
            self.samples[-1] = (now, total)
        else:
            self.samples.append((now, total))
        cutoff = now - self.seconds
        while len(self.samples) > 1 and self.samples[1][0] <= cutoff:
            self.samples.popleft()
        start, initial = self.samples[0]
        before = float(initial)
        if start < cutoff:
            end, after = self.samples[1]
            before += (after - before) * ((cutoff - start) / (end - start))
            start = cutoff
        return (total - before) / (now - start) if now > start else 0.0


class QueueMeter:
    """Track unique compressed layers across sequential Docker pulls."""

    def __init__(self, *, refs: list[str], cached: set[str], window: int) -> None:
        index = SizeIndex.model_validate_json(INDEX.read_bytes())
        self.sizes = index.sizes
        self.images = {ref: index.images.get(ref.split("@")[-1]) for ref in refs}
        self.unknown = {
            ref for ref, meta in self.images.items() if meta is None
        } - cached
        self.layers = {key for meta in self.images.values() if meta for key in meta[1]}
        self.total_size = sum(self.sizes[k] for k in self.layers)
        ids = subprocess.run(
            ["docker", "image", "ls", "--all", "--no-trunc", "-q"],
            capture_output=True,
            text=True,
            timeout=45,
        )
        local = set(ids.stdout.split()) if ids.returncode == 0 else set()
        self.done: set[str] = set()
        for ref, meta in index.images.items():
            if meta[0] in local:
                self.done.update(meta[1])
        for ref in cached:
            self.commit(ref=ref)
        self.total_wire = 0
        self.rate = WindowRate(seconds=window, now=time.monotonic())
        self.last_sample = self.last_print = -math.inf
        self.live_line = False
        self.speed = 0.0
        self.begin(ref=None)

    def begin(self, *, ref: str | None) -> None:
        """Start a pull with fresh provisional counters."""
        meta = self.images.get(ref) if ref else None
        self.keys = {k[:12]: k for k in meta[1]} if meta else {}
        self.received: dict[str, int] = {}
        self.provisional: dict[str, int] = {}
        self.last_print = -math.inf

    def commit(self, *, ref: str) -> None:
        """Record the layers of a verified local image."""
        meta = self.images.get(ref)
        if meta:
            self.done.update(meta[1])
        self.unknown.discard(ref)

    def clear_status(self) -> None:
        """Clear the live terminal line before printing diagnostics."""
        if self.live_line:
            print("\r\x1b[2K", end="", flush=True)
            self.live_line = False

    def line(self, *, line: str) -> None:
        """Consume a single Docker layer update."""
        match = LAYER.match(line)
        if not match:
            if line:
                self.clear_status()
                print(line, flush=True)
            return
        short, status = match.groups()
        key = self.keys.get(short[:12], short)
        size = self.sizes.get(key)
        match = DOWNLOADED.match(status)
        if match:
            # The daemon can fetch a layer again despite a matching local image ID.
            self.done.discard(key)
            amount = int(float(match[1]) * UNITS[match[2].upper()])
            if size is not None:
                amount = min(amount, size)
            old = self.received.get(key, 0)
            self.total_wire += amount - old if amount >= old else amount
            self.received[key] = amount
            self.provisional[key] = amount
        elif (
            status.startswith(("Already exists", "Download complete", "Pull complete"))
            and size is not None
        ):
            if status.startswith("Download complete") and key in self.received:
                self.total_wire += max(0, size - self.received[key])
                self.received[key] = size
            self.provisional[key] = size
        elif "error" in status.lower() or "retry" in status.lower():
            self.clear_status()
            print(line, flush=True)

    def remaining(self) -> int:
        """Return the remaining known compressed bytes."""
        return sum(
            self.sizes[k] - self.provisional.get(k, 0) for k in self.layers - self.done
        )

    def show(self, *, force: bool = False) -> None:
        """Refresh the single queue status line."""
        now = time.monotonic()
        if now - self.last_sample >= 1 or force:
            self.speed = self.rate.sample(now=now, total=self.total_wire)
            self.last_sample = now
        if not force and now - self.last_print < 5:
            return
        self.last_print = now
        left = self.remaining()
        eta = "waiting for data"
        if self.unknown:
            volume = f"≥ {left / 1e9:.2f} GB (some sizes unknown)"
        else:
            volume = f"{left / 1e9:.2f} GB"
            if left == 0:
                eta = "layers downloaded"
            elif self.speed > 0:
                minutes = math.ceil(left / self.speed / 60)
                eta = f"~{minutes // 60}h {minutes % 60:02}m"
        fraction = 1 - left / self.total_size if self.total_size else 1.0
        # Round down so that the last incomplete layer never displays 100%.
        percent = f"{math.floor(100 * fraction)}%" if not self.unknown else "?"
        speed = f"{self.speed / 1e6:.2f} MB/s"
        live = sys.stdout.isatty()
        width = max(1, shutil.get_terminal_size().columns - 1) if live else 110
        details = f"{speed} ({self.rate.seconds}s) | {volume} left | ETA {eta}"
        if len(details) + len(percent) + 16 > width:
            short_eta = "…" if eta == "waiting for data" else eta
            details = f"{speed} | {volume} | {short_eta}"
        bar_width = max(3, min(28, width - len(details) - len(percent) - 5))
        filled = int(bar_width * fraction)
        bar = (
            "#" * filled + "-" * (bar_width - filled)
            if not self.unknown
            else "?" * bar_width
        )
        status = f"[{bar}] {percent}  {details}"
        if live:
            self.clear_status()
            print(status[:width], end="\n" if force else "", flush=True)
            self.live_line = not force
        else:
            print(status, flush=True)


def tracked_pull(*, ref: str, queue_ref: str, timeout: int, meter: QueueMeter) -> bool:
    """Use a PTY so the Docker CLI emits layer counters even when logging to a file."""
    meter.begin(ref=queue_ref)
    master, slave = pty.openpty()
    try:
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 32, 180, 0, 0))
        proc = subprocess.Popen(
            ["docker", "pull", "--platform", "linux/amd64", ref],
            stdout=slave,
            stderr=slave,
        )
    except BaseException:
        os.close(master)
        raise
    finally:
        os.close(slave)
    deadline = time.monotonic() + timeout if timeout else math.inf
    buffer = b""
    try:
        while True:
            if time.monotonic() >= deadline:
                meter.clear_status()
                print(f"Timeout ({timeout}s) for {ref}", file=sys.stderr)
                return False
            meter.show()
            if not select.select([master], [], [], 1)[0]:
                continue
            try:
                chunk = os.read(master, 65536)
            except OSError as exc:
                if exc.errno != errno.EIO:
                    raise
                chunk = b""
            lines = re.split(rb"[\r\n]", buffer + chunk)
            buffer = lines.pop()
            if not chunk:
                lines.append(buffer)
            for raw in lines:
                meter.line(line=ANSI.sub("", raw.decode(errors="replace")).strip())
            if not chunk:
                break
        return proc.wait() == 0
    finally:
        os.close(master)
        if proc.poll() is None:
            proc.kill()
            proc.wait()
