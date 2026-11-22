#!/usr/bin/env python3
"""Download pinned Terminal-Bench CPU-only 4.0.0 images sequentially."""

import argparse
import fcntl
import re
import subprocess
import sys
import time
from pathlib import Path

from benchmark_dataset import task_manifest
from image_index import INDEX, generate_index
from pull_progress import QueueMeter, tracked_pull

WAL = "wal-recovery-ordering"
PLATFORM = "linux/amd64"
HERE = Path(__file__).resolve().parent


def image_queue(*, dataset: Path, role: str, include_wal: bool) -> list[str]:
    """Select distinct pinned image references for the requested roles."""
    tasks = task_manifest(dataset=dataset, task_name=None)
    if not any(task["directory"] == WAL for task in tasks):
        raise ValueError(f"Expected {WAL} in {dataset}")
    images: dict[str, None] = {}
    for task in tasks:
        if not include_wal and task["directory"] == WAL:
            continue
        for name, ref in task["images"].items():
            if role in ("all", name):
                images[ref] = None
    return list(images)


def present(*, ref: str) -> bool:
    """Check that Docker resolves the original digest for linux/amd64."""
    result = subprocess.run(
        ["docker", "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", ref],
        capture_output=True,
        text=True,
        timeout=45,
    )
    return result.returncode == 0 and result.stdout.strip() == PLATFORM


def digest_ref(*, ref: str) -> str:
    """Remove the tag while retaining the repository and digest."""
    name, digest = ref.rsplit("@", 1)
    prefix, _, tail = name.rpartition("/")
    return (prefix + "/" if prefix else "") + tail.split(":")[0] + "@" + digest


def cached_refs(*, refs: list[str]) -> set[str]:
    """Check local digests in bulk and still verify the platform."""
    # Docker emits the matched images even if other requested digests are missing.
    result = subprocess.run(
        [
            "docker",
            "image",
            "inspect",
            "--format",
            '{{.Os}}/{{.Architecture}} {{join .RepoDigests " "}}',
            *refs,
        ],
        capture_output=True,
        text=True,
        timeout=90,
    )
    verified = set()
    for line in result.stdout.splitlines():
        platform, *digests = line.split()
        if platform == PLATFORM:
            verified.update(digests)
    return {ref for ref in refs if digest_ref(ref=ref) in verified}


def pull_once(*, ref: str, queue_ref: str, timeout: int, meter: QueueMeter) -> bool:
    """Fetch and verify a digest, clearing provisional progress on exit."""
    try:
        if tracked_pull(
            ref=ref, queue_ref=queue_ref, timeout=timeout, meter=meter
        ) and present(ref=ref):
            meter.commit(ref=queue_ref)
            return True
        return False
    finally:
        # Discard unconfirmed partial layers on failure before estimating the retry.
        meter.clear_status()
        meter.begin(ref=None)
        if not sys.stdout.isatty():
            meter.show(force=True)


def download(
    *, refs: list[str], retries: int, timeout: int, mirror: str | None, window: int
) -> int:
    """Pull sequentially and defer retries until the next pass."""
    cached = cached_refs(refs=refs)
    meter = QueueMeter(refs=refs, cached=cached, window=window)
    if not sys.stdout.isatty():
        meter.show(force=True)
    pending = [ref for ref in refs if ref not in cached]
    print(f"Already cached: {len(cached)}; to download: {len(pending)}", flush=True)
    for attempt in range(retries + 1):
        if not pending:
            break
        if attempt:
            time.sleep(min(5 * attempt, 30))
        failed = []
        for index, ref in enumerate(pending, 1):
            print(f"[pass {attempt + 1}, {index}/{len(pending)}] {ref}", flush=True)
            # The export uses Docker Hub references. Seed layers from the mirror,
            # then resolve the original repo digest that Harbor will actually use.
            if mirror and attempt == 0:
                mirror_ref = f"{mirror}/{digest_ref(ref=ref)}"
                if not present(ref=mirror_ref) and not pull_once(
                    ref=mirror_ref, queue_ref=ref, timeout=timeout, meter=meter
                ):
                    print(
                        "Mirror unavailable; using Docker Hub for the rest of this run.",
                        flush=True,
                    )
                    mirror = None
            if not pull_once(ref=ref, queue_ref=ref, timeout=timeout, meter=meter):
                failed.append(ref)
        pending = failed
    if sys.stdout.isatty():
        meter.show(force=True)
    print(
        f"cached={len(cached)} downloaded={len(refs) - len(cached) - len(pending)} failed={len(pending)}"
    )
    if pending:
        print("\n".join(pending), file=sys.stderr)
    return int(bool(pending))


def main() -> int:
    """Run the command-line downloader or index generator."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--dataset",
        type=Path,
        default=HERE.parents[1] / ".tmp/harbor/data/terminal-bench-cpu-only",
    )
    parser.add_argument(
        "--role",
        choices=("agent", "verifier", "all"),
        default="all",
        help="agent images for installs, all images for a full benchmark",
    )
    parser.add_argument(
        "--include-wal", action="store_true", help="also include the completed WAL task"
    )
    modes = parser.add_mutually_exclusive_group()
    modes.add_argument(
        "--generate-index",
        action="store_true",
        help="regenerate all agent and verifier sizes, including WAL",
    )
    modes.add_argument(
        "--list",
        action="store_true",
        help="print distinct digest references without Docker",
    )
    parser.add_argument(
        "--retries", type=int, default=2, help="additional pulls per failed image"
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=0,
        help="optional wall-clock limit per pull in seconds (default: unlimited)",
    )
    parser.add_argument(
        "--mirror",
        metavar="HOST",
        help="Docker Hub mirror to try first, e.g. mirror.gcr.io",
    )
    parser.add_argument(
        "--speed-window",
        type=int,
        default=60,
        metavar="SECONDS",
        help="rolling time window for throughput and queue ETA (default: 60 seconds)",
    )
    args = parser.parse_args()
    if args.speed_window < 1:
        parser.error("--speed-window must be positive")
    if args.mirror and not re.fullmatch(r"[a-zA-Z0-9.-]+(?::[0-9]+)?", args.mirror):
        parser.error("--mirror expects a registry hostname with optional port")
    if args.retries < 0 or args.timeout < 0:
        parser.error("retries and timeout must be nonnegative")
    dataset = args.dataset.resolve()
    if args.generate_index:
        generate_index(
            refs=image_queue(dataset=dataset, role="all", include_wal=True), path=INDEX
        )
        return 0
    refs = image_queue(dataset=dataset, role=args.role, include_wal=args.include_wal)
    if args.list:
        print(*refs, sep="\n")
        return 0
    # A second invocation against the same export must not start another pull queue.
    with (dataset.parent / ".terminal-bench-image-pull.lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("Another download queue is active for this export.", file=sys.stderr)
            return 2
        subprocess.run(
            ["docker", "info", "--format", "{{.ServerVersion}}"], check=True, timeout=45
        )
        return download(
            refs=refs,
            retries=args.retries,
            timeout=args.timeout,
            mirror=args.mirror,
            window=args.speed_window,
        )


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        print(
            "Stopped. Run again to skip previously downloaded images.", file=sys.stderr
        )
        sys.exit(130)
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as exc:
        sys.exit(str(exc))
