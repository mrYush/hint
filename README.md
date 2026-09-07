# Hint - Context-aware assistant for developers

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

### Using Homebrew (macOS/Linux)

The easiest way to install `hint` is using Homebrew:

```bash
brew install hint
```

### Building from source

If you prefer to build from source, you'll need:

- Go 1.22 or higher
- Internet access for downloading dependencies
- API key for OpenAI or compatible service

Follow these steps:

1. Clone the repository:
   ```bash
   git clone https://github.com/mrYush/hint.git
   cd hint
   ```

2. Install dependencies:
   ```bash
   go mod tidy
   ```

3. Build:
   ```bash
   go build -o hint ./cmd/hint
   ```

4. Verify it works:
   ```bash
   ./hint --help
   ```

5. Install to your system:
   ```bash
   # Linux/MacOS
   sudo cp hint /usr/local/bin/
   # Windows
   cp hint.exe %USERPROFILE%\AppData\Local\bin\
   ```

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

All instruction files together are limited to 32 KiB by default; a longer
file is cut and a warning says so. The limit, and the directory overview
that goes into the same prompt, can be changed:

```bash
hint --instruction-budget 65536 ...   # bytes for all instruction files
hint --overview-depth 3 ...           # directory levels listed; 0 lists nothing
hint --overview-entries 200 ...       # cap on listed entries
```

or, in a config file:

```yaml
instructions:
  budget: 65536
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
