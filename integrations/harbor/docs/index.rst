Hint Harbor integration
=======================

The adapter invokes the production Hint CLI and leaves scoring to Harbor's
verifier. Each attempt gets fresh writable state. The session journal captures
the recorded conversation and tool calls. The transient system preamble is
constructed by the pinned Hint binary from the task context.

Adapter API
-----------

.. automodule:: hint_harbor.agent
   :members: HintAgent, HintOptions, HintAnswer, Usage, AgentError, HintBinaries, BinaryIdentity, SelectedBinary, HintTrial, EffectiveConfig, ExecutionRecord, CancelledError, select_binary, redact
   :exclude-members: model_config

.. autoclass:: hint_harbor.agent.HintAgent
   :no-index:
   :special-members: __init__

Trace conversion
----------------

.. automodule:: hint_harbor.trace
   :members: EmptyTrajectoryError, JournalRecord, JournalMessage, JournalCall, JournalPart, convert, read_journal
   :exclude-members: model_config
