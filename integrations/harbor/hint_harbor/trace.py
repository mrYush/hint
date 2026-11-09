"""Convert the pinned Hint journal format into Harbor ATIF."""

from collections.abc import Sequence
from pathlib import Path
from typing import Literal

from harbor.models.agent.context import AgentContext
from harbor.models.trajectories import (
    Agent,
    FinalMetrics,
    Observation,
    ObservationResult,
    Step,
    ToolCall,
    Trajectory,
)
from pydantic import BaseModel, Field, ValidationError


class JournalPart(BaseModel):
    """Text or reasoning component of a Hint message."""

    kind: str
    text: str = ""


class JournalCall(BaseModel):
    """Native tool invocation in the session journal."""

    id: str
    name: str
    arguments: dict[str, object] | None = None


class JournalMessage(BaseModel):
    """Native message from the production Hint CLI."""

    role: Literal["user", "system", "assistant", "tool"]
    content: list[JournalPart] = Field(default_factory=list)
    tool_calls: list[JournalCall] = Field(default_factory=list)
    tool_call_id: str | None = None


class JournalRecord(BaseModel):
    """A validated line of the Hint session journal."""

    kind: str
    id: str | None = None
    at: str | None = None
    message: JournalMessage | None = None


def convert(
    *, records: Sequence[JournalRecord], model: str, version: str, context: AgentContext
) -> Trajectory:
    """Build a Harbor trajectory from Hint's recorded conversation.

    :param records: Native session lines in chronological order.
    :param model: Exact inference model ID.
    :param version: Pinned adapter-facing Hint version.
    :param context: Provider-reported aggregate usage.
    :returns: Validated ATIF trajectory.
    :raises ValueError: If the journal is missing its session ID.
    """
    if not records or not records[0].id:
        raise ValueError("Hint journal is missing a session ID")
    steps: list[Step] = []
    call_steps: dict[str, Step] = {}
    for record in records:
        if record.kind == "compaction":
            continue  # The native journal retains checkpoints and their original history.
        if record.kind != "message" or record.message is None:
            continue
        message = record.message
        role = message.role
        parts = message.content
        body = "".join(part.text for part in parts if part.kind == "text")
        reasoning = "".join(part.text for part in parts if part.kind == "thinking")
        if role == "tool":
            call_id = message.tool_call_id
            step = call_steps.pop(call_id, None) if call_id else None
            if step is not None:
                if step.observation is None:
                    step.observation = Observation(results=[])
                step.observation.results.append(
                    ObservationResult(source_call_id=call_id, content=body)
                )
            continue
        calls = [
            ToolCall(
                tool_call_id=call.id,
                function_name=call.name,
                arguments=call.arguments or {},
            )
            for call in message.tool_calls
        ]
        step = Step(
            step_id=len(steps) + 1,
            timestamp=record.at,
            source="agent" if role == "assistant" else role,
            message=body,
            tool_calls=calls or None,
            reasoning_content=reasoning or None,
        )
        steps.append(step)
        call_steps.update((call.tool_call_id, step) for call in calls)
    return Trajectory(
        schema_version="ATIF-v1.7",
        session_id=records[0].id,
        agent=Agent(name="hint", version=version, model_name=model),
        steps=steps,
        final_metrics=FinalMetrics(
            total_steps=len(steps),
            total_prompt_tokens=context.n_input_tokens,
            total_completion_tokens=context.n_output_tokens,
            total_cached_tokens=context.n_cache_tokens,
        ),
    )


def read_journal(*, path: Path) -> list[JournalRecord]:
    """Read complete journal lines and tolerate a torn final write.

    :param path: Local redacted Hint journal.
    :returns: Validated records.
    :raises ValidationError: If an interior record is malformed.
    """
    lines = path.read_text(encoding="utf-8", errors="replace").split("\n")
    records = []
    for index, line in enumerate(lines):
        if not line:
            continue
        try:
            records.append(JournalRecord.model_validate_json(line))
        except ValidationError:
            if any(lines[index + 1 :]):
                raise
            # A process killed during a write can leave one torn tail record.
    return records
