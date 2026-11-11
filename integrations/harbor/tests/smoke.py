"""Run the production Hint loop and a separate verifier using local Docker."""

import argparse
import json
import os
import subprocess
import sys
import threading
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import Literal, override

from harbor.models.trajectories import Trajectory

KEY = "fake-harbor-canary-key"
MODEL = "fixture/model"
PROMPT = "Write the result. Юникод ' \" `touch injected` $(touch injected) ; $HOME\n\n"
IMAGE = "ubuntu@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852"


type SmokeMode = Literal["success", "auth", "timeout"]


@dataclass(frozen=True, slots=True, kw_only=True)
class SmokeArgs:
    """Arguments for one Docker regression run."""

    binary_dir: Path
    jobs: Path
    mode: SmokeMode
    host: str
    bind: str


class ProviderServer(ThreadingHTTPServer):
    """Local HTTP provider with typed fixture state."""

    def __init__(self, *, bind: str, instruction: str, mode: SmokeMode) -> None:
        self.instruction = instruction
        self.mode = mode
        self.called = threading.Event()
        self.release = threading.Event()
        super().__init__((bind, 0), Provider)


class Provider(BaseHTTPRequestHandler):
    """Serve deterministic Chat Completions events and controlled failures."""

    @property
    def provider(self) -> ProviderServer:
        """Return the fixture server used by this handler."""
        if not isinstance(self.server, ProviderServer):
            raise TypeError("Expected ProviderServer")
        return self.server

    @override
    def log_message(self, format: str, *args: object) -> None:
        """Suppress HTTP request logs during fixture execution."""

    def do_POST(self) -> None:
        """Validate a Hint request and send the next fixture response."""
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        assert self.path == "/v1/chat/completions"
        assert self.headers["Authorization"] == f"Bearer {KEY}"
        assert request["model"] == MODEL
        messages = request["messages"]
        user = [message for message in messages if message["role"] == "user"]
        assert len(user) == 1 and user[0]["content"] == self.provider.instruction
        self.provider.called.set()
        if self.provider.mode == "auth":
            self.send_response(401)
            self.end_headers()
            self.wfile.write(json.dumps({"error": {"message": KEY}}).encode())
            return
        if self.provider.mode == "timeout":
            self.provider.release.wait(60)
            return
        results = [message for message in messages if message["role"] == "tool"]
        if len(results) == 0:
            self.tool(
                call_id="probe",
                name="bash",
                arguments={
                    "command": (
                        'test "$(id -u)" = 1000 && test "$FIXTURE_TASK_ENV" = preserved '
                        "&& test ! -e /tests/test.sh && test ! -e /solution/solve.sh && test ! -e injected "
                        "&& test ! -e answer.txt && printf 'clean:%s' " + KEY
                    )
                },
            )
        elif len(results) == 1:
            assert results[0]["content"].strip() == "clean:***"
            self.tool(
                call_id="write",
                name="write_file",
                arguments={"path": "answer.txt", "content": "verified\n"},
            )
        else:
            assert len(results) == 2
            self.stream(
                choices=[{"delta": {"content": f"Done {KEY}"}, "finish_reason": "stop"}]
            )

    def tool(self, *, call_id: str, name: str, arguments: dict[str, str]) -> None:
        """Return a streamed native tool call.

        :param call_id: Provider-generated tool call ID.
        :param name: Hint tool name.
        :param arguments: Arguments passed to the tool.
        """
        self.stream(
            choices=[
                {
                    "delta": {
                        "tool_calls": [
                            {
                                "index": 0,
                                "id": call_id,
                                "type": "function",
                                "function": {
                                    "name": name,
                                    "arguments": json.dumps(arguments),
                                },
                            }
                        ]
                    },
                    "finish_reason": "tool_calls",
                }
            ]
        )

    def stream(self, *, choices: list[dict[str, object]]) -> None:
        """Send streamed choices followed by a usage event.

        :param choices: OpenAI-compatible choices for this request.
        """
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for data in (
            {"choices": choices},
            {
                "choices": [],
                "usage": {
                    "prompt_tokens": 40,
                    "completion_tokens": 5,
                    "prompt_tokens_details": {"cached_tokens": 3},
                },
            },
        ):
            self.wfile.write(f"data: {json.dumps(data)}\n\n".encode())
        self.wfile.write(b"data: [DONE]\n\n")


def make_task(*, task: Path, timeout: int) -> None:
    """Create a disposable fixture with a separate verifier.

    :param task: Fixture directory to create.
    :param timeout: Harbor deadline in seconds.
    """
    for name in ("environment", "solution", "tests"):
        (task / name).mkdir(parents=True)
    (task / "instruction.md").write_text(PROMPT, encoding="utf-8")
    (task / "task.toml").write_text(f"""
schema_version = "2.0"
artifacts = ["/app/answer.txt"]
[agent]
timeout_sec = {timeout}
user = 1000
[verifier]
environment_mode = "separate"
timeout_sec = 60
[environment]
cpus = 1
memory_mb = 512
""")
    (task / "environment/Dockerfile").write_text(
        f"FROM {IMAGE}\nWORKDIR /app\nENV FIXTURE_TASK_ENV=preserved\nRUN chown 1000:1000 /app\n"
    )
    (task / "solution/solve.sh").write_text(
        "#!/bin/sh\nprintf 'verified\\n' > /app/answer.txt\n"
    )
    (task / "tests/test.sh").write_text("""#!/bin/sh
mkdir -p /logs/verifier
if [ "$(cat /app/answer.txt)" = verified ]; then
    echo 1 > /logs/verifier/reward.txt
else
    echo 0 > /logs/verifier/reward.txt
fi
""")


def run(*, args: SmokeArgs, fixture: Path) -> None:
    """Exercise the installed agent through a local Harbor job.

    :param args: Binary location, job directory and fixture mode.
    :param fixture: Disposable fixture root.
    """
    if not __debug__:
        raise RuntimeError("Run the smoke test without Python optimization")
    timeout = 4 if args.mode == "timeout" else 90
    tasks = fixture / "tasks"
    for name in ("tool-trial-a", "tool-trial-b"):
        make_task(task=tasks / name, timeout=timeout)

    # Match Harbor's actual instruction parsing, including leading/trailing whitespace rules.
    from harbor.models.task.task import Task

    instruction = Task(tasks / "tool-trial-a").instruction
    with ProviderServer(
        bind=args.bind, instruction=instruction, mode=args.mode
    ) as server:
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        command = [
            str(Path(sys.executable).with_name("harbor")),
            "run",
            "-p",
            str(tasks),
            "-a",
            "hint_harbor.agent:HintAgent",
            "-m",
            MODEL,
            "--sm",
            "--base-url",
            f"http://{args.host}:{server.server_port}/v1",
            "--api-key-name",
            "HINT_API_KEY",
            "--ak",
            f"binary_dir={args.binary_dir.resolve()}",
            "--ak",
            "context_window=32768",
            "--ak",
            "route=compatible",
            "--ak",
            "reasoning_expectation=endpoint-default",
            "--ak",
            "allow_insecure_http=true",
            "-n",
            "1",
            "-k",
            "2" if args.mode == "success" else "1",
            "--max-retries",
            "0",
            "-o",
            str(args.jobs.resolve()),
            "--job-name",
            args.mode,
        ]
        if args.mode != "success":
            command.extend(("-i", "tool-trial-a"))
        try:
            subprocess.run(command, env={**os.environ, "HINT_API_KEY": KEY}, check=True)
        finally:
            server.release.set()
            server.shutdown()

    job = args.jobs / args.mode
    assert server.called.is_set(), "The fake provider was never called"
    results = sorted(job.glob("*/result.json"))
    assert len(results) == (4 if args.mode == "success" else 1), results
    session_ids = set()
    for path in results:
        result = json.loads(path.read_text())
        logs = path.parent / "agent"
        execution = json.loads((logs / "execution.json").read_text())
        trace = Trajectory.model_validate_json((logs / "trajectory.json").read_text())
        assert trace.steps[0].message == server.instruction
        session_ids.add(trace.session_id)
        if args.mode == "success":
            assert result["exception_info"] is None, result["exception_info"]
            assert result["verifier_environment_mode"] == "separate"
            assert result["verifier_result"]["rewards"]["reward"] == 1
            assert execution["exit_code"] == 0
            assert execution["usage"] == {
                "n_input_tokens": 120,
                "n_output_tokens": 15,
                "n_cache_tokens": 9,
                "cost_usd": None,
            }
            assert [
                call.function_name
                for step in trace.steps
                for call in step.tool_calls or []
            ] == ["bash", "write_file"]
        else:
            expected = (
                "AgentTimeoutError"
                if args.mode == "timeout"
                else "AgentAuthenticationError"
            )
            assert result["exception_info"]["exception_type"] == expected, result[
                "exception_info"
            ]
            assert execution["cancelled"] == (args.mode == "timeout")
    assert len(session_ids) == len(results)
    for path in job.rglob("*"):
        if path.is_file():
            assert KEY.encode() not in path.read_bytes(), path
    print(f"{args.mode}: {len(results)} verified trial records under {job}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary_dir", type=Path)
    parser.add_argument("jobs", type=Path)
    parser.add_argument(
        "--mode", choices=("success", "auth", "timeout"), default="success"
    )
    parser.add_argument("--host", default="host.docker.internal")
    parser.add_argument("--bind", default="127.0.0.1")
    args = SmokeArgs(**vars(parser.parse_args()))
    with TemporaryDirectory(prefix="hint-harbor-smoke-") as tmp:
        run(args=args, fixture=Path(tmp))
