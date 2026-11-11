"""Check the benchmark runner's accounting and configuration validation."""

import io
import json
from dataclasses import replace
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase
from unittest.mock import patch

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

    def test_live_run_requires_confirmed_budget_and_environment(self) -> None:
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
            smoke_budget_usd=1,
        )
        for budget, trusted in ((False, False), (False, True), (True, False)):
            with (
                self.subTest(budget=budget, trusted=trusted),
                patch("benchmark.task_manifest", return_value=[]),
                patch(
                    "benchmark.read_route_config",
                    return_value=route.model_copy(
                        update={
                            "provider_budget_confirmed": budget,
                            "trusted_environment_confirmed": trusted,
                        }
                    ),
                ),
                patch("benchmark.subprocess.run") as run,
            ):
                with self.assertRaisesRegex(ValueError, "Live runs require"):
                    launch(args=args)
                run.assert_not_called()

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
        self.assertIn("required for live Hint", output.getvalue())

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
            "smoke_budget_usd": 1.0,
        }
        self.assertEqual(
            RouteConfig.model_validate(data).api_key_env, "GATEWAY_API_KEY"
        )
        with self.assertRaises(ValidationError):
            RouteConfig.model_validate({**data, "api_key": "inline"})
