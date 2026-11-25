"""Read the pinned Terminal-Bench CPU-only export for Harbor tools."""

import re
import tomllib
from pathlib import Path

from typing_extensions import TypedDict

DATASET = "terminal-bench/terminal-bench-cpu-only@4.0.0"


class TaskManifestEntry(TypedDict):
    """Public task identifiers, image digests and resource limits."""

    directory: str
    name: str
    images: dict[str, str]
    agent_resources: dict[str, int | float]
    verifier_resources: dict[str, int | float]


def task_manifest(*, dataset: Path, task_name: str | None) -> list[TaskManifestEntry]:
    """Read task names, pinned image references and numeric resource limits.

    :param dataset: Local exported dataset.
    :param task_name: Directory name to select, or all tasks when omitted.
    :returns: Sanitized task entries.
    """
    tasks: list[TaskManifestEntry] = []
    paths = sorted(dataset.glob("*/task.toml"))
    if len(paths) != 63:
        raise ValueError(
            f"Expected 63 exported tasks for {DATASET}; found {len(paths)}"
        )
    for path in paths:
        if task_name and path.parent.name != task_name:
            continue
        task = tomllib.loads(path.read_text(encoding="utf-8"))
        agent_env = task["environment"]
        verifier_env = task.get("verifier", {}).get("environment", agent_env)
        images = {
            "agent": agent_env["docker_image"],
            "verifier": verifier_env["docker_image"],
        }
        if any(
            re.fullmatch(r"[^@\s]+@sha256:[0-9a-fA-F]{64}", ref) is None
            for ref in images.values()
        ):
            raise ValueError(
                f"Invalid pinned SHA-256 image digest for {path.parent.name}"
            )
        resource_keys = ("cpus", "memory_mb", "storage_mb", "gpus", "build_timeout_sec")
        tasks.append(
            {
                "directory": path.parent.name,
                "name": task["task"]["name"],
                "images": images,
                "agent_resources": {
                    key: agent_env[key] for key in resource_keys if key in agent_env
                },
                "verifier_resources": {
                    key: verifier_env[key]
                    for key in resource_keys
                    if key in verifier_env
                },
            }
        )
    if not tasks:
        raise ValueError(f"Task not found: {task_name}")
    return tasks
