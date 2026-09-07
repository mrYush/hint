# Hint - Context-aware assistant for developers

[![CI](https://github.com/mrYush/hint/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/mrYush/hint/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/mrYush/hint?include_prereleases)](https://github.com/mrYush/hint/releases)

`hint` is a CLI agent for the directory you run it in. It reads your
project, answers questions about it, and — with your confirmation — edits
files and runs commands. It talks to any OpenAI-compatible API and can fall
back to a local [Ollama](https://github.com/ollama/ollama).

## Features

- An interactive session by default; a one-shot answer with `-p`, plain
  text or JSON, for scripts
- Tools the agent uses itself: read, list, glob, grep, edit and write files,
  run shell commands — every edit shown as a diff and every command
  confirmed before it runs
- Automatic context: an overview of the directory (respecting `.gitignore`)
  and your own instructions from a `HINT.md`, global or per project
- Conversations recorded as sessions you can continue, list and pick up by id
- Provider profiles with a default/fallback pair, configured from the
  command line, environment variables or YAML files

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

### Homebrew (macOS/Linux)

```bash
brew install --cask mrYush/hint/hint
```

The tap is `mrYush/hint`; the cask installs the release binary and brings
ripgrep with it. Pre-releases are not published to the tap.

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

You need Go 1.22 or higher.

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

Run `hint` with no arguments in your project:

```
$ hint
hint: interactive session; /help for commands, Ctrl-D or /exit to quit

> what does main.go do?
...
> and where is it tested?
...
> /exit
```

Each line is one question; the conversation carries over from one to the
next. `/help` lists the commands, `/exit` (or Ctrl-D) leaves. Ctrl-C while
an answer is being written stops that answer and returns to the prompt; the
session is kept.

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
Sessions are one JSONL file each under
`~/.local/share/hint/sessions/<project>/` (`$XDG_DATA_HOME` is respected),
one per working directory; delete a file to forget the conversation. The
system prompt is not stored — each run builds it afresh — and neither are
`a(lways)` answers, so a continued session asks again before running
commands. When the conversation outgrows the model's context window it is
summarized, and the summary is what a later run continues from.

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

### Quick start (zero config)

Without any config file, `hint` talks to the [api-bar](https://api-bar.ru)
gateway by default — one key is enough:

```bash
export API_BAR_KEY=<your_api_bar_key>
hint -p "How do I run this project?"
```

If only `OPENAI_API_KEY` is set, a legacy `openai` profile on
`https://api.openai.com/v1` is used instead.

### Configuration file

```yaml
providers:
  - name: api-bar
    kind: openai                                # OpenAI Chat Completions dialect
    base_url: https://api-bar.ru/route/openai   # hint appends /chat/completions
    api_key: ${API_BAR_KEY}                     # $VAR / ${VAR} references are expanded
    model: gpt-4o
    context_window: 128000                      # tokens; sizes the compaction (optional)
  - name: local
    kind: ollama                                # same dialect, no key required
    base_url: http://localhost:11434/v1
    model: qwen2.5:7b
    context_window: 8192
default_provider: api-bar
fallback_provider: local
```

The project file overlays the global one profile-by-profile (matched by
`name`, non-empty fields win), so a project can override just the model or
key of a globally defined profile.

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
