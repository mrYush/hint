# Hint - Context-aware assistant for developers

`hint` is a CLI utility that helps developers get contextual suggestions based on the current working directory and its contents. The utility integrates with OpenAI-compatible APIs to generate relevant answers to your questions.

## Features

- Automatic analysis of the current directory to create context
- Support for various configuration methods (command line, environment variables, file)
- Compatibility with OpenAI and other compatible APIs (Azure OpenAI, etc.)
- Simple and intuitive interface

## Installation

### Requirements

- Go version 1.20 or higher
- Internet access for downloading dependencies
- API key for OpenAI or compatible service

### Building from source

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

4. Install to your system:
   ```bash
   # Linux/MacOS
   sudo cp hint /usr/local/bin/
   # Windows
   cp hint.exe %USERPROFILE%\AppData\Local\bin\
   ```

## Configuration

The utility supports several configuration methods:
### Via command line flags

```bash
hint --api-key=<your_api_key> --model=<model>
```

### Via environment variables

```bash
export HINT_API_KEY=<your_api_key>
export HINT_MODEL=<model>
export HINT_API_URL=<api_url>
```

### Via configuration file

Create a file `~/.config/hint.yaml` or `./hint.yaml` with the following content:

```yaml
api_key: <your_api_key>
model: <model>
api_url: <api_url>
```

## Usage

### Basic examples

```bash
hint "How to use hint?"
```
