Hint Harbor integration
=======================

The adapter invokes the production Hint CLI and leaves scoring to Harbor's
verifier. Each attempt gets fresh writable state. The session journal captures
the recorded conversation and tool calls. The transient system preamble is
constructed by the pinned Hint binary from the task context.

.. toctree::
   :maxdepth: 2

   api
