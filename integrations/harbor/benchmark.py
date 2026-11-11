"""Run or inspect the pinned Terminal-Bench CPU-only dataset with local Harbor."""

import argparse
import json
import os
import re
import subprocess
import sys
import tomllib
from collections.abc import Mapping
from dataclasses import dataclass
from importlib.metadata import version
from pathlib import Path

import yaml
from pydantic import BaseModel, ConfigDict, Field, ValidationError
from typing_extensions import TypedDict

from hint_harbor.agent import (
    HINT_VERSION,
    REVISION,
    HintAgent,
    Reasoning,
    Route,
)

DATASET = "terminal-bench/terminal-bench-cpu-only@4.0.0"
AGENT = "hint_harbor.agent:HintAgent"


class TaskManifestEntry(TypedDict):
    """Public task identifiers, image digests and resource limits."""

    directory: str
    name: str
    images: dict[str, str]
    agent_resources: dict[str, int | float]
    verifier_resources: dict[str, int | float]


class RouteConfig(BaseModel):
    """Operator-supplied route for a model run."""

    model_config = ConfigDict(extra="forbid")
    model: str
    base_url: str
    context_window: int = Field(gt=0)
    route: Route
    reasoning_expectation: Reasoning
    api_key_env: str = Field(pattern=r"^[A-Za-z_][A-Za-z0-9_]*$")
    trusted_environment_confirmed: bool = False


def read_route_config(*, path: Path) -> RouteConfig:
    """Load an operator route without including YAML contents in errors.

    :param path: Operator-supplied YAML file.
    :returns: Validated route configuration.
    :raises ValueError: If the file contains invalid YAML or route settings.
    :raises OSError: If the file cannot be read.
    """
    try:
        return RouteConfig.model_validate(
            yaml.safe_load(path.read_text(encoding="utf-8"))
        )
    except (ValidationError, yaml.YAMLError):
        raise ValueError(
            "Invalid route YAML. Check the fields in route.example.yaml and provide "
            "only the credential environment variable name."
        ) from None


class TrialResult(BaseModel):
    """Auditable subset of a Harbor result."""

    trial_name: str
    task_name: str
    task_id: dict[str, object]
    task_checksum: str
    exception_info: dict[str, str | None] | None = None
    verifier_result: dict[str, dict[str, float] | None] | None = None


class ManifestScope(BaseModel):
    """Fields needed to inspect a saved manifest."""

    tasks: list[dict[str, object]]
    attempts: int


class ResolvedTask(BaseModel):
    """Resolved task identity from Harbor's job lock."""

    name: str
    digest: str
    path: str | None = None


class HintManifest(BaseModel):
    """Sanitized route and pinned binary metadata."""

    version: str = HINT_VERSION
    commit: str = REVISION
    binary_sha256: dict[str, str]
    model: str
    base_url: str
    route: str
    reasoning_expectation: str
    reasoning_enforced: bool = False
    context_window: int
    api_key_env: str
    trusted_environment_confirmed: bool = False
    fallback: None = None
    output_token_override: None = None


class RunManifest(BaseModel):
    """Reproduction inputs and resolved identities for a Harbor job."""

    tasks: list[TaskManifestEntry]
    attempts: int
    dataset_requested: str = DATASET
    dataset_path: str
    adapter: str
    harbor: str
    python: str
    platform_request: str
    concurrency: int
    max_retries: int = 0
    dry_run: bool
    agent: str
    hint: HintManifest | None = None
    resolved_tasks: list[ResolvedTask] = Field(default_factory=list)
    harbor_exit_code: int | None = None
    observed_agent_platforms: list[str] = Field(default_factory=list)


class LockedTrial(BaseModel):
    """Subset of a job lock entry used for provenance."""

    task: ResolvedTask


class JobLock(BaseModel):
    """Subset of Harbor's job lock used for provenance."""

    trials: list[LockedTrial]


class TrialSummary(TypedDict):
    """One recorded verifier result and its available agent evidence."""

    trial: str
    task: str
    task_id: dict[str, object]
    task_checksum: str
    exception: str | None
    rewards: dict[str, float] | None
    agent: dict[str, object] | None
    execution: dict[str, object] | None
    artifact_errors: list[str]
    result: str


class JobSummary(TypedDict):
    """Attempt accounting across all expected tasks."""

    expected_attempts: int
    recorded_attempts: int
    missing_attempts: int
    trials: list[TrialSummary]
    unreadable_results: list[str]


@dataclass(frozen=True, slots=True, kw_only=True)
class RunArgs:
    """Typed arguments for a local Harbor job."""

    dataset: Path
    job: Path
    task: str | None
    all: bool
    oracle: bool
    dry_run: bool
    attempts: int
    concurrent: int
    platform: str
    binary_dir: Path | None
    model: str | None
    base_url: str | None
    context_window: int | None
    route: str | None
    reasoning_expectation: str | None
    route_config: Path | None


def write_json(*, path: Path, value: Mapping[str, object]) -> None:
    """Write a JSON artifact.

    :param path: Destination file.
    :param value: JSON-serializable object.
    :raises OSError: If the file cannot be written.
    """
    path.write_text(json.dumps(dict(value), indent=2) + "\n", encoding="utf-8")


def read_json(*, path: Path) -> dict[str, object]:
    """Read a JSON object from *path*.

    :param path: Source file.
    :returns: Parsed JSON object.
    :raises ValueError: If the artifact is invalid JSON or is not an object.
    :raises OSError: If the file cannot be read.
    """
    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError(f"Expected a JSON object in {path}")
    return value


def summarize(*, job: Path, task_count: int, attempts: int) -> JobSummary:
    """Report verifier outcomes, exceptions and missing attempts.

    :param job: Local Harbor job directory.
    :param task_count: Number of scheduled tasks.
    :param attempts: Independent attempts requested per task.
    :returns: Accounting and per-trial records.
    """
    trials: list[TrialSummary] = []
    unreadable_results: list[str] = []
    for path in sorted(job.glob("*/result.json")):
        artifact_errors: list[str] = []
        relative = str(path.relative_to(job))
        try:
            result = TrialResult.model_validate_json(path.read_text(encoding="utf-8"))
            trial = TrialSummary(
                trial=result.trial_name,
                task=result.task_name,
                task_id=result.task_id,
                task_checksum=result.task_checksum,
                exception=(result.exception_info or {}).get("exception_type"),
                rewards=(result.verifier_result or {}).get("rewards"),
                agent=None,
                execution=None,
                artifact_errors=artifact_errors,
                result=relative,
            )
        except (OSError, ValueError, KeyError):
            unreadable_results.append(relative)
            continue
        for field, name in (
            ("agent", "effective.json"),
            ("execution", "execution.json"),
        ):
            artifact = path.parent / "agent" / name
            if artifact.exists():
                try:
                    if field == "agent":
                        trial["agent"] = read_json(path=artifact)
                    else:
                        trial["execution"] = read_json(path=artifact)
                except (OSError, ValueError):
                    artifact_errors.append(name)
        trials.append(trial)
    expected = task_count * attempts
    return {
        "expected_attempts": expected,
        "recorded_attempts": len(trials),
        "missing_attempts": max(0, expected - len(trials)),
        "trials": trials,
        "unreadable_results": unreadable_results,
    }


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


def launch(*, args: RunArgs) -> int:
    """Validate and launch a pinned local Harbor job.

    :param args: Operator-selected scope and settings.
    :returns: Harbor status, or one for incomplete attempt accounting.
    """
    if args.oracle and any(
        value is not None
        for value in (
            args.route_config,
            args.model,
            args.base_url,
            args.context_window,
            args.route,
            args.reasoning_expectation,
            args.binary_dir,
        )
    ):
        raise ValueError("An Oracle job does not accept model route or binary options")
    tasks = task_manifest(dataset=args.dataset.resolve(), task_name=args.task)
    job = args.job.resolve()
    if job.exists():
        raise ValueError("Choose a fresh output directory")
    if args.attempts < 1 or args.concurrent < 1:
        raise ValueError("Attempts and concurrency must be positive")
    if args.oracle and (args.all or args.attempts != 1):
        raise ValueError("The oracle recipe runs one attempt on one task")

    command = [
        str(Path(sys.executable).with_name("harbor")),
        "run",
        "-p",
        str(args.dataset.resolve()),
        "-n",
        str(args.concurrent),
        "-k",
        str(args.attempts),
        "--max-retries",
        "0",
        "-o",
        str(job.parent),
        "--job-name",
        job.name,
    ]
    if args.task:
        command.extend(("-i", args.task))
    manifest = RunManifest(
        dataset_path=str(args.dataset.resolve()),
        tasks=tasks,
        adapter=version("hint-harbor"),
        harbor=version("harbor"),
        python=sys.version.split()[0],
        platform_request=args.platform,
        attempts=args.attempts,
        concurrency=args.concurrent,
        dry_run=args.dry_run,
        agent="oracle" if args.oracle else "hint",
    )
    env = {**os.environ, "DOCKER_DEFAULT_PLATFORM": args.platform}
    if args.oracle:
        if args.route_config is not None:
            raise ValueError("An Oracle job does not accept a model route")
        command.extend(("-a", "oracle"))
    else:
        if args.binary_dir is None:
            raise ValueError("Hint requires a binary directory")
        route_config = None
        if args.route_config is not None:
            if any(
                value is not None
                for value in (
                    args.model,
                    args.base_url,
                    args.context_window,
                    args.route,
                    args.reasoning_expectation,
                )
            ):
                raise ValueError("Use either --route-config or individual route flags")
            route_config = read_route_config(path=args.route_config)
        if not args.dry_run and (
            route_config is None or not route_config.trusted_environment_confirmed
        ):
            raise ValueError(
                "Live runs require route YAML confirming a trusted environment"
            )
        model = route_config.model if route_config else args.model
        endpoint = route_config.base_url if route_config else args.base_url
        context_window = (
            route_config.context_window if route_config else args.context_window
        )
        route = route_config.route if route_config else args.route
        reasoning = (
            route_config.reasoning_expectation
            if route_config
            else args.reasoning_expectation
        )
        key_name = route_config.api_key_env if route_config else "HINT_API_KEY"
        if not (model and endpoint and context_window and route and reasoning):
            raise ValueError(
                "Hint requires model, base URL, context, route and reasoning expectation"
            )
        if args.dry_run:
            # Harbor displays the last four characters of resolved credentials.
            env[key_name] = "hint-dry-run-placeholder"
        elif not env.get(key_name):
            raise ValueError(f"Set the credential environment variable {key_name}")
        directory = args.binary_dir.resolve()
        agent = HintAgent(
            logs_dir=job,
            model_name=model,
            skip_model_parsing=True,
            model_api_key_name=key_name,
            base_url=endpoint,
            binary_dir=directory,
            context_window=context_window,
            route=route,
            reasoning_expectation=reasoning,
            extra_env={key_name: env[key_name]} if key_name in env else None,
        )
        manifest.hint = HintManifest(
            binary_sha256={
                binary.architecture: binary.sha256
                for binary in (
                    agent.binaries.select(machine=machine)
                    for machine in ("x86_64", "aarch64")
                )
            },
            model=agent.model,
            base_url=agent.endpoint,
            route=route,
            reasoning_expectation=reasoning,
            context_window=context_window,
            api_key_env=key_name,
            trusted_environment_confirmed=route_config.trusted_environment_confirmed
            if route_config
            else False,
        )
        command.extend(
            (
                "-a",
                AGENT,
                "-m",
                model,
                "--sm",
                "--base-url",
                endpoint,
                "--api-key-name",
                key_name,
                "--ak",
                f"binary_dir={directory}",
                "--ak",
                f"context_window={context_window}",
                "--ak",
                f"route={route}",
                "--ak",
                f"reasoning_expectation={reasoning}",
            )
        )
    if args.dry_run:
        command.append("--dry-run")
    job.mkdir(parents=True)
    write_json(path=job / "manifest.json", value=manifest.model_dump(mode="json"))
    print(f"Manifest: {job / 'manifest.json'}", flush=True)
    result = subprocess.run(command, env=env)
    if not args.dry_run:
        summary = summarize(job=job, task_count=len(tasks), attempts=args.attempts)
        write_json(path=job / "summary.json", value=summary)
        if (job / "lock.json").exists():
            lock = JobLock.model_validate_json(
                (job / "lock.json").read_text(encoding="utf-8")
            )
            identities = {trial.task.digest: trial.task for trial in lock.trials}
            manifest.resolved_tasks = list(identities.values())
        manifest.harbor_exit_code = result.returncode
        manifest.observed_agent_platforms = sorted(
            {
                platform
                for trial in summary["trials"]
                if (agent_info := trial["agent"]) is not None
                and isinstance(platform := agent_info.get("platform"), str)
            }
        )
        write_json(path=job / "manifest.json", value=manifest.model_dump(mode="json"))
        print(f"Results: {job / 'summary.json'}", flush=True)
        if summary["missing_attempts"] and result.returncode == 0:
            return 1
    return result.returncode


def main() -> int:
    """Parse CLI arguments and run or inspect a job."""
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="operation", required=True)
    run = sub.add_parser("run")
    run.add_argument("dataset", type=Path)
    run.add_argument("job", type=Path)
    scope = run.add_mutually_exclusive_group(required=True)
    scope.add_argument("--task")
    scope.add_argument("--all", action="store_true")
    run.add_argument("--oracle", action="store_true")
    run.add_argument("--dry-run", action="store_true")
    run.add_argument("--attempts", type=int, default=1)
    run.add_argument("--concurrent", type=int, default=1)
    run.add_argument(
        "--platform", choices=("linux/amd64", "linux/arm64"), required=True
    )
    run.add_argument("--binary-dir", type=Path)
    run.add_argument(
        "--route-config",
        type=Path,
        help="Route YAML confirming a trusted environment; required for live Hint runs",
    )
    route_help = "Dry-run only; live Hint runs require --route-config YAML"
    run.add_argument("--model", help=route_help)
    run.add_argument("--base-url", help=route_help)
    run.add_argument("--context-window", type=int, help=route_help)
    run.add_argument(
        "--route", choices=("glm", "deepseek", "compatible"), help=route_help
    )
    run.add_argument(
        "--reasoning-expectation",
        choices=("endpoint-default", "non-thinking"),
        help=route_help,
    )
    inspect = sub.add_parser("inspect")
    inspect.add_argument("job", type=Path)
    args = parser.parse_args()
    if args.operation == "inspect":
        manifest = ManifestScope.model_validate_json(
            (args.job / "manifest.json").read_text()
        )
        print(
            json.dumps(
                summarize(
                    job=args.job,
                    task_count=len(manifest.tasks),
                    attempts=manifest.attempts,
                ),
                indent=2,
            )
        )
        return 0
    return launch(
        args=RunArgs(
            **{key: value for key, value in vars(args).items() if key != "operation"}
        )
    )


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, KeyError, yaml.YAMLError) as error:
        sys.exit(str(error))
