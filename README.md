# Hint - Context-aware assistant for developers

`hint` is a CLI utility that helps developers get contextual suggestions based on the current working directory and its contents. The utility integrates with OpenAI-compatible APIs to generate relevant answers to your questions.

## Features

- Automatic analysis of the current directory to create context
- Support for various configuration methods (command line, environment variables, file)
- Compatibility with OpenAI and other compatible APIs (Azure OpenAI, etc.)
- Simple and intuitive interface

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
   go build -o hint cmd/hint/main.go
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

## Configuration

Configuration is a set of named **provider profiles** plus a default/fallback
pair. Sources, highest priority first:

1. CLI flags (`--provider`, `--api-url`, `--api-key`, `--model`)
2. Environment variables (`HINT_*`)
3. Project file `./.hint/config.yaml`
4. Global file `~/.config/hint/config.yaml` (respects `$XDG_CONFIG_HOME`)

### Quick start (zero config)

Without any config file, `hint` talks to the [api-bar](https://api-bar.ru)
gateway by default — one key is enough:

```bash
export API_BAR_KEY=<your_api_bar_key>
hint "How do I run this project?"
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
  - name: local
    kind: ollama                                # same dialect, no key required
    base_url: http://localhost:11434/v1
    model: qwen2.5:7b
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
export HINT_API_KEY=<key>              # these three override the SELECTED
export HINT_API_URL=<base_url>         # profile only; the fallback keeps
export HINT_MODEL=<model>              # its own values
export HINT_DEBUG=true
```

### CLI flags

```bash
hint --provider=local "question"                 # pick a profile for one run
hint --api-key=<key> --model=gpt-4o-mini "..."   # override the selected profile
```

### Migrating from the flat config

The pre-profiles format (`api_url`/`api_key`/`model` at the top level of
`~/.config/hint.yaml` or `./hint.yaml`) is still read: the old paths work
with a deprecation warning, and the flat keys are mapped onto a single
profile named `default`. Move to the `providers` list at the new paths at
your convenience.

## Usage

### Basic examples

```bash
hint "How to use hint?"
```

### Common use cases

```bash
# Ask about how to run the project
hint "How do I run this project?"

# Ask about project structure
hint "Explain the directory structure of this project"

# Get help with a specific file
hint "What does the README.md file contain?"

# Ask for code explanation
hint "Explain what this code does"

# Get suggestions for solving issues
hint "I'm getting an error when running this command, what could be wrong?"
```

### Advanced usage

You can get more specific assistance by navigating to different directories in your project:

```bash
# Navigate to a subdirectory to focus the context
cd src/components/
hint "What do these components do?"
```

## How it works

1. When you run `hint` with a question, it captures your current working directory path
2. It scans the directory contents to create context
3. This context along with your question is sent to the configured LLM API
4. The LLM generates a response based on the context and question
5. The response is displayed in your terminal

## Troubleshooting

### Common issues

1. **Error "API key must be specified"**
   - Make sure you've set up your API key through one of the configuration methods
   - Check if the environment variable is properly set

2. **Error connecting to the API**
   - Verify your internet connection
   - Check if the API endpoint is correct and accessible

3. **Empty or irrelevant responses**
   - Try rephrasing your question to be more specific
   - Check if you're in the correct directory related to your question

### Debug mode

If you're having issues, you can get more detailed logs:

```bash
export HINT_DEBUG=true
hint "Your question"
```

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for the
development setup, conventions, and how work is planned. The project roadmap
lives in [PLAN.md](PLAN.md).

## License

MIT
