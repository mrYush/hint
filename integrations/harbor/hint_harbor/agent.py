"""Installed-agent adapter for the pinned Harbor and Hint releases."""

import asyncio
import json
import re
import shlex
import sys
from dataclasses import dataclass, field
from datetime import datetime, timezone
from functools import cached_property
from hashlib import file_digest
from importlib.metadata import version
from ipaddress import ip_address, ip_network
from pathlib import Path
from tempfile import TemporaryDirectory
from typing import ClassVar, Final, Literal, override
from urllib.parse import urlsplit
from uuid import uuid4

from harbor.agents.agent_auth_resolution import ModelConnectionSpec, ProviderAccess
from harbor.agents.capabilities import AgentCapabilities
from harbor.agents.installed.base import (
    AgentAuthenticationError,
    AgentSafetyRefusalError,
    ApiOverloadedError,
    ApiRateLimitError,
    BaseInstalledAgent,
    ContextWindowExceededError,
    ModelNotFoundError,
    NetworkConnectionError,
    NonZeroAgentExitCodeError,
    UnknownApiError,
)
from harbor.agents.options import InstalledAgentOptions
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext
from harbor.models.model_api_format import ModelApiFormat
from harbor.trial.errors import AgentTimeoutError
from pydantic import BaseModel, Field

from .trace import convert, read_journal

REVISION = "53264f88ad6900abab31c8e50e1281c0381ff45f"
HINT_VERSION: Final = f"harbor-baseline+{REVISION[:7]}"
BINARY = "/installed-agent/hint"
CHAT = ModelApiFormat.OPENAI_CHAT_COMPLETIONS
type Architecture = Literal["amd64", "arm64"]
type Route = Literal["glm", "deepseek", "compatible"]
type Reasoning = Literal["endpoint-default", "non-thinking"]
ARCHITECTURES: Final[dict[str, Architecture]] = {"x86_64": "amd64", "aarch64": "arm64"}


class CancelledError(NonZeroAgentExitCodeError):
    """Report a Hint cancellation without cancelling the parent Harbor job."""


ERRORS: Final[dict[str, type[Exception]]] = {
    "turn_limit": AgentTimeoutError,
    "canceled": CancelledError,
    "content_filtered": AgentSafetyRefusalError,
    "auth": AgentAuthenticationError,
    "rate_limited": ApiRateLimitError,
    "unavailable": ApiOverloadedError,
    "network": NetworkConnectionError,
    "timeout": NetworkConnectionError,
    "context_overflow": ContextWindowExceededError,
    "model_not_found": ModelNotFoundError,
    "invalid_request": UnknownApiError,
    "unknown": UnknownApiError,
}


class HintOptions(InstalledAgentOptions):
    """Options for a benchmark trial.

    :ivar binary_dir: Local directory containing the pinned Linux builds.
    :ivar context_window: Exact model context window in tokens.
    :ivar route: Provider family.
    :ivar reasoning_expectation: Operator declaration about gateway behavior.
    :ivar allow_insecure_http: Explicit opt-in for local fake-provider HTTP.
    """

    binary_dir: Path = Field(
        description="Directory built by build-hint.sh, with SHA256SUMS."
    )
    context_window: int = Field(
        ge=1, description="Context window of the exact model in tokens."
    )
    route: Route = Field(description="Provider family for compatibility checks.")
    allow_insecure_http: bool = False
    reasoning_expectation: Reasoning = Field(
        description="Endpoint reasoning behavior, declared by the operator."
    )


@dataclass(frozen=True, slots=True, kw_only=True)
class SelectedBinary:
    """Checksum-verified Linux build.

    :ivar path: Local binary path.
    :ivar sha256: Lowercase SHA-256 digest.
    :ivar architecture: Normalized Linux architecture.
    """

    path: Path
    sha256: str
    architecture: Architecture


class HintBinaries:
    """Read the pinned build checksums once for a set of Linux binaries.

    :ivar directory: Output directory of the pinned build script.
    """

    def __init__(self, *, directory: Path) -> None:
        self.directory = directory
        self._checksums: dict[str, str] = {}
        for line in (directory / "SHA256SUMS").read_text().splitlines():
            if not line.strip():
                continue
            match = re.fullmatch(
                r"([0-9a-f]{64})[ \t]+(hint-linux-(?:amd64|arm64))", line
            )
            if match is None or match[2] in self._checksums:
                raise ValueError("Invalid or duplicate entry in SHA256SUMS")
            self._checksums[match[2]] = match[1]

    def select(self, *, machine: str) -> SelectedBinary:
        """Verify the binary matching the container architecture.

        :param machine: Container architecture from uname.
        :returns: Verified binary identity.
        :raises ValueError: For unsupported architectures or invalid checksums.
        """
        if machine not in ARCHITECTURES:
            raise ValueError(f"Unsupported Hint Linux architecture: {machine}")
        arch = ARCHITECTURES[machine]
        binary = self.directory / f"hint-linux-{arch}"
        with binary.open("rb") as source:
            actual = file_digest(source, "sha256").hexdigest()
        if self._checksums.get(binary.name) != actual:
            raise ValueError(f"Checksum mismatch for {binary.name}")
        return SelectedBinary(path=binary, sha256=actual, architecture=arch)


def select_binary(*, directory: Path, machine: str) -> SelectedBinary:
    """Verify a binary from the pinned Linux builds.

    :param directory: Output directory of the pinned build script.
    :param machine: Container architecture from uname.
    :returns: Verified binary identity.
    :raises ValueError: If the architecture or checksum is invalid.
    :raises OSError: If the binary or checksum file cannot be read.
    """
    return HintBinaries(directory=directory).select(machine=machine)


@dataclass(frozen=True, slots=True, kw_only=True)
class BinaryIdentity:
    """Remote checksum and CLI version line from the installed binary."""

    sha256: str
    version: str


def redact(*, value: str, secret: str) -> str:
    """Remove the literal and JSON-escaped key from a log.

    :param value: Log contents.
    :param secret: Printable credential to mask.
    :returns: Redacted log contents.
    """
    # The key is printable ASCII, but it may have been escaped in JSON output.
    escaped = json.dumps(secret)[1:-1]
    go_escaped = escaped.translate(
        str.maketrans({"&": r"\u0026", "<": r"\u003c", ">": r"\u003e"})
    )
    for form in (secret, escaped, go_escaped):
        value = value.replace(form, "[REDACTED]")
    return value


class Usage(BaseModel):
    """Provider-reported usage returned by Hint."""

    input_tokens: int | None = None
    output_tokens: int | None = None
    cached_input_tokens: int | None = None


class AgentError(BaseModel):
    """Classified Hint process error."""

    kind: str | None = None
    status_code: int | None = None


class HintAnswer(BaseModel):
    """Machine-readable fields used from Hint stdout."""

    session_id: str | None = None
    finish_reason: str | None = None
    usage: Usage | None = None
    error: AgentError | None = None


class EffectiveConfig(BaseModel):
    """Sanitized record of effective Hint trial configuration."""

    hint: str
    hint_commit: str = REVISION
    hint_sha256: str
    platform: str
    adapter: str
    harbor: str
    python: str
    base_url: str
    model: str
    allow_insecure_http: bool = False
    api_format: ModelApiFormat = CHAT
    route: Route
    reasoning_expectation: Reasoning
    reasoning_enforced: Literal[False] = False
    output_token_override: None = None
    context_window: int
    fallback: None = None
    permission_mode: Literal["yolo"] = "yolo"
    profile: Literal["harbor"] = "harbor"
    tools: dict[str, str | bool] = {"source": "pinned Hint binary", "mcp": False}
    context_management: dict[str, str | int] = {
        "source": "pinned Hint binary",
        "compaction": "native",
        "instruction_budget_bytes": 32768,
        "overview_depth": 2,
        "overview_max_entries": 100,
    }
    system_prompt: dict[str, str | bool] = {
        "source": "pinned Hint binary and task project context",
        "captured": False,
    }


class ExecutionRecord(BaseModel):
    """Timing, process status and available usage for one attempt."""

    started_at: str
    finished_at: str
    exit_code: int | None
    cancelled: bool
    error_kind: str | None = None
    usage: dict[str, int | float | None]
    collection_errors: dict[str, str] = Field(default_factory=dict)


class HintAgent(BaseInstalledAgent):
    """Run the production Hint CLI inside a Harbor task.

    :cvar options_model: Trial option schema.
    :cvar capabilities: ATIF and explicit endpoint support.
    """

    options_model: ClassVar[type[InstalledAgentOptions] | None] = HintOptions
    capabilities: ClassVar[AgentCapabilities] = AgentCapabilities(
        atif=True, base_url=True
    )
    MODEL_CONNECTION: ClassVar[ModelConnectionSpec | None] = ModelConnectionSpec(
        agent_provider_access=ProviderAccess(
            api_key_candidate_names=["HINT_API_KEY"],
            base_url_candidate_names=["HINT_API_URL"],
            default_api_format=CHAT,
        ),
        supported_api_formats=[CHAT],
        api_key_destination_names={"default": "HINT_API_KEY"},
        base_url_destination_names={"default": "HINT_API_URL"},
        default_api_format_by_provider={"default": CHAT},
    )

    @override
    def __init__(
        self,
        *,
        logs_dir: Path,
        extra_env: dict[str, str] | None = None,
        version: str | None = None,
        prompt_template_path: Path | str | None = None,
        config: Path | str | dict[str, object] | None = None,
        **kwargs: object,
    ) -> None:
        """Resolve an explicit model and environment-based credential.

        :param logs_dir: Host directory for redacted artifacts.
        :param extra_env: Explicit Harbor environment overlay.
        :param version: Unsupported override of the pinned version.
        :param prompt_template_path: Unsupported instruction template.
        :param config: Unsupported native Hint configuration.
        :param kwargs: Harbor framework arguments and validated adapter options.
        :raises ValueError: If the selected route or configuration is unsupported.
        :raises AgentAuthenticationError: If a usable credential is unavailable.
        """
        if any(value is not None for value in (version, prompt_template_path, config)):
            raise ValueError(
                "Hint does not support version, prompt or native config overrides"
            )
        super().__init__(
            logs_dir=logs_dir,
            extra_env=extra_env,
            version=None,
            prompt_template_path=None,
            config=None,
            **kwargs,
        )
        self._version = HINT_VERSION
        options = self.hint_options
        if (
            options.version
            or options.reasoning_effort
            or options.prompt_template_path
            or options.disable_web_search
            or self.load_trajectory
            or self.skills_dir
            or self.mcp_servers
        ):
            raise ValueError("Hint evaluation does not support these runtime overrides")
        connection = self.model_connection
        model = connection.model_id
        endpoint = connection.user_supplied_base_url
        secret = connection.api_key
        if (
            not model
            or model != self.model_name
            or model != model.strip()
            or "\0" in model
        ):
            raise ValueError("Supply the exact model ID with --model and --sm")
        if not endpoint:
            raise ValueError("Supply an explicit Chat Completions base URL")
        try:
            url = urlsplit(endpoint)
        except ValueError:
            raise ValueError("Invalid Chat Completions base URL") from None
        if (
            url.scheme not in {"http", "https"}
            or not url.hostname
            or url.username is not None
            or url.query
            or url.fragment
            or any(c.isspace() for c in endpoint)
        ):
            raise ValueError(
                "Use an HTTP(S) base URL without credentials, query or fragment"
            )
        if url.scheme == "http" and not options.allow_insecure_http:
            raise ValueError(
                "HTTPS is required; allow_insecure_http is for local test providers"
            )
        if url.scheme == "http":
            local_host = url.hostname in {"localhost", "host.docker.internal"}
            try:
                address = ip_address(url.hostname)
                local_host = (
                    local_host
                    or address.is_loopback
                    or address in ip_network("10.0.0.0/8")
                    or address in ip_network("172.16.0.0/12")
                    or address in ip_network("192.168.0.0/16")
                )
            except ValueError:
                pass
            if not local_host:
                raise ValueError(
                    "HTTP opt-in is restricted to local test-provider addresses"
                )
        if not secret or not re.fullmatch(r"[!-~]+", secret):
            raise AgentAuthenticationError(
                "Supply an API key through the configured environment variable"
            )
        if secret in endpoint or secret in model:
            raise ValueError("Keep the API key out of the model ID and endpoint")
        is_deepseek = (
            options.route == "deepseek"
            or connection.provider == "deepseek"
            or "deepseek" in url.hostname
            or "deepseek" in model.lower()
        )
        if is_deepseek and (
            options.reasoning_expectation != "non-thinking"
            or "reasoner" in model.lower()
            or url.hostname.endswith(".deepseek.com")
            or url.hostname == "deepseek.com"
        ):
            raise ValueError(
                "DeepSeek requires a gateway with an operator-verified non-thinking route; "
                "Hint cannot round-trip reasoning_content on tool turns"
            )

        if (
            options.reasoning_expectation == "non-thinking"
            and options.route != "deepseek"
        ):
            raise ValueError(
                "non-thinking is only accepted for an operator-verified DeepSeek gateway"
            )
        self.model: str = model
        self.endpoint: str = endpoint
        self._secret: str = secret
        self._key_source_name = connection.api_key_source_name

    @property
    def hint_options(self) -> HintOptions:
        """Return the concrete Harbor-parsed options.

        :returns: Adapter options.
        :raises TypeError: If Harbor parsed a different options model.
        """
        if not isinstance(self.options, HintOptions):
            raise TypeError("Expected HintOptions from Harbor")
        return self.options

    @classmethod
    @override
    def options_schema(cls) -> dict[str, object]:
        """Expose only the options supported by this adapter.

        :returns: Harbor JSON Schema for the supported options.
        """
        schema = super().options_schema()
        for key in (
            "version",
            "prompt_template_path",
            "config",
            "disable_web_search",
            "reasoning_effort",
        ):
            schema["properties"].pop(key, None)
        return schema

    @cached_property
    def binaries(self) -> HintBinaries:
        """Return the local pinned binaries for this adapter instance.

        :returns: Local binaries and their expected checksums.
        :raises ValueError: If the checksum file contains invalid entries.
        :raises OSError: If the checksum file cannot be read.
        """
        return HintBinaries(directory=self.hint_options.binary_dir)

    async def verify_binary(
        self, *, environment: BaseEnvironment, expected: SelectedBinary
    ) -> BinaryIdentity:
        """Verify the installed checksum and commit against the selected build.

        :param environment: Target Harbor environment.
        :param expected: Local build selected for the container.
        :returns: Observed checksum and version.
        :raises RuntimeError: If the installed identity cannot be verified.
        """
        result = await self.exec_as_agent(
            environment=environment, command=f"sha256sum {BINARY} && {BINARY} --version"
        )
        lines = result.stdout.strip().splitlines()
        if len(lines) != 2 or not lines[0].split():
            raise RuntimeError("Could not identify the installed Hint binary")
        identity = BinaryIdentity(sha256=lines[0].split()[0], version=lines[1])
        if identity.sha256 != expected.sha256 or REVISION not in identity.version:
            raise RuntimeError(
                "Installed Hint identity differs from the benchmark baseline"
            )
        return identity

    @staticmethod
    @override
    def name() -> str:
        """Return the Harbor agent name."""
        return "hint"

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        """Install the verified Linux binary.

        :param environment: Target Harbor environment.
        :raises RuntimeError: If the remote binary fails identity verification.
        """
        machine = await self.exec_as_agent(
            environment=environment, command='test "$(uname -s)" = Linux && uname -m'
        )
        binary = self.binaries.select(machine=machine.stdout.strip())
        await environment.upload_file(source_path=binary.path, target_path=BINARY)
        await self.exec_as_root(environment=environment, command=f"chmod 755 {BINARY}")
        await self.verify_binary(environment=environment, expected=binary)

    @override
    async def run(
        self, instruction: str, environment: BaseEnvironment, context: AgentContext
    ) -> None:
        """Run the production CLI and preserve partial artifacts.

        :param instruction: Literal benchmark task text.
        :param environment: Target Harbor environment.
        :param context: Receives provider-reported usage.
        :raises ValueError: If the instruction cannot be passed to the CLI.
        :raises NonZeroAgentExitCodeError: If Hint fails during the turn.
        """
        if not instruction or "\0" in instruction:
            raise ValueError("Hint CLI requires a nonempty instruction without NUL")
        if len(instruction.encode("utf-8")) >= 128 * 1024:
            raise ValueError(
                "Instruction exceeds the Linux limit for one Hint CLI argument"
            )
        # Keep task state off the task artifact and Harbor log mounts.
        state = f"/tmp/hint-harbor-{uuid4().hex}"
        self.logs_dir.mkdir(parents=True, exist_ok=True)
        machine = await self.exec_as_agent(environment=environment, command="uname -m")
        binary = self.binaries.select(machine=machine.stdout.strip())
        identity = await self.verify_binary(environment=environment, expected=binary)
        effective = EffectiveConfig(
            hint=identity.version,
            hint_sha256=identity.sha256,
            platform=f"linux/{binary.architecture}",
            adapter=version("hint-harbor"),
            harbor=version("harbor"),
            python=sys.version.split()[0],
            base_url=self.endpoint,
            model=self.model,
            route=self.hint_options.route,
            reasoning_expectation=self.hint_options.reasoning_expectation,
            allow_insecure_http=self.hint_options.allow_insecure_http,
            context_window=self.hint_options.context_window,
        )
        self.write_json(name="effective.json", data=effective)
        started = datetime.now(timezone.utc)
        exit_code = None
        answer = HintAnswer()
        cancelled = False
        trial = HintTrial(
            agent=self, environment=environment, state=state, context=context
        )
        command = trial.command()
        try:
            owner = await self.exec_as_agent(
                environment=environment, command=f"umask 077; mkdir {state} && id -u"
            )
            uid = owner.stdout.strip()
            if not re.fullmatch(r"[0-9]+", uid):
                raise RuntimeError("Could not resolve the Hint agent user")
            with TemporaryDirectory(prefix="hint-harbor-input-") as tmp:
                for name, content in (
                    ("instruction", instruction),
                    ("credential", self._secret),
                ):
                    source = Path(tmp) / name
                    source.touch(mode=0o600)
                    source.write_bytes(content.encode())
                    await environment.upload_file(
                        source_path=source, target_path=f"{state}/{name}"
                    )
            files = f"{state}/instruction {state}/credential"
            await self.exec_as_root(
                environment=environment,
                command=f"chown {uid} {files} && chmod 600 {files}",
            )
            result = await environment.exec(command=command)
            exit_code = result.return_code
        except asyncio.CancelledError:
            cancelled = True
            raise
        finally:
            try:
                async with asyncio.timeout(15):
                    if cancelled:
                        try:
                            await trial.stop()
                        except Exception:
                            self.logger.warning(
                                "Could not stop Hint before collecting logs"
                            )
                    answer = await trial.collect()
            except Exception as error:
                answer = trial.answer
                trial.collection_errors["collection"] = type(error).__name__
                self.logger.warning(
                    "Could not collect Hint artifacts (%s)", type(error).__name__
                )
            finally:
                try:
                    await environment.exec(command=f"rm -rf {state}", timeout_sec=3)
                except Exception:
                    self.logger.warning("Could not remove Hint trial state")
                self.write_json(
                    name="execution.json",
                    data=ExecutionRecord(
                        started_at=started.isoformat(),
                        finished_at=datetime.now(timezone.utc).isoformat(),
                        exit_code=exit_code,
                        cancelled=cancelled,
                        collection_errors=trial.collection_errors,
                        error_kind=answer.error.kind if answer.error else None,
                        usage={
                            "n_input_tokens": context.n_input_tokens,
                            "n_output_tokens": context.n_output_tokens,
                            "n_cache_tokens": context.n_cache_tokens,
                            "cost_usd": context.cost_usd,
                        },
                    ),
                )
        if exit_code != 0 or answer.error or answer.finish_reason == "error":
            error = answer.error or AgentError()
            kind = error.kind or "unknown"
            exception = ERRORS.get(kind, NonZeroAgentExitCodeError)
            raise exception(
                f"Hint failed (exit={exit_code}, kind={kind}, status={error.status_code}); see agent logs"
            )
        if trial.collection_errors:
            raise RuntimeError(
                "Incomplete Hint artifacts; see execution.json collection_errors"
            )
        if not answer.session_id or not (self.logs_dir / "trajectory.json").is_file():
            raise RuntimeError("Hint did not return a recorded session and trajectory")

    def write_json(self, *, name: str, data: BaseModel) -> None:
        """Write sanitized trial metadata.

        :param name: Local artifact filename.
        :param data: JSON-serializable metadata.
        """
        (self.logs_dir / name).write_text(
            data.model_dump_json(indent=2) + "\n", encoding="utf-8"
        )


@dataclass(kw_only=True, slots=True)
class HintTrial:
    """One isolated Hint execution.

    :ivar agent: Adapter holding the resolved route.
    :ivar environment: Target Harbor environment.
    :ivar state: Agent-owned container directory.
    :ivar context: Harbor usage context for this attempt.
    :ivar collection_errors: Artifact names mapped to safe failure categories.
    :ivar answer: Parsed stdout retained even if later collection times out.
    """

    agent: HintAgent
    environment: BaseEnvironment
    state: str
    context: AgentContext
    collection_errors: dict[str, str] = field(default_factory=dict)
    answer: HintAnswer = field(default_factory=HintAnswer)

    def command(self) -> str:
        """Build a launch command without credentials or prompt contents.

        :returns: Quoted shell command.
        """
        # Start a distinct process group so cancelling docker exec can stop Hint.
        # The sentinel preserves trailing newlines when reading the instruction.
        state = self.state
        route = {
            "HINT_API_URL": self.agent.endpoint,
            "HINT_MODEL": self.agent.model,
            "HINT_CONTEXT_WINDOW": str(self.agent.hint_options.context_window),
        }
        settings = " ".join(
            shlex.quote(f"{key}={value}") for key, value in route.items()
        )
        script = f"""
set -eu
umask 077
printf '%s\\n' "$$" > {state}/pid
test ! -e {state}/cancel
test ! -e .hint/config.yaml && test ! -e hint.yaml || {{
    echo 'Hint project config would override benchmark settings' >&2
    exit 1
}}
prompt=$(cat {state}/instruction && printf .)
for name in "${{!HINT_@}}"; do unset "$name"; done
export {settings}
HINT_API_KEY=$(cat {state}/credential)
rm -f {state}/credential {state}/instruction
export HINT_API_KEY
mkdir -p "$HOME/.config/hint" "$XDG_DATA_HOME/hint/sessions" "$TMPDIR"
printf '%s\\n' 'default_provider: harbor' 'providers:' '  - name: harbor' '    kind: openai' > "$HOME/.config/hint/config.yaml"
exec {BINARY} -p "${{prompt%.}}" --output json --yolo
"""
        env = {
            "HOME": f"{state}/home",
            "XDG_CONFIG_HOME": f"{state}/home/.config",
            "XDG_DATA_HOME": f"{state}/data",
            "XDG_CACHE_HOME": f"{state}/cache",
            "XDG_STATE_HOME": f"{state}/state",
            "TMPDIR": f"{state}/tmp",
        }
        assignments = " ".join(
            shlex.quote(f"{key}={value}") for key, value in env.items()
        )
        key_source = self.agent._key_source_name
        scrub_key = f"-u {shlex.quote(key_source)} " if key_source else ""
        return (
            f"env -u BASH_ENV -u ENV {scrub_key}{assignments} setsid -w bash -c {shlex.quote(script)} "
            f"< /dev/null > {state}/stdout.json 2> {state}/stderr.log"
        )

    async def stop(self) -> None:
        """Stop the process group belonging to the cancelled attempt."""
        state = self.state
        await self.environment.exec(
            command=f"""
touch {state}/cancel || exit 0
test -s {state}/pid || exit 0
pid=$(cat {state}/pid)
case "$pid" in ''|*[!0-9]*) exit 1 ;; esac
test "$pid" -gt 1 || exit 1
kill -TERM "-$pid" 2>/dev/null || exit 0
sleep 2
kill -KILL "-$pid" 2>/dev/null || true
""",
            timeout_sec=5,
        )

    async def collect(self) -> HintAnswer:
        """Download and redact each independent source of trial evidence.

        :returns: Parsed stdout fields, if available.
        """
        state = self.state
        context = self.context
        answer = HintAnswer()
        with TemporaryDirectory(prefix="hint-harbor-output-") as tmp:
            raw = Path(tmp)
            for name in ("stdout.json", "stderr.log"):
                try:
                    exists = await self.environment.exec(
                        command=f"test -f {state}/{name}"
                    )
                    if exists.return_code != 0:
                        self.collection_errors[name] = "missing"
                        continue
                    local = raw / name
                    await self.environment.download_file(
                        source_path=f"{state}/{name}", target_path=local
                    )
                    if local.is_symlink():
                        raise ValueError("Unexpected symlink in Hint logs")
                    data = redact(
                        value=local.read_text(encoding="utf-8", errors="replace"),
                        secret=self.agent._secret,
                    )
                    (self.agent.logs_dir / name).write_text(data, encoding="utf-8")
                    if name == "stdout.json" and data.strip():
                        answer = HintAnswer.model_validate_json(data)
                        self.answer = answer
                    elif name == "stdout.json":
                        self.collection_errors[name] = "empty"
                except Exception as error:
                    self.collection_errors[name] = type(error).__name__
                    self.agent.logger.warning(
                        "Could not collect Hint %s (%s)", name, type(error).__name__
                    )
            usage = answer.usage or Usage()
            context.n_input_tokens = usage.input_tokens
            context.n_output_tokens = usage.output_tokens
            context.n_cache_tokens = usage.cached_input_tokens
            stage = "session"
            try:
                remote = f"{state}/data/hint/sessions"
                if (
                    await self.environment.exec(command=f"test -d {remote}")
                ).return_code == 0:
                    sessions = raw / "sessions"
                    await self.environment.download_dir(
                        source_dir=remote, target_dir=sessions
                    )
                    if sessions.is_symlink():
                        raise ValueError("Unexpected symlink in Hint sessions")
                    for path in sessions.glob("*/*.jsonl"):
                        if (
                            path.parent.is_symlink()
                            or path.is_symlink()
                            or not path.is_file()
                        ):
                            continue
                        clean = self.agent.logs_dir / "session.jsonl"
                        clean.write_text(
                            redact(
                                value=path.read_text(encoding="utf-8"),
                                secret=self.agent._secret,
                            ),
                            encoding="utf-8",
                        )
                        records = read_journal(path=clean)
                        if records:
                            stage = "trajectory"
                            trajectory = convert(
                                records=records,
                                model=self.agent.model,
                                version=HINT_VERSION,
                                context=context,
                            )
                            output = trajectory.model_dump_json(
                                exclude_none=True, indent=2
                            )
                            type(trajectory).model_validate_json(output)
                            (self.agent.logs_dir / "trajectory.json").write_text(
                                output, encoding="utf-8"
                            )
                        break
                else:
                    self.collection_errors["session"] = "missing"
                if not (self.agent.logs_dir / "trajectory.json").is_file():
                    self.collection_errors.setdefault("trajectory", "missing")
            except Exception as error:
                self.collection_errors[stage] = type(error).__name__
                self.agent.logger.warning(
                    "Could not collect Hint session or trajectory (%s)",
                    type(error).__name__,
                )
        return answer
