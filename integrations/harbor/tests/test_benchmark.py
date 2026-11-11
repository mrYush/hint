"""Check the benchmark runner's accounting and configuration validation."""

import hashlib
import io
import json
from dataclasses import replace
from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
from unittest import TestCase
from unittest.mock import patch
from weakref import ReferenceType, ref

from harbor.models.trial.error_policy import FailureCause, policy_for
from pydantic import ValidationError

from benchmark import (
    ManifestScope,
    RouteConfig,
    RunArgs,
    launch,
    main,
    read_route_config,
    summarize,
    task_manifest,
)
from hint_harbor.agent import ERRORS, HintAgent


class BenchmarkTests(TestCase):
    """Test offline review regressions without launching Docker."""

    def test_oracle_rejects_route_options_before_io(self) -> None:
        """Reject every conflicting option before accessing tasks or running Harbor."""
        args = RunArgs(
            dataset=Path("/missing"),
            job=Path("/missing-job"),
            task="one",
            all=False,
            oracle=True,
            dry_run=True,
            attempts=1,
            concurrent=1,
            platform="linux/amd64",
            binary_dir=None,
            model=None,
            base_url=None,
            context_window=None,
            route=None,
            reasoning_expectation=None,
            route_config=None,
        )
        for field, value in (
            ("model", "x"),
            ("base_url", "https://x"),
            ("context_window", 100),
            ("route", "glm"),
            ("reasoning_expectation", "endpoint-default"),
            ("route_config", Path("route.yaml")),
            ("binary_dir", Path("bin")),
        ):
            with self.subTest(field=field), patch("benchmark.subprocess.run") as run:
                with self.assertRaisesRegex(ValueError, "Oracle job"):
                    launch(args=replace(args, **{field: value}))
                run.assert_not_called()

    def test_live_run_requires_confirmed_environment(self) -> None:
        """Reject unconfirmed live jobs before credential access and subprocess launch."""
        args = RunArgs(
            dataset=Path("/missing"),
            job=Path("/missing-job"),
            task="one",
            all=False,
            oracle=False,
            dry_run=False,
            attempts=1,
            concurrent=1,
            platform="linux/amd64",
            binary_dir=Path("bin"),
            model=None,
            base_url=None,
            context_window=None,
            route=None,
            reasoning_expectation=None,
            route_config=Path("route.yaml"),
        )
        route = RouteConfig(
            model="model",
            base_url="https://example.invalid",
            context_window=100,
            route="glm",
            reasoning_expectation="endpoint-default",
            api_key_env="KEY",
        )
        for path in (None, Path("route.yaml")):
            with (
                self.subTest(route_config=path),
                patch("benchmark.task_manifest", return_value=[]),
                patch(
                    "benchmark.read_route_config",
                    return_value=route,
                ),
                patch("benchmark.subprocess.run") as run,
            ):
                with self.assertRaisesRegex(ValueError, "Live runs require"):
                    launch(args=replace(args, route_config=path))
                run.assert_not_called()

    def test_route_launch_releases_validation_agent(self) -> None:
        """Validate routes without budgets and release the agent before invoking Harbor."""
        for dry_run in (False, True):
            with self.subTest(dry_run=dry_run), TemporaryDirectory() as temp:
                self.check_route_launch(directory=Path(temp), dry_run=dry_run)

    def check_route_launch(self, *, directory: Path, dry_run: bool) -> None:
        """Check real route validation with disposable binaries and a subprocess stub."""
        route = directory / "route.yaml"
        route.write_text("""
model: gateway/model
base_url: https://example.invalid/v1
context_window: 32768
route: glm
reasoning_expectation: endpoint-default
api_key_env: KEY
trusted_environment_confirmed: true
""")
        binary_dir = directory / "bin"
        binary_dir.mkdir()
        checksums = []
        for architecture in ("amd64", "arm64"):
            binary = binary_dir / f"hint-linux-{architecture}"
            payload = architecture.encode()
            binary.write_bytes(payload)
            checksums.append(f"{hashlib.sha256(payload).hexdigest()}  {binary.name}")
        (binary_dir / "SHA256SUMS").write_text("\n".join(checksums))
        args = RunArgs(
            dataset=directory / "dataset",
            job=directory / "job",
            task="one",
            all=False,
            oracle=False,
            dry_run=dry_run,
            attempts=1,
            concurrent=1,
            platform="linux/amd64",
            binary_dir=binary_dir,
            model=None,
            base_url=None,
            context_window=None,
            route=None,
            reasoning_expectation=None,
            route_config=route,
        )
        validation_agent: ReferenceType[HintAgent] | None = None

        def validate_agent(
            *, logs_dir: Path, extra_env: dict[str, str], **kwargs: object
        ) -> HintAgent:
            nonlocal validation_agent
            agent = HintAgent(
                logs_dir=logs_dir,
                extra_env=extra_env,
                version=None,
                prompt_template_path=None,
                config=None,
                **kwargs,
            )
            validation_agent = ref(agent)
            return agent

        def run_harbor(command: list[str], *, env: dict[str, str]) -> SimpleNamespace:
            self.assertIsNotNone(validation_agent)
            assert validation_agent is not None
            self.assertIsNone(validation_agent())
            expected_key = "hint-dry-run-placeholder" if dry_run else "secret-canary"
            self.assertEqual(env["KEY"], expected_key)
            self.assertNotIn(expected_key, " ".join(command))
            self.assertEqual("--dry-run" in command, dry_run)
            if not dry_run:
                trial = args.job / "one"
                trial.mkdir()
                (trial / "result.json").write_text(
                    json.dumps(
                        {
                            "trial_name": "one",
                            "task_name": "one",
                            "task_id": {"path": "one"},
                            "task_checksum": "hash",
                            "verifier_result": {"rewards": {"reward": 0.0}},
                        }
                    )
                )
            return SimpleNamespace(returncode=0)

        with (
            patch(
                "benchmark.task_manifest",
                return_value=[
                    {
                        "directory": "one",
                        "name": "one",
                        "images": {},
                        "agent_resources": {},
                        "verifier_resources": {},
                    }
                ],
            ),
            patch.dict(
                "benchmark.os.environ",
                {} if dry_run else {"KEY": "secret-canary"},
                clear=True,
            ),
            patch("benchmark.HintAgent", side_effect=validate_agent),
            patch("benchmark.subprocess.run", side_effect=run_harbor) as run,
        ):
            self.assertEqual(launch(args=args), 0)
            run.assert_called_once()
        manifest = json.loads((args.job / "manifest.json").read_text())
        self.assertEqual(manifest["hint"]["model"], "gateway/model")
        self.assertEqual(manifest["hint"]["base_url"], "https://example.invalid/v1")
        self.assertEqual(manifest["hint"]["context_window"], 32768)
        self.assertEqual(set(manifest["hint"]["binary_sha256"]), {"amd64", "arm64"})
        self.assertTrue(manifest["hint"]["trusted_environment_confirmed"])
        for field in (
            "smoke_budget_usd",
            "smoke_budget_enforced",
            "provider_budget_confirmed",
        ):
            self.assertNotIn(field, manifest["hint"])
        self.assertNotIn("secret-canary", json.dumps(manifest))
        self.assertNotIn("hint-dry-run-placeholder", json.dumps(manifest))
        if not dry_run:
            summary = json.loads((args.job / "summary.json").read_text())
            self.assertEqual(summary["expected_attempts"], 1)
            self.assertEqual(summary["recorded_attempts"], 1)
            self.assertEqual(summary["missing_attempts"], 0)

    def test_dataset_export_contract(self) -> None:
        """Reject partial exports before lookup and distinguish a missing named task."""
        with TemporaryDirectory() as temp:
            directory = Path(temp)
            for count in (0, 1, 62, 63, 64):
                for index in range(count):
                    task = directory / str(index)
                    task.mkdir(exist_ok=True)
                    (task / "task.toml").touch()
                with self.subTest(count=count):
                    if count == 63:
                        with self.assertRaisesRegex(
                            ValueError, "Task not found: absent"
                        ):
                            task_manifest(dataset=directory, task_name="absent")
                    else:
                        with self.assertRaisesRegex(ValueError, "Expected 63 exported"):
                            task_manifest(dataset=directory, task_name=None)

    def test_summary(self) -> None:
        """Retain verifier outcomes and exception rows despite missing or corrupt evidence."""
        with TemporaryDirectory() as temp:
            job = Path(temp)
            for name, error in (
                ("success", None),
                ("failed", {"exception_type": "ApiError"}),
            ):
                trial = job / name
                (trial / "agent").mkdir(parents=True)
                (trial / "result.json").write_text(
                    json.dumps(
                        {
                            "trial_name": name,
                            "task_name": "one",
                            "task_id": {"path": "one"},
                            "task_checksum": "hash",
                            "exception_info": error,
                            "verifier_result": {
                                "rewards": {"reward": 0.0 if error else 1.0}
                            },
                        }
                    )
                )
            (job / "failed/agent/effective.json").write_text("{")
            (job / "corrupt").mkdir()
            (job / "corrupt/result.json").write_text("{")
            result = summarize(job=job, task_count=2, attempts=2)
        self.assertEqual(result["recorded_attempts"], 2)
        self.assertEqual(result["missing_attempts"], 2)
        self.assertEqual(result["unreadable_results"], ["corrupt/result.json"])
        self.assertEqual(result["trials"][0]["exception"], "ApiError")
        self.assertEqual(result["trials"][0]["artifact_errors"], ["effective.json"])
        self.assertEqual(result["trials"][1]["rewards"], {"reward": 1.0})

    def test_manifest_omits_environment_secrets(self) -> None:
        """Keep image and resource metadata while excluding task environment secrets."""
        with TemporaryDirectory() as temp:
            dataset = Path(temp)
            for index in range(63):
                task = dataset / str(index)
                task.mkdir()
                (task / "task.toml").write_text("""
[task]
name = "fixture"
[environment]
docker_image = "test@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
cpus = 1
[environment.env]
SECRET = "canary"
[verifier.environment]
docker_image = "verifier@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
memory_mb = 512
[verifier.environment.env]
SECRET = "canary"
""")
            shared = dataset / "0/task.toml"
            shared.write_text(shared.read_text().split("[verifier.environment]")[0])
            result = task_manifest(dataset=dataset, task_name=None)
        self.assertEqual(len(result), 63)
        self.assertEqual(result[0]["agent_resources"], {"cpus": 1})
        self.assertEqual(result[0]["images"]["verifier"], result[0]["images"]["agent"])
        self.assertEqual(result[0]["verifier_resources"], {"cpus": 1})
        self.assertEqual(result[1]["verifier_resources"], {"memory_mb": 512})
        self.assertNotIn("canary", json.dumps(result))

    def test_digest_validation(self) -> None:
        """Validate both image references with exact SHA-256 lengths and characters."""
        with TemporaryDirectory() as temp:
            dataset = Path(temp)
            for index in range(63):
                (dataset / str(index)).mkdir()
                (dataset / str(index) / "task.toml").touch()
            path = dataset / "0/task.toml"
            valid = "registry:5000/repo:tag@sha256:" + "ab" * 32
            for image in (
                valid,
                "repo@sha256:" + "AB" * 32,
                "repo@sha256:abc",
                valid + "0",
                valid[:-1],
                valid[:-1] + "g",
                "@sha256:" + "a" * 64,
                valid + "\n",
                valid + "@other",
            ):
                for section in ("environment", "verifier.environment"):
                    with self.subTest(image=image, section=section):
                        agent_image = image if section == "environment" else valid
                        verifier_image = (
                            image if section == "verifier.environment" else valid
                        )
                        path.write_text(
                            f'[task]\nname="fixture"\n[environment]\ndocker_image={json.dumps(agent_image)}\n[verifier.environment]\ndocker_image={json.dumps(verifier_image)}\n'
                        )
                        if image in (valid, "repo@sha256:" + "AB" * 32):
                            self.assertEqual(
                                len(task_manifest(dataset=dataset, task_name="0")), 1
                            )
                        else:
                            with self.assertRaisesRegex(
                                ValueError, "Invalid pinned SHA-256"
                            ):
                                task_manifest(dataset=dataset, task_name="0")

    def test_summary_propagates_programming_errors(self) -> None:
        """Do not classify unexpected attribute failures as unreadable result files."""
        with TemporaryDirectory() as temp:
            job = Path(temp)
            (job / "trial").mkdir()
            (job / "trial/result.json").write_text("{}")
            with patch(
                "benchmark.TrialResult.model_validate_json",
                side_effect=AttributeError("regression"),
            ):
                with self.assertRaisesRegex(AttributeError, "regression"):
                    summarize(job=job, task_count=1, attempts=1)

    def test_route_help(self) -> None:
        """Explain the live YAML requirement through the normal CLI help entry point."""
        output = io.StringIO()
        with (
            patch("sys.argv", ["benchmark.py", "run", "--help"]),
            patch("sys.stdout", output),
        ):
            with self.assertRaises(SystemExit) as stopped:
                main()
        self.assertEqual(stopped.exception.code, 0)
        self.assertEqual(output.getvalue().count("Dry-run only"), 5)
        help_text = " ".join(output.getvalue().split())
        self.assertIn("required for live Hint", help_text)
        self.assertIn("confirming a trusted environment", help_text)

    def test_legacy_manifest_scope(self) -> None:
        """Inspect existing jobs whose manifests included whole resource tables."""
        manifest = ManifestScope.model_validate(
            {
                "tasks": [{"agent_resources": {"docker_image": "legacy"}}],
                "attempts": 1,
            }
        )
        self.assertEqual(len(manifest.tasks) * manifest.attempts, 1)

    def test_harbor_error_policy(self) -> None:
        """Map Hint stop reasons to Harbor's pinned grading policy."""
        for kind, cause in (
            ("turn_limit", FailureCause.AGENT_EXHAUSTED_BUDGET),
            ("canceled", FailureCause.CANCELLED),
            ("content_filtered", FailureCause.AGENT_REFUSED_TASK),
        ):
            with self.subTest(kind=kind):
                policy = policy_for(ERRORS[kind].__name__)
                self.assertEqual(policy.cause, cause)
                self.assertFalse(policy.retry)

    def test_route_config_does_not_echo_inline_credentials(self) -> None:
        """Avoid exposing inline secrets in validation or YAML parse errors."""
        with TemporaryDirectory() as temp:
            path = Path(temp) / "route.yaml"
            for contents in (
                "api_key: secret-canary",
                "api_key: secret-canary: invalid",
            ):
                with self.subTest(contents=contents):
                    path.write_text(contents)
                    with self.assertRaisesRegex(
                        ValueError, "Invalid route YAML"
                    ) as error:
                        read_route_config(path=path)
                    self.assertNotIn("secret-canary", str(error.exception))
                    self.assertTrue(error.exception.__suppress_context__)

    def test_schema_and_route_config(self) -> None:
        """Expose supported options and reject inline secrets in route configuration."""
        properties = HintAgent.options_schema()["properties"]
        self.assertIsInstance(properties, dict)
        if isinstance(properties, dict):
            self.assertIn("reasoning_expectation", properties)
            self.assertNotIn("version", properties)
        data = {
            "model": "gateway/model",
            "base_url": "https://example.invalid/v1",
            "context_window": 32768,
            "route": "glm",
            "reasoning_expectation": "endpoint-default",
            "api_key_env": "GATEWAY_API_KEY",
        }
        self.assertEqual(
            RouteConfig.model_validate(data).api_key_env, "GATEWAY_API_KEY"
        )
        with self.assertRaises(ValidationError):
            RouteConfig.model_validate({**data, "api_key": "inline"})
