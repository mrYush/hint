# Harbor integration instructions

These rules apply within `integrations/harbor/` alongside the [repository rules](../../AGENTS.md).
Keep the package compatible with Python 3.12 and use the Harbor release pinned in [pyproject.toml](pyproject.toml).

## Python style and framework boundaries

- Use Ruff for formatting and linting and Pyright for type checking.
- Annotate function parameters and return values, including tests, and give structured data explicit field types.
- Write English reStructuredText docstrings for public modules, classes and functions.
  Describe meaningful parameters, return values and failure conditions, and keep the Sphinx API references current.
- Use keyword-only parameters for adapter-owned functions that take named inputs, and pass those inputs by name at call sites.
- Preserve signatures required by Harbor and other frameworks.
  In particular, Harbor 0.25.0 calls `install(self, environment)` and `run(self, instruction, environment, context)` positionally.
  Keep those overrides positional-compatible and mark them with `@override`.
  Use named arguments inside the adapter where its own signatures allow them.

## Choosing data shapes

- Use a typed `tuple` for a small positional value whose order is conventional or prescribed by an external API.
  If readers need names to distinguish the elements, use named fields.
- Use `TypedDict` when the value should stay a dictionary with known keys, as with an internally assembled JSON summary.
  Validate untrusted JSON or YAML at the boundary with a Pydantic model when runtime validation is needed.
- Use a `dataclass` for internal values or state with named attributes, such as a selected binary, parsed run options or per-attempt state.
  Group parameters when they represent one value or share a lifecycle.
  Use `frozen=True` for immutable values and keep state mutable where the lifecycle requires it.

## Checks

From this directory, use the environment described in the [development instructions](README.md#development).
Run `ruff format --check .`, `ruff check .`, `pyright`, `python -m unittest discover -s tests -v` and `sphinx-build -W -b html docs docs/_build` for Python changes.
For changes to trial execution, use the documented local Docker smoke with the fake provider.
