"""Regression tests for the pinned adapter contracts."""

import hashlib
import json
import shlex
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import AsyncMock, Mock, patch

from harbor.models.agent.context import AgentContext
from harbor.models.trajectories import Trajectory
from harbor.trial.errors import AgentTimeoutError

from hint_harbor.agent import (
    REVISION,
    AgentError,
    BinaryIdentity,
    HintAgent,
    HintAnswer,
    HintTrial,
    SelectedBinary,
    redact,
    select_binary,
)
from hint_harbor.trace import convert, read_journal


class AdapterTests(unittest.TestCase):
    """Check route, binary, trace and redaction contracts."""

    def agent(self, **overrides: object) -> HintAgent:
        """Create an isolated fixture agent with typed overrides."""
        defaults = {
            "model_name": "fixture/qualified-model",
            "skip_model_parsing": True,
            "base_url": "https://gateway.invalid/v1",
            "binary_dir": Path("/tmp/unused"),
            "context_window": 16384,
            "route": "glm",
            "reasoning_expectation": "endpoint-default",
            "model_api_key_name": "HINT_API_KEY",
        }
        with patch.dict(
            "os.environ", {"HINT_API_KEY": "fixture-test-token"}, clear=True
        ):
            return HintAgent(logs_dir=Path("/tmp/unused"), **{**defaults, **overrides})

    def test_exact_route(self) -> None:
        """Check exact route."""
        agent = self.agent()
        self.assertEqual(agent.model, "fixture/qualified-model")
        self.assertEqual(agent.endpoint, "https://gateway.invalid/v1")
        command = HintTrial(
            agent=agent,
            environment=Mock(),
            state="/tmp/hint-trial",
            context=AgentContext(),
        ).command()
        self.assertIn("HINT_CONTEXT_WINDOW=16384", command)
        self.assertIn("HINT_MODEL=fixture/qualified-model", command)
        self.assertNotIn(agent._secret, command)
        self.assertIn("--output json --yolo", command)

    def test_custom_credential_removed_from_launch_environment(self) -> None:
        """Strip the custom Harbor overlay key before starting the Hint shell."""
        name = "GATEWAY_API_KEY"
        secret = "fixture-gateway-token"
        agent = self.agent(model_api_key_name=name, extra_env={name: secret})
        self.assertIn(name, agent.extra_env)
        command = HintTrial(
            agent=agent, environment=Mock(), state="/tmp/test", context=AgentContext()
        ).command()
        self.assertEqual(
            shlex.split(command)[:7], ["env", "-u", "BASH_ENV", "-u", "ENV", "-u", name]
        )
        self.assertNotIn(secret, command)

    def test_unsupported_options(self) -> None:
        """Check unsupported options."""
        cases = [
            {"reasoning_expectation": "thinking"},
            {"reasoning_effort": "high"},
            {"max_tokens": 100},
            {"version": "latest"},
            {"prompt_template_path": "template"},
            {"config": {}},
            {"reasoning_expectation": "non-thinking"},
            {"route": "deepseek"},
            {
                "route": "deepseek",
                "reasoning_expectation": "non-thinking",
                "base_url": "https://api.deepseek.com/v1",
            },
            {"base_url": "https://user:secret@gateway.invalid/v1"},
            {"base_url": "https://gateway.invalid/v1?key=secret"},
            {"model_api_format": "openai_responses"},
        ]
        for options in cases:
            with self.subTest(options=options), self.assertRaises(ValueError):
                self.agent(**options)
        self.agent(route="deepseek", reasoning_expectation="non-thinking")

    def test_architecture_and_checksum(self) -> None:
        """Check architecture and checksum."""
        with TemporaryDirectory() as temp:
            directory = Path(temp)
            checksums = []
            for arch in ("amd64", "arm64"):
                file = directory / f"hint-linux-{arch}"
                payload = arch.encode()
                file.write_bytes(payload)
                checksums.append(f"{hashlib.sha256(payload).hexdigest()}  {file.name}")
            (directory / "SHA256SUMS").write_text("\n".join(checksums))
            for machine, expected in (("x86_64", "amd64"), ("aarch64", "arm64")):
                binary = select_binary(directory=directory, machine=machine)
                self.assertEqual(
                    (binary.path.name, binary.architecture),
                    (f"hint-linux-{expected}", expected),
                )
            with self.assertRaisesRegex(ValueError, "Unsupported"):
                select_binary(directory=directory, machine="riscv64")
            (directory / "hint-linux-amd64").write_text("tampered")
            with self.assertRaisesRegex(ValueError, "Checksum mismatch"):
                select_binary(directory=directory, machine="x86_64")

    def test_partial_trace_and_unknown_usage(self) -> None:
        """Check partial trace and unknown usage."""
        records = [
            {"kind": "session", "id": "example"},
            {
                "kind": "message",
                "at": "2026-11-08T13:00:00Z",
                "message": {
                    "role": "user",
                    "content": [{"kind": "text", "text": "α\n\n"}],
                },
            },
        ]
        with TemporaryDirectory() as temp:
            path = Path(temp) / "session.jsonl"
            path.write_text("\n".join(map(json.dumps, records)) + '\n{"kind":')
            trace = convert(
                records=read_journal(path=path),
                model="fake",
                version="dev",
                context=AgentContext(),
            )
        validated = Trajectory.model_validate_json(trace.model_dump_json())
        self.assertEqual(validated.steps[0].message, "α\n\n")
        assert validated.final_metrics is not None
        self.assertIsNone(validated.final_metrics.total_prompt_tokens)
        self.assertIsNone(validated.final_metrics.total_cost_usd)

    def test_redaction_in_plain_and_json_logs(self) -> None:
        """Check redaction in plain and json logs."""
        secret = 'key"with\\escapes'
        for value in (secret, json.dumps({"key": secret})):
            self.assertIn("[REDACTED]", redact(value=value, secret=secret))
            self.assertNotIn(secret, redact(value=value, secret=secret))


class CollectTests(unittest.IsolatedAsyncioTestCase):
    """Exercise independent artifact collection."""

    async def test_turn_limit_preserves_native_error_kind(self) -> None:
        """Record the Hint kind before Harbor normalizes its timeout exception."""
        agent = AdapterTests().agent()
        binary = SelectedBinary(
            path=Path("/tmp/hint"), sha256="fixture", architecture="amd64"
        )
        agent.__dict__["binaries"] = Mock(select=Mock(return_value=binary))
        environment = Mock()
        environment.exec = AsyncMock(return_value=Mock(return_code=1))
        environment.upload_file = AsyncMock()
        with TemporaryDirectory() as temp:
            agent.logs_dir = Path(temp)
            with (
                patch.object(
                    agent,
                    "verify_binary",
                    new_callable=AsyncMock,
                    return_value=BinaryIdentity(sha256=binary.sha256, version=REVISION),
                ),
                patch.object(
                    agent,
                    "exec_as_agent",
                    new_callable=AsyncMock,
                    return_value=Mock(stdout="1000"),
                ),
                patch.object(agent, "exec_as_root", new_callable=AsyncMock),
                patch.object(
                    HintTrial,
                    "collect",
                    new_callable=AsyncMock,
                    return_value=HintAnswer(error=AgentError(kind="turn_limit")),
                ),
                self.assertRaisesRegex(AgentTimeoutError, "kind=turn_limit"),
            ):
                await agent.run(
                    instruction="fixture",
                    environment=environment,
                    context=AgentContext(),
                )
            execution = json.loads((agent.logs_dir / "execution.json").read_text())
            self.assertEqual(execution["error_kind"], "turn_limit")
            self.assertFalse(execution["cancelled"])

    async def test_partial_download(self) -> None:
        """Retain stderr and the session if stdout download fails."""
        agent = AdapterTests().agent()
        environment = Mock()
        environment.exec = AsyncMock(return_value=Mock(return_code=0))

        async def download_file(*, source_path: str, target_path: Path) -> None:
            if source_path.endswith("stdout.json"):
                raise OSError("failed download")
            target_path.write_text("fixture-test-token")

        async def download_dir(*, source_dir: str, target_dir: Path) -> None:
            worktree = target_dir / "project"
            worktree.mkdir(parents=True)
            records = [
                {"kind": "session", "id": "test-session"},
                {
                    "kind": "message",
                    "message": {
                        "role": "user",
                        "content": [{"kind": "text", "text": "test"}],
                    },
                },
            ]
            (worktree / "session.jsonl").write_text("\n".join(map(json.dumps, records)))

        environment.download_file = AsyncMock(side_effect=download_file)
        environment.download_dir = AsyncMock(side_effect=download_dir)
        with TemporaryDirectory() as temp:
            agent.logs_dir = Path(temp)
            trial = HintTrial(
                agent=agent,
                environment=environment,
                state="/tmp/test",
                context=AgentContext(),
            )
            with self.assertLogs(agent.logger, level="WARNING"):
                answer = await trial.collect()
            self.assertIsNone(answer.session_id)
            self.assertEqual((agent.logs_dir / "stderr.log").read_text(), "[REDACTED]")
            self.assertTrue((agent.logs_dir / "trajectory.json").exists())


if __name__ == "__main__":
    unittest.main()
