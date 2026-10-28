# Security

## Reporting a vulnerability

If you believe you have found a security vulnerability in `hint`, please
do **not** open a public issue. Contact the maintainer privately (see the
repository owner's GitHub profile) and allow time for a fix before any
disclosure. You will get an acknowledgement, and the fix ships as a patch
of the current release.

Only the latest final release (the newest `vX.Y.Z` tag) receives security
fixes; `v0.1.0` is an earlier, unrelated application and is retracted in
`go.mod`.

## What the agent can do on your machine

`hint` edits files in the working directory and runs shell commands. How
much of that happens without asking is a per-run choice (README, "Run
modes"):

- `--ask` (default): every edit shows a diff and every command is shown
  before it runs; you answer `y`, `n` or `a` (remember this program and
  subcommand for the rest of the run).
- `--auto-edit`: edits inside the working directory apply without
  confirmation; commands still ask.
- `--yolo`: nothing asks. Every edit and every command the model proposes
  runs as you, with your environment and your permissions.

The file tools never leave the working directory in any mode and refuse
hint's own config and session files. `bash` is **not** sandboxed: an
approved command runs as your user and can read anything you can, hint's
config file included. Keys live in the OS secret store when `api_key:
keyring:<profile>` is used, tool subprocesses do not inherit the key
variables, and every tool result is scrubbed against the configured keys
— but a shell command you approve can still find them. Treat `--yolo`
as handing the model your shell.

## Rules for contributions

Two rules from the architecture apply to every change:

- **External input is untrusted.** Output of tools, MCP servers and files
  read from disk must never be treated as instructions to the agent or
  interpolated into privileged operations.
- **The permission system is not optional.** Any new tool must declare an
  action class (`read` / `write` / `execute`) and go through the
  permission layer; file tools stay confined to the working directory by
  default.
