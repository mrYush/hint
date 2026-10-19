# Hint - Context-aware assistant for developers

[![CI](https://github.com/mrYush/hint/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/mrYush/hint/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mrYush/hint?include_prereleases)](https://github.com/mrYush/hint/releases)

`hint` is a CLI agent for the directory you run it in. It reads your
project, answers questions about it, and — with your confirmation — edits
files and runs commands. It talks to any OpenAI-compatible API and can fall
back to a local [Ollama](https://github.com/ollama/ollama).

## Features

- A full-screen interactive session by default (a line REPL with
  `--plain`); a one-shot answer with `-p`, plain text or JSON, for scripts
- Tools the agent uses itself: read, list, glob, grep, edit and write files,
  run shell commands — every edit shown as a diff and every command
  confirmed before it runs
- Automatic context: an overview of the directory (respecting `.gitignore`)
  and your own instructions from a `HINT.md`, global or per project
- Conversations recorded as sessions you can continue, list, pick up by id
  and fork at any turn into a branch of their own
- Provider profiles with a default/fallback pair, configured from the
  command line, environment variables or YAML files, and switchable
  mid-conversation with `/profile`
- One core, several shells: `hint serve` runs the agent as a local
  JSON-RPC service that other terminals join with `hint --connect`, and
  `hint --mode rpc` speaks the same protocol on stdin/stdout for scripts
  and editors

## Installation

Every release ships prebuilt binaries for Linux, macOS and Windows on
amd64 and arm64 (a Raspberry Pi 4/5 runs the `linux_arm64` build) on the
[Releases page](https://github.com/mrYush/hint/releases). `hint --version`
tells which build you have.

`hint` uses [ripgrep](https://github.com/BurntSushi/ripgrep) for its `grep`
and `glob` tools when it is installed and falls back to a pure-Go search
otherwise: recommended, not required. To answer anything it needs an
OpenAI-compatible endpoint or a local Ollama; see
[Configuration](#configuration).

### apt (Ubuntu, Debian, Linux Mint, Pop!_OS, Raspberry Pi OS)

```bash
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://mryush.github.io/hint/hint.gpg | sudo tee /etc/apt/keyrings/hint.gpg >/dev/null
echo "deb [signed-by=/etc/apt/keyrings/hint.gpg] https://mryush.github.io/hint stable main" \
  | sudo tee /etc/apt/sources.list.d/hint.list
sudo apt update
sudo apt install hint
```

One `stable` suite serves every release of every apt-based distribution
on amd64 and arm64: the binary is static and does not depend on the
distribution's libraries. The package depends on `ripgrep`, so apt installs
it too. Updates arrive with `sudo apt update && sudo apt upgrade`.
The `Release` file is signed; `hint.gpg` is the public half of the key
and `signed-by` limits it to this one repository. Pre-releases are not
published to the repository. A single `.deb` is also attached to each
[release](https://github.com/mrYush/hint/releases) for `dpkg -i`.

### Homebrew (macOS/Linux)

```bash
brew install --cask mrYush/tap/hint
```

The tap is `mrYush/tap` (the repository `mrYush/homebrew-tap`); the cask
installs the release binary and brings ripgrep with it. Pre-releases are
not published to the tap.

### Release archive

Download the archive for your platform, check it against `checksums.txt`
from the same release, and put the binary on your `PATH`:

```bash
tar xzf hint_<version>_linux_arm64.tar.gz
sudo install hint /usr/local/bin/
hint --version
```

On Windows unpack the `.zip` and put `hint.exe` somewhere on `%PATH%`.

### go install

```bash
go install github.com/mrYush/hint/cmd/hint@latest
```

`hint --version` then reports the module version the toolchain recorded.

### Building from source

You need Go 1.26 or higher.

```bash
git clone https://github.com/mrYush/hint.git
cd hint
go build -o hint ./cmd/hint
./hint --version        # dev (commit <hash>, built <commit time>)
```

Then copy the binary somewhere on your `PATH` (`sudo cp hint /usr/local/bin/`
on Linux and macOS). `go build ./...`, `go test ./...` and `go vet ./...`
are the checks CI runs; [CONTRIBUTING.md](CONTRIBUTING.md) has the rest.

## Usage

### Interactive session

Run `hint` with no arguments in your project. On a terminal it opens the
full-screen shell: the conversation scrolls above the input with answers
rendered as Markdown, a file edit or a command asks for permission in a
dialog that shows the diff or the command (`y` / `n` / `a`), and the
header names the session and the provider profile in use. Enter sends; a
newline is Shift+Enter where the terminal reports it (kitty, WezTerm,
Ghostty, iTerm2), Alt+Enter or Ctrl+J, and on any terminal a `\` at the
end of the line before Enter. Ctrl+C while an answer is being written
stops that answer; Ctrl+D or `/exit` leaves, and the session is kept.

The conversation scrolls with ↑ on the input's first line and ↓ on its
last (a mouse wheel works the same way), Shift+↑/↓ by line, PgUp/PgDn by
page and Ctrl+Home/End to its ends; the header says how many lines are
below. Esc in an empty input (or Ctrl+↑) picks a message: ↑/↓ move
between messages, `y` copies the one picked, `f` forks the conversation
after that message's turn (see [Sessions](#sessions)), Esc returns to the
input. One-letter answers and commands (`y`/`n`/`a`, `j`/`k`) work in a
Cyrillic layout too, in the TUI and at the line prompts: `н` is `y`.

Tab completes `/commands` and their arguments; `/help` lists them:
`/sessions` shows this directory's sessions as a tree and resumes one,
`/profile` switches the provider profile, `/completion` shows or sets
when a model continues what you type (see [Completion of what you
type](#completion-of-what-you-type)), `/setup` and `/config` edit the
configuration, `/copy` and `ctrl+y` copy the last answer (or `question`,
`code`, `all`), `/mouse` switches to a mode where the wheel scrolls and a
click copies the block under it (off by default, so the terminal's own
text selection works).

`hint --plain` runs the same session as a line REPL, which is also what a
terminal that cannot draw the shell gets:

```
$ hint --plain
hint: interactive session; /help for commands, Ctrl-D or /exit to quit

> what does main.go do?
...
> /profile local
hint: answering with local/qwen2.5:7b from now on
> and where is it tested?
...
> /exit
```

Each line is one question; `/help`, `/profile` and `/exit` are its
commands. Both shells are clients of the same protocol as `hint serve`.

### One question

```bash
hint -p "How do I run this project?"
hint -p "Explain the directory structure of this project"
hint -p "What does the README.md file contain?"
```

`hint -p` prints the answer and exits, with the tool activity on stderr so
stdout stays the answer. For a script, `--output json` prints one object
instead:

```bash
hint -p "list the tests" --output json
# {"answer":"...","finish_reason":"stop","session_id":"3f2a...","usage":{...}}
```

A failed run still prints the object (with an `error` field) and exits with
status 1.

The older form `hint "question"` keeps working as a one-shot and prints a
note pointing at `-p`.

### Run modes

`hint` can edit files in the working directory and run shell commands.
What it may do without asking is a per-run choice:

```bash
hint -p "add a --version flag"            # --ask (default): every edit shows a
                                          # diff, every command is shown, and
                                          # you answer y / n / a(lways)
hint --auto-edit -p "fix the failing test"   # edits apply silently, commands still ask
hint --yolo -p "..."                         # nothing asks; prints a loud warning
```

Answering `a` to a command remembers its program and subcommand for the
rest of the run: `go test` covers `go test ./...` but not `go build`, and
it is offered only for programs with subcommands (`go`, `git`, `npm`,
`cargo`, `docker`, …); anything else, and every edit, is confirmed each
time. File tools never leave the working directory in any mode; `bash` is
not confined, which is why `--auto-edit` keeps asking for it. Without a
terminal on stdin (a script, `< /dev/null`) anything that needs an answer
is denied.

### One core, several shells

The agent runs as a core that every shell talks to over newline-delimited
JSON-RPC 2.0, even inside one process. `hint serve` runs that core on its
own, so a second terminal can join a conversation, or an editor can drive
it:

```bash
hint serve                       # the core on a Unix socket under $XDG_RUNTIME_DIR/hint
hint --connect -c                # another terminal: continue the same session on it
hint --connect unix:/path/hint.sock   # or name the socket (tcp:127.0.0.1:port on Windows)
hint --mode rpc                  # the same protocol on stdin/stdout, for a script or an editor
```

`hint serve` binds to the working directory it was started in; the tools
never leave it. Its socket is private to the user and guarded by a token
it writes next to the socket, which `--connect` reads. The protocol — the
handshake, sessions, turns and their events, permission questions, profile
switches and forks — is in [docs/rpc.md](docs/rpc.md).

### Sessions

Every run is recorded, so a later run can pick the conversation up:

```bash
hint -c                          # continue the most recent session of this directory
hint -r                          # list this directory's sessions and ask which one
hint --session 3f2a              # continue the session whose id starts with 3f2a
hint sessions                    # list this directory's sessions
hint --no-session -p "..."       # record nothing
```

The session flags work for both the interactive session and `-p`.

A session can branch. In the TUI, `f` on a picked message forks the
conversation after that message's turn, and `f` on a row of `/sessions`
forks the whole session: a new session starts with the copied history,
the shell moves to it, and the original is left as it was. Forks are
listed under the session they came from, in the TUI as a tree and in
`hint sessions` with a `forked from <id> after turn N` note. Over RPC the
same is `session.fork` ([docs/rpc.md](docs/rpc.md)).

Sessions are one JSONL file each under
`~/.local/share/hint/sessions/<project>/` (`$XDG_DATA_HOME` is respected),
one per working directory; delete a file to forget the conversation. The
system prompt is not stored — each run builds it afresh — and neither are
`a(lways)` answers, so a continued session asks again before running
commands. The provider profile a session was switched to is recorded, so
`-c` continues with it. When the conversation outgrows the model's
context window it is summarized, and the summary is what a later run
continues from.

### Profiles

A session answers with the default profile until told otherwise; the
switch takes effect for the next question and stays with the session:

```
> /profile                       # the profiles the configuration has, the one in use marked
> /profile local                 # answer with the local profile from now on
> /profile vllm Qwen/Qwen2.5-Coder-7B-Instruct   # that profile with another model it serves
```

In the TUI `/profile` alone opens a list to pick from, Tab completes the
names, and the header shows what is in use. Tab after a profile name
completes the model from what that profile's endpoint serves: the first
Tab on it asks the core (`hint models` does the same on the command
line), says so in the status line, and the list is kept for the rest of
the session; the next Tab offers the names. The same list completes the
value of `/config set providers.<profile>.model`. An endpoint that does
not answer the models request leaves the model a free value, with the
reason in the status line. A profile that cannot be used
(an openai profile without a key, an ollama profile without a model) is
listed with the reason and refused. The core re-reads the configuration
files on every switch, which is how `hint setup` and `/setup` take effect
without a restart. Switching `fallback_provider` off is not part of it:
the configured fallback stays behind whichever profile is in use, unless
it is the same one.

### Completion of what you type

The TUI can ask a model to continue the question you are typing. The
continuation is shown in the status line and Tab takes it; any other key
drops it. It is off by default, because every request sends the text
typed so far and the last question of the conversation to a provider:
with a cloud profile that text leaves the machine on every request.
`completion.mode` picks when the shell asks:

| mode | when a request goes out |
|---|---|
| `off` | never (the default) |
| `tab` | only when you press Tab on a question; Tab with a suggestion on offer accepts it instead |
| `idle` | after a pause in typing (`completion.idle_after`, 600ms), once the question is 12 characters or longer, at most once every two seconds and only after three more characters were typed |
| `both` | either of the two |

`tab` costs a request per Tab and nothing else, which is why it is the
mode to start with. `idle` is the one that spends tokens without a key
press; `/completion` in the TUI shows how many requests and tokens the
session has spent so far, and `/completion off|tab|idle|both` changes the
mode for the session without touching the config. Three failed requests
in a row switch completion off for the session, with a notice.

By default the session's own profile and model answer, so nothing extra
runs anywhere. A separate small model is optional:

```yaml
completion:
  mode: tab
  profile: local        # a profile from providers:, e.g. Ollama with a 1-3B model
  model: qwen2.5:1.5b   # optional: a model of that profile for completions only
  warn_after: 2s        # after this long the status line says the model is slow
```

Think twice about a second model on a small machine: Ollama keeps models
loaded, so a completion model beside the main one on a Raspberry Pi or
a laptop with little RAM can evict the main model and make every answer
start with a reload. On such a machine keep `profile` empty, or keep
completion off. A profile named here that does not exist, or cannot be
used, switches completion off with a warning at start rather than
stopping the run. Completions are never written to the session file.

### Configuration setup

`hint setup` asks for the profile, base URL, model and the name of the
environment variable that holds the API key, and writes them to
`~/.config/hint/config.yaml` (mode 0600, comments kept). When a provider
rejects the key, `hint` says which config files it is using and offers
the same questions (in the TUI: `/setup`, with the key masked). A project
`.hint/config.yaml` is never written, so a key does not end up in git.
A running session switches to the saved profile at once, so the next
question goes out with the new key; `hint setup` on its own is picked up
by the next start.

`hint config` changes one key at a time without an editor; every value
is checked against the key's type before anything is written, and the
file keeps its comments:

```bash
hint config                                     # what the next start uses, keys masked
hint config keys                                # every key, default, HINT_* variable, flag
hint config set overview.depth 3                # the global file
hint config set --project providers.vllm.model Qwen/Qwen2.5-Coder-7B-Instruct   # this repository only
hint config unset --project overview.depth      # back to the global value
```

In the TUI the same is `/config show | keys | set | unset`, with Tab
completing the subcommand, the key and, where there is a closed set, the
value; `/setup` edits every field of a provider profile in one form. An
API key is set through `/setup` or `hint setup` only, where it is masked,
and never as a literal into the project file.

### Models

```bash
hint models                      # what the default profile serves
hint --provider local models     # what another profile serves
```

For an Ollama profile the list shows the installed models with their sizes;
for any other profile it is the endpoint's `GET /models`.

## Project instructions

Put a `HINT.md` in your project to tell the assistant how to work there —
conventions, commands to run, things to avoid. `AGENTS.md` and `CLAUDE.md`
are read as compatible names, so a project that already keeps one for
another tool needs nothing new. `hint` reads the first of the three it
finds in each directory from the repository root down to the directory you
run it in, so a nearer file can refine the outer one.

Your own standing instructions go in `~/.config/hint/HINT.md`
(`$XDG_CONFIG_HOME` is respected); it is read before any project file.

All instruction files together are limited to 32 KiB by default, or a
sixteenth of the model's context window when the profile states one
(`context_window`), so a small local model is never handed more preamble
than it can read. When the files do not fit, nothing is dropped blindly:

- every file is guaranteed room for its outline, so a `HINT.md` next to
  you is never skipped because a larger one above spent the budget;
- a Markdown file over its share shows whole sections while they fit and
  the rest as heading and first sentence, marked `[...]`; a file without
  headings is cut at a line;
- the model has an `instructions` tool that returns any of these files
  in full or one section by heading, and answers "where does this rule
  come from" with a `path:line` — it reads the run's instruction files
  and nothing else, so a parent directory's `HINT.md` is reachable and
  `../.env` is not;
- as a last resort, and only if you say so (`instructions.summarize:
  true` or `--summarize-instructions`), a file that does not fit even as
  an outline is summarized by the model, marked `summary="true"` in the
  prompt, and the summary is cached under `~/.cache/hint/instructions/`
  (`$XDG_CACHE_HOME` is respected) until the file changes.

A warning on stderr names every file shown as less than itself.

A project that outgrows one file can split it. `HINT.md` stays a short
index and rule files under `.hint/rules/*.md` hold the detail:

```markdown
---
title: Go style
paths:
  - "**/*.go"
---
Keep errors last in a signature; run gofmt before every commit.
```

A rule with `paths:` is loaded only once a tool of the conversation reads
or writes a matching file; until then the prompt lists it by title, and
the model can read it early with the `instructions` tool. A rule without
`paths:` is loaded always, right after its directory's `HINT.md`. Patterns
use the syntax of the `glob` tool (`**`, `{a,b}`; a pattern without a
slash matches a file name at any depth), relative to the directory that
holds `.hint`. `.cursor/rules/*.mdc` files are read the same way
(`globs:`, `description:` and `alwaysApply:` are understood), so a team
that already keeps Cursor rules need not maintain a second tree. Rules
loaded this way stay loaded for the run, and a continued session (`-c`)
loads on its first request whatever the earlier run had loaded.

The limit, and the directory overview that goes into the same prompt, can
be changed:

```bash
hint --instruction-budget 65536 ...   # bytes for all instruction files (not scaled to the window)
hint --overview-depth 3 ...           # directory levels listed; 0 lists nothing
hint --overview-entries 200 ...       # cap on listed entries
hint --summarize-instructions ...     # allow model-written summaries of oversized files
```

or, in a config file:

```yaml
instructions:
  budget: 65536
  summarize: true
overview:
  depth: 3
  max_entries: 200
```

## Configuration

Configuration is a set of named **provider profiles** plus a default/fallback
pair. Sources, highest priority first:

1. CLI flags (`--provider`, `--api-url`, `--api-key`, `--model`, `--context-window`)
2. Environment variables (`HINT_*`)
3. Project file `./.hint/config.yaml`
4. Global file `~/.config/hint/config.yaml` (respects `$XDG_CONFIG_HOME`)

### First run

With no configuration at all, `hint` on a terminal says so and asks
whether to set it up now: the profile name and base URL (each has a
default, Enter accepts it), the model (required: a server serves what was
loaded into it, `hint models` lists them) and the API key, typed with the
echo off.
The answers go to `~/.config/hint/config.yaml` (mode 0600) and the start
continues. `hint setup` asks the same questions at any time and edits the
file in place, keeping its comments. In a script or a pipe nothing is
asked: the error names `hint setup` and the other ways below.

[`config.example.yaml`](config.example.yaml) in the repository root lists
**every key with its default and what it does**; copy it to the global
path for settings that apply everywhere, or to `.hint/config.yaml` in a
repository for that project only. A test keeps it in step with the code:
it must load, and it must mention every key the loader reads.

### Quick start

`hint` speaks the OpenAI Chat Completions dialect, so any server or
service that serves it is a provider: your own model behind
[vLLM](https://docs.vllm.ai) (or llama-server, LM Studio, TGI), a local
[Ollama](https://github.com/ollama/ollama), OpenAI itself, or an
aggregator such as [OpenRouter](https://openrouter.ai) or
[api-bar.ru](https://api-bar.ru). The quickest start is `hint setup`,
which asks for the base URL, the model and the key and writes the file
below. For a vLLM server on your network that is:

```bash
hint setup
# profile name [vllm]:
# base URL: http://inference.example.net:8000/v1
# model: Qwen/Qwen2.5-Coder-32B-Instruct
# API key, input hidden: ...        # whatever vLLM was started with (--api-key), or empty
hint -p "How do I run this project?"
```

For a local [Ollama](https://github.com/ollama/ollama) the file is
shorter still — `kind: ollama` needs no key — and it is the usual
fallback behind a bigger model, so the agent keeps working offline:

```yaml
providers:
  - name: local
    kind: ollama
    model: qwen2.5:7b                 # ollama pull qwen2.5:7b; hint models lists what is installed
```

Without any file, two environment variables still work as a zero-config
path: `OPENAI_API_KEY` selects `https://api.openai.com/v1`, and
`APIBAR_KEY` the api-bar.ru aggregator (`API_BAR_KEY`, the earlier
spelling, still works with a warning). An `openai` profile that names no
`base_url` is taken to be a server on this machine at
`http://localhost:8000/v1`, where vLLM listens by default. Every profile
names its model: there is no default, because a server serves whatever
was loaded into it.

### Configuration file

```yaml
providers:
  - name: vllm
    kind: openai                                # OpenAI Chat Completions dialect
    base_url: http://inference.example.net:8000/v1   # hint appends /chat/completions
    api_key: ${VLLM_API_KEY}                    # $VAR / ${VAR} references are expanded; a vLLM
                                                # started without a key accepts any value here
    model: Qwen/Qwen2.5-Coder-32B-Instruct      # a model the server serves (hint models lists them)
    context_window: 32768                       # tokens; sizes the compaction (optional)
  - name: local
    kind: ollama                                # same dialect, no key required
    base_url: http://localhost:11434/v1
    model: qwen2.5:7b
    context_window: 8192
    header_timeout: 15m                         # wait for response headers (default 60s)
  - name: cloud                                 # an aggregator works the same way:
    kind: openai                                # OpenRouter, api-bar.ru, OpenAI itself
    base_url: https://openrouter.ai/api/v1      # or https://api-bar.ru/route/openai
    api_key: ${OPENROUTER_API_KEY}
    model: anthropic/claude-sonnet-4
    context_window: 200000
default_provider: vllm
fallback_provider: local
```

The project file overlays the global one profile-by-profile (matched by
`name`, non-empty fields win), so a project can override just the model or
key of a globally defined profile. A key the more local file does not
mention is inherited from the more general one. Do not put a literal API
key in the project file, it ends up in git: use `${VAR}` there.

`header_timeout` bounds how long hint waits for the response headers of a
chat request; a Go duration such as `90s` or `15m`, units required.
A local server like `llama-server` sends them only after it has processed
the whole prompt, which on a Raspberry Pi takes minutes, so raise it for
such profiles.

### Environment variables

```bash
export HINT_PROVIDER=<profile_name>    # select the default profile
export HINT_FALLBACK_PROVIDER=<name>
export HINT_API_KEY=<key>              # these four override the SELECTED
export HINT_API_URL=<base_url>         # profile only; the fallback keeps
export HINT_MODEL=<model>              # its own values
export HINT_CONTEXT_WINDOW=<tokens>
export HINT_INSTRUCTION_BUDGET=<bytes> # the limits of the project context
export HINT_OVERVIEW_DEPTH=<levels>
export HINT_OVERVIEW_ENTRIES=<count>
export HINT_DEBUG=true                 # same as --debug
```

### CLI flags

```bash
hint --provider=local -p "question"                 # pick a profile for one run
hint --api-key=<key> --model=gpt-4o-mini -p "..."   # override the selected profile
```

### Migrating from the flat config

The pre-profiles format (`api_url`/`api_key`/`model` at the top level of
`~/.config/hint.yaml` or `./hint.yaml`) is still read: the old paths work
with a deprecation warning, and the flat keys are mapped onto a single
profile named `default`. Move to the `providers` list at the new paths at
your convenience.

## How it works

1. `hint` captures the working directory, reads the global and the
   project's instruction files, and lists the top levels of the directory,
   skipping hidden entries, dependency directories and whatever
   `.gitignore` excludes (inside a git repository, with git installed)
2. That context and the conversation so far go to the configured model
3. The model answers, or asks for tools — reading files, searching, editing,
   running commands — which `hint` runs (after your confirmation where one
   is needed) and feeds back, until the answer is complete
4. The answer streams to your terminal, and the exchange is appended to the
   session

## Troubleshooting

### Common issues

1. **Error "needs an API key"**
   - Make sure you've set up your API key through one of the configuration methods
   - Check if the environment variable is properly set

2. **Error connecting to the API**
   - Verify your internet connection
   - Check if the API endpoint is correct and accessible
   - `hint models` is a quick way to see whether the endpoint answers

3. **Empty or irrelevant responses**
   - Try rephrasing your question to be more specific
   - Check if you're in the correct directory related to your question

### Known issues

`v0.2.0-alpha` and `v0.2.0` ship with these confirmed bugs. They are
backlog (decided 2026-10-11) and do not block either tag. Details and the
full audit:
[`docs/plan/review-2026-09-28-go-practices.md`](docs/plan/review-2026-09-28-go-practices.md).

1. **Compaction loses the summary.** When a long conversation is
   compacted, the summary is dropped on the next request, so the model
   continues with the system prompt and the current exchange only. Keep
   conversations short or start a new session when the compaction notice
   appears.
2. **Crash after a compaction that follows a context-overflow error**
   (`slice bounds out of range`) when the profile sets `context_window`
   (or `HINT_CONTEXT_WINDOW` / `--context-window`). Leave it unset to
   avoid the path.
3. **A session file can become unreadable** if `hint` is interrupted at
   the exact end of a write; the next `hint -c` then fails to open it.
   Start a new session; the old file can be repaired by adding a newline
   after the last complete line.
4. `grep` on a single file with more than 200 matches reports an error
   instead of a truncated list; search a directory or narrow the pattern.
5. `write_file` on a file `hint` cannot read shows the overwrite prompt
   as "too large to diff" instead of the read error.
6. Not in `v0.2.0`, on `develop` since WP1.4: a gateway that answers a
   failed request with both a short `error` and a longer `message` (the
   NestJS shape, `{"error":"Unauthorized","message":"Invalid API key"}`)
   is reported by the `error` field alone. `hint --debug` shows the full
   response body.

### Debug mode

`--debug` (or `HINT_DEBUG=true`) writes a trace of the run — the requests
sent, the responses, the tools called — to a file under
`~/.local/state/hint/log/` (`$XDG_STATE_HOME` is respected), one file per
run; the path is printed at the start. API keys are masked in it. Attach
the file when reporting a problem.

```bash
hint --debug -p "Your question"
```

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for the
development setup, conventions, and how work is planned. The project roadmap
lives in [PLAN.md](PLAN.md).

## License

MIT
