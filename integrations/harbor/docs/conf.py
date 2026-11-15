"""Sphinx configuration for the adapter API."""

project = "Hint Harbor"
extensions = ["sphinx.ext.autodoc"]
autodoc_typehints = "description"
nitpicky = True
exclude_patterns = ["_build"]

# Harbor does not ship Sphinx cross-reference targets.
nitpick_ignore_regex = [
    ("py:class", r"harbor\..*"),
    ("py:class", r"pydantic\..*"),
]

nitpick_ignore = [
    ("py:class", "pathlib.Path"),
    ("py:class", "collections.abc.Mapping"),
    ("py:class", "collections.abc.Sequence"),
    ("py:class", "ConfigDict"),
    ("py:class", "Route"),
    ("py:class", "Reasoning"),
    ("py:class", "Architecture"),
    ("py:class", "AgentError"),
    ("py:class", "Usage"),
    ("py:exc", "AgentAuthenticationError"),
    ("py:exc", "NonZeroAgentExitCodeError"),
    ("py:exc", "ValidationError"),
]
