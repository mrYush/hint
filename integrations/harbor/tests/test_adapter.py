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
from pydantic import ValidationError

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

    def test_https_required(self) -> None:
        """Require deliberate opt-in before sending credentials over test HTTP."""
        for endpoint in ("http://gateway.invalid/v1", "http://localhost:1234/v1"):
            with self.subTest(endpoint=endpoint):
                with self.assertRaisesRegex(ValueError, "HTTPS is required"):
                    self.agent(base_url=endpoint)
        with self.assertRaisesRegex(ValueError, "restricted to local"):
            self.agent(base_url="http://gateway.invalid/v1", allow_insecure_http=True)
        self.assertEqual(
            self.agent(
                base_url="http://localhost:1234/v1", allow_insecure_http=True
            ).endpoint,
            "http://localhost:1234/v1",
        )

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

    def test_journal_tail_validation(self) -> None:
        """Recover unfinished JSON only at EOF and reject complete corrupt records."""
        header = '{"kind":"session","id":"test"}\n'
        cases = [
            ('{"kind":', True),
            ('{"kind":\n', False),
            ('{"kind":\n   ', False),
            ('{"kind":\n{"kind":"compaction"}\n', False),
            ('{"kind":42}', False),
            ('{"kind": invalid}', False),
            ('{"kind":"message","message":{"role":"invalid"}}', False),
            ('{"kind":"compaction"}', True),
            ('{"kind":"compaction"}\n', True),
        ]
        with TemporaryDirectory() as temp:
            path = Path(temp) / "session.jsonl"
            for tail, valid in cases:
                with self.subTest(tail=tail):
                    path.write_text(header + tail)
                    if valid:
                        self.assertEqual(read_journal(path=path)[0].id, "test")
                    else:
                        with self.assertRaises(ValidationError):
                            read_journal(path=path)

    def test_tool_trajectory_round_trip(self) -> None:
        """Validate serialized tool observations while tolerating future journal kinds."""
        records = [
            {"kind": "session", "id": "tool-session"},
            {
                "kind": "message",
                "message": {
                    "role": "user",
                    "content": [{"kind": "text", "text": "Inspect and write. α"}],
                },
            },
            {
                "kind": "message",
                "message": {
                    "role": "assistant",
                    "content": [
                        {"kind": "thinking", "text": "Inspect first."},
                        {"kind": "future-content", "text": "ignored"},
                    ],
                    "tool_calls": [
                        {"id": "probe", "name": "bash", "arguments": {"command": "pwd"}}
                    ],
                },
            },
            {
                "kind": "message",
                "message": {
                    "role": "tool",
                    "tool_call_id": "probe",
                    "content": [{"kind": "text", "text": "/app\n"}],
                },
            },
            {"kind": "compaction"},
            {"kind": "future-record"},
            {
                "kind": "message",
                "message": {
                    "role": "assistant",
                    "tool_calls": [
                        {
                            "id": "write",
                            "name": "write_file",
                            "arguments": {"path": "answer.txt", "content": "α\n"},
                        }
                    ],
                },
            },
            {
                "kind": "message",
                "message": {
                    "role": "tool",
                    "tool_call_id": "write",
                    "content": [{"kind": "text", "text": "written"}],
                },
            },
            {
                "kind": "message",
                "message": {
                    "role": "assistant",
                    "content": [{"kind": "text", "text": "Done."}],
                },
            },
        ]
        with TemporaryDirectory() as temp:
            journal = Path(temp) / "session.jsonl"
            journal.write_text("\n".join(map(json.dumps, records)) + "\n")
            trace = convert(
                records=read_journal(path=journal),
                model="fixture/model",
                version="test",
                context=AgentContext(),
            )
        validated = Trajectory.model_validate_json(
            trace.model_dump_json(exclude_none=True, indent=2)
        )
        self.assertEqual(validated.session_id, "tool-session")
        self.assertEqual([step.step_id for step in validated.steps], [1, 2, 3, 4])
        self.assertEqual(validated.steps[0].message, "Inspect and write. α")
        self.assertEqual(validated.steps[1].reasoning_content, "Inspect first.")
        for step, call_id, name, result in (
            (validated.steps[1], "probe", "bash", "/app\n"),
            (validated.steps[2], "write", "write_file", "written"),
        ):
            self.assertIsNotNone(step.tool_calls)
            self.assertIsNotNone(step.observation)
            assert step.tool_calls is not None and step.observation is not None
            self.assertEqual(step.tool_calls[0].tool_call_id, call_id)
            self.assertEqual(step.tool_calls[0].function_name, name)
            self.assertEqual(step.observation.results[0].source_call_id, call_id)
            self.assertEqual(step.observation.results[0].content, result)
        assert validated.final_metrics is not None
        self.assertEqual(validated.final_metrics.total_steps, 4)
        self.assertIsNone(validated.final_metrics.total_prompt_tokens)
        self.assertIsNone(validated.final_metrics.total_cost_usd)

    def test_future_provider_values(self) -> None:
        """Preserve provider values outside today's known finish and error kinds."""
        answer = HintAnswer.model_validate_json(
            '{"finish_reason":"future-stop","error":{"kind":"future-error"}}'
        )
        self.assertEqual(answer.finish_reason, "future-stop")
        assert answer.error is not None
        self.assertEqual(answer.error.kind, "future-error")

    def test_journal_invalid_utf8(self) -> None:
        """Do not replace invalid bytes in an otherwise complete JSON record."""
        with TemporaryDirectory() as temp:
            path = Path(temp) / "session.jsonl"
            path.write_bytes(b'{"kind":"session","id":"invalid-\xff"}\n')
            with self.assertRaises(ValidationError):
                read_journal(path=path)

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

        async def collect(trial: HintTrial) -> HintAnswer:
            trial.collection_errors["session"] = "OSError"
            trial.answer = HintAnswer(error=AgentError(kind="turn_limit"))
            raise TimeoutError("fixture-test-token")

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
                    new=collect,
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
            self.assertEqual(
                execution["collection_errors"],
                {"session": "OSError", "collection": "TimeoutError"},
            )

    async def test_missing_and_invalid_evidence(self) -> None:
        """Distinguish absent artifacts from invalid JSON without leaking validation input."""
        agent = AdapterTests().agent()
        environment = Mock()
        environment.exec = AsyncMock(return_value=Mock(return_code=1))
        with TemporaryDirectory() as temp:
            agent.logs_dir = Path(temp)
            trial = HintTrial(
                agent=agent,
                environment=environment,
                state="/tmp/test",
                context=AgentContext(),
            )
            await trial.collect()
            self.assertEqual(trial.collection_errors["stdout.json"], "missing")
            self.assertEqual(trial.collection_errors["session"], "missing")
            environment.exec.return_value = Mock(return_code=0)

            async def invalid(*, source_path: str, target_path: Path) -> None:
                target_path.write_text('{"session_id": ["fixture-test-token"]}')

            environment.download_file = AsyncMock(side_effect=invalid)
            environment.download_dir = AsyncMock(
                side_effect=OSError("fixture-test-token")
            )
            trial.collection_errors.clear()
            with self.assertLogs(agent.logger, level="WARNING") as logs:
                await trial.collect()
            self.assertEqual(trial.collection_errors["stdout.json"], "ValidationError")
            self.assertEqual(trial.collection_errors["session"], "OSError")
            self.assertNotIn("fixture-test-token", str(logs.output))

    async def test_session_selection(self) -> None:
        """Match header IDs, retain rejected evidence and mark empty trajectories."""
        cases = [
            (["other", "target"], "target", True, None),
            (["target", "other"], "target", True, None),
            (["other"], "target", True, "session_id_not_found"),
            (["target", "target"], "target", True, "ambiguous_session_id"),
            (["target"], None, True, "unconfirmed_session_id"),
            (["target", "other"], None, True, "unconfirmed_session_id"),
            (["target"], "target", False, "empty_trajectory"),
        ]
        for ids, expected_id, with_message, error in cases:
            with (
                self.subTest(ids=ids, expected_id=expected_id, error=error),
                TemporaryDirectory() as temp,
            ):
                agent = AdapterTests().agent()
                agent.logs_dir = Path(temp)
                environment = Mock()
                environment.exec = AsyncMock(return_value=Mock(return_code=0))

                async def download_file(*, source_path: str, target_path: Path) -> None:
                    target_path.write_text(
                        json.dumps({"session_id": expected_id})
                        if source_path.endswith("stdout.json")
                        else ""
                    )

                async def download_dir(*, source_dir: str, target_dir: Path) -> None:
                    project = target_dir / "project"
                    project.mkdir(parents=True)
                    for index, session_id in enumerate(ids):
                        records: list[dict[str, object]] = [
                            {"kind": "session", "id": session_id}
                        ]
                        if with_message:
                            records.append(
                                {
                                    "kind": "message",
                                    "message": {
                                        "role": "user",
                                        "content": [
                                            {"kind": "text", "text": session_id}
                                        ],
                                    },
                                }
                            )
                        (project / f"{index}.jsonl").write_text(
                            "\n".join(map(json.dumps, records)) + "\n"
                        )

                environment.download_file = AsyncMock(side_effect=download_file)
                environment.download_dir = AsyncMock(side_effect=download_dir)
                trial = HintTrial(
                    agent=agent,
                    environment=environment,
                    state="/tmp/test",
                    context=AgentContext(),
                )
                await trial.collect()
                self.assertEqual(
                    len(list((agent.logs_dir / "sessions").glob("*.jsonl"))), len(ids)
                )
                if error is None:
                    self.assertEqual(trial.collection_errors, {})
                else:
                    self.assertIn(error, trial.collection_errors.values())
                primary = agent.logs_dir / "trajectory.json"
                if error in {
                    "session_id_not_found",
                    "ambiguous_session_id",
                    "empty_trajectory",
                } or (expected_id is None and len(ids) > 1):
                    self.assertFalse(primary.exists())
                else:
                    trace = Trajectory.model_validate_json(primary.read_text())
                    self.assertEqual(trace.session_id, "target")
                    self.assertEqual(bool(trace.steps), with_message)

    async def test_success_rejects_empty_trajectory(self) -> None:
        """Exercise the run boundary with an empty collected trajectory."""
        agent = AdapterTests().agent()
        binary = SelectedBinary(
            path=Path("/tmp/hint"), sha256="fixture", architecture="amd64"
        )
        agent.__dict__["binaries"] = Mock(select=Mock(return_value=binary))
        environment = Mock()
        environment.exec = AsyncMock(return_value=Mock(return_code=0))
        environment.upload_file = AsyncMock()

        async def collect(trial: HintTrial) -> HintAnswer:
            trial.collection_errors["trajectory"] = "empty_trajectory"
            (agent.logs_dir / "trajectory.json").write_text('{"steps": []}')
            return HintAnswer(session_id="target")

        with TemporaryDirectory() as temp:
            agent.logs_dir = Path(temp)
            with (
                patch.object(
                    agent,
                    "verify_binary",
                    new_callable=AsyncMock,
                    return_value=BinaryIdentity(sha256="fixture", version=REVISION),
                ),
                patch.object(
                    agent,
                    "exec_as_agent",
                    new_callable=AsyncMock,
                    return_value=Mock(stdout="1000"),
                ),
                patch.object(agent, "exec_as_root", new_callable=AsyncMock),
                patch.object(HintTrial, "collect", new=collect),
            ):
                with self.assertRaisesRegex(RuntimeError, "Incomplete Hint artifacts"):
                    await agent.run("fixture", environment, AgentContext())
            execution = json.loads((agent.logs_dir / "execution.json").read_text())
            self.assertEqual(execution["exit_code"], 0)
            self.assertEqual(
                execution["collection_errors"]["trajectory"], "empty_trajectory"
            )

    async def test_partial_download(self) -> None:
        """Retain stderr and the session if stdout download fails."""
        agent = AdapterTests().agent()
        environment = Mock()
        environment.exec = AsyncMock(return_value=Mock(return_code=0))

        async def download_file(*, source_path: str, target_path: Path) -> None:
            if source_path.endswith("stdout.json"):
                raise OSError("fixture-test-token failed download")
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
            self.assertEqual(
                trial.collection_errors,
                {"stdout.json": "OSError", "session": "unconfirmed_session_id"},
            )
            self.assertNotIn("fixture-test-token", json.dumps(trial.collection_errors))
            self.assertEqual((agent.logs_dir / "stderr.log").read_text(), "[REDACTED]")
            self.assertTrue((agent.logs_dir / "trajectory.json").exists())


if __name__ == "__main__":
    unittest.main()
